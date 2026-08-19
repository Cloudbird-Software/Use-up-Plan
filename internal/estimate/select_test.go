package estimate

import (
	"math"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/ledger"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/semantics"
)

// ---- 结构选择测试脚手架 ----

// selSpec 构造结构选择测试 plan：单桶、候选窗型由参数给定、数值参数全
// frozen（point 先验）——结构比较在已知参数下进行，信号纯度最高：
// 似然差只来自窗语义差异，不受参数拟合噪声污染。
func selSpec(kinds []qdl.WindowKind, length time.Duration) (*qdl.PlanSpec, qdl.ParamPoint) {
	mk := func(id string, v float64) qdl.Parameter {
		return qdl.Parameter{
			ID: id, Unit: "units", Frozen: true,
			Prior: qdl.Distribution{Kind: qdl.DistPoint, Params: map[string]float64{"value": v}},
		}
	}
	spec := &qdl.PlanSpec{
		ID: "t/sel@2026-08", Vendor: "t",
		Parameters: []qdl.Parameter{mk("t.C", 200), mk("t.w", 0.002)},
		Buckets: []qdl.Bucket{{
			ID: "b", Unit: qdl.DimOpaqueUnits, Capacity: qdl.Ref("t.C"),
			Window: qdl.Window{KindCandidates: kinds, Length: qdl.Duration{Duration: length}, Reset: qdl.ResetZero},
			Charge: qdl.ChargeRule{Terms: []qdl.Term{{Dim: qdl.DimInputTokens, Coeff: qdl.Ref("t.w")}}},
		}},
	}
	return spec, qdl.ParamPoint{"t.C": 200, "t.w": 0.002}
}

// timedStore 建临时事件库，按给定时刻（升序）追加负载。
func timedStore(t *testing.T, ts []time.Time, ps []ledger.Payload) ledger.Store {
	t.Helper()
	if len(ts) != len(ps) {
		t.Fatalf("时间与负载数不一致: %d vs %d", len(ts), len(ps))
	}
	s, err := ledger.NewJSONLStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	for i := range ps {
		if _, err := s.Append(ts[i], ps[i]); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
	return s
}

// synthForKind 用「真值结构」生成合成观测：请求流 + 观测点进事件库，
// 观测 y = round(100·U_true/C)（整数百分比量化）。模型正确指定的
// 合成范式——辨识器恢复出真结构即验证选择机制。
func synthForKind(t *testing.T, spec *qdl.PlanSpec, truth qdl.ParamPoint, kind qdl.WindowKind) *Dataset {
	t.Helper()
	// 请求流：前 2.5h 密集、之后稀疏（拉开 sliding 的渐进衰减与
	// tumbling 的整窗归零在观测序列上的形状差）。
	reqH := []float64{0.5, 1, 1.5, 2, 2.5, 6, 6.5, 7, 10}
	obsH := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	rng := rand.New(rand.NewSource(7))
	t0 := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	at := func(h float64) time.Time { return t0.Add(time.Duration(h * float64(time.Hour))) }

	var ts []time.Time
	var ps []ledger.Payload
	for _, h := range reqH {
		tokens := 1000 + rng.Float64()*8000 // 每请求扣 2~18（1~9%）
		req := &semantics.Request{ChannelID: "ch", Model: "m",
			Dims: map[qdl.Dim]float64{qdl.DimInputTokens: tokens}}
		deltas, err := semantics.Charge(spec, req, truth, semantics.ChargeModeLinearEV)
		if err != nil {
			t.Fatalf("Charge: %v", err)
		}
		ts = append(ts, at(h))
		ps = append(ps, &ledger.ChargeEvent{
			RequestID: "r" + time.Duration(h*10).String(), PlanID: spec.ID,
			ChannelID: "ch", Model: "m",
			Dims:         map[qdl.Dim]float64{qdl.DimInputTokens: tokens},
			BucketDeltas: deltas, ThetaVersion: "truth",
		})
	}
	for _, h := range obsH {
		ts = append(ts, at(h))
		ps = append(ps, &ledger.ObservationEvent{
			PlanID: spec.ID, BucketID: "b", Semantic: qdl.SemUsedPct,
			RawValue: "0", Quantization: qdl.Quantization{Kind: "integer"},
			Source: qdl.ObsResponseHeader, Trust: 1,
		})
	}
	// 时刻升序稳定合并（请求与观测交错）。
	idx := make([]int, len(ts))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return ts[idx[a]].Before(ts[idx[b]]) })
	ots, ops := make([]time.Time, len(ts)), make([]ledger.Payload, len(ps))
	for i, j := range idx {
		ots[i], ops[i] = ts[j], ps[j]
	}
	store := timedStore(t, ots, ops)

	// 真值结构重放 → 观测取整。
	ds0 := ExtractDatasetOrFatal(t, spec, store)
	dsT := &Dataset{Spec: cloneSpecWithKind(spec, 0, kind), Store: store, Obs: ds0.Obs}
	mus, err := dsT.Predict(truth)
	if err != nil {
		t.Fatalf("真值结构 Predict: %v", err)
	}
	obs := make([]ObsPoint, len(ds0.Obs))
	for i, o := range ds0.Obs {
		o.Y = math.Round(mus[i])
		obs[i] = o
	}
	return &Dataset{Spec: spec, Store: store, Obs: obs}
}

// ExtractDatasetOrFatal 是 ExtractDataset 的测试便捷封装。
func ExtractDatasetOrFatal(t *testing.T, spec *qdl.PlanSpec, store ledger.Store) *Dataset {
	t.Helper()
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	return ds
}

// ---- 选择器 ----

// TestSelectStructureRecoversSliding 真结构 sliding_exact：5h 窗下
// 渐进衰减 vs 整窗归零的观测形状差应给出 > 0.9 后验（Phase 2 验收口径）。
func TestSelectStructureRecoversSliding(t *testing.T) {
	spec, truth := selSpec([]qdl.WindowKind{qdl.WindowTumblingAnchoredOnFirstUse, qdl.WindowSlidingExact}, 5*time.Hour)
	ds := synthForKind(t, spec, truth, qdl.WindowSlidingExact)
	choices, err := SelectStructure(ds, truth, truth, SelectOptions{})
	if err != nil {
		t.Fatalf("SelectStructure: %v", err)
	}
	if len(choices) != 1 || choices[0].BucketID != "b" || choices[0].Field != "window.kind" {
		t.Fatalf("应恰有一个 window.kind 选择: %+v", choices)
	}
	if p := choices[0].Posterior["sliding_exact"]; p < 0.9 {
		t.Fatalf("sliding_exact 后验 %.3f < 0.9（scores=%v）", p, choices[0].Scores)
	}
}

// TestSelectStructureRecoversTumbling 真结构 anchored tumbling：
// 反向对照——同一机制必须两个方向都能判。
func TestSelectStructureRecoversTumbling(t *testing.T) {
	spec, truth := selSpec([]qdl.WindowKind{qdl.WindowTumblingAnchoredOnFirstUse, qdl.WindowSlidingExact}, 5*time.Hour)
	ds := synthForKind(t, spec, truth, qdl.WindowTumblingAnchoredOnFirstUse)
	choices, err := SelectStructure(ds, truth, truth, SelectOptions{})
	if err != nil {
		t.Fatalf("SelectStructure: %v", err)
	}
	if p := choices[0].Posterior["tumbling_anchored_on_first_use"]; p < 0.9 {
		t.Fatalf("tumbling 后验 %.3f < 0.9（scores=%v）", p, choices[0].Scores)
	}
}

// TestSelectStructureRecoversCalendar 真结构 tumbling_calendar：
// 固定日历锚点整窗归零 vs sliding 渐进衰减（跨 UTC 零点边界的数据）。
func TestSelectStructureRecoversCalendar(t *testing.T) {
	spec, truth := selSpec([]qdl.WindowKind{qdl.WindowSlidingExact, qdl.WindowTumblingCalendar}, 6*time.Hour)
	ds := synthForKind(t, spec, truth, qdl.WindowTumblingCalendar)
	choices, err := SelectStructure(ds, truth, truth, SelectOptions{})
	if err != nil {
		t.Fatalf("SelectStructure: %v", err)
	}
	if p := choices[0].Posterior["tumbling_calendar"]; p < 0.9 {
		t.Fatalf("tumbling_calendar 后验 %.3f < 0.9（scores=%v）", p, choices[0].Scores)
	}
}

// TestSelectStructureDeterministic 同输入两次调用逐位一致（估计器确定性
// 契约延伸到结构选择——审计与复现的硬要求）。
func TestSelectStructureDeterministic(t *testing.T) {
	spec, truth := selSpec([]qdl.WindowKind{qdl.WindowTumblingAnchoredOnFirstUse, qdl.WindowSlidingExact}, 5*time.Hour)
	ds := synthForKind(t, spec, truth, qdl.WindowSlidingExact)
	a, err := SelectStructure(ds, truth, truth, SelectOptions{})
	if err != nil {
		t.Fatalf("第一次: %v", err)
	}
	b, err := SelectStructure(ds, truth, truth, SelectOptions{})
	if err != nil {
		t.Fatalf("第二次: %v", err)
	}
	for _, cand := range []string{"sliding_exact", "tumbling_anchored_on_first_use"} {
		if a[0].Posterior[cand] != b[0].Posterior[cand] {
			t.Fatalf("后验非确定: %s %v vs %v", cand, a[0].Posterior[cand], b[0].Posterior[cand])
		}
		if a[0].Scores[cand] != b[0].Scores[cand] {
			t.Fatalf("打分非确定: %s %v vs %v", cand, a[0].Scores[cand], b[0].Scores[cand])
		}
	}
}

// TestSelectStructureGuards 无观测报错；单候选桶跳过。
func TestSelectStructureGuards(t *testing.T) {
	spec, truth := selSpec([]qdl.WindowKind{qdl.WindowTumblingAnchoredOnFirstUse, qdl.WindowSlidingExact}, 5*time.Hour)
	ds := synthForKind(t, spec, truth, qdl.WindowSlidingExact)
	if _, err := SelectStructure(&Dataset{Spec: spec, Store: ds.Store}, truth, truth, SelectOptions{}); err == nil {
		t.Fatal("无观测应报错")
	}
	// 单候选：无结构未知量，返回空。
	one, _ := selSpec([]qdl.WindowKind{qdl.WindowSlidingExact}, 5*time.Hour)
	ds1 := &Dataset{Spec: one, Store: ds.Store, Obs: ds.Obs}
	choices, err := SelectStructure(ds1, truth, truth, SelectOptions{})
	if err != nil {
		t.Fatalf("单候选不应报错: %v", err)
	}
	if len(choices) != 0 {
		t.Fatalf("单候选桶应跳过: %+v", choices)
	}
}

// TestStructEvents 事件生成：before 取自 spec 现有后验（无则 nil），
// after 为选择结果，Validate 的概率向量约束必须满足。
func TestStructEvents(t *testing.T) {
	spec, truth := selSpec([]qdl.WindowKind{qdl.WindowTumblingAnchoredOnFirstUse, qdl.WindowSlidingExact}, 5*time.Hour)
	ds := synthForKind(t, spec, truth, qdl.WindowSlidingExact)
	choices, err := SelectStructure(ds, truth, truth, SelectOptions{})
	if err != nil {
		t.Fatalf("SelectStructure: %v", err)
	}
	evs, err := StructEvents(spec, choices)
	if err != nil {
		t.Fatalf("StructEvents: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("应产出 1 个事件: %d", len(evs))
	}
	ev := evs[0]
	if ev.PlanID != spec.ID || ev.BucketID != "b" || ev.Field != "window.kind" {
		t.Fatalf("事件身份字段: %+v", ev)
	}
	if ev.PosteriorBefore != nil {
		t.Fatalf("首次判定 before 应为 nil: %v", ev.PosteriorBefore)
	}
	sum := 0.0
	for _, p := range ev.PosteriorAfter {
		sum += p
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("后验和 %v ≠ 1", sum)
	}

	// 写回先验后验再选一次：before 应带上一次结果。
	spec.Buckets[0].Window.KindPosterior = map[qdl.WindowKind]float64{
		qdl.WindowSlidingExact:               ev.PosteriorAfter["sliding_exact"],
		qdl.WindowTumblingAnchoredOnFirstUse: ev.PosteriorAfter["tumbling_anchored_on_first_use"],
	}
	evs2, err := StructEvents(spec, choices)
	if err != nil {
		t.Fatalf("StructEvents(带 before): %v", err)
	}
	if evs2[0].PosteriorBefore == nil || len(evs2[0].PosteriorBefore) != 2 {
		t.Fatalf("before 应含两候选: %+v", evs2[0].PosteriorBefore)
	}
}

// ---- 类别型参数排除 ----

// TestCategoricalParamExcludedFromSpace 类别型结构参数（discrete +
// categories，如 GLM 的 prompt_granularity）不得进数值自由空间——
// 无 float64 语义且先验恒 -Inf，进去了整个估计爆掉（C1 修复的 bug）。
func TestCategoricalParamExcludedFromSpace(t *testing.T) {
	spec := &qdl.PlanSpec{
		ID: "t/cat@2026-08", Vendor: "t",
		Parameters: []qdl.Parameter{
			{ID: "g", Unit: "categorical", Prior: qdl.Distribution{
				Kind: qdl.DistDiscrete, Categories: []string{"turn", "request"},
				CategoryProbs: []float64{0.5, 0.5},
			}},
			{ID: "t.C", Unit: "units", Prior: qdl.Distribution{
				Kind: qdl.DistNormal, Params: map[string]float64{"mu": 100, "sigma": 150}}},
		},
		Buckets: []qdl.Bucket{{
			ID: "b", Unit: qdl.DimOpaqueUnits, Capacity: qdl.Ref("t.C"),
			Window: qdl.Window{KindCandidates: []qdl.WindowKind{qdl.WindowSlidingExact},
				Length: qdl.Duration{Duration: time.Hour}, Reset: qdl.ResetZero},
			Charge: qdl.ChargeRule{Terms: []qdl.Term{{Dim: qdl.DimInputTokens, Coeff: qdl.Const(0.01)}}},
		}},
	}
	store := timedStore(t,
		[]time.Time{mustTime("2026-08-20T08:00:00Z"), mustTime("2026-08-20T08:10:00Z")},
		[]ledger.Payload{
			&ledger.ChargeEvent{RequestID: "r1", PlanID: spec.ID, ChannelID: "ch", Model: "m",
				Dims:         map[qdl.Dim]float64{qdl.DimInputTokens: 500},
				BucketDeltas: map[string]float64{"b": 5}, ThetaVersion: "v1"},
			&ledger.ObservationEvent{PlanID: spec.ID, BucketID: "b", Semantic: qdl.SemUsedPct,
				RawValue: "5", Quantization: qdl.Quantization{Kind: "integer"},
				Source: qdl.ObsResponseHeader, Trust: 1},
		})
	ds := ExtractDatasetOrFatal(t, spec, store)
	ps, err := NewParamSpace(spec, nil)
	if err != nil {
		t.Fatalf("NewParamSpace: %v", err)
	}
	for _, id := range ps.IDs {
		if id == "g" {
			t.Fatalf("类别参数 g 不应进自由空间: %v", ps.IDs)
		}
	}
	// 类别参数在位时估计不爆（修复前此处 -Inf 失败）。
	res, err := Estimate(ds, qdl.ParamPoint{"t.C": 100}, qdl.ParamPoint{"t.C": 100}, EstimateOptions{})
	if err != nil {
		t.Fatalf("Estimate 带类别参数: %v", err)
	}
	if math.IsInf(res.LogLikelihood, 0) || math.IsNaN(res.LogLikelihood) {
		t.Fatalf("LogLikelihood 应有限: %v", res.LogLikelihood)
	}
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
