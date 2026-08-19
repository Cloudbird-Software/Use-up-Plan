package qdl

import (
	"fmt"
	"time"
)

// RiskProfile 把 ToS/封号风险显式建模（Intent §1.8/§10.2）：封号概率作为折损系数
// 进价值模型，ban_blast_radius 强制面对「同支付方式/同 IP 连坐」拓扑。
type RiskProfile struct {
	TOSViolationClass string   `yaml:"tos_violation_class"` // none | grey | explicit_breach
	BanHazardMonthly  float64  `yaml:"ban_hazard_monthly"`
	BanBlastRadius    []string `yaml:"ban_blast_radius,omitempty"` // 连坐范围（账号/支付方式/IP 拓扑）
	PrepaidAtRiskUSD  float64  `yaml:"prepaid_at_risk_usd"`
}

// Grant 是一笔额度授予：基础/活动赠送/加购/结转/推荐。
type Grant struct {
	ID         string     `yaml:"id"`
	Kind       string     `yaml:"kind"` // base | promo | topup | rollover | referral
	BucketID   string     `yaml:"bucket_id"`
	Amount     Coeff      `yaml:"amount"`
	GrantedAt  *time.Time `yaml:"granted_at"`
	ExpiresAt  *time.Time `yaml:"expires_at"`
	Conditions string     `yaml:"conditions"` // 自然语言原文，供文档语义 diff
}

// CalibrationGauge 是标度规范（Intent §0.3）：只观测百分比时 (w, C) 只能辨识到
// 公共尺度因子。把 w 锚定到官方按量价目表后，C 的单位自动变成「等价 API 美元」
// ——「这个 plan 用尽等于买了多少美元的 API」直接成为辨识问题的规范化常数。
// 若拟合残差显著，残差本身就是厂商模糊定价关系的定量证据。
type CalibrationGauge struct {
	Mode               string          `yaml:"mode"` // anchor_to_vendor_ratecard | anchor_to_reference_model_usd | anchor_to_observed_absolute
	RatecardUSDPerUnit map[Dim]float64 `yaml:"ratecard_usd_per_unit,omitempty"`
	ReferenceModel     string          `yaml:"reference_model"`
	Note               string          `yaml:"note"`
}

// PlanSpec 是一份厂商订阅计划的完整 QDL 描述。加载（loader.Load）后不可变；
// Validate 是安全契约的唯一执法点，loader 与测试都必须调用。
type PlanSpec struct {
	ID                    string           `yaml:"id"` // "anthropic/max20@2026-08"
	Vendor                string           `yaml:"vendor"`
	PlanName              string           `yaml:"plan_name"`
	PriceUSDPerPeriod     float64          `yaml:"price_usd_per_period"`
	Period                string           `yaml:"period"` // month | year | week | one_off
	SpecVersion           string           `yaml:"spec_version"`
	VendorDocSnapshotHash string           `yaml:"vendor_doc_snapshot_hash"` // 页面语义 diff 用
	EffectiveFrom         time.Time        `yaml:"effective_from"`
	EffectiveUntil        *time.Time       `yaml:"effective_until"`
	Buckets               []Bucket         `yaml:"buckets,omitempty"`
	Channels              []Channel        `yaml:"channels,omitempty"`
	Grants                []Grant          `yaml:"grants,omitempty"`
	Parameters            []Parameter      `yaml:"parameters,omitempty"`
	Gauge                 CalibrationGauge `yaml:"gauge"`
	Risk                  RiskProfile      `yaml:"risk"`
}

// Bucket 按 ID 查找桶；不存在返回 nil。
func (p *PlanSpec) Bucket(id string) *Bucket {
	for i := range p.Buckets {
		if p.Buckets[i].ID == id {
			return &p.Buckets[i]
		}
	}
	return nil
}

// Channel 按 ID 查找通道；不存在返回 nil。
func (p *PlanSpec) Channel(id string) *Channel {
	for i := range p.Channels {
		if p.Channels[i].ID == id {
			return &p.Channels[i]
		}
	}
	return nil
}

// Param 按 ID 查找参数；不存在返回 nil。
func (p *PlanSpec) Param(id string) *Parameter {
	for i := range p.Parameters {
		if p.Parameters[i].ID == id {
			return &p.Parameters[i]
		}
	}
	return nil
}

// ParamIDs 返回全部参数 ID（确定性顺序）。
func (p *PlanSpec) ParamIDs() []string {
	ids := make([]string, 0, len(p.Parameters))
	for _, prm := range p.Parameters {
		ids = append(ids, prm.ID)
	}
	return ids
}

// Validate 执行全部结构校验与安全契约。返回的 error 文案以 "qdl:" 开头并带定位前缀。
// 规则清单（Intent §2/AGENTS 不变量）：
//   - ID 唯一性：bucket / channel / parameter / grant
//   - 一切 ParamRef（含 ExogenousRateParam 字符串引用）可解析
//   - SPILL_TO_PAYG 必须 RequiresExplicitEnable=true（缺省拒绝）
//   - vendor_doc 来源参数永不 frozen（公开数字只配当先验）
//   - Window.KindCandidates 非空；Bucket.Unit / Term.Dim 合法
//   - Grant.BucketID 必须指向存在的桶；ObsBinding.Trust ∈ [0,1]
//   - 空 Overflow 规范化为 [hard_block]（安全缺省）
func (p *PlanSpec) Validate() error {
	if err := p.validateClosedSets(); err != nil {
		return err
	}
	paramIDs := map[string]bool{}
	for i := range p.Parameters {
		prm := &p.Parameters[i]
		if prm.ID == "" {
			return fmt.Errorf("qdl: 存在空参数 ID")
		}
		if paramIDs[prm.ID] {
			return fmt.Errorf("qdl: 参数 ID %q 重复", prm.ID)
		}
		paramIDs[prm.ID] = true
		if err := prm.Prior.Validate(); err != nil {
			return fmt.Errorf("qdl: 参数 %q prior: %w", prm.ID, err)
		}
		if prm.Posterior != nil {
			if err := prm.Posterior.Validate(); err != nil {
				return fmt.Errorf("qdl: 参数 %q posterior: %w", prm.ID, err)
			}
		}
		if prm.Bounds[0] != nil && prm.Bounds[1] != nil && *prm.Bounds[0] > *prm.Bounds[1] {
			return fmt.Errorf("qdl: 参数 %q bounds lower=%v > upper=%v", prm.ID, *prm.Bounds[0], *prm.Bounds[1])
		}
		if prm.Provenance == ProvenanceVendorDoc && prm.Frozen {
			return fmt.Errorf("qdl: 参数 %q 来源为 vendor_doc 却 frozen——公开声称值永不冻结", prm.ID)
		}
	}
	resolve := func(where string, c Coeff) error {
		if c.IsRef() && !paramIDs[c.RefID()] {
			return fmt.Errorf("qdl: %s 引用未知参数 %q", where, c.RefID())
		}
		return nil
	}

	bucketIDs := map[string]bool{}
	for i := range p.Buckets {
		b := &p.Buckets[i]
		if b.ID == "" {
			return fmt.Errorf("qdl: 存在空桶 ID")
		}
		if bucketIDs[b.ID] {
			return fmt.Errorf("qdl: 桶 ID %q 重复", b.ID)
		}
		bucketIDs[b.ID] = true
		if !b.Unit.Valid() {
			return fmt.Errorf("qdl: 桶 %q 计量单位 %q 未知", b.ID, b.Unit)
		}
		if len(b.Window.KindCandidates) == 0 {
			return fmt.Errorf("qdl: 桶 %q 的 window.kind_candidates 为空——结构未知也必须给候选集", b.ID)
		}
		if err := resolve(fmt.Sprintf("桶 %q 容量", b.ID), b.Capacity); err != nil {
			return err
		}
		if err := validateCharge(&b.Charge, b.ID, resolve); err != nil {
			return err
		}
		if b.ExogenousRateParam != "" && !paramIDs[b.ExogenousRateParam] {
			return fmt.Errorf("qdl: 桶 %q 引用未知外生消耗参数 %q", b.ID, b.ExogenousRateParam)
		}
		for j, ob := range b.Observability {
			if ob.Trust < 0 || ob.Trust > 1 {
				return fmt.Errorf("qdl: 桶 %q 观测绑定[%d] trust=%v 越界 [0,1]", b.ID, j, ob.Trust)
			}
		}
		if len(b.Overflow) == 0 {
			b.Overflow = []OverflowStep{{Action: OverflowHardBlock}} // 安全缺省
		}
		for _, ov := range b.Overflow {
			if ov.Action == OverflowSpillToPAYG && !ov.RequiresExplicitEnable {
				return fmt.Errorf("qdl: 桶 %q 的 spill_to_payg 未设 requires_explicit_enable——安全契约缺省拒绝", b.ID)
			}
		}
	}
	// 二轮：spill 目标桶存在性（此时 bucketIDs 已收集完整）。
	for _, b := range p.Buckets {
		for _, ov := range b.Overflow {
			if ov.Action == OverflowSpillToBucket && ov.Target != "" && !bucketIDs[ov.Target] {
				return fmt.Errorf("qdl: 桶 %q 溢出到未知目标桶 %q", b.ID, ov.Target)
			}
		}
	}

	channelIDs := map[string]bool{}
	for _, ch := range p.Channels {
		if ch.ID == "" {
			return fmt.Errorf("qdl: 存在空通道 ID")
		}
		if channelIDs[ch.ID] {
			return fmt.Errorf("qdl: 通道 ID %q 重复", ch.ID)
		}
		channelIDs[ch.ID] = true
		for dim, c := range ch.Admission.Limits {
			if !dim.Valid() {
				return fmt.Errorf("qdl: 通道 %q 准入约束维度 %q 未知", ch.ID, dim)
			}
			if err := resolve(fmt.Sprintf("通道 %q 准入 %q", ch.ID, dim), c); err != nil {
				return err
			}
		}
	}

	grantIDs := map[string]bool{}
	for _, g := range p.Grants {
		if g.ID == "" || grantIDs[g.ID] {
			return fmt.Errorf("qdl: 授予 ID %q 为空或重复", g.ID)
		}
		grantIDs[g.ID] = true
		if !bucketIDs[g.BucketID] {
			return fmt.Errorf("qdl: 授予 %q 指向未知桶 %q", g.ID, g.BucketID)
		}
		if err := resolve(fmt.Sprintf("授予 %q 量", g.ID), g.Amount); err != nil {
			return err
		}
	}
	if p.Vendor == "" || p.PlanName == "" || p.ID == "" {
		return fmt.Errorf("qdl: plan 的 id/vendor/plan_name 必填")
	}
	return nil
}

// validateCharge 校验一条扣减规则内的全部 ParamRef 与维度合法性。
func validateCharge(r *ChargeRule, bucketID string, resolve func(string, Coeff) error) error {
	w := fmt.Sprintf("桶 %q 扣减规则", bucketID)
	if err := resolve(w+".flat", r.Flat); err != nil {
		return err
	}
	if err := resolve(w+".floor", r.Floor); err != nil {
		return err
	}
	for _, t := range r.Terms {
		if !t.Dim.Valid() {
			return fmt.Errorf("qdl: %s 含未知计量维度 %q", w, t.Dim)
		}
		if err := resolve(fmt.Sprintf("%s 项 %q", w, t.Dim), t.Coeff); err != nil {
			return err
		}
	}
	for _, pat := range sortedKeys(r.ModelMultiplier) {
		if err := resolve(fmt.Sprintf("%s 模型倍率 %q", w, pat), r.ModelMultiplier[pat]); err != nil {
			return err
		}
	}
	for _, e := range sortedKeys(r.EffortMultiplier) {
		if err := resolve(fmt.Sprintf("%s 努力级倍率 %q", w, e), r.EffortMultiplier[e]); err != nil {
			return err
		}
	}
	return nil
}
