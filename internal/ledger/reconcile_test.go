package ledger

import (
	"fmt"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// series 是「扣减 + 观测」交替时间线构造器（对账测试专用）。
// θ 口径：每 1000 token 扣 1（容量 100）→ 每笔 1000 token = 1%。
type series struct {
	specID string
	t      time.Time
	evs    []Payload
}

func newSeries(specID string) *series {
	return &series{specID: specID, t: time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)}
}

// charge 追加 n 笔各 1000 token 的扣减。
func (s *series) charge(n int) *series {
	for i := 0; i < n; i++ {
		s.t = s.t.Add(time.Minute)
		s.evs = append(s.evs, &ChargeEvent{
			RequestID: fmt.Sprintf("r%d", len(s.evs)), PlanID: s.specID,
			ChannelID: "ch", Model: "m",
			Dims:         map[qdl.Dim]float64{qdl.DimInputTokens: 1000},
			BucketDeltas: map[string]float64{"b5": 1}, ThetaVersion: "v1",
		})
	}
	return s
}

// chargeTokens 追加一笔任意 token 数的扣减（预测可为小数百分比）。
func (s *series) chargeTokens(tokens float64) *series {
	s.t = s.t.Add(time.Minute)
	s.evs = append(s.evs, &ChargeEvent{
		RequestID: fmt.Sprintf("r%d", len(s.evs)), PlanID: s.specID,
		ChannelID: "ch", Model: "m",
		Dims:         map[qdl.Dim]float64{qdl.DimInputTokens: tokens},
		BucketDeltas: map[string]float64{"b5": tokens * 0.001}, ThetaVersion: "v1",
	})
	return s
}

// obs 追加整数百分比观测（used_pct）。
func (s *series) obs(usedPct int) *series {
	s.t = s.t.Add(time.Minute)
	s.evs = append(s.evs, &ObservationEvent{
		PlanID: s.specID, BucketID: "b5", Semantic: qdl.SemUsedPct,
		RawValue:     fmt.Sprintf("%d", usedPct),
		Quantization: qdl.Quantization{Kind: "integer"},
		Source:       qdl.ObsUsageEndpoint, Trust: 1,
	})
	return s
}

// obsRaw 追加任意语义/原始值的观测。
func (s *series) obsRaw(sem qdl.Semantic, raw string) *series {
	s.t = s.t.Add(time.Minute)
	s.evs = append(s.evs, &ObservationEvent{
		PlanID: s.specID, BucketID: "b5", Semantic: sem,
		RawValue: raw, Quantization: qdl.Quantization{Kind: "integer"},
		Source: qdl.ObsUsageEndpoint, Trust: 1,
	})
	return s
}

// TestReconcileAttributions 归因表核心模式（注入式验收：Intent §3.4 表）。
func TestReconcileAttributions(t *testing.T) {
	spec := testSpec()

	cases := []struct {
		name string
		evs  []Payload
		want AttributionKind
	}{
		{
			// ① 量化噪声：预测 2.4/4.1/5.7，整数观测 2/4/6 → 残差 -0.4/-0.1/+0.3
			name: "量化噪声",
			evs: newSeries(spec.ID).
				chargeTokens(2400).obs(2).
				chargeTokens(1700).obs(4).
				chargeTokens(1600).obs(6).evs,
			want: AttributionQuantNoise,
		},
		{
			// ② 外生消耗：预测 1/2/3，观测 4/5/6 → 恒定 +3（手动用了）
			name: "外生消耗",
			evs: newSeries(spec.ID).
				charge(1).obs(4).charge(1).obs(5).charge(1).obs(6).evs,
			want: AttributionExogenous,
		},
		{
			// ③ 系数漂移：残差 [0,0,0,+5,+5,+5]，变点在第 3 条观测后
			name: "系数漂移",
			evs: newSeries(spec.ID).
				charge(2).obs(2).charge(2).obs(4).charge(2).obs(6).
				charge(2).obs(13).charge(2).obs(15).charge(2).obs(17).evs,
			want: AttributionDrift,
		},
		{
			// ④ 未建模 flat：残差 [2,3,4,6] 与区间请求数 [1,1,1,2] 成比例
			name: "未建模flat",
			evs: newSeries(spec.ID).
				charge(1).obs(3).charge(1).obs(5).charge(1).obs(7).
				charge(2).obs(11).evs,
			want: AttributionUnmodeledFlat,
		},
		{
			// ⑤ 负偏：预测 3/6/9，观测 1/4/7 → 恒定 -2（θ 高估）
			name: "负偏",
			evs: newSeries(spec.ID).
				charge(3).obs(1).charge(3).obs(4).charge(3).obs(7).evs,
			want: AttributionNegativeBias,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := mkStore(t, c.evs...)
			rep, err := Reconcile(spec, store, testTheta())
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if len(rep.Buckets) != 1 {
				t.Fatalf("报告桶数 %d，应 1", len(rep.Buckets))
			}
			br := rep.Buckets[0]
			if br.Attribution != c.want {
				t.Fatalf("归因 = %s（%s），应 %s", br.Attribution, br.Evidence, c.want)
			}
			if br.Evidence == "" {
				t.Fatal("归因证据为空")
			}
		})
	}
}

// TestReconcileQuantNoiseValues 量化噪声场景的残差数值抽查。
func TestReconcileQuantNoiseValues(t *testing.T) {
	spec := testSpec()
	store := mkStore(t, newSeries(spec.ID).
		chargeTokens(2400).obs(2).
		chargeTokens(1700).obs(4).
		chargeTokens(1600).obs(6).evs...)
	rep, err := Reconcile(spec, store, testTheta())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	br := rep.Buckets[0]
	wantRes := []float64{-0.4, -0.1, 0.3}
	for i, w := range wantRes {
		if r := br.Residuals[i].Residual; r < w-1e-9 || r > w+1e-9 {
			t.Fatalf("残差[%d] = %v，应 %v", i, r, w)
		}
		if br.Residuals[i].PredictedPct == 0 {
			t.Fatalf("预测[%d] 不应为 0", i)
		}
	}
}

// TestReconcileStructureMisjudged 观测重置但账本未重置 → 结构错判。
func TestReconcileStructureMisjudged(t *testing.T) {
	spec := testSpec()
	reset := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	// b5 是 5h 首用起锚 tumbling：8 分钟内积累 8%，远未到窗边界；
	// 但厂商观测到已重置——窗口语义判断错误的直接证据。
	evs := newSeries(spec.ID).charge(8).evs
	evs = append(evs, &ResetObservedEvent{
		PlanID: spec.ID, BucketID: "b5", PrevU: 8, NewU: 0, ResetAtReported: &reset,
	})
	store := mkStore(t, evs...)
	rep, err := Reconcile(spec, store, testTheta())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	br := rep.Buckets[0]
	if br.Attribution != AttributionStructure {
		t.Fatalf("归因 = %s（%s），应 structure_misjudged", br.Attribution, br.Evidence)
	}
	if br.ResetMismatch != 1 {
		t.Fatalf("ResetMismatch = %d，应 1", br.ResetMismatch)
	}
}

// TestReconcileRemainingPct remaining 语义换算为已用百分比后归因。
func TestReconcileRemainingPct(t *testing.T) {
	spec := testSpec()
	store := mkStore(t, newSeries(spec.ID).
		charge(2).obsRaw(qdl.SemRemainingPct, "91"). // 已用 9%，预测 2% → +7
		charge(2).obsRaw(qdl.SemRemainingPct, "87"). // 已用 13%，预测 4% → +9
		charge(2).obsRaw(qdl.SemRemainingPct, "84"). // 已用 16%，预测 6% → +10
		evs...)
	rep, err := Reconcile(spec, store, testTheta())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	br := rep.Buckets[0]
	if br.N != 3 || br.Residuals[0].ObservedPct != 9 {
		t.Fatalf("remaining→used 换算失败: N=%d obs=%v", br.N, br.Residuals[0].ObservedPct)
	}
	if br.Attribution != AttributionExogenous {
		t.Fatalf("归因 = %s（%s），应 exogenous_drain", br.Attribution, br.Evidence)
	}
}

// TestReconcileUnparseableSkipped 不支持的语义计入 ParseSkipped → 观测不足。
func TestReconcileUnparseableSkipped(t *testing.T) {
	spec := testSpec()
	store := mkStore(t, newSeries(spec.ID).
		charge(1).obsRaw(qdl.SemResetAtISO, "not-a-number").evs...)
	rep, err := Reconcile(spec, store, testTheta())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	br := rep.Buckets[0]
	if br.ParseSkipped != 1 || br.N != 0 {
		t.Fatalf("ParseSkipped=%d N=%d，应 1/0", br.ParseSkipped, br.N)
	}
	if br.Attribution != AttributionInsufficient {
		t.Fatalf("归因 = %s，应 insufficient_data", br.Attribution)
	}
}

// TestReconcileSkipUnknownBucketObs 指向未知桶的观测不炸、不进报告。
func TestReconcileSkipUnknownBucketObs(t *testing.T) {
	spec := testSpec()
	store := mkStore(t, &ObservationEvent{
		PlanID: spec.ID, BucketID: "b_nope", Semantic: qdl.SemUsedPct,
		RawValue: "50", Quantization: qdl.Quantization{Kind: "integer"},
		Source: qdl.ObsResponseHeader, Trust: 1,
	})
	rep, err := Reconcile(spec, store, testTheta())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(rep.Buckets) != 0 {
		t.Fatalf("未知桶观测不应产生报告项: %+v", rep.Buckets)
	}
}
