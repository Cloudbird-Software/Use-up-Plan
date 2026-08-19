package qdl

import "time"

// WindowKind 是窗口语义分类学（Intent.md §1.4）。结构决定套利是否存在
// （如 anchored tumbling 有窗口边界套利，true sliding 没有），故结构本身
// 是待辨识的离散未知量，不写死单值。
type WindowKind string

const (
	WindowTumblingAnchoredOnFirstUse WindowKind = "tumbling_anchored_on_first_use" // 首次请求起锚，到期整体归零（Claude 5h?）
	WindowTumblingAccountAnchored    WindowKind = "tumbling_account_anchored"      // 账号固定重置时刻（Claude 周窗 Pro/Max?）
	WindowTumblingCalendar           WindowKind = "tumbling_calendar"              // 日历对齐（多数免费档 RPD）
	WindowSlidingExact               WindowKind = "sliding_exact"                  // 真滚动：每笔消耗独立过期
	WindowTokenBucketContinuous      WindowKind = "token_bucket_continuous"        // 连续回补（多数 RPM/TPM 的真实实现，允许 burst）
	WindowBillingCycle               WindowKind = "billing_cycle"
	WindowOneShotExpiring            WindowKind = "one_shot_expiring" // 一次性额度 + 绝对过期时刻
	WindowNever                      WindowKind = "never"             // 纯余额，不重置
)

// ResetPolicy 是重置语义（与窗口类型正交）。
type ResetPolicy string

const (
	ResetZero             ResetPolicy = "zero" // 归零
	ResetRefillToFull     ResetPolicy = "refill_to_full"
	ResetRolloverCapped   ResetPolicy = "rollover_capped" // 结转，上限 = k × 周期额度
	ResetRolloverUncapped ResetPolicy = "rollover_uncapped"
	ResetDecayExponential ResetPolicy = "decay_exponential"
)

// Window 描述一个桶的时间语义。KindCandidates 必须不少于 1 个；
// KindPosterior 为结构辨识（estimate 模块）回写的后验。
type Window struct {
	KindCandidates      []WindowKind
	KindPosterior       map[WindowKind]float64 // kind -> 概率；nil = 尚未辨识
	Length              time.Duration          // 窗长；token_bucket/never 可为零值
	AnchorUTC           string                 // 账号锚点，如 "WED 20:00"；"UNKNOWN" = 待从 resets_at 序列反推
	CalendarAlign       string                 // utc_midnight | local_midnight | billing_day
	RefillRate          Coeff                  // token_bucket：单位/秒
	Burst               Coeff                  // token_bucket：突发容量
	ExpiresAt           *time.Time             // one_shot 的绝对过期时刻
	Reset               ResetPolicy
	RolloverCapMultiple *float64 // rollover_capped 的 k
}

// Kind 返回窗口语义的 MAP 估计：后验概率最大者；无后验时取候选首位
// （先验下保守默认）。semantics.advance 按它分派。
func (w *Window) Kind() WindowKind {
	if len(w.KindPosterior) > 0 {
		best, bestP := w.KindCandidates[0], -1.0
		for k, p := range w.KindPosterior {
			if p > bestP {
				best, bestP = k, p
			}
		}
		return best
	}
	if len(w.KindCandidates) == 0 {
		return ""
	}
	return w.KindCandidates[0]
}
