// Package estimate 实现 Intent §4 的参数辨识：量化似然、在线点估计、
//（P2-1 T6 注入：纯源码演进，测试不动——预期 test-integrity 绿）
// gauge fixing、整数吸附、离线后验与漂移检测。
//
// 深接口分层：本包只消费「(θ → 预测值 μ) + (观测值 y, 步长 s, 噪声 σ)」的
// 似然面，不从存储层直接读事件（数据集组装见 dataset.go）。核心信念
// （Intent §0 难点 1）：厂商计量的结构与数值都不可信，一切数值槽位
// (w, C, mult, flat) 是带置信区间的可学习量。
package estimate

import (
	"fmt"
	"math"
	"sort"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

const lnSqrt2Pi = 0.9189385332046727 // log(√(2π))

// logPhiTail 是 log Φ(-w)（w ≥ 0，左尾）的稳定实现：
// w < 8 时用 erfc（精确）；w ≥ 8 时用渐近展开 log(φ(w)/w)·(1-1/w²+3/w⁴)，
// 避免浮点下溢——μ 与 y 相距很远时（优化器探索的远处点）必须保住梯度信息。
func logPhiTail(w float64) float64 {
	if w < 0 {
		w = -w
	}
	if w < 8 {
		return math.Log(0.5 * math.Erfc(w/math.Sqrt2))
	}
	// Φ(-w) = φ(w)/w · (1 - 1/w² + 3/w⁴ - ...)；取前两项修正，相对误差 < 1e-8（w≥8）。
	phi := -0.5*w*w - lnSqrt2Pi - math.Log(w)
	return phi + math.Log1p(-1/(w*w)+3/(w*w*w*w))
}

// logPhi 是 log Φ(z) 的稳定实现（任意 z）。
func logPhi(z float64) float64 {
	if z >= 0 {
		// Φ(z) = 1 - Φ(-z)；Φ(-z) 小，log1p 保精度。
		t := logPhiTail(z)
		return math.Log1p(-math.Exp(t))
	}
	return logPhiTail(-z)
}

// phi 是标准正态密度。
func phi(z float64) float64 { return math.Exp(-0.5*z*z - lnSqrt2Pi) }

// logDiffExp 计算 log(e^a - e^b)（a > b）。e^a-e^b 下溢时退化为 a（b 可忽略）。
func logDiffExp(a, b float64) float64 {
	if b == math.Inf(-1) {
		return a
	}
	d := b - a   // ≤ 0
	if d < -40 { // e^d < 4e-18：b 对差无贡献
		return a
	}
	return a + math.Log1p(-math.Exp(d))
}

// QuantizedLogProb 是量化百分比观测的对数似然（Intent §4.1 原式）：
//
//	P(y|μ) = Φ((y+s/2-μ)/σ) - Φ((y-s/2-μ)/σ)
//
// s = 量化步长（整数百分比 1.0、一位小数 0.1），σ = 观测噪声
// （含 attribution lag 错位）。量化被当作观测模型的一部分——单个观测
// 信息量低但不为零，成千上万个叠加收敛（Intent 原文）。s = 0 时退化为
// 以 σ 为带宽的平滑 exact 似然。
func QuantizedLogProb(mu, y, s, sigma float64) float64 {
	if sigma <= 0 {
		sigma = 1e-9
	}
	half := s / 2
	zu := (y + half - mu) / sigma
	zl := (y - half - mu) / sigma
	// 分三段避免灾难性消去：
	switch {
	case zu <= 0: // 两端都在左尾：log(Φ(zu)-Φ(zl)) = logΦ(zu) + log(1-e^{logΦ(zl)-logΦ(zu)})
		return logDiffExp(logPhi(zu), logPhi(zl))
	case zl >= 0: // 两端都在右尾：Φ(zu)-Φ(zl) = Φ(-zl)-Φ(-zu)
		return logDiffExp(logPhi(-zl), logPhi(-zu))
	default: // 跨 0：两端都不小，直接差
		v := 0.5*math.Erfc(-zu/math.Sqrt2) - 0.5*math.Erfc(-zl/math.Sqrt2)
		if v <= 0 {
			return math.Inf(-1)
		}
		return math.Log(v)
	}
}

// ExactLogProb 是精确计数观测的对数似然（免费档 RPM、credits 桶）：
// 观测无量化时 y 与 μ 的差只剩归因错位噪声，用窄高斯近似。
func ExactLogProb(mu, y, sigma float64) float64 {
	if sigma <= 0 {
		sigma = 0.25 // 计数观测量级为 1 的最小噪声地板，防退化
	}
	z := (y - mu) / sigma
	return -0.5*z*z - math.Log(sigma) - lnSqrt2Pi
}

// WallLogProb 是撞墙观测的对数似然（Intent §4.1）：
//
//	P(wall|θ) = 1[Σwx ≥ C]·(1-ε) + ε
//
// 撞墙是信息量最大的观测：配合「撞墙前最后一个成功请求」的 Σwx < C
// 得到 C 的夹逼区间。ε 是误报率（429 可能来自并发等其他约束）。
// geC 报告预测的累计消耗是否达到容量（mu_pct ≥ 100 即 Σwx ≥ C）。
func WallLogProb(muPct float64, hit bool, eps float64) float64 {
	if eps < 0 || eps >= 1 {
		eps = 0.05
	}
	predictedHit := muPct >= 100
	if hit == predictedHit {
		if predictedHit {
			return math.Log(1 - eps) // 撞墙且模型认为该撞：强证据
		}
		return math.Log(1 - eps) // 未撞且模型认为不该撞
	}
	return math.Log(eps) // 观测与模型矛盾：强惩罚
}

// PriorLogProb 是参数先验的对数密度（qdl.Distribution 的数值方法面）。
// 未知 kind、越界（uniform 之外、lognormal 的非正值）返回 -inf。
func PriorLogProb(d *qdl.Distribution, x float64) float64 {
	if d == nil {
		return 0 // 无先验 = 平坦
	}
	p := func(k string) float64 { return d.Params[k] }
	switch d.Kind {
	case qdl.DistPoint:
		if math.Abs(x-p("value")) < 1e-12 {
			return 0
		}
		return math.Inf(-1)
	case qdl.DistNormal:
		mu, s := p("mu"), p("sigma")
		if s <= 0 {
			return math.Inf(-1)
		}
		z := (x - mu) / s
		return -0.5*z*z - math.Log(s) - lnSqrt2Pi
	case qdl.DistLognormal:
		mu, s := p("mu"), p("sigma")
		if s <= 0 || x <= 0 {
			return math.Inf(-1)
		}
		lx := math.Log(x)
		z := (lx - mu) / s
		// log N(x; e^mu, s) = -log(x) - log(s) - ½z² - ½log(2π)
		return -math.Log(x) - math.Log(s) - 0.5*z*z - lnSqrt2Pi
	case qdl.DistUniform:
		lo, hi := p("low"), p("high")
		if x < lo || x > hi || hi <= lo {
			return math.Inf(-1)
		}
		return -math.Log(hi - lo)
	case qdl.DistDiscrete:
		if len(d.Values) == 0 || len(d.Probs) != len(d.Values) {
			return math.Inf(-1)
		}
		for i, v := range d.Values {
			if math.Abs(x-v) < 1e-12 {
				return math.Log(d.Probs[i])
			}
		}
		return math.Inf(-1)
	default:
		return math.Inf(-1)
	}
}

// logLikelihood 是纯观测项的对数似然（不含先验）——BIC 结构选择
// （select.go）的打分原料：不同结构候选共享同一参数先验，比较只能用
// 似然项，否则先验会被计入两次。
func logLikelihood(mus []float64, obs []ObsPoint) (float64, error) {
	if len(mus) != len(obs) {
		return 0, fmt.Errorf("estimate: 预测数 %d 与观测数 %d 不一致", len(mus), len(obs))
	}
	total := 0.0
	for j, o := range obs {
		var lp float64
		switch o.Kind {
		case ObsPct:
			lp = QuantizedLogProb(mus[j], o.Y, o.Step, o.Sigma)
		case ObsExact:
			lp = ExactLogProb(mus[j], o.Y, o.Sigma)
		case ObsWall:
			lp = WallLogProb(mus[j], o.Hit, o.Eps)
		default:
			return 0, fmt.Errorf("estimate: 未知观测类型 %q", o.Kind)
		}
		if math.IsNaN(lp) {
			return 0, fmt.Errorf("estimate: 第 %d 个观测的似然为 NaN（μ=%v y=%v s=%v σ=%v）",
				j, mus[j], o.Y, o.Step, o.Sigma)
		}
		total += lp
	}
	return total, nil
}

// logPosterior 组装负对数后验（在线点估计的目标函数）：
//
//	NLL(θ) = -Σ_j logP(y_j | μ_j(θ)) - Σ_i logPrior(θ_i)
//
// 常数项（与 θ 无关的归一化）省略。
func logPosterior(mus []float64, obs []ObsPoint, theta qdl.ParamPoint, priors map[string]*qdl.Distribution) (float64, error) {
	ll, err := logLikelihood(mus, obs)
	if err != nil {
		return 0, err
	}
	// 先验项按参数 ID 排序求和：map 迭代序随机 + 浮点加法不可结合会产出
	// ULP 级差异，量化似然窄谷里的优化器随之走不同线搜索路径（估计器必须
	// 逐位可复现——审计与 warm-start 测试的硬要求）。
	ids := make([]string, 0, len(theta))
	for id := range theta {
		if _, ok := priors[id]; ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		ll += PriorLogProb(priors[id], theta[id])
	}
	return ll, nil
}
