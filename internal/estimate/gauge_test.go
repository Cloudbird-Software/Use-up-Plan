package estimate

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// ---- gauge fixing（Intent §0.3 / §4.2） ----

// TestCouplingGroups 单桶全耦合 + 共享参数跨桶传递合并。
func TestCouplingGroupsBasic(t *testing.T) {
	spec, _ := estSpec(false, false) // 全自由：{t.C, t.w, t.f}
	groups := CouplingGroups(spec)
	if len(groups) != 1 {
		t.Fatalf("单桶应恰一组，得 %d: %+v", len(groups), groups)
	}
	g := groups[0]
	if len(g.ParamIDs) != 3 || len(g.BucketIDs) != 1 || g.BucketIDs[0] != "b5" {
		t.Fatalf("组内容: %+v", g)
	}
	for _, id := range []string{"t.C", "t.w", "t.f"} {
		found := false
		for _, p := range g.ParamIDs {
			if p == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("参数 %q 不在耦合组 %v", id, g.ParamIDs)
		}
	}

	// 第二个桶共享 mult 参数 → 传递合并为一组
	spec2 := &qdl.PlanSpec{
		ID: "t/est2@2026-08", Vendor: "t",
		Parameters: append(spec.Parameters,
			qdl.Parameter{ID: "t.mult", Unit: "dimensionless", Prior: qdl.Point(2)},
		),
		Buckets: append(append([]qdl.Bucket(nil), spec.Buckets...), qdl.Bucket{
			ID: "b7", Unit: qdl.DimOpaqueUnits, Capacity: qdl.Const(80),
			Window: qdl.Window{KindCandidates: []qdl.WindowKind{qdl.WindowTumblingAnchoredOnFirstUse},
				Length: qdl.Duration{Duration: 7 * 24 * time.Hour}, Reset: qdl.ResetZero},
			Charge: qdl.ChargeRule{
				Terms:           []qdl.Term{{Dim: qdl.DimInputTokens, Coeff: qdl.Ref("t.w")}},
				ModelMultiplier: map[string]qdl.Coeff{"m-opus-*": qdl.Ref("t.mult")},
			},
		}),
	}
	groups2 := CouplingGroups(spec2)
	if len(groups2) != 1 {
		t.Fatalf("共享 t.w 的两桶应合并为一组，得 %d: %+v", len(groups2), groups2)
	}
	if len(groups2[0].BucketIDs) != 2 {
		t.Fatalf("组应含两桶: %+v", groups2[0])
	}

	// 独立桶（引用独立参数）→ 两组
	spec3 := &qdl.PlanSpec{
		ID: "t/est3@2026-08", Vendor: "t",
		Parameters: append(spec.Parameters,
			qdl.Parameter{ID: "t.rpm", Unit: "requests_per_minute", Prior: qdl.Point(50)},
		),
		Buckets: append(append([]qdl.Bucket(nil), spec.Buckets...), qdl.Bucket{
			ID: "brpm", Unit: qdl.DimRequests, Capacity: qdl.Ref("t.rpm"),
			Window: qdl.Window{KindCandidates: []qdl.WindowKind{qdl.WindowTokenBucketContinuous},
				Length: qdl.Duration{Duration: time.Minute}, Reset: qdl.ResetZero},
			Charge: qdl.ChargeRule{Flat: qdl.Const(1)},
		}),
	}
	if gs := CouplingGroups(spec3); len(gs) != 2 {
		t.Fatalf("独立桶应两组，得 %d: %+v", len(gs), gs)
	}
}

// TestValidateGauge 无 frozen 的耦合组报尺度不可辨识；锚定 w 后通过。
func TestValidateGauge(t *testing.T) {
	spec, _ := estSpec(false, false) // 全自由
	problems := ValidateGauge(spec)
	if len(problems) != 1 {
		t.Fatalf("未锚定应报 1 个问题，得 %d", len(problems))
	}
	if !strings.Contains(problems[0].Message, "尺度不可辨识") {
		t.Fatalf("诊断文案应指出尺度不可辨识: %q", problems[0].Message)
	}

	specAnchored, _ := estSpec(true, false) // w frozen（gauge 锚定）
	if problems := ValidateGauge(specAnchored); len(problems) != 0 {
		t.Fatalf("锚定后应无问题: %+v", problems)
	}
}

// TestGaugeSummary ratecard 锚定下的可解释读数：容量等价美元、倍率偏离、
// 缓存权重比（Intent §4.2 的三条可直接行动结论）。
func TestGaugeSummary(t *testing.T) {
	spec := &qdl.PlanSpec{
		ID: "v/t@2026-08", Vendor: "v",
		Gauge: qdl.CalibrationGauge{
			Mode: "anchor_to_vendor_ratecard",
			RatecardUSDPerUnit: map[qdl.Dim]float64{
				qdl.DimInputTokens:     0.000003,
				qdl.DimCacheReadTokens: 0.0000003, // API 1 折
			},
		},
		Parameters: []qdl.Parameter{
			{ID: "v.C", Unit: "usd_equivalent", Prior: qdl.Point(245), Frozen: false},
			{ID: "v.w_in", Unit: "usd_per_token", Prior: qdl.Point(0.000003), Frozen: true},
			{ID: "v.w_cache", Unit: "usd_per_token", Prior: qdl.Point(0.000003), Frozen: false},
			{ID: "v.mult_opus", Unit: "dimensionless", Prior: qdl.Point(2)},
		},
		Buckets: []qdl.Bucket{{
			ID: "b5", Unit: qdl.DimOpaqueUnits, Capacity: qdl.Ref("v.C"),
			Window: qdl.Window{KindCandidates: []qdl.WindowKind{qdl.WindowTumblingAnchoredOnFirstUse},
				Length: qdl.Duration{Duration: 5 * time.Hour}, Reset: qdl.ResetZero},
			Charge: qdl.ChargeRule{
				Terms: []qdl.Term{
					{Dim: qdl.DimInputTokens, Coeff: qdl.Ref("v.w_in")},
					{Dim: qdl.DimCacheReadTokens, Coeff: qdl.Ref("v.w_cache")},
				},
				ModelMultiplier: map[string]qdl.Coeff{"m-opus-*": qdl.Ref("v.mult_opus")},
			},
		}},
	}
	theta := qdl.ParamPoint{
		"v.C": 245, "v.w_in": 0.000003, "v.w_cache": 0.000003, "v.mult_opus": 2.0,
	}
	reads := GaugeSummary(spec, theta)
	var sawCap, sawMult, sawCache bool
	for _, r := range reads {
		switch r.Kind {
		case "capacity_usd_equiv":
			sawCap = true
			if r.Subject != "b5" || math.Abs(r.Value-245) > 1e-9 {
				t.Fatalf("容量读数: %+v", r)
			}
		case "multiplier_deviation":
			sawMult = true
			if !strings.Contains(r.Interpretation, "劝退") {
				t.Fatalf("mult=2.0 应报额外惩罚: %q", r.Interpretation)
			}
		case "weight_ratio":
			sawCache = true
			// w_cache/w_in = 1.0，API 价目比 0.1 → 订阅不给缓存折扣
			if !strings.Contains(r.Interpretation, "缓存") {
				t.Fatalf("权重比读数: %q", r.Interpretation)
			}
		}
	}
	if !sawCap || !sawMult || !sawCache {
		t.Fatalf("三类读数应齐全: cap=%v mult=%v cache=%v", sawCap, sawMult, sawCache)
	}

	// 未锚定（无 ratecard）时不产出等价美元读数，但 mult 读数仍在
	spec.Gauge.Mode = "anchor_to_reference_model_usd"
	theta2 := qdl.ParamPoint{"v.C": 245, "v.mult_opus": 2.0}
	if reads := GaugeSummary(spec, theta2); len(reads) != 1 {
		t.Fatalf("未锚定时只应有 mult 读数，得 %d: %+v", len(reads), reads)
	}
}

// TestZQuantile Acklam 逆 CDF 的关键分位数精度（CI 边界用途 ±0.001 足够）。
func TestZQuantile(t *testing.T) {
	cases := []struct {
		p, want float64
	}{
		{0.975, 1.959964},
		{0.95, 1.644854},
		{0.90, 1.281552},
		{0.5, 0.0},
		{0.025, -1.959964},
		{0.001, -3.090232},
	}
	for _, c := range cases {
		got := zQuantile(c.p)
		if math.Abs(got-c.want) > 1e-3 {
			t.Fatalf("zQuantile(%v) = %v，want %v", c.p, got, c.want)
		}
	}
}
