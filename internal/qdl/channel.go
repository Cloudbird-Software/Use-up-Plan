package qdl

// Reliability 是通道可靠性档案。免费档的核心属性不是价格是可中断性：
// mid_stream 中断 + 不可续写 ⇒ 只有幂等、可丢弃、可重跑的任务类能路由到这里
// （LP 的硬约束，不是启发式）。
type Reliability struct {
	InterruptionHazardPerHour float64
	InterruptionGranularity   string // mid_stream | between_requests
	ResumeSupported           bool
	LatencyP50MS              *float64
	LatencyP99MS              *float64
}

// SpoofingSpec 声明通道需要伪装成官方客户端的细节及 ToS 风险注记。
type SpoofingSpec struct {
	UserAgent                  string
	RequiredHeaders            map[string]string
	SystemPromptPrefixRequired bool
	TOSNote                    string
}

// ModelBinding 把内部逻辑模型名映射到厂商模型 ID，并携带能力与质量分。
type ModelBinding struct {
	LogicalModel  string // 内部统一名，如 "tier1-reasoner"
	VendorModelID string
	Family        string
	Aliases       []string
	Capabilities  []string // tool_use / vision / caching ...
	Tokenizer     string   // 跨厂商 token 归一用（token 密度差 20–30%）
	ContextWindow *int
	QualityScores map[string]float64 // 私有 eval 结果 → 进 LP 价值函数 v_jk
}

// AdmissionPolicy 是通道准入约束（非累积的瞬时约束在此，不建桶）。
type AdmissionPolicy struct {
	Limits               map[InstantDim]Coeff // concurrency / context_tokens_peak / ...
	AllowedModels        []string             // nil = 全部
	DeniedModels         []string
	RequiredCapabilities []string
	ForbiddenFeatures    []string
}

// Channel 是一个调用通道（CLI OAuth / OpenAI 兼容端点 / web）。
type Channel struct {
	ID               string
	Protocol         string // anthropic_messages | openai_chat | openai_responses | gemini | custom_cli | web
	BaseURL          string
	Auth             string // oauth_bearer | api_key | cookie_session
	Models           []ModelBinding
	Admission        AdmissionPolicy
	Reliability      Reliability
	SpoofingRequired *SpoofingSpec
}
