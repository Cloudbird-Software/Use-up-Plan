package semantics

import (
	"fmt"
	"math"
	"path"
	"strings"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// ChargeMode 是扣减的两种模式（Intent §3.2 关键工程分离，必须写进契约）：
//
//	EXACT     记账用：ceil / floor / max 全部精确应用
//	LINEAR_EV 规划用：量化替换为期望值，max 替换为线性上界
//
// 规划器（LP）只能用后者，记账器只能用前者。两者的差值累积成
// linearization_residual——ceil 效应在小请求高频场景可造成 20%+ 偏差
// （每请求 ceil 到 1k token 而平均请求 300 token ⇒ 实际消耗是名义的 3.3 倍），
// 这正是「LP 说还有 5% 余量、实际已经撞墙」诡异 bug 的来源。
type ChargeMode int

const (
	ChargeModeExact ChargeMode = iota
	ChargeModeLinearEV
)

// Request 是一次调用的原始计量量（Intent §3.3：dims 必须存原始物理量——
// 真实 token 数，不存已加权结果，这是重放的前提）。
type Request struct {
	ChannelID string // 走哪个通道
	Model     string // 厂商模型 ID（model_multiplier 的 glob 匹配对象）
	Effort    string // 努力级（effort_multiplier 的匹配对象）
	// Dims 是原始维度量（input_tokens=12345 之类）。缺失维度按 0 计。
	Dims map[qdl.Dim]float64
	// ContextTokensPeak 是上下文峰值（准入约束 context_tokens_peak 用，
	// 非累积维度，不进 Dims）。
	ContextTokensPeak float64
}

// ResolvedTerm 是求值后的加权项。
type ResolvedTerm struct {
	Dim      qdl.Dim
	W        float64 // 权重（已 resolve）
	Quantize qdl.Quantize
}

// ResolvedCharge 是 ChargeRule 的求值结果（与 ResolvedBucket 同层：Coeff
// 双态在此折算，charge 计算只做代数）。
type ResolvedCharge struct {
	Flat             float64
	Floor            float64
	Terms            []ResolvedTerm
	ModelMultiplier  map[string]float64 // pattern → 倍率（glob）
	EffortMultiplier map[string]float64
	Quantize         qdl.Quantize // 桶级量化
}

// ResolveCharge 把 ChargeRule 的全部 Coeff 按 θ 求值。
func ResolveCharge(r *qdl.ChargeRule, theta qdl.ParamPoint) (ResolvedCharge, error) {
	if r == nil {
		return ResolvedCharge{}, fmt.Errorf("semantics: ResolveCharge(nil)")
	}
	rc := ResolvedCharge{
		Quantize:         r.Quantize,
		ModelMultiplier:  map[string]float64{},
		EffortMultiplier: map[string]float64{},
	}
	var err error
	if rc.Flat, err = r.Flat.Resolve(theta); err != nil {
		return ResolvedCharge{}, fmt.Errorf("semantics: charge.flat: %w", err)
	}
	if rc.Floor, err = r.Floor.Resolve(theta); err != nil {
		return ResolvedCharge{}, fmt.Errorf("semantics: charge.floor: %w", err)
	}
	for _, t := range r.Terms {
		w, err := t.Coeff.Resolve(theta)
		if err != nil {
			return ResolvedCharge{}, fmt.Errorf("semantics: charge 项 %q: %w", t.Dim, err)
		}
		rc.Terms = append(rc.Terms, ResolvedTerm{Dim: t.Dim, W: w, Quantize: t.Quantize})
	}
	for pat, c := range r.ModelMultiplier {
		v, err := c.Resolve(theta)
		if err != nil {
			return ResolvedCharge{}, fmt.Errorf("semantics: charge.model_multiplier[%q]: %w", pat, err)
		}
		rc.ModelMultiplier[pat] = v
	}
	for e, c := range r.EffortMultiplier {
		v, err := c.Resolve(theta)
		if err != nil {
			return ResolvedCharge{}, fmt.Errorf("semantics: charge.effort_multiplier[%q]: %w", e, err)
		}
		rc.EffortMultiplier[e] = v
	}
	return rc, nil
}

// Charge 计算一次请求在全部命中桶上的扣减（Intent §3.2：给定完整请求，
// 返回每桶 Δ）。命中桶 = BucketSet：scope 匹配的桶同时扣减。
// 纯函数；dims 缺失按 0 计。
func Charge(spec *qdl.PlanSpec, req *Request, theta qdl.ParamPoint, mode ChargeMode) (map[string]float64, error) {
	if spec == nil || req == nil {
		return nil, fmt.Errorf("semantics: Charge(nil)")
	}
	out := map[string]float64{}
	for i := range spec.Buckets {
		b := &spec.Buckets[i]
		if !BucketMatches(b, req) {
			continue
		}
		rc, err := ResolveCharge(&b.Charge, theta)
		if err != nil {
			return nil, fmt.Errorf("semantics: 桶 %q: %w", b.ID, err)
		}
		out[b.ID] = ChargeOne(&rc, req, mode)
	}
	return out, nil
}

// ChargeOne 在单个桶上计算扣减（Intent §1.6 公式）：
//
//	EXACT:     q_b( max( m(model)·e(effort)·( flat + Σ w_d·q_d(x_d) ), floor ) )
//	LINEAR_EV: m·e·( flat + Σ w_d·ev_d(x_d) ) + floor        （仿射，LP 可解）
//
// 倍率乘在 (flat+Σ) 整体上——per-request 桶（flat=1、terms 空）的「高级模型
// 消耗更多配额」正由此生效；token 桶 flat=0 时退化为 Intent 原式 m·e·Σ。
// LINEAR_EV 的线性化：量化取期望（均匀假设下 E[ceil(X/s)·s] ≈ x+s/2），
// max(a, floor) 取线性上界 a+floor（floor=0 时退化为 a 本身）。
func ChargeOne(rc *ResolvedCharge, req *Request, mode ChargeMode) float64 {
	m := globBest(rc.ModelMultiplier, req.Model)
	e := globBest(rc.EffortMultiplier, req.Effort)
	sum := 0.0
	for _, t := range rc.Terms {
		x := req.Dims[t.Dim]
		if mode == ChargeModeExact {
			sum += t.W * t.Quantize.Apply(x)
		} else {
			sum += t.W * quantizeEV(t.Quantize, x)
		}
	}
	if mode == ChargeModeExact {
		raw := m * e * (rc.Flat + sum)
		return rc.Quantize.Apply(math.Max(raw, rc.Floor))
	}
	// LINEAR_EV：floor 常数项移入（线性上界 m·e·(flat+Σ) + floor），不做 max。
	return m*e*(rc.Flat+sum) + rc.Floor
}

// ChargeUpperBound 是 EXACT 模式的严格上界（admit 的撞墙风险评估用）：
// 量化取上界（ceil 本身是上界、floor 取下界时的期望）、max 取上界 raw+floor。
func ChargeUpperBound(rc *ResolvedCharge, req *Request) float64 {
	m := globBest(rc.ModelMultiplier, req.Model)
	e := globBest(rc.EffortMultiplier, req.Effort)
	sum := 0.0
	for _, t := range rc.Terms {
		sum += t.W * quantizeUB(t.Quantize, req.Dims[t.Dim])
	}
	raw := m * e * (rc.Flat + sum)
	ub := raw + rc.Floor // max(raw, floor) <= raw+floor（非负假设）
	return quantizeUB(rc.Quantize, ub)
}

// quantizeEV 是量化函数的期望值线性近似（均匀假设）：
// ceil → x+s/2；floor → max(0, x-s/2)；round → x；none → x。
func quantizeEV(q qdl.Quantize, x float64) float64 {
	if q.Mode == qdl.QuantizeNone || q.Step <= 0 {
		return x
	}
	switch q.Mode {
	case qdl.QuantizeCeil:
		return x + q.Step/2
	case qdl.QuantizeFloor:
		return math.Max(0, x-q.Step/2)
	default: // round
		return x
	}
}

// quantizeUB 是量化函数的上界：ceil/round → x+s；floor → x；none → x。
func quantizeUB(q qdl.Quantize, x float64) float64 {
	if q.Mode == qdl.QuantizeNone || q.Step <= 0 {
		return x
	}
	switch q.Mode {
	case qdl.QuantizeCeil, qdl.QuantizeRound:
		return x + q.Step
	default: // floor
		return x
	}
}

// globBest 在 pattern→倍率表里找匹配 key 的最长 pattern 的倍率；无匹配为 1。
func globBest(table map[string]float64, key string) float64 {
	best, bestPat := 1.0, ""
	for pat, v := range table {
		if ok, err := path.Match(pat, key); err == nil && ok && len(pat) > len(bestPat) {
			best, bestPat = v, pat
		}
	}
	return best
}

// BucketMatches 判定请求是否命中桶（BucketSet 成员）：scope 的模型/模型族/
// 通道/努力级过滤全部通过（nil = 不过滤；模型支持 glob，模型族按厂商模型 ID
// 前缀匹配——"claude-sonnet" 族覆盖 "claude-sonnet-4-6"）。
func BucketMatches(b *qdl.Bucket, req *Request) bool {
	if len(b.Scope.Models) > 0 && !globMatchAny(b.Scope.Models, req.Model) {
		return false
	}
	if len(b.Scope.ModelFamilies) > 0 && !familyMatchAny(b.Scope.ModelFamilies, req.Model) {
		return false
	}
	if len(b.Scope.Channels) > 0 && !contains(b.Scope.Channels, req.ChannelID) {
		return false
	}
	if len(b.Scope.EffortTiers) > 0 && !contains(b.Scope.EffortTiers, req.Effort) {
		return false
	}
	return true
}

// familyMatchAny 报告厂商模型 ID 是否属于某个模型族（前缀语义：
// 族名是模型 ID 的第一段前缀，如 claude-sonnet → claude-sonnet-4-6）。
func familyMatchAny(families []string, model string) bool {
	for _, f := range families {
		if strings.HasPrefix(model, f) {
			return true
		}
	}
	return false
}

func globMatchAny(patterns []string, s string) bool {
	for _, p := range patterns {
		if ok, err := path.Match(p, s); err == nil && ok {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
