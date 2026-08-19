package qdl

// Dim 是计量维度分类学（Intent.md §1.3）。漏一维就建不出该厂商的模型，故必须穷尽。
// token 维必须细分：缓存写/读、推理 token 的权重与普通输入输出不同。
type Dim string

const (
	// 计数类（integer counting）。
	DimRequests Dim = "requests" // HTTP 请求数
	DimTurns    Dim = "turns"    // 用户轮次；一个 turn 可触发 20 个 requests（高价值结构未知数）
	DimSteps    Dim = "steps"    // agent 步 / 工具调用轮
	DimSessions Dim = "sessions" // 会话总数
	DimTasks    Dim = "tasks"    // 云端任务数（如 Codex cloud）

	// Token 类（按权重细分）。
	DimInputTokens      Dim = "input_tokens"       // 未缓存输入
	DimCacheWriteTokens Dim = "cache_write_tokens" // 写缓存（常 1.25×）
	DimCacheReadTokens  Dim = "cache_read_tokens"  // 读缓存（常 0.1×）
	DimOutputTokens     Dim = "output_tokens"
	DimReasoningTokens  Dim = "reasoning_tokens" // 独立维：常与 output 分开计价
	DimTotalTokens      Dim = "total_tokens"     // 仅厂商只暴露聚合量时使用（derived）

	// 时间/算力类。
	DimReasoningSeconds Dim = "reasoning_seconds" // Codex 5h 窗疑似按推理时间计量的证据
	DimWallSeconds      Dim = "wall_seconds"
	DimGPUSeconds       Dim = "gpu_seconds"

	// 货币类。
	DimCredits     Dim = "credits"      // 厂商虚拟币
	DimCurrencyUSD Dim = "currency_usd" // 真钱（overage / PAYG）

	// 多模态类。
	DimImagesIn        Dim = "images_in"
	DimImagePixelsIn   Dim = "image_pixels_in"
	DimImagesOut       Dim = "images_out"
	DimAudioSecondsIn  Dim = "audio_seconds_in"
	DimAudioSecondsOut Dim = "audio_seconds_out"
	DimVideoSeconds    Dim = "video_seconds"
	DimCharacters      Dim = "characters" // 部分中文厂商按字符计价，与 token 不可通约

	// 不透明类。
	DimOpaqueUnits Dim = "opaque_units" // 厂商内部加权分，唯一可观测是百分比；辨识工作的主战场
)

// InstantDim 是瞬时约束维（非累积！不是桶，是准入约束，进 AdmissionPolicy.Limits）。
type InstantDim string

const (
	InstantConcurrency       InstantDim = "concurrency"         // 并发请求数
	InstantContextTokensPeak InstantDim = "context_tokens_peak" // 单次上下文上限
	InstantMaxOutputTokens   InstantDim = "max_output_tokens"
	InstantMinIntervalMS     InstantDim = "min_interval_ms" // 相邻请求最小间隔
)

// dims 是全部合法计量维度（loader 校验未知维度用）。
var dims = map[Dim]bool{
	DimRequests: true, DimTurns: true, DimSteps: true, DimSessions: true, DimTasks: true,
	DimInputTokens: true, DimCacheWriteTokens: true, DimCacheReadTokens: true,
	DimOutputTokens: true, DimReasoningTokens: true, DimTotalTokens: true,
	DimReasoningSeconds: true, DimWallSeconds: true, DimGPUSeconds: true,
	DimCredits: true, DimCurrencyUSD: true,
	DimImagesIn: true, DimImagePixelsIn: true, DimImagesOut: true,
	DimAudioSecondsIn: true, DimAudioSecondsOut: true, DimVideoSeconds: true,
	DimCharacters: true, DimOpaqueUnits: true,
}

// Valid 报告 d 是否为已知计量维度。
func (d Dim) Valid() bool { return dims[d] }

// instantDims 是全部合法瞬时约束维度。
var instantDims = map[InstantDim]bool{
	InstantConcurrency: true, InstantContextTokensPeak: true,
	InstantMaxOutputTokens: true, InstantMinIntervalMS: true,
}

// Valid 报告 d 是否为已知瞬时约束维度。
func (d InstantDim) Valid() bool { return instantDims[d] }
