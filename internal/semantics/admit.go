package semantics

import (
	"fmt"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// AdmissionDecision 是准入三态（Intent §3.2）。
type AdmissionDecision string

const (
	AdmitAllow         AdmissionDecision = "ALLOW"
	AdmitDenyAdmission AdmissionDecision = "DENY_ADMISSION"  // 违反准入（context 超长、并发满）→ 换桶或改请求
	AdmitDenyQuota     AdmissionDecision = "DENY_QUOTA"      // 桶满 → 换桶，并写入撞墙观测（高价值辨识数据）
	AdmitAllowWithRisk AdmissionDecision = "ALLOW_WITH_RISK" // 预测流中途可能撞墙（免费档正常工作模式）
)

// Admission 是 admit 的结果。DENY 时 Reason 说明哪个约束/桶拒绝；
// DENY_QUOTA 带 RetryAfter（到下一重置时刻的距离）；ALLOW_WITH_RISK 带
// PBreak（流中途撞墙概率估计，由 EV 与上界之差归一）。
type Admission struct {
	Decision   AdmissionDecision
	Reason     string
	RetryAfter *time.Duration
	PBreak     float64
}

// AdmissionContext 是 admit 所需的运行时快照（深接口：显式输入，无隐藏状态）。
// 桶状态必须已 advance 到 Now（调用方职责——admit 不做时间推进）。
type AdmissionContext struct {
	Now time.Time
	// Concurrency 是 channel_id → 当前并发数（瞬时约束，非累积）。
	Concurrency map[string]int
}

// Admit 判定「能否发这个请求，发了会不会撞墙」（Intent §3.2）。
// 三种拒绝/风险态的优先级：DENY_ADMISSION > DENY_QUOTA > ALLOW_WITH_RISK > ALLOW；
// 多桶（BucketSet）取最严格结果，p_break 取最大。
func Admit(spec *qdl.PlanSpec, state *SystemState, req *Request,
	theta qdl.ParamPoint, actx *AdmissionContext) (Admission, error) {
	if spec == nil || state == nil || req == nil || actx == nil {
		return Admission{}, fmt.Errorf("semantics: Admit(nil)")
	}
	if a := checkAdmissionPolicy(spec, req, theta, actx); a.Decision != AdmitAllow {
		return a, nil
	}

	result := Admission{Decision: AdmitAllow}
	for i := range spec.Buckets {
		b := &spec.Buckets[i]
		if !BucketMatches(b, req) {
			continue
		}
		rb, err := ResolveBucket(b, theta)
		if err != nil {
			return Admission{}, fmt.Errorf("semantics: 桶 %q: %w", b.ID, err)
		}
		rc, err := ResolveCharge(&b.Charge, theta)
		if err != nil {
			return Admission{}, fmt.Errorf("semantics: 桶 %q: %w", b.ID, err)
		}
		u := 0.0
		if bs, ok := state.Buckets[b.ID]; ok {
			u = bs.U
		}
		remaining := rb.Capacity - u
		needEV := ChargeOne(&rc, req, ChargeModeLinearEV)
		needUB := ChargeUpperBound(&rc, req)

		switch {
		case remaining < needEV && needEV > 0:
			// 必撞墙：按窗型给出到下一重置的等待
			retry := retryAfter(&rb, actx.Now)
			return Admission{
				Decision:   AdmitDenyQuota,
				Reason:     fmt.Sprintf("桶 %q 剩余 %v 不足以覆盖期望扣减 %v", b.ID, remaining, needEV),
				RetryAfter: retry,
			}, nil
		case remaining < needUB:
			// 可能撞墙：p_break = 落入 (remaining, UB] 的比例（EV..UB 区间均匀假设）
			p := (needUB - remaining) / (needUB - needEV)
			if needUB == needEV {
				p = 1
			}
			if p > result.PBreak {
				result.PBreak = p
				result.Decision = AdmitAllowWithRisk
				result.Reason = fmt.Sprintf("桶 %q 剩余 %v 低于扣减上界 %v", b.ID, remaining, needUB)
			}
		}
	}
	return result, nil
}

// checkAdmissionPolicy 检查通道准入（瞬时约束）：并发、上下文峰值、
// 模型允许/禁止清单。未找到通道 = 无准入约束（spec 校验外的宽容缺省）。
func checkAdmissionPolicy(spec *qdl.PlanSpec, req *Request, theta qdl.ParamPoint, actx *AdmissionContext) Admission {
	ch := spec.Channel(req.ChannelID)
	if ch == nil {
		return Admission{Decision: AdmitAllow}
	}
	apol := &ch.Admission
	limit := func(d qdl.InstantDim) (float64, bool) {
		c, ok := apol.Limits[d]
		if !ok {
			return 0, false
		}
		v, err := c.Resolve(theta)
		if err != nil {
			return 0, false // 引用无法解析时跳过该约束（Resolve 层已有显式报错路径）
		}
		return v, true
	}
	if lim, ok := limit(qdl.InstantConcurrency); ok {
		cur := actx.Concurrency[req.ChannelID]
		if float64(cur+1) > lim {
			return Admission{
				Decision: AdmitDenyAdmission,
				Reason:   fmt.Sprintf("通道 %q 并发 %d+1 超上限 %v", req.ChannelID, cur, lim),
			}
		}
	}
	if lim, ok := limit(qdl.InstantContextTokensPeak); ok && req.ContextTokensPeak > lim {
		return Admission{
			Decision: AdmitDenyAdmission,
			Reason: fmt.Sprintf("通道 %q 上下文峰值 %v 超上限 %v",
				req.ChannelID, req.ContextTokensPeak, lim),
		}
	}
	if len(apol.DeniedModels) > 0 && contains(apol.DeniedModels, req.Model) {
		return Admission{Decision: AdmitDenyAdmission, Reason: "模型在 denied_models"}
	}
	if len(apol.AllowedModels) > 0 && !contains(apol.AllowedModels, req.Model) {
		return Admission{Decision: AdmitDenyAdmission, Reason: "模型不在 allowed_models"}
	}
	return Admission{Decision: AdmitAllow}
}

// retryAfter 尽力计算到下一重置时刻的距离（DENY_QUOTA 的 retry 提示）。
// 周期锚定窗取下一重置时刻；sliding 取最早过期明细；其余不可推算返回 nil。
func retryAfter(rb *ResolvedBucket, now time.Time) *time.Duration {
	switch rb.Kind {
	case qdl.WindowTumblingAccountAnchored, qdl.WindowTumblingCalendar, qdl.WindowBillingCycle:
		if !rb.HasAnchor || rb.Length <= 0 {
			return nil
		}
		next := rb.Anchor0.Add(time.Duration(kOf(rb, now)+1) * rb.Length)
		d := next.Sub(now)
		return &d
	case qdl.WindowSlidingExact:
		// 逐笔过期没有统一重置时刻；最早明细过期即释放部分容量。
		// 此处不深入明细（admit 的 state 只带聚合 u），返回 nil 由上层
		// 按明细状态自行精算。
		return nil
	default:
		return nil
	}
}
