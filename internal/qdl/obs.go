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
	Kind     string `yaml:"kind" json:"kind"` // exact | integer | decimals | unknown
	Decimals *int   `yaml:"decimals" json:"decimals,omitempty"`
}

// ObsBinding 声明「一个桶的某观测量从哪里读、语义是什么、精度多少」。
// collect 模块按此声明式解析；观测是辨识的原料。
type ObsBinding struct {
	Source          ObsSource         `yaml:"source"`
	Locator         string            `yaml:"locator"` // header 名 / jsonpath / regex / css selector / 本地日志 glob
	Semantic        Semantic          `yaml:"semantic"`
	Quantization    Quantization      `yaml:"quantization"`
	AttributionLagS float64           `yaml:"attribution_lag_s"` // 归因延迟（记账时间戳 ≠ 消耗时间戳的错位窗口）
	Trust           float64           `yaml:"trust"`             // 0..1，观测噪声权重
	URL             string            `yaml:"url"`               // usage_endpoint 专用
	Auth            string            `yaml:"auth"`              // oauth_bearer | api_key | cookie
	ExtraHeaders    map[string]string `yaml:"extra_headers,omitempty"`
	PollIntervalS   *int              `yaml:"poll_interval_s"`
}

// UnmarshalYAML 应用 Pydantic 等价缺省（Intent §2.1 ObsBinding）：
// trust 缺省 1.0（显式 0 合法保留——「该观测源是垃圾」是真实语义）；
// quantization.kind 缺省 unknown。
func (o *ObsBinding) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Source          ObsSource         `yaml:"source"`
		Locator         string            `yaml:"locator"`
		Semantic        Semantic          `yaml:"semantic"`
		Quantization    Quantization      `yaml:"quantization"`
		AttributionLagS float64           `yaml:"attribution_lag_s"`
		Trust           *float64          `yaml:"trust"`
		URL             string            `yaml:"url"`
		Auth            string            `yaml:"auth"`
		ExtraHeaders    map[string]string `yaml:"extra_headers,omitempty"`
		PollIntervalS   *int              `yaml:"poll_interval_s"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	o.Source = raw.Source
	o.Locator = raw.Locator
	o.Semantic = raw.Semantic
	o.Quantization = raw.Quantization
	if o.Quantization.Kind == "" {
		o.Quantization.Kind = "unknown"
	}
	o.AttributionLagS = raw.AttributionLagS
	o.Trust = 1.0
	if raw.Trust != nil {
		o.Trust = *raw.Trust
	}
	o.URL = raw.URL
	o.Auth = raw.Auth
	o.ExtraHeaders = raw.ExtraHeaders
	o.PollIntervalS = raw.PollIntervalS
	return nil
}

// UnmarshalYAML 应用缺省：quantization.kind 缺省 unknown。
func (q *Quantization) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Kind     string `yaml:"kind"`
		Decimals *int   `yaml:"decimals"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	q.Kind = raw.Kind
	if q.Kind == "" {
		q.Kind = "unknown"
	}
	q.Decimals = raw.Decimals
	return nil
}
