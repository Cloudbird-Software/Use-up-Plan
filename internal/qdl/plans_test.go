package qdl

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// seedPlans 是全部种子 plan 的登记表（Intent §2.2–§2.4 原型落地）。
// 新增种子 plan 必须在此登记——加载 golden 是种子文件的准入契约。
var seedPlans = []string{
	filepath.Join("..", "..", "qdl", "plans", "anthropic", "max20@2026-08.qdl.yaml"),
	filepath.Join("..", "..", "qdl", "plans", "zai", "glm-coding-max@2026-08.qdl.yaml"),
	filepath.Join("..", "..", "qdl", "plans", "free", "_template.qdl.yaml"),
}

// TestSeedPlansLoad 加载 golden：三个种子 plan 必须原样通过完整加载管线
// （$ref 展开 → 严格解码 → 缺省规范化 → 安全契约校验）。
func TestSeedPlansLoad(t *testing.T) {
	for _, path := range seedPlans {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			spec, err := Load(path)
			if err != nil {
				t.Fatalf("种子 plan 加载失败: %v", err)
			}
			if spec.ID == "" || len(spec.Buckets) == 0 {
				t.Fatalf("空 plan: %+v", spec)
			}
		})
	}
}

// TestSeedPlansRoundTrip 种子 plan 的序列化契约：LoadBytes(Marshal(s)) 等价。
func TestSeedPlansRoundTrip(t *testing.T) {
	for _, path := range seedPlans {
		t.Run(filepath.Base(path), func(t *testing.T) {
			spec, err := Load(path)
			if err != nil {
				t.Fatalf("加载: %v", err)
			}
			out, err := Marshal(spec)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			spec2, err := LoadBytes(out)
			if err != nil {
				t.Fatalf("回读: %v\n---\n%s", err, out)
			}
			if !reflect.DeepEqual(spec, spec2) {
				t.Fatalf("往返后不等价:\n旧: %+v\n新: %+v", spec, spec2)
			}
		})
	}
}

// TestSeedPlanAnthropicMax20 抽查 Max20 的关键结构：不透明分、共享池、
// $ref 共享 charge、模型族专用窗、PAYG 安全契约、frozen gauge 参数。
func TestSeedPlanAnthropicMax20(t *testing.T) {
	spec, err := Load(seedPlans[0])
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	if spec.ID != "anthropic/max20@2026-08" || spec.Vendor != "anthropic" {
		t.Fatalf("基本信息: %s / %s", spec.ID, spec.Vendor)
	}
	if spec.Risk.TOSViolationClass != "explicit_breach" || spec.Risk.BanHazardMonthly != 0.08 {
		t.Fatalf("风险档案: %+v", spec.Risk)
	}
	// gauge 价目锚定四维
	for _, dim := range []Dim{DimInputTokens, DimCacheWriteTokens, DimCacheReadTokens, DimOutputTokens} {
		if _, ok := spec.Gauge.RatecardUSDPerUnit[dim]; !ok {
			t.Fatalf("gauge 价目缺维度 %q", dim)
		}
	}
	if len(spec.Buckets) != 5 {
		t.Fatalf("桶数: %d", len(spec.Buckets))
	}

	b5h := spec.Bucket("b_5h")
	if b5h.Unit != DimOpaqueUnits || !b5h.ExogenousDrain {
		t.Fatalf("b_5h 不透明分/外生消耗: %+v", b5h)
	}
	if b5h.ExogenousRateParam != "anthropic.max20.exo_rate_5h" {
		t.Fatalf("外生消耗率参数: %q", b5h.ExogenousRateParam)
	}
	// 共享池：网页/桌面端偷额度
	if b5h.Scope.Level != ScopeCrossProductPool || b5h.Scope.PoolID != "anthropic_subscription_pool" ||
		len(b5h.Scope.SharedWithProducts) != 4 {
		t.Fatalf("b_5h 共享池 scope: %+v", b5h.Scope)
	}
	// 结构未知：5h 窗两候选带后验
	if len(b5h.Window.KindCandidates) != 2 || b5h.Window.KindPosterior[WindowTumblingAnchoredOnFirstUse] != 0.6 {
		t.Fatalf("b_5h 窗候选/后验: %+v", b5h.Window)
	}
	if b5h.Window.Length.Duration != 5*time.Hour {
		t.Fatalf("b_5h 窗长: %v", b5h.Window.Length.Duration)
	}
	// 5 个加权项 + 3 个模型倍率（Opus 待估 / Sonnet frozen gauge / Haiku 待估）
	if len(b5h.Charge.Terms) != 5 || len(b5h.Charge.ModelMultiplier) != 3 {
		t.Fatalf("b_5h charge: %d 项 %d 倍率", len(b5h.Charge.Terms), len(b5h.Charge.ModelMultiplier))
	}
	// 三通道信息架构：usage endpoint（主）+ local_log（精确账本）
	sources := map[ObsSource]int{}
	for _, ob := range b5h.Observability {
		sources[ob.Source]++
	}
	if sources[ObsUsageEndpoint] != 2 || sources[ObsLocalLog] != 1 {
		t.Fatalf("b_5h 观测绑定: %+v", sources)
	}

	// $ref 共享 charge：三个周窗桶的 charge 与 b_5h 深相等（深拷贝、无共享）
	for _, id := range []string{"b_7d_all", "b_7d_sonnet", "b_7d_opus"} {
		b := spec.Bucket(id)
		if b == nil {
			t.Fatalf("桶 %q 缺失", id)
		}
		if !reflect.DeepEqual(b.Charge, b5h.Charge) {
			t.Fatalf("桶 %q 的 $ref charge 与 b_5h 不等", id)
		}
	}
	// 模型族专用窗
	if s := spec.Bucket("b_7d_sonnet").Scope; s.Level != ScopeModelFamily || s.ModelFamilies[0] != "claude-sonnet" {
		t.Fatalf("b_7d_sonnet scope: %+v", s)
	}
	// 周窗锚点待辨识（UNKNOWN → 运行时显式报错，不是静默缺省）
	if spec.Bucket("b_7d_all").Window.AnchorUTC != "UNKNOWN" {
		t.Fatalf("b_7d_all anchor 应为 UNKNOWN")
	}

	// PAYG 安全契约：显式开启才放行
	extra := spec.Bucket("b_extra_credits")
	if extra == nil {
		t.Fatal("桶 b_extra_credits 缺失")
	}
	paygOK := false
	for _, ov := range extra.Overflow {
		if ov.Action == OverflowSpillToPAYG && ov.RequiresExplicitEnable {
			paygOK = true
		}
	}
	if !paygOK {
		t.Fatalf("b_extra_credits 缺合法 PAYG 溢出: %+v", extra.Overflow)
	}

	// frozen 只属于 gauge：mult_sonnet 冻结、vendor_doc 的 C_5h_prompts 类永不冻结
	ms := spec.Param("anthropic.max20.mult_sonnet")
	if ms == nil || !ms.Frozen || ms.Provenance != ProvenanceGauge {
		t.Fatalf("mult_sonnet 应为 frozen gauge: %+v", ms)
	}
	nFrozen := 0
	for _, prm := range spec.Parameters {
		if prm.Frozen {
			nFrozen++
		}
	}
	if nFrozen != 1 {
		t.Fatalf("frozen 参数应恰有 1 个（gauge），实为 %d", nFrozen)
	}
}

// TestSeedPlanGLMCodingMax 抽查 per-request 桶的结构套利形态：
// flat=1 + terms 空 + prompt_granularity 类别型结构未知数。
func TestSeedPlanGLMCodingMax(t *testing.T) {
	spec, err := Load(seedPlans[1])
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	if spec.ID != "zai/glm-coding-max@2026-08" {
		t.Fatalf("ID: %s", spec.ID)
	}
	b := spec.Bucket("b_5h_prompts")
	if b == nil {
		t.Fatal("桶 b_5h_prompts 缺失")
	}
	// ★ 套利形态：flat 常量 1、terms 空（token 边际成本为 0）
	if v, ok := b.Charge.Flat.Constant(); !ok || v != 1.0 {
		t.Fatalf("flat 应为常量 1.0: (%v,%v)", v, ok)
	}
	if len(b.Charge.Terms) != 0 {
		t.Fatalf("terms 应为空: %+v", b.Charge.Terms)
	}
	// 高级模型倍率待估（vendor 不公开）
	if !b.Charge.ModelMultiplier["glm-5.1"].IsRef() {
		t.Fatalf("glm-5.1 倍率应为 ParamRef: %+v", b.Charge.ModelMultiplier["glm-5.1"])
	}
	// prompt_granularity：类别型离散（turn/request/step，倍率差 10~50×）
	pg := spec.Param("zai.max.prompt_granularity")
	if pg == nil || pg.Prior.Kind != DistDiscrete {
		t.Fatalf("prompt_granularity: %+v", pg)
	}
	if !reflect.DeepEqual(pg.Prior.Categories, []string{"turn", "request", "step"}) {
		t.Fatalf("granularity 候选: %+v", pg.Prior.Categories)
	}
	// vendor_doc 声称值永不 frozen
	cp := spec.Param("zai.max.C_5h_prompts")
	if cp == nil || cp.Provenance != ProvenanceVendorDoc || cp.Frozen {
		t.Fatalf("C_5h_prompts 应为 vendor_doc 且未冻结: %+v", cp)
	}
	// 通道：Anthropic 兼容端点 + 瞬时准入约束
	ch := spec.Channel("glm_anthropic_compat")
	if ch == nil || ch.Protocol != "anthropic_messages" || ch.BaseURL == "" {
		t.Fatalf("通道: %+v", ch)
	}
	if _, ok := ch.Admission.Limits[InstantConcurrency]; !ok {
		t.Fatalf("缺并发准入约束: %+v", ch.Admission.Limits)
	}
}

// TestSeedPlanFreeTemplate 抽查免费档模板：RPM/RPD/TPM 三维硬桶 +
// mid_stream 高中断通道（LP 硬约束的来源）。
func TestSeedPlanFreeTemplate(t *testing.T) {
	spec, err := Load(seedPlans[2])
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	if spec.PriceUSDPerPeriod != 0 {
		t.Fatalf("免费档价格应为 0: %v", spec.PriceUSDPerPeriod)
	}
	if len(spec.Buckets) != 3 {
		t.Fatalf("三维桶: %d", len(spec.Buckets))
	}
	rpm := spec.Bucket("b_rpm")
	if rpm == nil || rpm.Scope.Level != ScopeCredential {
		t.Fatalf("b_rpm: %+v", rpm)
	}
	// RPM 桶类型待辨识（token bucket 允许 burst vs 离散分钟窗硬断）
	if len(rpm.Window.KindCandidates) != 2 || rpm.Window.KindPosterior[WindowTokenBucketContinuous] != 0.7 {
		t.Fatalf("b_rpm 窗候选: %+v", rpm.Window)
	}
	if !rpm.Window.RefillRate.IsRef() || !rpm.Window.Burst.IsRef() {
		t.Fatalf("b_rpm refill/burst 应为 ParamRef: %+v", rpm.Window)
	}
	// RPD 日历对齐 UTC 零点；TPM 按 token 计量
	rpd := spec.Bucket("b_rpd")
	if rpd.Window.CalendarAlign != "utc_midnight" || rpd.Window.KindCandidates[0] != WindowTumblingCalendar {
		t.Fatalf("b_rpd 窗: %+v", rpd.Window)
	}
	if spec.Bucket("b_tpm").Unit != DimInputTokens {
		t.Fatalf("b_tpm 计量: %v", spec.Bucket("b_tpm").Unit)
	}
	// 全部硬断（免费档无溢出瀑布）
	for _, b := range spec.Buckets {
		if len(b.Overflow) != 1 || b.Overflow[0].Action != OverflowHardBlock {
			t.Fatalf("桶 %q 应硬断: %+v", b.ID, b.Overflow)
		}
	}
	// ★ 高中断 + mid_stream + 不可续写：只有可丢弃任务能进来
	ch := spec.Channel("free_openai_compat")
	if ch == nil {
		t.Fatal("通道缺失")
	}
	rel := ch.Reliability
	if rel.InterruptionHazardPerHour != 0.45 || rel.InterruptionGranularity != "mid_stream" || rel.ResumeSupported {
		t.Fatalf("可靠性档案: %+v", rel)
	}
	if lim, ok := ch.Admission.Limits[InstantConcurrency]; !ok {
		_ = lim
		t.Fatal("缺并发准入约束")
	}
}
