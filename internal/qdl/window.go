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
	KindCandidates      []WindowKind           `yaml:"kind_candidates,omitempty"`
	KindPosterior       map[WindowKind]float64 `yaml:"kind_posterior,omitempty"` // kind -> 概率；nil = 尚未辨识
	Length              Duration               `yaml:"length"`                   // 窗长（ISO 8601 或 Go 原生时长）；token_bucket/never 可为零值
	AnchorUTC           string                 `yaml:"anchor_utc"`               // 账号锚点，如 "WED 20:00"；"UNKNOWN" = 待从 resets_at 序列反推
	CalendarAlign       string                 `yaml:"calendar_align"`           // utc_midnight | local_midnight | billing_day
	RefillRate          Coeff                  `yaml:"refill_rate"`              // token_bucket：单位/秒
	Burst               Coeff                  `yaml:"burst"`                    // token_bucket：突发容量
	ExpiresAt           *time.Time             `yaml:"expires_at"`               // one_shot 的绝对过期时刻
	Reset               ResetPolicy            `yaml:"reset"`
	RolloverCapMultiple *float64               `yaml:"rollover_cap_multiple"` // rollover_capped 的 k
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

// UnmarshalYAML 应用 Pydantic 等价缺省：reset 缺省 zero；refill_rate/burst
// 缺省 Const(0)（非 token_bucket 窗口无回补语义）。后验键为字符串、此处转
// WindowKind（未知键由 Validate 的封闭集校验拒绝）。
func (w *Window) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		KindCandidates      []WindowKind       `yaml:"kind_candidates"`
		KindPosterior       map[string]float64 `yaml:"kind_posterior"`
		Length              Duration           `yaml:"length"`
		AnchorUTC           string             `yaml:"anchor_utc"`
		CalendarAlign       string             `yaml:"calendar_align"`
		RefillRate          *Coeff             `yaml:"refill_rate"`
		Burst               *Coeff             `yaml:"burst"`
		ExpiresAt           *time.Time         `yaml:"expires_at"`
		Reset               ResetPolicy        `yaml:"reset"`
		RolloverCapMultiple *float64           `yaml:"rollover_cap_multiple"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	w.KindCandidates = raw.KindCandidates
	if len(raw.KindPosterior) > 0 {
		w.KindPosterior = make(map[WindowKind]float64, len(raw.KindPosterior))
		for k, v := range raw.KindPosterior {
			w.KindPosterior[WindowKind(k)] = v
		}
	}
	w.Length = raw.Length
	w.AnchorUTC = raw.AnchorUTC
	w.CalendarAlign = raw.CalendarAlign
	w.RefillRate = Const(0)
	if raw.RefillRate != nil {
		w.RefillRate = *raw.RefillRate
	}
	w.Burst = Const(0)
	if raw.Burst != nil {
		w.Burst = *raw.Burst
	}
	w.ExpiresAt = raw.ExpiresAt
	w.Reset = raw.Reset
	if w.Reset == "" {
		w.Reset = ResetZero
	}
	w.RolloverCapMultiple = raw.RolloverCapMultiple
	return nil
}
