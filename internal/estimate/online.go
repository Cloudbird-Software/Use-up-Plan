package estimate

import (
	"errors"
	"fmt"
	"math"

	"gonum.org/v1/gonum/optimize"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// EstimateOptions 是在线点估计的运行参数。
type EstimateOptions struct {
	MaxIterations int // 主迭代上限（默认 200；warm-start 下通常远小于此）
	// Freeze 把参数临时冻结在给定值（B4 整数吸附后的重拟合）：冻结参数
	// 退出自由空间，值合入 base。spec 内 Frozen 参数不受此字段影响。
	Freeze map[string]float64
}

func (o EstimateOptions) withDefaults() EstimateOptions {
	if o.MaxIterations <= 0 {
		o.MaxIterations = 200
	}
	return o
}

// Result 是一次在线点估计的产物。Theta 是完整快照（含 frozen），
// 可直接喂给 semantics.Charge / ledger 重放；LogPosterior 供模型选择
// （B3 后续 BIC/边际似然）与收敛监控。
type Result struct {
	Theta        qdl.ParamPoint // 完整参数快照（自由 + frozen + base 常量）
	LogPosterior float64        // 收敛点的对数后验（含先验与似然）
	FreeIDs      []string       // 自由参数 ID（与估计顺序一致）
	Iterations   int            // 主迭代数
	FuncEvals    int            // 目标函数求值数
	Converged    bool           // 优化器报告收敛（否则是迭代上限截断）
	Status       string         // 优化器终止状态的人类可读描述
	NObs         int            // 参与拟合的观测点数
}

// Estimate 跑一次在线点估计（Intent §4.6：L-BFGS 风格低延迟增量更新，
// gonum 落地为 BFGS + 无界 z 空间变换 + 梯度缩放 preconditioning）。
// theta0 是 warm-start 起点（上一轮估计或先验中位数；缺的参数自动从先验补）。
func Estimate(ds *Dataset, base qdl.ParamPoint, theta0 qdl.ParamPoint, opts EstimateOptions) (*Result, error) {
	opts = opts.withDefaults()
	if len(opts.Freeze) > 0 {
		merged := qdl.ParamPoint{}
		for k, v := range base {
			merged[k] = v
		}
		for k, v := range opts.Freeze {
			merged[k] = v
		}
		base = merged
	}
	prob, err := NewProblem(ds, base, theta0, opts.Freeze)
	if err != nil {
		return nil, err
	}
	res := &Result{FreeIDs: prob.Space.IDs, NObs: len(ds.Obs)}

	// 无自由参数（全 frozen）：估计是恒等操作，只算一次后验。
	if prob.Space.N() == 0 {
		theta := qdl.ParamPoint{}
		for k, v := range base {
			theta[k] = v
		}
		lp, err := evalLogPosterior(ds, theta, prob.Space.Priors)
		if err != nil {
			return nil, err
		}
		res.Theta, res.LogPosterior, res.Converged, res.Status = theta, lp, true, "无自由参数"
		return res, nil
	}

	if f := prob.NLL(nil); math.IsInf(f, 1) || math.IsNaN(f) {
		return nil, fmt.Errorf("estimate: 初始点目标值 %v——theta0/base 与 spec 或数据集不相容", f)
	}

	prob2 := optimize.Problem{
		Func: prob.NLL,
		Grad: prob.NLLGrad,
	}
	// u 空间原点即 theta0（zRef 参考点）。L-BFGS（ROADMAP 选型：L-BFGS 风格）
	// + MoreThuente 强 Wolfe 线搜索——量化似然的窄谷 + 远处悬崖面上，
	// Bisection 的步长倍增会冲进悬崖，MoreThuente 的保护性插值更稳健。
	// DecreaseFactor 显式给 1e-4：gonum 默认 0 时 Armijo 退化为「不增即可」，
	// 谷底浮点抖动会让曲率条件永不被满足、bracket 区间塌缩。
	u0 := make([]float64, prob.Space.N())
	settings := &optimize.Settings{
		MajorIterations: opts.MaxIterations,
		// 在线估计的收敛语义（Intent §4.6）：后验提升 < 1e-8 连续 10 代即停。
		// 量化观测单点信息量低（±s/2 区间内无差异），把 NLL 磨到机器精度
		// 没有意义——warm-start 下次观测到来时还会继续移动。
		Converger: &optimize.FunctionConverge{Absolute: 1e-8, Iterations: 10},
	}
	out, err := optimize.Minimize(prob2, u0, settings, &optimize.LBFGS{
		Linesearcher: &optimize.MoreThuente{DecreaseFactor: 1e-4},
	})
	if err != nil {
		// 线搜索失败不是估计失败：optLoc 已持有至今最优点（每次 MajorIteration
		// 更新），f 从初值大幅下降后卡在窄谷是量化似然的固有形状。降级返回，
		// Converged=false 如实报告；warm-start 语义下下一轮继续 refine。
		stuck := errors.Is(err, optimize.ErrLinesearcherFailure) ||
			errors.Is(err, optimize.ErrNoProgress)
		if !stuck || out == nil || math.IsNaN(out.F) || math.IsInf(out.F, 1) {
			return nil, fmt.Errorf("estimate: 优化器失败: %w", err)
		}
	} else if math.IsNaN(out.F) || math.IsInf(out.F, 1) {
		return nil, fmt.Errorf("estimate: 收敛点目标值非有限（%v）——数据集与参数化不相容", out.F)
	}

	free := prob.Space.FromZ(prob.uToZ(out.X))
	res.Theta = prob.Space.CompleteTheta(free, base)
	res.LogPosterior = -out.F
	res.Iterations = out.Stats.MajorIterations
	res.FuncEvals = out.Stats.FuncEvaluations
	res.Converged = out.Status > 0
	res.Status = out.Status.String()
	return res, nil
}

// evalLogPosterior 在给定完整 θ 下算一次对数后验（不做优化）。
func evalLogPosterior(ds *Dataset, theta qdl.ParamPoint, priors map[string]*qdl.Distribution) (float64, error) {
	mus, err := ds.Predict(theta)
	if err != nil {
		return 0, err
	}
	return logPosterior(mus, ds.Obs, theta, priors)
}
