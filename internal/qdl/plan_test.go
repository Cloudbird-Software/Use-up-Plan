package qdl

import (
	"strings"
	"testing"
	"time"
)

// validPlan 构造一个最小合法 plan（供 Validate 正/负用例复用）。
func validPlan() *PlanSpec {
	return &PlanSpec{
		ID: "t/plan@2026-08", Vendor: "t", PlanName: "Plan",
		EffectiveFrom: time.Now(),
		Parameters: []Parameter{
			{ID: "t.C", Unit: "usd_equivalent", Prior: Point(100), Provenance: ProvenanceAssumed},
			{ID: "t.w_in", Unit: "usd_per_token", Prior: Point(3e-6), Provenance: ProvenanceGauge, Frozen: true},
		},
		Buckets: []Bucket{{
			ID: "b1", Unit: DimOpaqueUnits,
			Capacity: Ref("t.C"),
			Window:   Window{KindCandidates: []WindowKind{WindowTumblingCalendar}, Length: 24 * time.Hour, Reset: ResetZero},
			Scope:    Scope{Level: ScopeAccount},
			Charge:   ChargeRule{Terms: []Term{{Dim: DimInputTokens, Coeff: Ref("t.w_in")}}},
		}},
		Channels: []Channel{{ID: "c1", Protocol: "openai_chat", Auth: "api_key"}},
		Gauge:    CalibrationGauge{Mode: "anchor_to_vendor_ratecard"},
	}
}

func TestValidateOK(t *testing.T) {
	p := validPlan()
	if err := p.Validate(); err != nil {
		t.Fatalf("合法 plan 不应报错: %v", err)
	}
	if len(p.Buckets[0].Overflow) != 1 || p.Buckets[0].Overflow[0].Action != OverflowHardBlock {
		t.Fatal("空 overflow 必须规范化为 hard_block 安全缺省")
	}
}

func TestValidateContracts(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*PlanSpec)
		want string
	}{
		{"未知参数引用", func(p *PlanSpec) { p.Buckets[0].Capacity = Ref("t.missing") }, "未知参数"},
		{"PAYG 未显式开启", func(p *PlanSpec) {
			p.Buckets[0].Overflow = []OverflowStep{{Action: OverflowSpillToPAYG}}
		}, "requires_explicit_enable"},
		{"vendor_doc 冻结", func(p *PlanSpec) {
			p.Parameters[1].Provenance = ProvenanceVendorDoc
		}, "vendor_doc"},
		{"空窗口候选", func(p *PlanSpec) { p.Buckets[0].Window.KindCandidates = nil }, "kind_candidates"},
		{"重复桶 ID", func(p *PlanSpec) {
			p.Buckets = append(p.Buckets, p.Buckets[0])
		}, "重复"},
		{"spill 目标桶不存在", func(p *PlanSpec) {
			p.Buckets[0].Overflow = []OverflowStep{{Action: OverflowSpillToBucket, Target: "nope"}}
		}, "未知目标桶"},
		{"未知计量维度", func(p *PlanSpec) {
			p.Buckets[0].Charge.Terms = []Term{{Dim: Dim("nope"), Coeff: Const(1)}}
		}, "未知计量维度"},
		{"trust 越界", func(p *PlanSpec) {
			p.Buckets[0].Observability = []ObsBinding{{Trust: 1.5, Source: ObsResponseHeader, Semantic: SemUsedPct}}
		}, "trust"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := validPlan()
			c.mut(p)
			err := p.Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("期望错误含 %q, got %v", c.want, err)
			}
		})
	}
}

func TestChargeRuleMultiplierFor(t *testing.T) {
	r := &ChargeRule{ModelMultiplier: map[string]Coeff{
		"claude-*":      Const(2),
		"claude-opus-*": Const(5),
	}}
	if c, ok := r.MultiplierFor("claude-opus-4-6"); !ok {
		t.Fatal("claude-opus-4-6 应命中倍率")
	} else if v, _ := c.Constant(); v != 5 {
		// 命中两个 pattern（claude-* 与 claude-opus-*）时应取更具体者（倍率 5）。
		t.Fatalf("应取最长 pattern 的倍率 5, got %v", v)
	}
	if c, ok := r.MultiplierFor("claude-sonnet-4-6"); !ok {
		t.Fatal("claude-sonnet-4-6 应命中 claude-*")
	} else if v, _ := c.Constant(); v != 2 {
		t.Fatalf("倍率应为 2, got %v", v)
	}
	if _, ok := r.MultiplierFor("gpt-5"); ok {
		t.Fatal("gpt-5 不应命中任何倍率")
	}
}

func TestPlanAccessors(t *testing.T) {
	p := validPlan()
	if p.Bucket("b1") == nil || p.Bucket("zz") != nil {
		t.Fatal("Bucket 访问器语义错误")
	}
	if p.Channel("c1") == nil || p.Param("t.C") == nil || p.Param("zz") != nil {
		t.Fatal("Channel/Param 访问器语义错误")
	}
	if ids := p.ParamIDs(); len(ids) != 2 || ids[0] != "t.C" {
		t.Fatalf("ParamIDs: %v", ids)
	}
}
