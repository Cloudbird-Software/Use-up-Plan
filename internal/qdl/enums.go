package qdl

import (
	"fmt"
	"sort"
)

// 本文件是 QDL 全部枚举封闭集的唯一登记处。loader 解码后的值、手写构造的
// PlanSpec 一律经 PlanSpec.Validate → validateClosedSets 执法：未知枚举值
// 一律拒绝（等价于 Intent §2.1 Pydantic 的 Literal 约束）。

// 封闭集登记表。值一律引用类型常量，保证与代码同源。
var (
	periods = map[string]bool{
		"month": true, "year": true, "week": true, "one_off": true,
	}
	provenances = map[string]bool{
		string(ProvenanceVendorDoc): true, string(ProvenanceVendorAPI): true,
		string(ProvenanceEstimated): true, string(ProvenanceAssumed): true,
		string(ProvenanceGauge): true,
	}
	distKinds = map[string]bool{
		string(DistLognormal): true, string(DistNormal): true, string(DistUniform): true,
		string(DistPoint): true, string(DistDiscrete): true,
	}
	windowKinds = map[string]bool{
		string(WindowTumblingAnchoredOnFirstUse): true, string(WindowTumblingAccountAnchored): true,
		string(WindowTumblingCalendar): true, string(WindowSlidingExact): true,
		string(WindowTokenBucketContinuous): true, string(WindowBillingCycle): true,
		string(WindowOneShotExpiring): true, string(WindowNever): true,
	}
	resetPolicies = map[string]bool{
		string(ResetZero): true, string(ResetRefillToFull): true, string(ResetRolloverCapped): true,
		string(ResetRolloverUncapped): true, string(ResetDecayExponential): true,
	}
	overflowActions = map[string]bool{
		string(OverflowSpillToBucket): true, string(OverflowSpillToPAYG): true,
		string(OverflowHardBlock): true, string(OverflowHardBlockResetHint): true,
		string(OverflowDegradeModel): true, string(OverflowDegradeSpeed): true,
		string(OverflowTruncateContext): true, string(OverflowQueue): true,
		string(OverflowSilentQualityDrop): true,
	}
	linearizations = map[string]bool{
		string(LinearExactLinear): true, string(LinearExpectedEV): true, string(LinearUpperBound): true,
	}
	obsSources = map[string]bool{
		string(ObsResponseHeader): true, string(ObsUsageEndpoint): true, string(ObsErrorBody): true,
		string(ObsLocalLog): true, string(ObsWebDOM): true, string(ObsSDKField): true,
	}
	semantics = map[string]bool{
		string(SemUsedPct): true, string(SemRemainingPct): true, string(SemUsedAbs): true,
		string(SemRemainingAbs): true, string(SemLimitAbs): true, string(SemResetAtEpochMS): true,
		string(SemResetAtISO): true, string(SemResetAfterS): true, string(SemWindowMinutes): true,
		string(SemReason): true, string(SemPlanType): true,
	}
	quantizationKinds = map[string]bool{
		"exact": true, "integer": true, "decimals": true, "unknown": true,
	}
	channelProtocols = map[string]bool{
		"anthropic_messages": true, "openai_chat": true, "openai_responses": true,
		"gemini": true, "custom_cli": true, "web": true,
	}
	channelAuths = map[string]bool{
		"oauth_bearer": true, "api_key": true, "cookie_session": true,
	}
	obsAuths = map[string]bool{
		"oauth_bearer": true, "api_key": true, "cookie": true,
	}
	grantKinds = map[string]bool{
		"base": true, "promo": true, "topup": true, "rollover": true, "referral": true,
	}
	tosClasses = map[string]bool{
		"none": true, "grey": true, "explicit_breach": true,
	}
	gaugeModes = map[string]bool{
		"anchor_to_vendor_ratecard": true, "anchor_to_reference_model_usd": true,
		"anchor_to_observed_absolute": true,
	}
	scopeLevels = map[string]bool{
		string(ScopeAccount): true, string(ScopeOrganization): true, string(ScopeWorkspace): true,
		string(ScopeCredential): true, string(ScopeSubscription): true, string(ScopeCrossProductPool): true,
		string(ScopeModelFamily): true,
	}
	calendarAligns = map[string]bool{
		"utc_midnight": true, "local_midnight": true, "billing_day": true,
	}
	driftDetectors = map[string]bool{
		"cusum": true, "page_hinkley": true,
	}
	interruptionGranularities = map[string]bool{
		"mid_stream": true, "between_requests": true,
	}
)

// inSet 报告值是否在封闭集内，并产出定位化错误文案。
func inSet(where, val string, set map[string]bool) error {
	if !set[val] {
		return fmt.Errorf("qdl: %s 的值 %q 不在封闭集内", where, val)
	}
	return nil
}

// inSetOrEmpty 同 inSet，但允许空串：空 = 走该字段的缺省语义（loader 会补齐；
// 手写构造的 PlanSpec 允许留空，semantics 侧按缺省分派）。
func inSetOrEmpty(where, val string, set map[string]bool) error {
	if val == "" {
		return nil
	}
	return inSet(where, val, set)
}

// sortedDims 确定性遍历 Dim 键（错误信息稳定，fuzz/golden 友好）。
func sortedDims(m map[Dim]float64) []Dim {
	ks := make([]Dim, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	return ks
}

// validateClosedSets 逐字段执法全部枚举封闭集。空串视为「未填」：必填字段
// 由 Validate 的必填检查拒绝，可选字段（如 calendar_align）允许留空。
func (p *PlanSpec) validateClosedSets() error {
	if err := inSet("period", p.Period, periods); err != nil {
		return err
	}
	if err := inSetOrEmpty("gauge.mode", p.Gauge.Mode, gaugeModes); err != nil {
		return err
	}
	if err := inSetOrEmpty("risk.tos_violation_class", p.Risk.TOSViolationClass, tosClasses); err != nil {
		return err
	}
	for _, dim := range sortedDims(p.Gauge.RatecardUSDPerUnit) {
		if !dim.Valid() {
			return fmt.Errorf("qdl: gauge.ratecard_usd_per_unit 含未知计量维度 %q", dim)
		}
	}
	for _, prm := range p.Parameters {
		if err := inSetOrEmpty(fmt.Sprintf("参数 %q provenance", prm.ID), string(prm.Provenance), provenances); err != nil {
			return err
		}
		if err := inSet(fmt.Sprintf("参数 %q prior.kind", prm.ID), string(prm.Prior.Kind), distKinds); err != nil {
			return err
		}
		if prm.Posterior != nil {
			if err := inSet(fmt.Sprintf("参数 %q posterior.kind", prm.ID), string(prm.Posterior.Kind), distKinds); err != nil {
				return err
			}
		}
		if prm.Drift != nil {
			if err := inSetOrEmpty(fmt.Sprintf("参数 %q drift.detector", prm.ID), prm.Drift.Detector, driftDetectors); err != nil {
				return err
			}
		}
	}
	for i := range p.Buckets {
		b := &p.Buckets[i]
		w := fmt.Sprintf("桶 %q", b.ID)
		if err := inSet(w+".scope.level", string(b.Scope.Level), scopeLevels); err != nil {
			return err
		}
		if err := inSetOrEmpty(w+".window.reset", string(b.Window.Reset), resetPolicies); err != nil {
			return err
		}
		if b.Window.CalendarAlign != "" {
			if err := inSet(w+".window.calendar_align", b.Window.CalendarAlign, calendarAligns); err != nil {
				return err
			}
		}
		for j, k := range b.Window.KindCandidates {
			if err := inSet(fmt.Sprintf("%s.window.kind_candidates[%d]", w, j), string(k), windowKinds); err != nil {
				return err
			}
		}
		for k := range b.Window.KindPosterior {
			if err := inSet(w+".window.kind_posterior 键", string(k), windowKinds); err != nil {
				return err
			}
		}
		if err := inSetOrEmpty(w+".charge.linearization", string(b.Charge.Linearization), linearizations); err != nil {
			return err
		}
		for j, ov := range b.Overflow {
			if err := inSet(fmt.Sprintf("%s.overflow[%d].action", w, j), string(ov.Action), overflowActions); err != nil {
				return err
			}
		}
		for j, ob := range b.Observability {
			ow := fmt.Sprintf("%s.observability[%d]", w, j)
			if err := inSet(ow+".source", string(ob.Source), obsSources); err != nil {
				return err
			}
			if err := inSet(ow+".semantic", string(ob.Semantic), semantics); err != nil {
				return err
			}
			if err := inSetOrEmpty(ow+".quantization.kind", ob.Quantization.Kind, quantizationKinds); err != nil {
				return err
			}
			if ob.Auth != "" {
				if err := inSet(ow+".auth", ob.Auth, obsAuths); err != nil {
					return err
				}
			}
		}
	}
	for _, ch := range p.Channels {
		c := fmt.Sprintf("通道 %q", ch.ID)
		if err := inSet(c+".protocol", ch.Protocol, channelProtocols); err != nil {
			return err
		}
		if err := inSet(c+".auth", ch.Auth, channelAuths); err != nil {
			return err
		}
		if err := inSetOrEmpty(c+".reliability.interruption_granularity", ch.Reliability.InterruptionGranularity, interruptionGranularities); err != nil {
			return err
		}
	}
	for _, g := range p.Grants {
		if err := inSet(fmt.Sprintf("授予 %q kind", g.ID), g.Kind, grantKinds); err != nil {
			return err
		}
	}
	return nil
}
