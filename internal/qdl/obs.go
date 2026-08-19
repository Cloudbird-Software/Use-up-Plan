package qdl

// ObsSource 是观测通道。三通道信息架构（Intent §5.1，优先级不可颠倒）：
// 响应头/usage 字段（主，零成本零延迟）→ usage endpoint（次，低频轮询）→ 网页 DOM（末，每日一次）。
type ObsSource string

const (
	ObsResponseHeader ObsSource = "response_header"
	ObsUsageEndpoint  ObsSource = "usage_endpoint"
	ObsErrorBody      ObsSource = "error_body"
	ObsLocalLog       ObsSource = "local_log"
	ObsWebDOM         ObsSource = "web_dom"
	ObsSDKField       ObsSource = "sdk_field"
)

// Semantic 是观测量语义（同一原始数字在不同字段含义完全不同）。
type Semantic string

const (
	SemUsedPct        Semantic = "used_pct"
	SemRemainingPct   Semantic = "remaining_pct"
	SemUsedAbs        Semantic = "used_abs"
	SemRemainingAbs   Semantic = "remaining_abs"
	SemLimitAbs       Semantic = "limit_abs"
	SemResetAtEpochMS Semantic = "reset_at_epoch_ms"
	SemResetAtISO     Semantic = "reset_at_iso"
	SemResetAfterS    Semantic = "reset_after_s"
	SemWindowMinutes  Semantic = "window_minutes"
	SemReason         Semantic = "reason"
	SemPlanType       Semantic = "plan_type"
)

// Quantization 是观测值的量化精度（量化似然的步长 s：整数百分比 s=1.0，
// Codex 一位小数 s=0.1——后者的单位观测信息量高 10 倍，决定了先做 Codex）。
type Quantization struct {
	Kind     string // exact | integer | decimals | unknown
	Decimals *int
}

// ObsBinding 声明「一个桶的某观测量从哪里读、语义是什么、精度多少」。
// collect 模块按此声明式解析；观测是辨识的原料。
type ObsBinding struct {
	Source          ObsSource
	Locator         string // header 名 / jsonpath / regex / css selector / 本地日志 glob
	Semantic        Semantic
	Quantization    Quantization
	AttributionLagS float64 // 归因延迟（记账时间戳 ≠ 消耗时间戳的错位窗口）
	Trust           float64 // 0..1，观测噪声权重
	URL             string  // usage_endpoint 专用
	Auth            string  // oauth_bearer | api_key | cookie
	ExtraHeaders    map[string]string
	PollIntervalS   *int
}
