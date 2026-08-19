package qdl

// Reliability 是通道可靠性档案。免费档的核心属性不是价格是可中断性：
// mid_stream 中断 + 不可续写 ⇒ 只有幂等、可丢弃、可重跑的任务类能路由到这里
// （LP 的硬约束，不是启发式）。
type Reliability struct {
	InterruptionHazardPerHour float64  `yaml:"interruption_hazard_per_hour"`
	InterruptionGranularity   string   `yaml:"interruption_granularity"` // mid_stream | between_requests
	ResumeSupported           bool     `yaml:"resume_supported"`
	LatencyP50MS              *float64 `yaml:"latency_p50_ms"`
	LatencyP99MS              *float64 `yaml:"latency_p99_ms"`
}

// SpoofingSpec 声明通道需要伪装成官方客户端的细节及 ToS 风险注记。
type SpoofingSpec struct {
	UserAgent                  string            `yaml:"user_agent"`
	RequiredHeaders            map[string]string `yaml:"required_headers"`
	SystemPromptPrefixRequired bool              `yaml:"system_prompt_prefix_required"`
	TOSNote                    string            `yaml:"tos_note"`
}

// ModelBinding 把内部逻辑模型名映射到厂商模型 ID，并携带能力与质量分。
type ModelBinding struct {
	LogicalModel  string             `yaml:"logical_model"` // 内部统一名，如 "tier1-reasoner"
	VendorModelID string             `yaml:"vendor_model_id"`
	Family        string             `yaml:"family"`
	Aliases       []string           `yaml:"aliases"`
	Capabilities  []string           `yaml:"capabilities"` // tool_use / vision / caching ...
	Tokenizer     string             `yaml:"tokenizer"`    // 跨厂商 token 归一用（token 密度差 20–30%）
	ContextWindow *int               `yaml:"context_window"`
	QualityScores map[string]float64 `yaml:"quality_scores"` // 私有 eval 结果 → 进 LP 价值函数 v_jk
}

// AdmissionPolicy 是通道准入约束（非累积的瞬时约束在此，不建桶）。
type AdmissionPolicy struct {
	Limits               map[InstantDim]Coeff `yaml:"limits"`         // concurrency / context_tokens_peak / ...
	AllowedModels        []string             `yaml:"allowed_models"` // nil = 全部
	DeniedModels         []string             `yaml:"denied_models"`
	RequiredCapabilities []string             `yaml:"required_capabilities"`
	ForbiddenFeatures    []string             `yaml:"forbidden_features"`
}

// Channel 是一个调用通道（CLI OAuth / OpenAI 兼容端点 / web）。
type Channel struct {
	ID               string          `yaml:"id"`
	Protocol         string          `yaml:"protocol"` // anthropic_messages | openai_chat | openai_responses | gemini | custom_cli | web
	BaseURL          string          `yaml:"base_url"`
	Auth             string          `yaml:"auth"` // oauth_bearer | api_key | cookie_session
	Models           []ModelBinding  `yaml:"models"`
	Admission        AdmissionPolicy `yaml:"admission"`
	Reliability      Reliability     `yaml:"reliability"`
	SpoofingRequired *SpoofingSpec   `yaml:"spoofing_required"`
}
