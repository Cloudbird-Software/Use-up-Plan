package estimate

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"

	"gonum.org/v1/gonum/mat"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/ledger"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// 离线后验（Intent §4.6 架构建议的另一半）：在线 L-BFGS 点估计供路由器
// 毫秒级使用；离线 Metropolis-Hastings 全量后验供审计与置信区间使用。
// 两者写同一 ParamUpdateEvent 流（reason: online|offline），由本文件的
// PosteriorResult.ParamUpdates 产出离线侧事件。
//
// 选型（ROADMAP S2）：Go 无 numpyro/NUTS 等价物，量化似然下随机游走
// MH 通常够用——参数量 20~100、目标面窄谷但不病态（z 空间变换已把
// box 约束消掉）。对比实验见 TestLaplaceVsPosterior：Laplace CI（snap
// 用）与 MH CI 同覆盖真值，宽度同量级。

// PosteriorOptions 是离线后验采样的运行参数。
type PosteriorOptions struct {
	Seed uint64 // PCG 种子（默认 1）。采样确定性可复现——审计报告的硬要求
	// BurnIn 预热迭代数（默认 1500）：自适应 proposal 在此期间学样本协方差
	// 与步长；预热样本全部丢弃，采样期 proposal 冻结保证正确平稳分布。
	BurnIn int
	// Samples 保留样本数（默认 1000；抽稀后）。
	Samples int
	// Thin 抽稀间隔（默认 3）：降低自相关让 ESS 接近保留数。
	Thin int
	// Freeze 把参数临时冻结在给定值（吸附后重采样剩余参数；同 Estimate.Freeze）。
	Freeze map[string]float64
}

func (o PosteriorOptions) withDefaults() PosteriorOptions {
	if o.BurnIn <= 0 {
		o.BurnIn = 1500
	}
	if o.Samples <= 0 {
		o.Samples = 1000
	}
	if o.Thin <= 0 {
		o.Thin = 3
	}
	return o
}

// PosteriorSummary 是单参数的后验摘要：90% 等尾可信区间 [Q05, Q95]。
type PosteriorSummary struct {
	Mean float64
	SD   float64
	Q05  float64
	Q50  float64
	Q95  float64
}

// PosteriorResult 是一次离线后验采样的产物。
type PosteriorResult struct {
	FreeIDs []string         // 自由参数 ID（与样本向量列序一致）
	Samples []qdl.ParamPoint // 保留样本（物理空间，含 frozen 基线）
	Summary map[string]PosteriorSummary
	Priors  map[string]*qdl.Distribution // ParamUpdates 的 PosteriorBefore 来源

	AcceptRate   float64            // 采样期接受率（诊断：0.1~0.5 健康）
	ESS          map[string]float64 // 每参数有效样本量（Geyer 初始正序列）
	LogPosterior float64            // theta0（通常为在线 MLE）处的对数后验
	NObs         int
}

// SamplePosterior 跑离线全量后验（自适应随机游走 Metropolis-Hastings，
// z 空间采样——box 约束经变换消解，proposal 永不落在非法域）。
// theta0 建议传在线估计（Estimate）的结果：链从后验众数附近起步，
// 预热只需学形状不需爬坡。
func SamplePosterior(ds *Dataset, base qdl.ParamPoint, theta0 qdl.ParamPoint, opts PosteriorOptions) (*PosteriorResult, error) {
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
	n := prob.Space.N()
	if n == 0 {
		return nil, fmt.Errorf("estimate: SamplePosterior 无自由参数——离线后验需要至少一个可学习参数")
	}

	z := prob.ZRef()
	lp := -prob.nllZ(z)
	if !finite(lp) {
		return nil, fmt.Errorf("estimate: SamplePosterior 初始点对数后验非有限（%v）——theta0 与数据集不相容", lp)
	}
	lpInit := lp

	rng := rand.New(rand.NewPCG(opts.Seed, 0x6ff66c69)) // "offli"——流常数
	res := &PosteriorResult{
		FreeIDs: prob.Space.IDs, Priors: prob.Space.Priors,
		LogPosterior: lpInit, NObs: len(ds.Obs),
	}

	// ---- 自适应 proposal 状态 ----
	w := newWelford(n)
	var L *mat.TriDense        // 协方差 Cholesky 下三角（预热期末冻结）
	lam := 1.0                 // 全局步长缩放（Robbins-Monro 逼近期目标接受率）
	diag := make([]float64, n) // 对角兜底 proposal（协方差学成之前）
	for i := range diag {
		diag[i] = 0.1 * math.Max(1, math.Abs(z[i]))
	}
	prop := make([]float64, n)
	eps := make([]float64, n)
	const targetAccept = 0.3 // n=1 最优 0.44、n→∞ 0.234 的中庸值

	drawProposal := func(dst, cur []float64) {
		if L != nil {
			// v ~ N(0, Σ)，Σ = L·Lᵀ；步长 2.38/√n 是高斯目标的最优缩放
			s := lam * 2.38 / math.Sqrt(float64(n))
			for j := range eps {
				eps[j] = rng.NormFloat64()
			}
			for i := 0; i < n; i++ {
				acc := 0.0
				for j := 0; j <= i; j++ {
					acc += L.At(i, j) * eps[j]
				}
				dst[i] = cur[i] + s*acc
			}
		} else {
			for i := 0; i < n; i++ {
				dst[i] = cur[i] + lam*diag[i]*rng.NormFloat64()
			}
		}
	}

	// ---- 预热：学协方差 + 步长，样本丢弃 ----
	for t := 1; t <= opts.BurnIn; t++ {
		drawProposal(prop, z)
		lpProp := -prob.nllZ(prop)
		accept := lpProp-lp > math.Log(rng.Float64())
		if accept {
			copy(z, prop)
			lp = lpProp
		}
		w.add(z)
		// 样本足够后定期刷新 Cholesky（采样期用冻结因子）
		if w.n >= 2*n+10 && t%50 == 0 {
			if Lc, ok := w.chol(); ok {
				L = Lc
			}
		}
		// Robbins-Monro 步长自适应：增益递减（遍历性友好），lambda 钳箱防漂飞
		g := 1 / math.Sqrt(float64(t))
		if accept {
			lam *= math.Exp(g * (1 - targetAccept))
		} else {
			lam *= math.Exp(-g * targetAccept)
		}
		lam = math.Min(math.Max(lam, 1e-6), 1e6)
	}

	// ---- 采样：proposal 冻结，抽稀保留 ----
	accepted, tried := 0, 0
	for t := 1; len(res.Samples) < opts.Samples; t++ {
		drawProposal(prop, z)
		lpProp := -prob.nllZ(prop)
		accept := lpProp-lp > math.Log(rng.Float64())
		tried++
		if accept {
			copy(z, prop)
			lp = lpProp
			accepted++
		}
		if t%opts.Thin == 0 {
			res.Samples = append(res.Samples,
				prob.Space.CompleteTheta(prob.Space.FromZ(z), base))
		}
	}
	res.AcceptRate = float64(accepted) / float64(tried)

	res.Summary = summarizePosterior(res.Samples, prob.Space.IDs)
	res.ESS = map[string]float64{}
	for _, id := range prob.Space.IDs {
		x := make([]float64, len(res.Samples))
		for k, s := range res.Samples {
			x[k] = s[id]
		}
		res.ESS[id] = ess(x)
	}
	return res, nil
}

// ParamUpdates 把后验摘要转成 ParamUpdateEvent 序列（Intent §4.6：在线/
// 离线写同一事件流，reason 标注 estimator 来源——此处恒 "offline"）。
// 正支持参数（Q05 > 0，exp 变换保正的计量参数）摘要为 lognormal——
// 中位数口径对 skewed 后验更忠实；跨零参数用 normal。
func (r *PosteriorResult) ParamUpdates(evidenceIDs []int64) []ledger.ParamUpdateEvent {
	evs := make([]ledger.ParamUpdateEvent, 0, len(r.FreeIDs))
	for _, id := range r.FreeIDs {
		s := r.Summary[id]
		var after qdl.Distribution
		switch {
		case s.SD <= 0:
			after = qdl.Point(s.Q50) // 退化链（先查 AcceptRate/ESS 诊断）
		case s.Q05 > 0:
			// mu 取 log(样本中位数)——lognormal 的中位数口径与样本分位数
			// 严格一致；sigma 取对数样本标准差（形状）。
			lm, ls, cnt := 0.0, 0.0, 0
			for _, sp := range r.Samples {
				if v, ok := sp[id]; ok && v > 0 {
					lv := math.Log(v)
					lm += lv
					ls += lv * lv
					cnt++
				}
			}
			lmean := lm / float64(cnt)
			lvar := ls/float64(cnt) - lmean*lmean
			if lvar < 0 {
				lvar = 0
			}
			after = qdl.Distribution{Kind: qdl.DistLognormal,
				Params: map[string]float64{"mu": math.Log(s.Q50), "sigma": math.Sqrt(lvar)}}
		default:
			after = qdl.Distribution{Kind: qdl.DistNormal,
				Params: map[string]float64{"mu": s.Mean, "sigma": s.SD}}
		}
		ev := ledger.ParamUpdateEvent{
			ParamID:        id,
			PosteriorAfter: after,
			EvidenceIDs:    append([]int64(nil), evidenceIDs...),
			Reason:         "offline",
		}
		if pr := r.Priors[id]; pr != nil {
			before := *pr
			ev.PosteriorBefore = &before
		}
		evs = append(evs, ev)
	}
	return evs
}

// EvidenceIDs 返回参与拟合的观测事件 seq（ParamUpdates 的证据指认）。
func (ds *Dataset) EvidenceIDs() []int64 {
	ids := make([]int64, 0, len(ds.Obs))
	for i := range ds.Obs {
		if ds.Obs[i].Seq > 0 {
			ids = append(ids, ds.Obs[i].Seq)
		}
	}
	return ids
}

// ---- 内部工具 ----

// welfordCov 是 z 空间样本协方差的在线累积器（Welford）。
type welfordCov struct {
	n    int
	mean []float64
	m2   *mat.Dense
}

func newWelford(n int) *welfordCov {
	return &welfordCov{mean: make([]float64, n), m2: mat.NewDense(n, n, nil)}
}

func (w *welfordCov) add(x []float64) {
	w.n++
	inv := 1 / float64(w.n)
	old := append([]float64(nil), w.mean...)
	for i := range x {
		w.mean[i] = old[i] + (x[i]-old[i])*inv
	}
	for i := range x {
		for j := i; j < len(x); j++ {
			d := (x[i] - old[i]) * (x[j] - w.mean[j])
			w.m2.Set(i, j, w.m2.At(i, j)+d)
			if i != j {
				w.m2.Set(j, i, w.m2.At(i, j))
			}
		}
	}
}

// chol 返回样本协方差（+对角抖动保正定）的 Cholesky 下三角因子。
func (w *welfordCov) chol() (*mat.TriDense, bool) {
	n := len(w.mean)
	if w.n < 2 {
		return nil, false
	}
	c := mat.NewSymDense(n, nil)
	scale := 0.0
	for i := 0; i < n; i++ {
		v := w.m2.At(i, i) / float64(w.n-1)
		c.SetSym(i, i, v)
		if v > scale {
			scale = v
		}
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			c.SetSym(i, j, w.m2.At(i, j)/float64(w.n-1))
		}
	}
	eps := 1e-10 * (1 + scale)
	for i := 0; i < n; i++ {
		c.SetSym(i, i, c.At(i, i)+eps)
	}
	var ch mat.Cholesky
	if ok := ch.Factorize(c); !ok {
		return nil, false
	}
	L := mat.NewTriDense(n, mat.Lower, nil)
	ch.LTo(L)
	return L, true
}

// summarizePosterior 逐参数算均值/标准差与 5%/50%/95% 分位数。
func summarizePosterior(samples []qdl.ParamPoint, ids []string) map[string]PosteriorSummary {
	out := map[string]PosteriorSummary{}
	for _, id := range ids {
		x := make([]float64, len(samples))
		for k, s := range samples {
			x[k] = s[id]
		}
		sort.Float64s(x)
		sum := 0.0
		for _, v := range x {
			sum += v
		}
		mean := sum / float64(len(x))
		var ss float64
		for _, v := range x {
			ss += (v - mean) * (v - mean)
		}
		sd := 0.0
		if len(x) > 1 {
			sd = math.Sqrt(ss / float64(len(x)-1))
		}
		out[id] = PosteriorSummary{
			Mean: mean, SD: sd,
			Q05: quantileSorted(x, 0.05), Q50: quantileSorted(x, 0.5), Q95: quantileSorted(x, 0.95),
		}
	}
	return out
}

// quantileSorted 线性插值分位数（输入必须已升序）。
func quantileSorted(x []float64, p float64) float64 {
	if len(x) == 0 {
		return math.NaN()
	}
	if len(x) == 1 {
		return x[0]
	}
	h := p * float64(len(x)-1)
	lo := int(math.Floor(h))
	hi := int(math.Ceil(h))
	if lo == hi {
		return x[lo]
	}
	frac := h - float64(lo)
	return x[lo]*(1-frac) + x[hi]*frac
}

// ess 是有效样本量（Geyer 初始正序列估计量）：自相关系数按相邻 lag 成对
// 求和（ρ_k + ρ_{k+1}），在首个非正对处截断，τ = 1 + 2Σρ → ESS = N/τ。
// 这是 MCMC 收敛诊断的保守下界口径。
func ess(x []float64) float64 {
	n := len(x)
	if n < 4 {
		return float64(n)
	}
	mean := 0.0
	for _, v := range x {
		mean += v
	}
	mean /= float64(n)
	d := make([]float64, n)
	for i, v := range x {
		d[i] = v - mean
	}
	var c0 float64
	for _, v := range d {
		c0 += v * v
	}
	if c0 <= 0 {
		return float64(n) // 常量链：ESS 退化，按 N 计（诊断由 AcceptRate 兜底）
	}
	rho := func(lag int) float64 {
		s := 0.0
		for i := 0; i+lag < n; i++ {
			s += d[i] * d[i+lag]
		}
		return s / c0
	}
	tau := 1.0
	for k := 1; k+1 < n; k += 2 {
		g := rho(k) + rho(k+1)
		if g <= 0 {
			break
		}
		tau += 2 * g
	}
	if tau < 1 {
		tau = 1
	}
	e := float64(n) / tau
	return math.Min(math.Max(e, 1), float64(n))
}
