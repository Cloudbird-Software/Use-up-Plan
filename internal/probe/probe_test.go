package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/ledger"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// TestBuiltinsLoadAndValidate 内置剧本库全部过校验（接入即资产：任何一个
// 剧本坏掉，对应结构问题就永远判不了）。
func TestBuiltinsLoadAndValidate(t *testing.T) {
	pbs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	want := []string{
		"null_field_presence", "pool_sharedness", "prompt_granularity",
		"rpm_bucket_type", "weekly_window_anchor", "window_kind_5h",
	}
	if len(pbs) != len(want) {
		t.Fatalf("内置剧本数 %d ≠ %d: %v", len(pbs), len(want), ids(pbs))
	}
	for i, id := range want {
		if pbs[i].ID != id {
			t.Fatalf("剧本[%d] = %s，应按 ID 排序为 %s", i, pbs[i].ID, id)
		}
	}
}

// TestBuiltinsCoverIntentTable Intent §4.3 判别式表的六行全部有对应剧本
// （每行至少一个判别式 + 明确的成本类别）。
func TestBuiltinsCoverIntentTable(t *testing.T) {
	pbs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	byID := map[string]*Playbook{}
	for _, pb := range pbs {
		byID[pb.ID] = pb
	}
	// 每个剧本至少声明一个判别式与一个证据需求（Validate 已强制，
	// 此处验证六行的覆盖面而非重复执法）。
	for _, id := range []string{"window_kind_5h", "weekly_window_anchor",
		"prompt_granularity", "rpm_bucket_type", "null_field_presence", "pool_sharedness"} {
		pb, ok := byID[id]
		if !ok {
			t.Fatalf("缺剧本 %s", id)
		}
		if len(pb.Discriminators) == 0 || len(pb.Needs) == 0 || len(pb.Candidates) < 2 {
			t.Fatalf("剧本 %s 声明不完整: %+v", id, pb)
		}
	}
	// 判别式 kind 的封闭集覆盖（C3 的实现清单）。
	kinds := map[string]bool{}
	for _, pb := range pbs {
		for _, d := range pb.Discriminators {
			kinds[d.Kind] = true
		}
	}
	for _, k := range []string{"resets_at_constancy", "cliff_vs_stair", "step_counting", "null_presence", "pool_sync"} {
		if !kinds[k] {
			t.Fatalf("判别式 kind %s 无任何剧本使用（封闭集与资产脱节）", k)
		}
	}
}

// TestParsePlaybookStrict 严格模式：未知字段报错（schema 演进必须显式）。
func TestParsePlaybookStrict(t *testing.T) {
	raw := []byte("id: x\nquestion: q\nbucket_glob: b*\ncandidates: [a, b]\ncost: passive\nneeds: [{semantic: used_pct, min_count: 1}]\ndiscriminators: [{id: d, description: e, kind: cliff_vs_stair}]\nunknown_field: 1\n")
	if _, err := parsePlaybook(raw); err == nil {
		t.Fatal("未知字段应报错（严格模式）")
	}
}

// TestPlaybookValidateRejects 封闭集与结构完整性的拒绝面。
func TestPlaybookValidateRejects(t *testing.T) {
	base := func(mut func(*Playbook)) *Playbook {
		pb := &Playbook{
			ID: "p", Question: "q", BucketGlob: "b*",
			Candidates: []string{"a", "b"}, Cost: CostPassive,
			Needs:          []EvidenceNeed{{Semantic: qdl.SemUsedPct, MinCount: 1}},
			Discriminators: []Discriminator{{ID: "d", Description: "e", Kind: "cliff_vs_stair"}},
		}
		mut(pb)
		return pb
	}
	cases := map[string]func(*Playbook){
		"缺 glob":      func(p *Playbook) { p.BucketGlob = "" },
		"glob 语法非法":   func(p *Playbook) { p.BucketGlob = "[" },
		"单候选":         func(p *Playbook) { p.Candidates = []string{"a"} },
		"cost 越界":     func(p *Playbook) { p.Cost = Cost("expensive") },
		"无需求":         func(p *Playbook) { p.Needs = nil },
		"semantic 越界": func(p *Playbook) { p.Needs[0].Semantic = qdl.Semantic("vibes") },
		"min_count 零": func(p *Playbook) { p.Needs[0].MinCount = 0 },
		"无判别式":        func(p *Playbook) { p.Discriminators = nil },
		"判别式 kind 越界": func(p *Playbook) { p.Discriminators[0].Kind = "magic" },
	}
	for name, mut := range cases {
		if err := base(mut).Validate(); err == nil {
			t.Fatalf("%s 应被拒绝", name)
		}
	}
	if err := base(func(p *Playbook) {}).Validate(); err != nil {
		t.Fatalf("基准剧本不应报错: %v", err)
	}
}

// TestMatchBucket glob 语义。
func TestMatchBucket(t *testing.T) {
	pb := &Playbook{BucketGlob: "b_5h*"}
	for b, want := range map[string]bool{
		"b_5h": true, "b_5h_v2": true, "b_7d_all": false, "": false,
	} {
		if got := pb.MatchBucket(b); got != want {
			t.Fatalf("MatchBucket(%q) = %v，应 %v", b, got, want)
		}
	}
}

// TestLoadFile 文件加载路径。
func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.yaml")
	src, err := os.ReadFile("playbooks/window_kind_5h.yaml")
	if err != nil {
		t.Fatalf("读取内置剧本: %v", err)
	}
	if err := os.WriteFile(p, src, 0o644); err != nil {
		t.Fatalf("写入临时剧本: %v", err)
	}
	pb, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if pb.ID != "window_kind_5h" {
		t.Fatalf("剧本 ID: %s", pb.ID)
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("缺文件应报错")
	}
}

// ---- dry-run ----

// dryRunStore 构造临时事件库并写入观测（时间升序）。
func dryRunStore(t *testing.T, planID string, obs []struct {
	ts     time.Time
	bucket string
	sem    qdl.Semantic
	raw    string
}) ledger.Store {
	t.Helper()
	s, err := ledger.NewJSONLStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for _, o := range obs {
		if _, err := s.Append(o.ts, &ledger.ObservationEvent{
			PlanID: planID, BucketID: o.bucket, Semantic: o.sem,
			RawValue: o.raw, Quantization: qdl.Quantization{Kind: "integer"},
			Source: qdl.ObsUsageEndpoint, Trust: 1,
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	return s
}

// TestDryRunReadyAndInsufficient 数据充分性的两个方向：够与不够。
func TestDryRunReadyAndInsufficient(t *testing.T) {
	pbs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	pb := findByID(t, pbs, "window_kind_5h")
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)

	// 充足：24 个 used_pct 跨 13h + 6 个 reset_at_iso 跨 3h（跨窗边界）。
	var obs []struct {
		ts     time.Time
		bucket string
		sem    qdl.Semantic
		raw    string
	}
	for i := 0; i < 24; i++ {
		obs = append(obs, struct {
			ts     time.Time
			bucket string
			sem    qdl.Semantic
			raw    string
		}{t0.Add(time.Duration(i) * 35 * time.Minute), "b_5h", qdl.SemUsedPct, "37"})
	}
	for i := 0; i < 6; i++ {
		obs = append(obs, struct {
			ts     time.Time
			bucket string
			sem    qdl.Semantic
			raw    string
		}{t0.Add(time.Duration(i) * 40 * time.Minute), "b_5h", qdl.SemResetAtISO, "2026-08-20T12:00:00Z"})
	}
	store := dryRunStore(t, "p1", obs)
	res, err := DryRun(store, pb, "p1")
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if res.Status != StatusReady {
		t.Fatalf("应 ready: %v（缺口 %v）", res.Status, res.Missing())
	}
	// 证据序列完整保留（判别式的输入）：24 + 6 条，时间升序。
	if len(res.Series[qdl.SemUsedPct].Samples) != 24 || len(res.Series[qdl.SemResetAtISO].Samples) != 6 {
		t.Fatalf("序列长度: used=%d reset=%d",
			len(res.Series[qdl.SemUsedPct].Samples), len(res.Series[qdl.SemResetAtISO].Samples))
	}
	if res.Series[qdl.SemUsedPct].Samples[0].RawValue != "37" {
		t.Fatalf("原始串应保真: %q", res.Series[qdl.SemUsedPct].Samples[0].RawValue)
	}

	// 不足：只有 3 个观测。
	store2 := dryRunStore(t, "p1", obs[:3])
	res2, err := DryRun(store2, pb, "p1")
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if res2.Status != StatusInsufficient {
		t.Fatalf("应 insufficient: %v", res2.Status)
	}
	missing := strings.Join(res2.Missing(), "; ")
	if !strings.Contains(missing, "used_pct") || !strings.Contains(missing, "reset_at_iso") {
		t.Fatalf("缺口报告应含两个语义: %q", missing)
	}
}

// TestDryRunFilters glob 桶过滤 + plan 过滤 + 语义不匹配的观测不入序列。
func TestDryRunFilters(t *testing.T) {
	pbs, _ := Builtins()
	pb := findByID(t, pbs, "window_kind_5h")
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	obs := []struct {
		ts     time.Time
		bucket string
		sem    qdl.Semantic
		raw    string
	}{
		{t0, "b_5h", qdl.SemUsedPct, "10"},                      // 命中
		{t0.Add(time.Minute), "b_7d_all", qdl.SemUsedPct, "20"}, // glob 不匹配
		{t0.Add(2 * time.Minute), "b_5h", qdl.SemPlanType, "x"}, // 语义不需要
	}
	store := dryRunStore(t, "p1", obs)
	res, err := DryRun(store, pb, "p1")
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if got := len(res.Series[qdl.SemUsedPct].Samples); got != 1 {
		t.Fatalf("used_pct 序列应只含 glob 命中的 1 条，得 %d", got)
	}
	// 其他 plan 的事件不可见。
	store2 := dryRunStore(t, "OTHER", obs)
	res2, err := DryRun(store2, pb, "p1")
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if got := len(res2.Series[qdl.SemUsedPct].Samples); got != 0 {
		t.Fatalf("跨 plan 事件不应入序列，得 %d", got)
	}
	if res2.Status != StatusInsufficient {
		t.Fatalf("无证据应 insufficient: %v", res2.Status)
	}
}

// TestDryRunSpanRequirement 跨度要求：样本数够但跨度不够 → 不足。
func TestDryRunSpanRequirement(t *testing.T) {
	pb := &Playbook{
		ID: "span", Question: "q", BucketGlob: "b*", Candidates: []string{"a", "b"},
		Cost:           CostPassive,
		Needs:          []EvidenceNeed{{Semantic: qdl.SemUsedPct, MinCount: 2, MinSpan: 10 * time.Hour}},
		Discriminators: []Discriminator{{ID: "d", Description: "e", Kind: "cliff_vs_stair"}},
	}
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	store := dryRunStore(t, "p1", []struct {
		ts     time.Time
		bucket string
		sem    qdl.Semantic
		raw    string
	}{
		{t0, "b", qdl.SemUsedPct, "1"}, {t0.Add(time.Hour), "b", qdl.SemUsedPct, "2"},
	})
	res, err := DryRun(store, pb, "p1")
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if res.Status != StatusInsufficient || res.Needs[0].Satisfied {
		t.Fatalf("跨度 1h < 10h 应不满足: %+v", res.Needs)
	}
	if res.Needs[0].SpanHave != time.Hour {
		t.Fatalf("SpanHave = %v，应 1h", res.Needs[0].SpanHave)
	}
}

// TestDryRunGuards nil 参数与空 planID。
func TestDryRunGuards(t *testing.T) {
	if _, err := DryRun(nil, &Playbook{}, "p"); err == nil {
		t.Fatal("nil store 应报错")
	}
	store := dryRunStore(t, "p1", nil)
	if _, err := DryRun(store, nil, "p"); err == nil {
		t.Fatal("nil 剧本应报错")
	}
	if _, err := DryRun(store, &Playbook{}, ""); err == nil {
		t.Fatal("空 planID 应报错")
	}
}

func findByID(t *testing.T, pbs []*Playbook, id string) *Playbook {
	t.Helper()
	for _, pb := range pbs {
		if pb.ID == id {
			return pb
		}
	}
	t.Fatalf("找不到剧本 %s", id)
	return nil
}

func ids(pbs []*Playbook) []string {
	var out []string
	for _, pb := range pbs {
		out = append(out, pb.ID)
	}
	return out
}
