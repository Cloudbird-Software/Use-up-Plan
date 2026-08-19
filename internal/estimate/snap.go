package estimate

import (
	"fmt"
	"math"
	"sort"

	"gonum.org/v1/gonum/mat"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// 整数吸附（Intent §4.4）：厂商内部系数几乎总是整齐值（1、5、0.1、0.25）。
// 流程：MLE 点估计 → Laplace 近似 90% CI → 候选（snap_candidates 或连分数
// 逼近）落入 CI 即吸附并冻结 → 重拟合其余参数——自由度每减一，其余参数
// 的 CI 收窄一轮（级联式精度提升）。按吸附置信度（相对距离）升序逐个吸附，
// 每次吸附做似然比复核（ΔNLL > χ²₀.₉₉(1)/2 = 3.317 则撤销——数据在 1%
// 水平强烈反对该整齐值假设）。LR 阈值比 Wald CI 宽一档是刻意的：量化似然
// 的谷尖锐而 MLE 本身被量化噪声偏移，两口径分歧时容忍温和劣化、只拦
// 强烈反对（如容量候选远离真值的场景）。

// Interval 是参数的置信区间（物理 θ 空间）。
type Interval struct {
	Lo, Hi float64
}

// Contains 报告 v 是否落在 [Lo, Hi]。
func (iv Interval) Contains(v float64) bool { return v >= iv.Lo && v <= iv.Hi }

// HalfWidth 是 CI 半宽（变换非对称时取较大一侧）。
func (iv Interval) HalfWidth() float64 {
	return math.Max(iv.Hi-(iv.Hi+iv.Lo)/2, (iv.Hi+iv.Lo)/2-iv.Lo)
}

// LaplaceOptions 控制 Laplace 近似 CI 的数值行为。
type LaplaceOptions struct {
	Confidence float64 // 置信水平（默认 0.9；Intent §4.4 用 90%）
	// HessianStep 是 z 空间二阶差分步长系数（h = coef·max(1,|z|)，默认 1e-4，
	// 中心差分最优 ~eps^(1/4)）。求值成本 O(n²) 次 NLL——离线批处理场景。
	HessianStep float64
}

func (o LaplaceOptions) withDefaults() LaplaceOptions {
	if o.Confidence <= 0 || o.Confidence >= 1 {
		o.Confidence = 0.9
	}
	if o.HessianStep <= 0 {
		o.HessianStep = 1e-4
	}
	return o
}

// zQuantile 返回标准正态的分位数（Acklam 逆 CDF 近似，|误差|<4.5e-4——
// CI 边界用途足够）。
func zQuantile(p float64) float64 {
	a := []float64{-3.969683028665376e1, 2.209460984245205e2, -2.759285104469687e2,
		1.383577518672690e2, -3.066479806614716e1, 2.506628277459239}
	b := []float64{-5.447609879822406e1, 1.615858368580409e2, -1.556989798598866e2,
		6.680131188771972e1, -1.328068155288572e1}
	c := []float64{-7.784894002430293e-3, -3.223964580411365e-1, -2.400758277161838,
		-2.549732539343734, 4.374664141464968, 2.938163982698783}
	d := []float64{7.784695709041462e-3, 3.224671290700398e-1, 2.445134137142996,
		3.754408661907416}
	pLow, pHigh := 0.02425, 1-0.02425
	var q, r float64
	switch {
	case p < pLow:
		q = math.Sqrt(-2 * math.Log(p))
		q = (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p > pHigh:
		q = math.Sqrt(-2 * math.Log(1-p))
		q = -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	default:
		q = p - 0.5
		r = q * q
		q = (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	}
	return q
}

// LaplaceCI 在 MLE 附近做 Laplace 近似（数值 Hessian 逆 = z 空间协方差），
// 把置信区间映射回物理 θ 空间。frozen 集合与 Estimate 的 Freeze 语义一致
// （吸附循环中每轮扩大）。返回自由参数 ID → CI；Hessian 非正定（MLE 落在
// 量化平谷上）时该参数的 CI 缺失——调用方视为不可吸附。
func LaplaceCI(ds *Dataset, base qdl.ParamPoint, thetaHat qdl.ParamPoint,
	freeze map[string]float64, opts LaplaceOptions) (map[string]Interval, error) {
	opts = opts.withDefaults()
	prob, err := NewProblem(ds, base, thetaHat, freeze)
	if err != nil {
		return nil, err
	}
	n := prob.Space.N()
	if n == 0 {
		return map[string]Interval{}, nil
	}
	zHat := prob.uToZ(nil) // theta0 = thetaHat → u 原点即 MLE
	H := hessianZ(prob, zHat, opts.HessianStep)
	var cov *mat.SymDense
	// Hessian 非正定时尝试对角加载（量化平谷上的折衷）；加载量按 10 倍
	// 递进到 1e-2 仍失败则如实报错——CI 比瞎猜强不了就别给。
	for ridge := 0.0; ridge <= 1e-2; ridge = math.Max(ridge*10, 1e-8) {
		Hc := mat.NewSymDense(n, nil)
		for i := 0; i < n; i++ {
			for j := i; j < n; j++ {
				Hc.SetSym(i, j, H.At(i, j))
			}
		}
		if ridge > 0 {
			for i := 0; i < n; i++ {
				Hc.SetSym(i, i, Hc.At(i, i)*(1+ridge)+ridge)
			}
		}
		var chol mat.Cholesky
		if chol.Factorize(Hc) {
			var inv mat.SymDense
			if err := chol.InverseTo(&inv); err == nil {
				cov = &inv
				break
			}
		}
	}
	if cov == nil {
		return nil, fmt.Errorf("estimate: LaplaceCI Hessian 非正定且对角加载失败——MLE 位于平谷，无可用 CI")
	}
	zq := zQuantile(0.5 + opts.Confidence/2)
	out := map[string]Interval{}
	for i, id := range prob.Space.IDs {
		sigma := math.Sqrt(math.Max(cov.At(i, i), 0))
		lo := prob.Space.zToPhys(id, zHat[i]-zq*sigma)
		hi := prob.Space.zToPhys(id, zHat[i]+zq*sigma)
		iv := Interval{Lo: math.Min(lo, hi), Hi: math.Max(lo, hi)}
		// 量化平谷上的奇异方向（Hessian≈0 → σ 巨大 → 无界 CI）：剔除该参数。
		// 无界 CI 会让「任何候选都落入 CI」——吸附准入退化为 LR 复核独撑，
		// 不如显式声明该参数本轮不可吸附。
		if math.IsInf(iv.Lo, 0) || math.IsInf(iv.Hi, 0) || math.IsNaN(iv.Lo+iv.Hi) {
			continue
		}
		out[id] = iv
	}
	return out, nil
}

// hessianZ 数值 Hessian（z 空间）：对角用三点公式，非对角用四点交叉公式。
// 步长 h = step·max(1,|z|)。+Inf 悬崖附近的差分点污染时该元素记 0
// （正定性由调用方的对角加载兜底）。
func hessianZ(prob *Problem, z []float64, step float64) *mat.Dense {
	n := len(z)
	H := mat.NewDense(n, n, nil)
	f0 := prob.nllZ(z)
	h := make([]float64, n)
	for i := range h {
		h[i] = step * math.Max(1, math.Abs(z[i]))
	}
	for i := 0; i < n; i++ {
		zp := append([]float64(nil), z...)
		zm := append([]float64(nil), z...)
		zp[i] += h[i]
		zm[i] -= h[i]
		fp, fm := prob.nllZ(zp), prob.nllZ(zm)
		if finite3(f0, fp, fm) {
			H.Set(i, i, (fp-2*f0+fm)/(h[i]*h[i]))
		}
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			zpp := append([]float64(nil), z...)
			zpm := append([]float64(nil), z...)
			zmp := append([]float64(nil), z...)
			zmm := append([]float64(nil), z...)
			zpp[i] += h[i]
			zpp[j] += h[j]
			zpm[i] += h[i]
			zpm[j] -= h[j]
			zmp[i] -= h[i]
			zmp[j] += h[j]
			zmm[i] -= h[i]
			zmm[j] -= h[j]
			fpp, fpm, fmp, fmm := prob.nllZ(zpp), prob.nllZ(zpm), prob.nllZ(zmp), prob.nllZ(zmm)
			if finite4(fpp, fpm, fmp, fmm) {
				H.Set(i, j, (fpp-fpm-fmp+fmm)/(4*h[i]*h[j]))
				H.Set(j, i, H.At(i, j))
			}
		}
	}
	return H
}

func finite3(a, b, c float64) bool {
	return finite(a) && finite(b) && finite(c)
}

func finite4(a, b, c, d float64) bool {
	return finite(a) && finite(b) && finite(c) && finite(d)
}

func finite(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }

// SnapCandidate 是一个可吸附的整齐值。
type SnapCandidate struct {
	ParamID string
	Value   float64
	Source  string // "snap_candidates" | "rational_approx"
	// RelDistance = |θ̂ - v|/σ̂：标准化吸附距离（越小置信度越高）。
	RelDistance float64
}

// SnapStep 记录一轮吸附的完整决策。
type SnapStep struct {
	Candidate  SnapCandidate
	Accepted   bool
	LRDeltaNLL float64 // 重拟合后 NLL 相对吸附前的变化（正 = 劣化）
	Reason     string  // 接受/拒绝原因
}

// SnapOptions 是整数吸附的运行参数。
type SnapOptions struct {
	Estimate EstimateOptions // 内层点估计
	Laplace  LaplaceOptions  // CI 数值行为
	// MaxDenominator 是连分数逼近的分母上限（默认 24：0.25=1/4、
	// 0.4=2/5、0.75=3/4 级别的「厂商整齐值」全覆盖；PSLQ 的工程替代——
	// 连分数收敛子就是最佳有理逼近，且无浮点精度陷阱）。
	MaxDenominator int
	// MaxRounds 吸附轮数上限（默认 8；每轮吸附至多 1 个参数）
	MaxRounds int
}

func (o SnapOptions) withDefaults() SnapOptions {
	if o.MaxDenominator <= 0 {
		o.MaxDenominator = 24
	}
	if o.MaxRounds <= 0 {
		o.MaxRounds = 8
	}
	return o
}

// SnapReport 是级联吸附的最终产物。
type SnapReport struct {
	Initial *Result             // 吸附前的 MLE
	Final   *Result             // 吸附后的重拟合（Final.Theta 是最终参数快照）
	Steps   []SnapStep          // 逐轮决策（接受与拒绝都有）
	Snapped map[string]float64  // 被吸附冻结的参数 → 吸附值
	CI      map[string]Interval // 最终自由参数 CI（级联收窄后的）
}

// Snap 跑完整的整数吸附循环（Intent §4.4）：
//  1. Estimate → MLE θ̂
//  2. LaplaceCI → 每个自由参数的 90% CI
//  3. 收集落入 CI 的候选（spec 的 snap_candidates；无命中时连分数逼近）
//  4. 按相对距离升序逐个试吸附：冻结 → 重拟合 → 似然比复核
//     （ΔNLL ≤ 1.355 = χ²₀.₉(1)/2 接受，否则撤销）
//  5. 循环直到无可吸附候选或轮数上限
func Snap(ds *Dataset, base qdl.ParamPoint, theta0 qdl.ParamPoint, opts SnapOptions) (*SnapReport, error) {
	opts = opts.withDefaults()
	res, err := Estimate(ds, base, theta0, opts.Estimate)
	if err != nil {
		return nil, fmt.Errorf("estimate: Snap 初始估计: %w", err)
	}
	report := &SnapReport{Initial: res, Snapped: map[string]float64{}}
	tried := map[string]bool{} // 已试过的 (paramID,value) 组合
	freeze := map[string]float64{}
	for k, v := range opts.Estimate.Freeze {
		freeze[k] = v
	}

	for round := 0; round < opts.MaxRounds; round++ {
		ci, err := LaplaceCI(ds, base, res.Theta, freeze, opts.Laplace)
		if err != nil {
			break // 平谷上 CI 不可用：停止吸附，保留至今结果
		}
		cands := collectSnapCandidates(ds.Spec, res.Theta, ci, opts.MaxDenominator, freeze, tried)
		if len(cands) == 0 {
			report.CI = ci
			break
		}
		best := cands[0]
		tried[best.ParamID+"|"+fmt.Sprint(best.Value)] = true
		freeze[best.ParamID] = best.Value

		inner := opts.Estimate
		inner.Freeze = freeze
		res2, err := Estimate(ds, base, res.Theta, inner)
		if err != nil {
			delete(freeze, best.ParamID)
			report.Steps = append(report.Steps, SnapStep{
				Candidate: best, Accepted: false,
				Reason: "重拟合失败: " + err.Error(),
			})
			continue
		}
		deltaNLL := -res2.LogPosterior + res.LogPosterior // 正 = 吸附后更差
		if deltaNLL > 3.317 {
			delete(freeze, best.ParamID)
			report.Steps = append(report.Steps, SnapStep{
				Candidate: best, Accepted: false, LRDeltaNLL: deltaNLL,
				Reason: fmt.Sprintf("似然比拒绝：ΔNLL=%.2f > χ²₀.₉₉(1)/2=3.317", deltaNLL),
			})
			continue
		}
		res = res2
		report.Snapped[best.ParamID] = best.Value
		report.Steps = append(report.Steps, SnapStep{
			Candidate: best, Accepted: true, LRDeltaNLL: deltaNLL,
			Reason: fmt.Sprintf("落入 90%% CI 且似然比通过（ΔNLL=%.2f）", deltaNLL),
		})
	}
	report.Final = res
	if report.CI == nil {
		if ci, err := LaplaceCI(ds, base, res.Theta, freeze, opts.Laplace); err == nil {
			report.CI = ci
		}
	}
	return report, nil
}

// collectSnapCandidates 收集全部可吸附候选并按相对距离升序排序。
// 排除：已 frozen（spec 或 freeze）、已试过、θ̂ 无 CI 的参数。
// snap_candidates 优先；该参数无候选命中时才用连分数逼近。
func collectSnapCandidates(spec *qdl.PlanSpec, theta qdl.ParamPoint, ci map[string]Interval,
	maxDen int, freeze map[string]float64, tried map[string]bool) []SnapCandidate {
	var out []SnapCandidate
	for i := range spec.Parameters {
		p := &spec.Parameters[i]
		if p.Frozen {
			continue
		}
		if _, ok := freeze[p.ID]; ok {
			continue
		}
		hat, ok := theta[p.ID]
		if !ok {
			continue
		}
		iv, ok := ci[p.ID]
		if !ok {
			// Laplace CI 缺失（量化平谷：MLE 格点对齐使单参数方向曲率为零，
			// 数据在该方向无信息）→ 贝叶斯语义下后验由先验主导，吸附准入
			// 回退先验 90% 区间；LR 复核此时是真正的守门员。
			piv, ok := priorInterval(&p.Prior)
			if !ok {
				continue // 先验也无界：本轮不可吸附
			}
			iv = piv
		}
		sigma := math.Max(iv.HalfWidth(), 1e-12)
		try := func(v float64, source string) {
			if !iv.Contains(v) {
				return
			}
			key := p.ID + "|" + fmt.Sprint(v)
			if tried[key] {
				return
			}
			out = append(out, SnapCandidate{
				ParamID: p.ID, Value: v, Source: source,
				RelDistance: math.Abs(hat-v) / sigma,
			})
		}
		// Intent §4.4 语义：声明了 snap_candidates 就只信候选（第 2 步）；
		// 未声明才用连分数逼近找整数关系（第 3 步，pslq 的工程替代）。
		if len(p.SnapCandidates) > 0 {
			for _, c := range p.SnapCandidates {
				try(c, "snap_candidates")
			}
			continue
		}
		for _, r := range bestRationalApprox(hat, maxDen) {
			try(r, "rational_approx")
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RelDistance != out[j].RelDistance {
			return out[i].RelDistance < out[j].RelDistance
		}
		return out[i].ParamID < out[j].ParamID
	})
	return out
}

// priorInterval 是先验的 90% 中心区间（θ 空间）。先验也无界时 ok=false。
func priorInterval(d *qdl.Distribution) (Interval, bool) {
	p := func(k string) float64 { return d.Params[k] }
	zq := zQuantile(0.95)
	switch d.Kind {
	case qdl.DistNormal:
		return Interval{Lo: p("mu") - zq*p("sigma"), Hi: p("mu") + zq*p("sigma")}, true
	case qdl.DistLognormal:
		med := math.Exp(p("mu"))
		return Interval{Lo: med * math.Exp(-zq*p("sigma")), Hi: med * math.Exp(zq*p("sigma"))}, true
	case qdl.DistUniform:
		return Interval{Lo: p("low"), Hi: p("high")}, true
	case qdl.DistPoint:
		v := p("value")
		return Interval{Lo: v, Hi: v}, true
	case qdl.DistDiscrete:
		if len(d.Values) == 0 {
			return Interval{}, false
		}
		lo, hi := d.Values[0], d.Values[0]
		for _, v := range d.Values {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		return Interval{Lo: lo, Hi: hi}, true
	}
	return Interval{}, false
}

// bestRationalApprox 返回 x 的连分数收敛子中分母 ≤ maxDen 的全部候选
// （按 |x - p/q| 升序，至多 5 个）。连分数收敛子是最佳有理逼近定理的
// 直接应用——给定分母上限不存在比收敛子更近的分数。CI 过滤与似然比
// 复核负责最终择优，这里宁可多给。
func bestRationalApprox(x float64, maxDen int) []float64 {
	if !finite(x) || maxDen < 1 {
		return nil
	}
	if x == math.Trunc(x) { // 整数自身：无需逼近
		return []float64{x}
	}
	type frac struct {
		p, q int64
		d    float64
	}
	convs := []frac{{0, 1, 0}, {1, 0, 0}} // p_{-2}/q_{-2}=0/1, p_{-1}/q_{-1}=1/0
	xrem := math.Abs(x)
	for i := 0; i < 20; i++ {
		a := math.Floor(xrem)
		p := int64(a)*convs[len(convs)-1].p + convs[len(convs)-2].p
		q := int64(a)*convs[len(convs)-1].q + convs[len(convs)-2].q
		v := float64(p) / float64(q)
		convs = append(convs, frac{p, q, math.Abs(math.Abs(x) - v)})
		if q > int64(maxDen) {
			break
		}
		rem := xrem - a
		if rem < 1e-12 {
			break
		}
		xrem = 1 / rem
		if !finite(xrem) || xrem > 1e12 {
			break
		}
	}
	sign := 1.0
	if x < 0 {
		sign = -1
	}
	var out []float64
	for _, c := range convs[2:] { // 跳过两个种子
		if c.q <= 0 || c.q > int64(maxDen) || c.p < 0 {
			continue
		}
		out = append(out, sign*float64(c.p)/float64(c.q))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}
