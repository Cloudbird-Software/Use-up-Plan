package probe

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// ---- 测试辅助 ----

func smp(t time.Time, bucket, raw string) EvidenceSample {
	return EvidenceSample{Ts: t, BucketID: bucket, RawValue: raw}
}

func oneSeries(sem qdl.Semantic, samples ...EvidenceSample) map[qdl.Semantic]*EvidenceSeries {
	return map[qdl.Semantic]*EvidenceSeries{sem: {Semantic: sem, Samples: samples}}
}

// builtinByID 从内置剧本库取指定 ID 的剧本。
func builtinByID(t *testing.T, id string) *Playbook {
	t.Helper()
	pbs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	return findByID(t, pbs, id)
}

// verdictOf 取剧本里指定 kind 的第一个 verdict（剧本里每 kind 至多一个）。
func verdictOf(t *testing.T, vs []Verdict, kind string) Verdict {
	t.Helper()
	for _, v := range vs {
		if v.Kind == kind {
			return v
		}
	}
	t.Fatalf("缺 kind %s 的 verdict: %+v", kind, vs)
	return Verdict{}
}

// ---- resets_at_constancy ----

// TestResetsAtConstancyAnchored 锚定窗：resets_at 窗内恒定，跳变只发生在
// 与整窗重置断崖重合的边界 → constant。
func TestResetsAtConstancyAnchored(t *testing.T) {
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	var reset []EvidenceSample
	for i := 0; i < 10; i++ {
		ts := t0.Add(time.Duration(i) * time.Hour)
		val := "2026-08-20T13:00:00Z"
		if i >= 5 { // 13:00 起进入第二个 5h 窗
			val = "2026-08-20T18:00:00Z"
		}
		reset = append(reset, smp(ts, "b_5h", val))
	}
	var used []EvidenceSample
	w1 := []string{"5", "12", "20", "28", "35", "43", "50", "58", "65", "70"}
	w2 := []string{"2", "8", "15", "22", "30", "37", "44", "51", "58", "64"}
	for i, val := range append(append([]string{}, w1...), w2...) {
		used = append(used, smp(t0.Add(time.Duration(i)*30*time.Minute), "b_5h", val))
	}
	series := oneSeries(qdl.SemResetAtISO, reset...)
	series[qdl.SemUsedPct] = &EvidenceSeries{Semantic: qdl.SemUsedPct, Samples: used}

	pb := builtinByID(t, "window_kind_5h")
	vs, err := Discriminate(pb, series, Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v := verdictOf(t, vs, "resets_at_constancy")
	if v.Finding != FindingConstant || v.Candidate != "tumbling_anchored_on_first_use" {
		t.Fatalf("finding=%q candidate=%q（应 constant / tumbling_anchored_on_first_use）: %s",
			v.Finding, v.Candidate, v.Evidence)
	}
	if v.Confidence < 0.9 {
		t.Fatalf("置信度 %.3f < 0.9: %s", v.Confidence, v.Evidence)
	}
}

// TestResetsAtConstancySliding 滑窗：resets_at 随旧用量滑出持续前移，
// 跳变后用量仍高 → shifting。
func TestResetsAtConstancySliding(t *testing.T) {
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	var reset []EvidenceSample
	for i := 0; i < 12; i++ {
		// true sliding：resets_at = 最旧用量时刻 + 5h，随消耗持续后延。
		val := t0.Add(time.Duration(i)*30*time.Minute + 5*time.Hour)
		reset = append(reset, smp(t0.Add(time.Duration(i)*30*time.Minute), "b_5h", val.Format(time.RFC3339)))
	}
	decay := []string{"65", "68", "70", "68", "66", "63", "59", "54", "48", "41", "33", "24"}
	var used []EvidenceSample
	for i, val := range decay {
		used = append(used, smp(t0.Add(time.Duration(i)*30*time.Minute), "b_5h", val))
	}
	series := oneSeries(qdl.SemResetAtISO, reset...)
	series[qdl.SemUsedPct] = &EvidenceSeries{Semantic: qdl.SemUsedPct, Samples: used}

	pb := builtinByID(t, "window_kind_5h")
	vs, err := Discriminate(pb, series, Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v := verdictOf(t, vs, "resets_at_constancy")
	if v.Finding != FindingShifting || v.Candidate != "sliding_exact" {
		t.Fatalf("finding=%q candidate=%q（应 shifting / sliding_exact）: %s",
			v.Finding, v.Candidate, v.Evidence)
	}
	if v.Confidence < 0.9 {
		t.Fatalf("置信度 %.3f < 0.9: %s", v.Confidence, v.Evidence)
	}
}

// TestResetsAtConstancyNoShift 无跳变（观测未覆盖窗边界）：恒定但低置信。
func TestResetsAtConstancyNoShift(t *testing.T) {
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	series := oneSeries(qdl.SemResetAtISO,
		smp(t0, "b_5h", "2026-08-20T13:00:00Z"),
		smp(t0.Add(time.Hour), "b_5h", "2026-08-20T13:00:00Z"),
		smp(t0.Add(2*time.Hour), "b_5h", "2026-08-20T13:00:00Z"),
	)
	pb := builtinByID(t, "window_kind_5h")
	vs, err := Discriminate(pb, series, Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v := verdictOf(t, vs, "resets_at_constancy")
	if v.Finding != FindingConstant {
		t.Fatalf("finding=%q（应 constant）", v.Finding)
	}
	if v.Confidence != 0.5 {
		t.Fatalf("无跳变时置信度应 0.5，得 %.3f", v.Confidence)
	}
}

// ---- cliff_vs_stair ----

// TestCliffVsStairUsedTumbling 整窗归零的单步断崖 → cliff。
func TestCliffVsStairUsedTumbling(t *testing.T) {
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	vals := []string{"5", "20", "35", "50", "65", "70", "2", "15", "30", "45", "58"}
	var used []EvidenceSample
	for i, v := range vals {
		used = append(used, smp(t0.Add(time.Duration(i)*30*time.Minute), "b_5h", v))
	}
	pb := builtinByID(t, "window_kind_5h")
	vs, err := Discriminate(pb, oneSeries(qdl.SemUsedPct, used...), Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v := verdictOf(t, vs, "cliff_vs_stair")
	if v.Finding != FindingCliff || v.Candidate != "tumbling_anchored_on_first_use" {
		t.Fatalf("finding=%q candidate=%q（应 cliff / tumbling）: %s", v.Finding, v.Candidate, v.Evidence)
	}
}

// TestCliffVsStairUsedSliding 滑窗的渐进衰减 → stair。
func TestCliffVsStairUsedSliding(t *testing.T) {
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	vals := []string{"65", "68", "70", "68", "66", "63", "59", "54", "48", "41", "33", "24"}
	var used []EvidenceSample
	for i, v := range vals {
		used = append(used, smp(t0.Add(time.Duration(i)*30*time.Minute), "b_5h", v))
	}
	pb := builtinByID(t, "window_kind_5h")
	vs, err := Discriminate(pb, oneSeries(qdl.SemUsedPct, used...), Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v := verdictOf(t, vs, "cliff_vs_stair")
	if v.Finding != FindingStair || v.Candidate != "sliding_exact" {
		t.Fatalf("finding=%q candidate=%q（应 stair / sliding）: %s", v.Finding, v.Candidate, v.Evidence)
	}
	if v.Confidence < 0.9 {
		t.Fatalf("置信度 %.3f < 0.9: %s", v.Confidence, v.Evidence)
	}
}

// TestCliffVsStairRemaining 两个方向：token bucket 连续回补（小步上升）
// → stair；离散计数窗边界跳满（近空 → 满的单步）→ cliff。
func TestCliffVsStairRemaining(t *testing.T) {
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	pb := builtinByID(t, "rpm_bucket_type")

	// token bucket：burst 后剩余 3，静默期每 20s 回补 3（速率 9/min）。
	refill := []string{"3", "6", "9", "12", "15", "18", "21", "24", "27", "30"}
	var smpls []EvidenceSample
	for i, v := range refill {
		smpls = append(smpls, smp(t0.Add(time.Duration(i)*20*time.Second), "b_rpm", v))
	}
	vs, err := Discriminate(pb, oneSeries(qdl.SemRemainingAbs, smpls...), Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v := verdictOf(t, vs, "cliff_vs_stair")
	if v.Finding != FindingStair || v.Candidate != "token_bucket_continuous" {
		t.Fatalf("token bucket: finding=%q candidate=%q（应 stair）: %s", v.Finding, v.Candidate, v.Evidence)
	}
	if v.Confidence < 0.9 {
		t.Fatalf("token bucket 置信度 %.3f < 0.9: %s", v.Confidence, v.Evidence)
	}

	// 离散窗：静默期剩余恒定（无回补），边界一步跳满。
	discrete := []string{"3", "3", "3", "30", "30", "30"}
	smpls = nil
	for i, v := range discrete {
		smpls = append(smpls, smp(t0.Add(time.Duration(i)*20*time.Second), "b_rpm", v))
	}
	vs, err = Discriminate(pb, oneSeries(qdl.SemRemainingAbs, smpls...), Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v = verdictOf(t, vs, "cliff_vs_stair")
	if v.Finding != FindingCliff || v.Candidate != "tumbling_calendar" {
		t.Fatalf("离散窗: finding=%q candidate=%q（应 cliff / tumbling_calendar）: %s", v.Finding, v.Candidate, v.Evidence)
	}
}

// ---- step_counting ----

func TestStepCounting(t *testing.T) {
	t0 := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	pb := builtinByID(t, "prompt_granularity")
	cases := []struct {
		delta    int
		want     string
		wantConf float64
	}{
		{1, FindingTurn, 1.0},    // 每 turn 计 1
		{5, FindingRequest, 1.0}, // 每 request 计 1（1 turn 含 5 request）
		{10, FindingStep, 1.0},   // 每 step 计 1（请求+工具往返）
		{3, "", 0},               // 不匹配任何假设 → 不可判定
	}
	for _, c := range cases {
		smpls := []EvidenceSample{
			smp(t0, "b_prompts", "100"),
			smp(t0.Add(2*time.Minute), "b_prompts", "100"),
			smp(t0.Add(4*time.Minute), "b_prompts", fmt.Sprintf("%d", 100+c.delta)),
			smp(t0.Add(6*time.Minute), "b_prompts", fmt.Sprintf("%d", 100+c.delta)),
		}
		vs, err := Discriminate(pb, oneSeries(qdl.SemUsedAbs, smpls...), Options{})
		if err != nil {
			t.Fatalf("Discriminate: %v", err)
		}
		v := verdictOf(t, vs, "step_counting")
		if v.Finding != c.want {
			t.Fatalf("delta=%d: finding=%q（应 %q）: %s", c.delta, v.Finding, c.want, v.Evidence)
		}
		if c.want != "" && v.Confidence < c.wantConf-1e-9 {
			t.Fatalf("delta=%d: 置信度 %.3f < %.3f", c.delta, v.Confidence, c.wantConf)
		}
	}
}

// ---- null_presence ----

func TestNullPresence(t *testing.T) {
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	pb := builtinByID(t, "null_field_presence")

	// 消耗后字段出现（null → 数值）→ window_unused。
	vs, err := Discriminate(pb, oneSeries(qdl.SemUsedPct,
		smp(t0, "b_7d_opus", "null"),
		smp(t0.Add(5*time.Minute), "b_7d_opus", "null"),
		smp(t0.Add(10*time.Minute), "b_7d_opus", "3"),
		smp(t0.Add(15*time.Minute), "b_7d_opus", "5"),
	), Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v := verdictOf(t, vs, "null_presence")
	if v.Finding != FindingAppearsAfterUse || v.Candidate != "window_unused" {
		t.Fatalf("appears: finding=%q candidate=%q: %s", v.Finding, v.Candidate, v.Evidence)
	}

	// 消耗确认 + 仍全 null → window_absent。
	vs, err = Discriminate(pb, oneSeries(qdl.SemUsedPct,
		smp(t0, "b_7d_opus", "null"),
		smp(t0.Add(5*time.Minute), "b_7d_opus", "null"),
		smp(t0.Add(10*time.Minute), "b_7d_opus", "null"),
	), Options{UsageConfirmed: true})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v = verdictOf(t, vs, "null_presence")
	if v.Finding != FindingStaysNull || v.Candidate != "window_absent" {
		t.Fatalf("stays: finding=%q candidate=%q: %s", v.Finding, v.Candidate, v.Evidence)
	}

	// 全 null 且无消耗确认 → 不可判定（不能硬猜）。
	vs, err = Discriminate(pb, oneSeries(qdl.SemUsedPct,
		smp(t0, "b_7d_opus", "null"),
		smp(t0.Add(5*time.Minute), "b_7d_opus", "null"),
	), Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v = verdictOf(t, vs, "null_presence")
	if v.Finding != "" || v.Candidate != "" {
		t.Fatalf("无消耗确认应不可判定: finding=%q candidate=%q", v.Finding, v.Candidate)
	}
}

// ---- pool_sync ----

func TestPoolSync(t *testing.T) {
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	pb := builtinByID(t, "pool_sharedness")

	mk := func(general, opus []string) map[qdl.Semantic]*EvidenceSeries {
		var smpls []EvidenceSample
		for i, v := range general {
			smpls = append(smpls, smp(t0.Add(time.Duration(i)*20*time.Minute), "b_7d", v))
		}
		for i, v := range opus {
			smpls = append(smpls, smp(t0.Add(time.Duration(i)*20*time.Minute), "b_7d_opus", v))
		}
		return oneSeries(qdl.SemUsedPct, smpls...)
	}

	// 共池：只用 Opus 期间两桶同步上涨。
	vs, err := Discriminate(pb, mk(
		[]string{"12", "12", "16", "16", "20", "20", "24", "24", "28"},
		[]string{"8", "8", "12", "12", "16", "16", "20", "20", "24"},
	), Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v := verdictOf(t, vs, "pool_sync")
	if v.Finding != FindingSynchronized || v.Candidate != "shared_pool" {
		t.Fatalf("共池: finding=%q candidate=%q: %s", v.Finding, v.Candidate, v.Evidence)
	}
	if v.Confidence < 0.9 {
		t.Fatalf("共池置信度 %.3f < 0.9: %s", v.Confidence, v.Evidence)
	}

	// 独立池：专桶涨、总桶平（孤立上涨证伪共池）。
	vs, err = Discriminate(pb, mk(
		[]string{"12", "12", "12", "12", "12", "12", "12", "12", "12"},
		[]string{"8", "8", "12", "12", "16", "16", "20", "20", "24"},
	), Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v = verdictOf(t, vs, "pool_sync")
	if v.Finding != FindingIndependent || v.Candidate != "independent_pools" {
		t.Fatalf("独立池: finding=%q candidate=%q: %s", v.Finding, v.Candidate, v.Evidence)
	}
	if v.Confidence < 0.9 {
		t.Fatalf("独立池置信度 %.3f < 0.9: %s", v.Confidence, v.Evidence)
	}
}

// ---- Conclude 聚合 ----

func TestConclude(t *testing.T) {
	pb := &Playbook{ID: "p", Candidates: []string{"a", "b"}}

	// 平票取字典序最小；每个 0.8 置信度留 0.2 在 sink。
	c := Conclude(pb, []Verdict{
		{DiscriminatorID: "d1", Candidate: "b", Confidence: 0.8},
		{DiscriminatorID: "d2", Candidate: "a", Confidence: 0.8},
	})
	if c.Candidate != "a" {
		t.Fatalf("平票应取字典序最小 a，得 %q", c.Candidate)
	}
	if c.Posterior["a"] != 0.4 || c.Posterior["b"] != 0.4 || math.Abs(c.Inconclusive-0.2) > 1e-9 {
		t.Fatalf("后验 %v sink %v（应 a=b=0.4, sink=0.2）", c.Posterior, c.Inconclusive)
	}

	// 质量守恒：单判别式 conf 0.6 → 后验 0.6 / 不可判定 0.4。
	c = Conclude(pb, []Verdict{{DiscriminatorID: "d", Candidate: "a", Confidence: 0.6}})
	if c.Posterior["a"] != 0.6 || math.Abs(c.Inconclusive-0.4) > 1e-9 {
		t.Fatalf("后验 %v sink %v（应 0.6/0.4）", c.Posterior["a"], c.Inconclusive)
	}

	// 不可判定判别式的全部质量落 sink。
	c = Conclude(pb, []Verdict{{DiscriminatorID: "d"}})
	if c.Candidate != "" || c.Inconclusive != 1 {
		t.Fatalf("全不可判定: candidate=%q sink=%v", c.Candidate, c.Inconclusive)
	}

	// 空 verdict / nil 剧本。
	if c := Conclude(pb, nil); c.Candidate != "" {
		t.Fatalf("空 verdict 应无结论")
	}
	if c := Conclude(nil, nil); c.Candidate != "" || c.Posterior == nil {
		t.Fatalf("nil 剧本应返回零值")
	}
}

// ---- 确定性与防御 ----

// TestDiscriminateDeterministic 同输入两次运行输出逐位一致（map 迭代序
// 不得影响结果——审计与复现的硬要求）。
func TestDiscriminateDeterministic(t *testing.T) {
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	var smpls []EvidenceSample
	for i := 0; i < 9; i++ {
		ts := t0.Add(time.Duration(i) * 20 * time.Minute)
		g := []string{"12", "12", "16", "16", "20", "20", "24", "24", "28"}[i]
		o := []string{"8", "8", "12", "12", "16", "16", "20", "20", "24"}[i]
		smpls = append(smpls, smp(ts, "b_7d", g), smp(ts, "b_7d_opus", o))
	}
	pb := builtinByID(t, "pool_sharedness")
	v1, err := Discriminate(pb, oneSeries(qdl.SemUsedPct, smpls...), Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	v2, err := Discriminate(pb, oneSeries(qdl.SemUsedPct, smpls...), Options{})
	if err != nil {
		t.Fatalf("Discriminate: %v", err)
	}
	if !reflect.DeepEqual(v1, v2) {
		t.Fatalf("判别式输出不确定:\n%+v\n%+v", v1, v2)
	}
}

// TestDiscriminateGuards nil 剧本与未知 kind 报错；证据缺失返回不可判定
// verdict 而非报错（判别式的输入面允许数据不全）。
func TestDiscriminateGuards(t *testing.T) {
	if _, err := Discriminate(nil, nil, Options{}); err == nil {
		t.Fatal("nil 剧本应报错")
	}
	pb := &Playbook{
		ID: "p", Question: "q", BucketGlob: "b*", Candidates: []string{"a", "b"},
		Cost:           CostPassive,
		Needs:          []EvidenceNeed{{Semantic: qdl.SemUsedPct, MinCount: 1}},
		Discriminators: []Discriminator{{ID: "d", Description: "e", Kind: "magic"}},
	}
	if _, err := Discriminate(pb, nil, Options{}); err == nil {
		t.Fatal("未知 kind 应报错（封闭集与实现脱节）")
	}
	// 空证据：全部判别式不可判定，不 panic 不报错。
	pb.Discriminators = []Discriminator{{
		ID: "d", Description: "e", Kind: "cliff_vs_stair",
		Mapping: map[string]string{FindingCliff: "a", FindingStair: "b"},
	}}
	vs, err := Discriminate(pb, nil, Options{})
	if err != nil {
		t.Fatalf("空证据应返回不可判定而非报错: %v", err)
	}
	if len(vs) != 1 || vs[0].Finding != "" || vs[0].Candidate != "" {
		t.Fatalf("空证据应不可判定: %+v", vs)
	}
}

// ---- ROADMAP C3 验收：五类结构问题判定后验 > 0.9 ----

// TestAcceptanceFiveStructureQuestions 合成数据上五类结构问题（5h 窗类型/
// 周窗锚定/prompt 粒度/RPM 桶类型/共池）经完整管线（判别式 + 聚合）
// 判定后验 > 0.9（docs/ROADMAP.md Phase 2 验收口径）。
func TestAcceptanceFiveStructureQuestions(t *testing.T) {
	t0 := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC) // 周一 08:00

	// 5h 锚定窗：两窗完整覆盖，边界断崖 + resets_at 窗内恒定。
	var used5h, reset5h []EvidenceSample
	w1 := []string{"5", "12", "20", "28", "35", "43", "50", "58", "65", "70"}
	w2 := []string{"2", "8", "15", "22", "30", "37", "44", "51", "58", "64"}
	for i, val := range append(append([]string{}, w1...), w2...) {
		used5h = append(used5h, smp(t0.Add(time.Duration(i)*30*time.Minute), "b_5h", val))
	}
	for i := 0; i < 10; i++ {
		ts := t0.Add(time.Duration(i) * time.Hour)
		val := "2026-08-17T13:00:00Z"
		if i >= 5 {
			val = "2026-08-17T18:00:00Z"
		}
		reset5h = append(reset5h, smp(ts, "b_5h", val))
	}

	// 周窗账号锚定：两周用量（周内爬升 + 周界断崖）+ 周界 resets_at 同星期几同时刻。
	var used7d, reset7d []EvidenceSample
	for i := 0; i < 62; i++ { // 每 3h 一个样本，跨 186h（> 1 周）
		ts := t0.Add(time.Duration(i) * 3 * time.Hour)
		var val string
		switch h := i * 3; {
		case h < 72: // 周内前三天爬升
			val = fmt.Sprintf("%d", 10+i*2)
		case h < 168: // 高位徘徊
			val = "78"
		case h == 168: // 周界断崖
			val = "3"
		default: // 第二周重新爬升
			val = fmt.Sprintf("%d", 3+(i-56)*3)
		}
		used7d = append(used7d, smp(ts, "b_7d", val))
	}
	for i := 0; i < 17; i++ { // 每 12h 一个样本，跨 192h
		ts := t0.Add(time.Duration(i) * 12 * time.Hour)
		val := "2026-08-24T08:00:00Z"
		if i*12 >= 168 {
			val = "2026-08-31T08:00:00Z"
		}
		reset7d = append(reset7d, smp(ts, "b_7d", val))
	}

	// prompt 粒度：1 turn（5 request）配额掉 5 → request 计量。
	usedAbs := []EvidenceSample{
		smp(t0, "b_prompts", "100"),
		smp(t0.Add(2*time.Minute), "b_prompts", "100"),
		smp(t0.Add(4*time.Minute), "b_prompts", "105"),
		smp(t0.Add(6*time.Minute), "b_prompts", "105"),
	}

	// RPM token bucket：burst 后剩余 3，静默期连续小步回补。
	var rpm []EvidenceSample
	for i, v := range []string{"3", "6", "9", "12", "15", "18", "21", "24", "27", "30"} {
		rpm = append(rpm, smp(t0.Add(time.Duration(i)*20*time.Second), "b_rpm", v))
	}

	// 共池：只用 Opus 期间双桶同步上涨。
	var pool []EvidenceSample
	for i, gv := range []string{"12", "12", "16", "16", "20", "20", "24", "24", "28"} {
		ov := []string{"8", "8", "12", "12", "16", "16", "20", "20", "24"}[i]
		ts := t0.Add(time.Duration(i) * 20 * time.Minute)
		pool = append(pool, smp(ts, "b_7d", gv), smp(ts, "b_7d_opus", ov))
	}

	series5h := oneSeries(qdl.SemResetAtISO, reset5h...)
	series5h[qdl.SemUsedPct] = &EvidenceSeries{Semantic: qdl.SemUsedPct, Samples: used5h}
	series7d := oneSeries(qdl.SemResetAtISO, reset7d...)
	series7d[qdl.SemUsedPct] = &EvidenceSeries{Semantic: qdl.SemUsedPct, Samples: used7d}

	cases := []struct {
		playbook string
		series   map[qdl.Semantic]*EvidenceSeries
		want     string
	}{
		{"window_kind_5h", series5h, "tumbling_anchored_on_first_use"},
		{"weekly_window_anchor", series7d, "tumbling_account_anchored"},
		{"prompt_granularity", oneSeries(qdl.SemUsedAbs, usedAbs...), "request"},
		{"rpm_bucket_type", oneSeries(qdl.SemRemainingAbs, rpm...), "token_bucket_continuous"},
		{"pool_sharedness", oneSeries(qdl.SemUsedPct, pool...), "shared_pool"},
	}
	for _, c := range cases {
		pb := builtinByID(t, c.playbook)
		vs, err := Discriminate(pb, c.series, Options{})
		if err != nil {
			t.Fatalf("%s: Discriminate: %v", c.playbook, err)
		}
		con := Conclude(pb, vs)
		if con.Candidate != c.want {
			t.Fatalf("%s: 结论 %q（应 %q），verdicts=%+v", c.playbook, con.Candidate, c.want, vs)
		}
		if con.Confidence <= 0.9 {
			t.Fatalf("%s: 后验 %.4f ≤ 0.9（verdicts=%+v）", c.playbook, con.Confidence, vs)
		}
	}
}
