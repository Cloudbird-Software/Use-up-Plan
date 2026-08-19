package semantics

import (
	"math"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// mkPlan 构造单桶单通道最小 spec（charge/admit 测试用）。
func mkPlan(t *testing.T) *qdl.PlanSpec {
	t.Helper()
	return &qdl.PlanSpec{
		ID: "t/test@2026-08", Vendor: "t", PlanName: "Test",
		Buckets: []qdl.Bucket{{
			ID:       "b_tok",
			Unit:     qdl.DimOpaqueUnits,
			Capacity: qdl.Const(1000),
			Window:   qdl.Window{KindCandidates: []qdl.WindowKind{qdl.WindowNever}},
			Scope:    qdl.Scope{Level: qdl.ScopeAccount},
			Charge: qdl.ChargeRule{
				Flat: qdl.Const(0), Floor: qdl.Const(0),
				Terms: []qdl.Term{
					{Dim: qdl.DimInputTokens, Coeff: qdl.Const(1)},
					{Dim: qdl.DimOutputTokens, Coeff: qdl.Const(3), Quantize: qdl.Quantize{Mode: qdl.QuantizeCeil, Step: 1000}},
				},
			},
		}},
		Channels: []qdl.Channel{{
			ID: "ch1", Protocol: "anthropic_messages", Auth: "oauth_bearer",
			Admission: qdl.AdmissionPolicy{Limits: map[qdl.InstantDim]qdl.Coeff{
				qdl.InstantConcurrency:       qdl.Const(5),
				qdl.InstantContextTokensPeak: qdl.Const(200000),
			}},
		}},
	}
}

func TestChargeExactVsLinearEV(t *testing.T) {
	spec := mkPlan(t)
	req := &Request{
		ChannelID: "ch1", Model: "model-x",
		Dims: map[qdl.Dim]float64{
			qdl.DimInputTokens:  2500,
			qdl.DimOutputTokens: 300, // ceil(300/1000)*1000 = 1000（EXACT）
		},
	}
	// EXACT：2500 + 3×1000 = 5500
	exact, err := Charge(spec, req, nil, ChargeModeExact)
	if err != nil {
		t.Fatalf("Charge EXACT: %v", err)
	}
	if exact["b_tok"] != 5500 {
		t.Fatalf("EXACT 应精确量化: %v", exact["b_tok"])
	}
	// LINEAR_EV：2500 + 3×(300+500) = 4900（期望值近似）
	ev, err := Charge(spec, req, nil, ChargeModeLinearEV)
	if err != nil {
		t.Fatalf("Charge EV: %v", err)
	}
	if ev["b_tok"] != 4900 {
		t.Fatalf("LINEAR_EV 期望近似: %v", ev["b_tok"])
	}
	// 上界：2500 + 3×(300+1000) = 6400
	rc, _ := ResolveCharge(&spec.Buckets[0].Charge, nil)
	if ub := ChargeUpperBound(&rc, req); ub != 6400 {
		t.Fatalf("上界: %v", ub)
	}
	// 不变式：EV <= EXACT <= UB（本例成立；一般由量化凸性保证近似序）
	if !(ev["b_tok"] <= exact["b_tok"] && exact["b_tok"] <= 6400) {
		t.Fatalf("序不变式破坏: ev=%v exact=%v ub=6400", ev["b_tok"], exact["b_tok"])
	}
}

func TestChargeFloorMaxAndMultiplier(t *testing.T) {
	// floor 与模型倍率
	spec := mkPlan(t)
	b := &spec.Buckets[0]
	b.Charge.Floor = qdl.Const(10)
	b.Charge.ModelMultiplier = map[string]qdl.Coeff{"model-opus-*": qdl.Const(2)}
	b.Charge.Terms = []qdl.Term{{Dim: qdl.DimRequests, Coeff: qdl.Const(1)}}
	req := &Request{Model: "model-opus-1", Dims: map[qdl.Dim]float64{qdl.DimRequests: 3}}
	// EXACT: max(2×3, 10) = 10
	got, err := Charge(spec, req, nil, ChargeModeExact)
	if err != nil || got["b_tok"] != 10 {
		t.Fatalf("floor: %v err=%v", got["b_tok"], err)
	}
	req.Dims[qdl.DimRequests] = 10
	// EXACT: max(2×10, 10) = 20
	got, _ = Charge(spec, req, nil, ChargeModeExact)
	if got["b_tok"] != 20 {
		t.Fatalf("倍率: %v", got["b_tok"])
	}
	// glob 最长 pattern 优先（model-opus-pro 同时命中 * 与精确 pattern，取后者）
	req.Model = "model-opus-pro"
	b.Charge.ModelMultiplier["model-opus-pro"] = qdl.Const(4)
	got, _ = Charge(spec, req, nil, ChargeModeExact)
	if got["b_tok"] != 40 {
		t.Fatalf("最长 pattern: %v", got["b_tok"])
	}
	// LINEAR_EV 含 floor 上界：0 + 10 + 4×10 = 50
	ev, _ := Charge(spec, req, nil, ChargeModeLinearEV)
	if ev["b_tok"] != 50 {
		t.Fatalf("EV floor 上界: %v", ev["b_tok"])
	}
}

// TestGlobBestDeterministicTie 等长 pattern 平手曾按 map 迭代序随机取胜——
// 修复后取字典序最小者，且跨调用稳定（估计器逐位可复现的前提）。
func TestGlobBestDeterministicTie(t *testing.T) {
	table := map[string]float64{
		"m-a-*":   3, // 与 m-*-x 等长（5），匹配 m-a-x 时平手
		"m-*-x":   2, // 字典序更小
		"m-a-b-*": 5, // 更长，出现时无条件取胜
	}
	for i := 0; i < 200; i++ {
		if got := globBest(table, "m-a-x"); got != 2 {
			t.Fatalf("等长平手应取字典序最小 pattern 的倍率 2，得 %v（第 %d 次）", got, i)
		}
	}
	if got := globBest(table, "m-a-b-x"); got != 5 {
		t.Fatalf("最长 pattern 应取胜，得 %v", got)
	}
	if got := globBest(table, "m-a-y"); got != 3 {
		t.Fatalf("唯一匹配 m-a-* 应得 3，得 %v", got)
	}
	if got := globBest(table, "zzz"); got != 1 {
		t.Fatalf("无匹配应得默认 1，得 %v", got)
	}
}

func TestChargeParamRefAndScope(t *testing.T) {
	spec := mkPlan(t)
	b := &spec.Buckets[0]
	b.Charge.Terms[0].Coeff = qdl.Ref("t.w_in")
	// scope 过滤：模型不在 scope.models 则不命中
	b.Scope.Models = []string{"model-x"}

	req := &Request{Model: "model-y", Dims: map[qdl.Dim]float64{qdl.DimInputTokens: 100}}
	got, err := Charge(spec, req, nil, ChargeModeExact)
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("scope 外模型不应命中: %v", got)
	}
	req.Model = "model-x"
	if _, err := Charge(spec, req, nil, ChargeModeExact); err == nil {
		t.Fatal("缺参数应报错")
	}
	theta := qdl.ParamPoint{"t.w_in": 0.5}
	got, _ = Charge(spec, req, theta, ChargeModeExact)
	if got["b_tok"] != 50 {
		t.Fatalf("ParamRef 权重: %v", got["b_tok"])
	}
}

// mkAdmitPlan 构造 admit 测试专用 spec：容量 1000，charge = 1×output(ceil 400)。
// output=300 时 EXACT=400、EV=300+200=500、UB=300+400=700——三者分离，
// 恰好覆盖 ALLOW / ALLOW_WITH_RISK / DENY_QUOTA 的判定边界。
func mkAdmitPlan(t *testing.T) *qdl.PlanSpec {
	t.Helper()
	return &qdl.PlanSpec{
		ID: "t/admit@2026-08", Vendor: "t", PlanName: "Admit",
		Buckets: []qdl.Bucket{{
			ID:       "b_tok",
			Unit:     qdl.DimOpaqueUnits,
			Capacity: qdl.Const(1000),
			Window:   qdl.Window{KindCandidates: []qdl.WindowKind{qdl.WindowNever}},
			Scope:    qdl.Scope{Level: qdl.ScopeAccount},
			Charge: qdl.ChargeRule{
				Flat: qdl.Const(0), Floor: qdl.Const(0),
				Terms: []qdl.Term{
					{Dim: qdl.DimOutputTokens, Coeff: qdl.Const(1), Quantize: qdl.Quantize{Mode: qdl.QuantizeCeil, Step: 400}},
				},
			},
		}},
		Channels: []qdl.Channel{{
			ID: "ch1", Protocol: "anthropic_messages", Auth: "oauth_bearer",
			Admission: qdl.AdmissionPolicy{Limits: map[qdl.InstantDim]qdl.Coeff{
				qdl.InstantConcurrency:       qdl.Const(5),
				qdl.InstantContextTokensPeak: qdl.Const(200000),
			}},
		}},
	}
}

func TestAdmitThreeStates(t *testing.T) {
	spec := mkAdmitPlan(t)
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) // 周六 12:00 UTC
	theta := qdl.ParamPoint{}
	actx := &AdmissionContext{Now: now, Concurrency: map[string]int{}}
	// output=300：EXACT=400、EV=500、UB=700
	req := &Request{
		ChannelID: "ch1", Model: "model-x", ContextTokensPeak: 100000,
		Dims: map[qdl.Dim]float64{qdl.DimOutputTokens: 300},
	}

	// 1) 充足（remaining 1000 ≥ UB 700）：ALLOW
	st := &SystemState{Buckets: map[string]BucketState{"b_tok": {U: 0}}}
	a, err := Admit(spec, st, req, theta, actx)
	if err != nil || a.Decision != AdmitAllow || a.PBreak != 0 {
		t.Fatalf("充足应 ALLOW: %+v err=%v", a, err)
	}
	// 2) 风险带（remaining 600 ∈ [EV 500, UB 700)）：ALLOW_WITH_RISK
	st.Buckets["b_tok"] = BucketState{U: 400}
	a, _ = Admit(spec, st, req, theta, actx)
	if a.Decision != AdmitAllowWithRisk || a.PBreak <= 0 || a.PBreak >= 1 {
		t.Fatalf("风险带应 ALLOW_WITH_RISK: %+v", a)
	}
	wantP := (700.0 - 600) / (700.0 - 500)
	if math.Abs(a.PBreak-wantP) > 1e-9 {
		t.Fatalf("p_break: got %v want %v", a.PBreak, wantP)
	}
	// 3) 桶满（remaining 400 < EV 500）：DENY_QUOTA
	st.Buckets["b_tok"] = BucketState{U: 600}
	a, _ = Admit(spec, st, req, theta, actx)
	if a.Decision != AdmitDenyQuota || a.Reason == "" {
		t.Fatalf("桶满应 DENY_QUOTA: %+v", a)
	}
	// 4) 准入拒绝：并发满（5+1 > 上限 5）
	actx.Concurrency["ch1"] = 5
	st.Buckets["b_tok"] = BucketState{U: 0}
	a, _ = Admit(spec, st, req, theta, actx)
	if a.Decision != AdmitDenyAdmission {
		t.Fatalf("并发满应 DENY_ADMISSION: %+v", a)
	}
	// 5) 准入拒绝：context 超长
	actx.Concurrency["ch1"] = 0
	req.ContextTokensPeak = 300000
	a, _ = Admit(spec, st, req, theta, actx)
	if a.Decision != AdmitDenyAdmission {
		t.Fatalf("context 超长应 DENY_ADMISSION: %+v", a)
	}
	req.ContextTokensPeak = 100000
	// 6) DENY_QUOTA 的 retry_after：周期锚定窗（锚 MON 00:00 + 24h 窗长 ⇒
	//    每日 00:00 重置）；now=周六 12:00 → 下一重置周日 00:00，retry=12h
	spec2 := mkAdmitPlan(t)
	spec2.Buckets[0].Window = qdl.Window{
		KindCandidates: []qdl.WindowKind{qdl.WindowTumblingAccountAnchored},
		Length:         qdl.Duration{Duration: 24 * time.Hour},
		AnchorUTC:      "MON 00:00",
	}
	st2 := &SystemState{Buckets: map[string]BucketState{"b_tok": {U: 900}}}
	a, err = Admit(spec2, st2, req, theta, actx)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if a.Decision != AdmitDenyQuota || a.RetryAfter == nil || *a.RetryAfter != 12*time.Hour {
		t.Fatalf("周期窗 DENY_QUOTA 应带 retry_after=12h: %+v", a)
	}
}
