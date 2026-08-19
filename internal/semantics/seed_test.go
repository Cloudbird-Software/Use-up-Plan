package semantics

import (
	"path/filepath"
	"testing"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// seedPlanPath 解析仓库根 qdl/plans/ 下的种子 plan 路径（跨模块 golden：
// 种子文件是 qdl 与 semantics 的共同契约面，两侧都有准入测试）。
func seedPlanPath(t *testing.T, rel ...string) string {
	t.Helper()
	parts := append([]string{"..", "..", "qdl", "plans"}, rel...)
	return filepath.Join(parts...)
}

// TestSeedPlanAnthropicBucketSet 验证 Max20 的 BucketSet 命中：
// 共享池桶（b_5h/b_7d_all）对全模型生效，模型族周窗只对本族模型生效。
func TestSeedPlanAnthropicBucketSet(t *testing.T) {
	spec, err := qdl.Load(seedPlanPath(t, "anthropic", "max20@2026-08.qdl.yaml"))
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	req := &Request{ChannelID: "claude_code_oauth", Model: "claude-sonnet-4-6"}
	hit := hitBucketIDs(spec, req)
	// 共享池 ×2 + Sonnet 族专用窗 + 加购 credits 桶（无过滤、按 currency_usd 计扣）
	want := map[string]bool{"b_5h": true, "b_7d_all": true, "b_7d_sonnet": true, "b_extra_credits": true}
	if len(hit) != len(want) {
		t.Fatalf("sonnet 命中桶: %v", hit)
	}
	for id := range want {
		if !hit[id] {
			t.Fatalf("sonnet 应命中 %q: %v", id, hit)
		}
	}
	req.Model = "claude-opus-4-6"
	hit = hitBucketIDs(spec, req)
	if !hit["b_7d_opus"] || hit["b_7d_sonnet"] {
		t.Fatalf("opus 命中桶（族过滤）: %v", hit)
	}
	req.Model = "claude-haiku-4-5"
	hit = hitBucketIDs(spec, req)
	if hit["b_7d_sonnet"] || hit["b_7d_opus"] {
		t.Fatalf("haiku 不应命中族专用窗: %v", hit)
	}
	if !hit["b_5h"] || !hit["b_7d_all"] {
		t.Fatalf("haiku 应命中共享池: %v", hit)
	}
}

// TestSeedPlanAnthropicCharge 验证 gauge 锚定下的扣减代数：
// opaque_units ≡ API 美元，θ 取「订阅价 = API 价 + 缓存全额折扣」。
func TestSeedPlanAnthropicCharge(t *testing.T) {
	spec, err := qdl.Load(seedPlanPath(t, "anthropic", "max20@2026-08.qdl.yaml"))
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	// θ：mult_opus=1、mult_sonnet=1、mult_haiku=0.25、w_cache_read=3e-7（API 折扣）
	theta := qdl.ParamPoint{
		"anthropic.max20.mult_opus":    1.0,
		"anthropic.max20.mult_sonnet":  1.0,
		"anthropic.max20.mult_haiku":   0.25,
		"anthropic.max20.w_cache_read": 3.0e-7,
	}
	req := &Request{
		ChannelID: "claude_code_oauth", Model: "claude-sonnet-4-6",
		Dims: map[qdl.Dim]float64{
			qdl.DimInputTokens:     1_000_000, // $3.00
			qdl.DimCacheReadTokens: 1_000_000, // $0.30（ratio 0.1）
			qdl.DimOutputTokens:    100_000,   // $1.50
			qdl.DimReasoningTokens: 50_000,    // $0.75
		},
	}
	got, err := Charge(spec, req, theta, ChargeModeExact)
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	// b_5h = 3.00 + 0.30 + 1.50 + 0.75 = 5.55 等价 API 美元
	if d := got["b_5h"] - 5.55; d > 1e-9 || d < -1e-9 {
		t.Fatalf("b_5h 扣减: %v（want 5.55）", got["b_5h"])
	}
	// $ref 共享 charge：周窗同步扣同额
	if got["b_7d_all"] != got["b_5h"] || got["b_7d_sonnet"] != got["b_5h"] {
		t.Fatalf("共享 charge 应同步扣减: %+v", got)
	}
}

// TestSeedPlanGLMPerRequestCharge 验证结构套利的另一半：per-request 桶
// 扣减与 token 数完全无关（terms 空 + flat=1），只乘模型倍率。
func TestSeedPlanGLMPerRequestCharge(t *testing.T) {
	spec, err := qdl.Load(seedPlanPath(t, "zai", "glm-coding-max@2026-08.qdl.yaml"))
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	theta := qdl.ParamPoint{"zai.max.mult_advanced": 2.0}
	// 巨大的上下文也不能让 per-request 桶多扣一个单位
	req := &Request{
		ChannelID: "glm_anthropic_compat", Model: "glm-5.1",
		Dims: map[qdl.Dim]float64{
			qdl.DimInputTokens:  900_000,
			qdl.DimOutputTokens: 100_000,
		},
	}
	got, err := Charge(spec, req, theta, ChargeModeExact)
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if got["b_5h_prompts"] != 2.0 { // flat 1 × mult_advanced 2
		t.Fatalf("高级模型每请求扣 2: %v", got["b_5h_prompts"])
	}
	req.Model = "glm-5-turbo"
	got, _ = Charge(spec, req, theta, ChargeModeExact)
	if got["b_5h_prompts"] != 1.0 {
		t.Fatalf("turbo 每请求扣 1: %v", got["b_5h_prompts"])
	}
	// LINEAR_EV 同值（无量化无 floor，两模式恒等——per-request 桶天然无残差）
	ev, _ := Charge(spec, req, theta, ChargeModeLinearEV)
	if ev["b_5h_prompts"] != got["b_5h_prompts"] {
		t.Fatalf("per-request 桶两模式应恒等: exact=%v ev=%v", got, ev)
	}
}

// TestSeedPlanFreeTierAdmit 验证免费档多维硬桶的准入：RPM 桶满 → DENY_QUOTA。
func TestSeedPlanFreeTierAdmit(t *testing.T) {
	spec, err := qdl.Load(seedPlanPath(t, "free", "_template.qdl.yaml"))
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	theta := qdl.ParamPoint{
		"generic.free.rpm":        10,
		"generic.free.rpm_refill": 10.0 / 60,
		"generic.free.rpd":        200,
		"generic.free.tpm":        100_000,
		"generic.free.tpm_refill": 100_000.0 / 60,
	}
	req := &Request{
		ChannelID: "free_openai_compat", Model: "some-model",
		Dims: map[qdl.Dim]float64{qdl.DimRequests: 1},
	}
	// RPM 桶已耗 9/10：第 10 个请求仍 ALLOW（remaining 1 ≥ EV 1）
	st := &SystemState{Buckets: map[string]BucketState{
		"b_rpm": {U: 9}, "b_rpd": {U: 0}, "b_tpm": {U: 0},
	}}
	a, err := Admit(spec, st, req, theta, &AdmissionContext{Concurrency: map[string]int{}})
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if a.Decision != AdmitAllow {
		t.Fatalf("第 10 个请求应 ALLOW: %+v", a)
	}
	// 耗尽后第 11 个 → DENY_QUOTA
	st.Buckets["b_rpm"] = BucketState{U: 10}
	a, _ = Admit(spec, st, req, theta, &AdmissionContext{Concurrency: map[string]int{}})
	if a.Decision != AdmitDenyQuota {
		t.Fatalf("超 RPM 应 DENY_QUOTA: %+v", a)
	}
	// 并发准入：免费档 concurrency=1，已占用 1 → DENY_ADMISSION（优先于桶状态）
	st.Buckets["b_rpm"] = BucketState{U: 0}
	a, _ = Admit(spec, st, req, theta, &AdmissionContext{Concurrency: map[string]int{"free_openai_compat": 1}})
	if a.Decision != AdmitDenyAdmission {
		t.Fatalf("并发满应 DENY_ADMISSION: %+v", a)
	}
}

// hitBucketIDs 返回请求命中的全部桶 ID（BucketSet 语义的观测窗）。
func hitBucketIDs(spec *qdl.PlanSpec, req *Request) map[string]bool {
	hit := map[string]bool{}
	for i := range spec.Buckets {
		if BucketMatches(&spec.Buckets[i], req) {
			hit[spec.Buckets[i].ID] = true
		}
	}
	return hit
}
