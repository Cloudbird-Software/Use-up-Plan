package ledger

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// testSpec 构造重放/对账测试用 plan：单桶 b5（5h 首用起锚 tumbling 窗，
// 容量参数引用 C，扣减 = w × input_tokens，均为参数引用以覆盖 Coeff 求值）。
func testSpec() *qdl.PlanSpec {
	return &qdl.PlanSpec{
		ID:     "t/replay@2026-08",
		Vendor: "t",
		Parameters: []qdl.Parameter{
			{ID: "t.C", Unit: "units", Prior: qdl.Point(100)},
			{ID: "t.w", Unit: "units_per_token", Prior: qdl.Point(0.001)},
		},
		Buckets: []qdl.Bucket{{
			ID:       "b5",
			Unit:     qdl.DimOpaqueUnits,
			Capacity: qdl.Ref("t.C"),
			Window: qdl.Window{
				KindCandidates: []qdl.WindowKind{qdl.WindowTumblingAnchoredOnFirstUse},
				Length:         qdl.Duration{Duration: 5 * time.Hour},
				Reset:          qdl.ResetZero,
			},
			Charge: qdl.ChargeRule{
				Terms: []qdl.Term{{Dim: qdl.DimInputTokens, Coeff: qdl.Ref("t.w")}},
			},
		}},
	}
}

// testTheta 完整参数快照：C=100, w=0.001（每 1000 token 扣 1）。
func testTheta() qdl.ParamPoint {
	return qdl.ParamPoint{"t.C": 100, "t.w": 0.001}
}

// mkStore 建临时事件库并写入事件（时间必须升序）。
func mkStore(t *testing.T, events ...Payload) Store {
	t.Helper()
	s, err := NewJSONLStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	base := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	for i, p := range events {
		if _, err := s.Append(base.Add(time.Duration(i)*time.Minute), p); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}
	return s
}

// TestReplayAsRecorded 存量口径：U 等于事件里 bucket_deltas 的和；
// 观测事件不动账本。
func TestReplayAsRecorded(t *testing.T) {
	spec := testSpec()
	store := mkStore(t,
		&ChargeEvent{
			RequestID: "r1", PlanID: spec.ID, ChannelID: "ch", Model: "m",
			Dims:         map[qdl.Dim]float64{qdl.DimInputTokens: 1000},
			BucketDeltas: map[string]float64{"b5": 1.0}, ThetaVersion: "v1",
		},
		&ObservationEvent{
			PlanID: spec.ID, BucketID: "b5", Semantic: qdl.SemUsedPct,
			RawValue: "1", Quantization: qdl.Quantization{Kind: "integer"},
			Source: qdl.ObsResponseHeader, Trust: 1,
		},
		&ChargeEvent{
			RequestID: "r2", PlanID: spec.ID, ChannelID: "ch", Model: "m",
			Dims:         map[qdl.Dim]float64{qdl.DimInputTokens: 3000},
			BucketDeltas: map[string]float64{"b5": 3.0}, ThetaVersion: "v1",
		},
	)
	res, err := Replay(spec, store, ReplayOptions{Theta: testTheta(), Mode: ReplayAsRecorded})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	st := res.State.Buckets["b5"]
	if st.U != 4.0 {
		t.Fatalf("U = %v，应 4.0（1+3，观测不动账）", st.U)
	}
	if st.Anchor == nil || !st.Anchor.Equal(time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("首用起锚失败: %+v", st.Anchor)
	}
	if s := res.Stats["b5"]; s.Charges != 2 || s.TotalDelta != 4.0 {
		t.Fatalf("统计: %+v", s)
	}
}

// TestReplayRecompute 重算口径：用 θ 对原始 dims 重算扣减（1000 token → 1）。
func TestReplayRecompute(t *testing.T) {
	spec := testSpec()
	store := mkStore(t,
		&ChargeEvent{
			RequestID: "r1", PlanID: spec.ID, ChannelID: "ch", Model: "m",
			Dims:         map[qdl.Dim]float64{qdl.DimInputTokens: 2500},
			BucketDeltas: map[string]float64{"b5": 99}, ThetaVersion: "v1", // 存量是错的
		},
	)
	res, err := Replay(spec, store, ReplayOptions{Theta: testTheta(), Mode: ReplayRecompute})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if st := res.State.Buckets["b5"]; st.U != 2.5 {
		t.Fatalf("U = %v，应 2.5（2500×0.001，忽略存量 99）", st.U)
	}
}

// TestReplayWindowReset 窗语义生效：第二次扣减落在 5h 窗外 → 先归零再入账。
func TestReplayWindowReset(t *testing.T) {
	spec := testSpec()
	s, err := NewJSONLStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	defer s.Close()
	base := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	c1 := &ChargeEvent{
		RequestID: "r1", PlanID: spec.ID, ChannelID: "ch", Model: "m",
		Dims:         map[qdl.Dim]float64{qdl.DimInputTokens: 9000},
		BucketDeltas: map[string]float64{"b5": 9}, ThetaVersion: "v1",
	}
	c2 := &ChargeEvent{
		RequestID: "r2", PlanID: spec.ID, ChannelID: "ch", Model: "m",
		Dims:         map[qdl.Dim]float64{qdl.DimInputTokens: 2000},
		BucketDeltas: map[string]float64{"b5": 2}, ThetaVersion: "v1",
	}
	if _, err := s.Append(base, c1); err != nil {
		t.Fatal(err)
	}
	// 6 小时后：跨过 5h 窗边界（首用锚点 + 5h）
	if _, err := s.Append(base.Add(6*time.Hour), c2); err != nil {
		t.Fatal(err)
	}
	res, err := Replay(spec, s, ReplayOptions{Theta: testTheta(), Mode: ReplayAsRecorded})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if st := res.State.Buckets["b5"]; st.U != 2.0 {
		t.Fatalf("U = %v，应 2.0（窗归零后仅第二笔）", st.U)
	}
}

// TestReplayTimeReversal 事件时间倒流必须报错（契约：按时间升序入库）。
func TestReplayTimeReversal(t *testing.T) {
	spec := testSpec()
	base := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	s, err := NewJSONLStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	defer s.Close()
	s.Append(base, &ChargeEvent{
		RequestID: "r1", PlanID: spec.ID, ThetaVersion: "v",
		Dims: map[qdl.Dim]float64{qdl.DimInputTokens: 1}, BucketDeltas: map[string]float64{"b5": 1},
	})
	s.Append(base.Add(-time.Hour), &ChargeEvent{
		RequestID: "r2", PlanID: spec.ID, ThetaVersion: "v",
		Dims: map[qdl.Dim]float64{qdl.DimInputTokens: 1}, BucketDeltas: map[string]float64{"b5": 1},
	})
	if _, err := Replay(spec, s, ReplayOptions{Theta: testTheta(), Mode: ReplayAsRecorded}); err == nil {
		t.Fatal("时间倒流应报错")
	}
}

// TestReplayFiltersOtherPlan 其他 plan 的事件不进本 spec 的重放。
func TestReplayFiltersOtherPlan(t *testing.T) {
	spec := testSpec()
	store := mkStore(t,
		&ChargeEvent{
			RequestID: "r1", PlanID: "other/plan@2026-08", ThetaVersion: "v",
			Dims:         map[qdl.Dim]float64{qdl.DimInputTokens: 5000},
			BucketDeltas: map[string]float64{"b_other": 5},
		},
	)
	res, err := Replay(spec, store, ReplayOptions{Theta: testTheta(), Mode: ReplayAsRecorded})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if st := res.State.Buckets["b5"]; st.U != 0 {
		t.Fatalf("他 plan 事件污染: U = %v", st.U)
	}
}
