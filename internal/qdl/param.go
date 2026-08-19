package qdl

import (
	"fmt"
	"time"
)

// Provenance 是参数值的来源。厂商公开声称（vendor_doc）只配当先验、永不 frozen；
// gauge 是标度规范锚定值，是唯一允许 frozen 的来源。
type Provenance string

const (
	ProvenanceVendorDoc Provenance = "vendor_doc" // 厂商公开声称——全部不可信（Intent §10.1）
	ProvenanceVendorAPI Provenance = "vendor_api" // 厂商 API 返回
	ProvenanceEstimated Provenance = "estimated"  // 本系统辨识出来的
	ProvenanceAssumed   Provenance = "assumed"    // 人工拍的
	ProvenanceGauge     Provenance = "gauge"      // 标度规范固定（打破尺度不可辨识性）
)

// DistributionKind 是先验/后验分布的封闭集合。
type DistributionKind string

const (
	DistLognormal DistributionKind = "lognormal" // 中位数 = exp(mu)
	DistNormal    DistributionKind = "normal"
	DistUniform   DistributionKind = "uniform"
	DistPoint     DistributionKind = "point" // 确定值
	DistDiscrete  DistributionKind = "discrete"
)

// Distribution 是参数分布的封闭描述。数值方法（CDF/采样/分位数）在 estimate 模块。
//
// YAML 形态（loader 解析）：
//
//	{kind: lognormal, params: {mu: 2.7, sigma: 1.0}}
//	{kind: point,     params: {value: 1.0}}
//	{kind: uniform,   params: {low: 0.05, high: 1.0}}
//	{kind: discrete,  values: [1200, 1600], probs: [0.5, 0.5]}          // 数值型
//	{kind: discrete,  categories: [turn, request], probs: [0.5, 0.5]}  // 类别型（结构未知数）
type Distribution struct {
	Kind          DistributionKind   `yaml:"kind"`
	Params        map[string]float64 `yaml:"params,omitempty"` // point: value / normal|lognormal: mu,sigma / uniform: low,high
	Values        []float64          `yaml:"values,omitempty"` // 数值型离散分布
	Probs         []float64          `yaml:"probs,omitempty"`
	Categories    []string           `yaml:"categories,omitempty"` // 类别型离散分布（结构辨识用，如 prompt 粒度 turn/request/step）
	CategoryProbs []float64          `yaml:"category_probs,omitempty"`
}

// UnmarshalYAML 归一类别型离散分布的书写形态（Intent §2.1 契约：类别型用
// `probs` 配 `categories`）。加载后类别型概率统一存在 CategoryProbs，
// 数值型存在 Probs——往返稳定，estimate 模块无需二义分派。
func (d *Distribution) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Kind          DistributionKind   `yaml:"kind"`
		Params        map[string]float64 `yaml:"params"`
		Values        []float64          `yaml:"values"`
		Probs         []float64          `yaml:"probs"`
		Categories    []string           `yaml:"categories"`
		CategoryProbs []float64          `yaml:"category_probs"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	*d = Distribution{
		Kind:          raw.Kind,
		Params:        raw.Params,
		Values:        raw.Values,
		Probs:         raw.Probs,
		Categories:    raw.Categories,
		CategoryProbs: raw.CategoryProbs,
	}
	if len(raw.Categories) > 0 && len(raw.CategoryProbs) == 0 && len(raw.Probs) > 0 {
		d.CategoryProbs = raw.Probs
		d.Probs = nil
	}
	return nil
}

// Point 构造确定值分布。
func Point(v float64) Distribution {
	return Distribution{Kind: DistPoint, Params: map[string]float64{"value": v}}
}

// Validate 校验分布的结构一致性：kind 必填且已知（封闭集检查在 enums.go），
// 各 kind 的必备 params 齐全、取值合法（sigma>=0、low<=high），离散分布的
// 值/类别与概率长度一致、概率非负且和为 1（±1e-6 容差，浮点手写友好）。
func (d *Distribution) Validate() error {
	has := func(k string) bool { _, ok := d.Params[k]; return ok }
	switch d.Kind {
	case DistPoint:
		if !has("value") {
			return fmt.Errorf("qdl: point 分布缺 params.value")
		}
	case DistNormal, DistLognormal:
		if !has("mu") || !has("sigma") {
			return fmt.Errorf("qdl: %s 分布缺 params.mu/sigma", d.Kind)
		}
		if d.Params["sigma"] < 0 {
			return fmt.Errorf("qdl: %s 分布 sigma=%v 为负", d.Kind, d.Params["sigma"])
		}
	case DistUniform:
		if !has("low") || !has("high") {
			return fmt.Errorf("qdl: uniform 分布缺 params.low/high")
		}
		if d.Params["low"] > d.Params["high"] {
			return fmt.Errorf("qdl: uniform 分布 low=%v > high=%v", d.Params["low"], d.Params["high"])
		}
	case DistDiscrete:
		numeric, categorical := len(d.Values) > 0, len(d.Categories) > 0
		if numeric == categorical { // 两者都要有且仅有一个形态
			return fmt.Errorf("qdl: discrete 分布必须且只能提供 values（数值型）或 categories（类别型）之一")
		}
		n := len(d.Values)
		if n == 0 {
			n = len(d.Categories)
		}
		if len(d.Probs) == 0 && len(d.CategoryProbs) == 0 {
			return fmt.Errorf("qdl: discrete 分布缺 probs/category_probs")
		}
		ps := d.Probs
		if len(ps) == 0 {
			ps = d.CategoryProbs
		}
		if len(ps) != n {
			return fmt.Errorf("qdl: discrete 分布值数 %d 与概率数 %d 不一致", n, len(ps))
		}
		sum := 0.0
		for _, p := range ps {
			if p < 0 {
				return fmt.Errorf("qdl: discrete 分布含负概率 %v", p)
			}
			sum += p
		}
		if sum < 1-1e-6 || sum > 1+1e-6 {
			return fmt.Errorf("qdl: discrete 分布概率和 %v ≠ 1", sum)
		}
	default:
		return fmt.Errorf("qdl: 分布 kind 为空或未知 %q", d.Kind)
	}
	return nil
}

// DriftState 是参数漂移检测器的运行状态（CUSUM/Page-Hinkley，见 estimate/drift）。
type DriftState struct {
	Detector          string     `yaml:"detector"`            // cusum | page_hinkley
	Statistic         float64    `yaml:"statistic"`           // 当前累积统计量
	LastChangepointAt *time.Time `yaml:"last_changepoint_at"` // 最近变点
	Alarm             bool       `yaml:"alarm"`               // 越过阈值，触发局部重估 + StructureUpdateEvent
}

// Parameter 是待估参数（一等公民）。权重与容量建模为带置信区间的可学习量，
// 而非写死常量——这是整个系统区别于现有开源工具的核心决策（Intent §0 难点 1）。
type Parameter struct {
	ID         string        `yaml:"id"`   // 全局唯一，e.g. "anthropic.max20.C_5h"
	Unit       string        `yaml:"unit"` // "usd_equivalent" / "prompts" / "dimensionless" ...
	Prior      Distribution  `yaml:"prior"`
	Posterior  *Distribution `yaml:"posterior"` // nil = 尚未估计
	Provenance Provenance    `yaml:"provenance"`
	Bounds     [2]*float64   `yaml:"bounds"` // [lower, upper]；nil = 该侧无界
	// SnapCandidates：厂商内部系数几乎总是整齐值（1、5、0.1、0.25）。
	// 估计落入候选的 90% 区间内则吸附并 frozen（级联收窄其余参数）。
	SnapCandidates []float64   `yaml:"snap_candidates,omitempty"`
	Frozen         bool        `yaml:"frozen"` // gauge 锚定参数不再更新
	Drift          *DriftState `yaml:"drift"`
}

// ParamPoint 是一次求值所用的参数取值快照（MAP 点估计或采样点）。
type ParamPoint map[string]float64

// Coeff 是「常量或参数引用」的双态系数：所有数值槽位（容量、权重、倍率、flat、floor）
// 的统一类型。零值不是合法状态；用 Const 或 Ref 构造，用 Resolve 求值。
type Coeff struct {
	ref string  // 非空 ⇒ 指向 Parameter.ID
	val float64 // ref 为空时的常量值
}

// Const 构造常量系数。
func Const(v float64) Coeff { return Coeff{val: v} }

// Ref 构造参数引用系数。
func Ref(id string) Coeff { return Coeff{ref: id} }

// IsRef 报告该系数是否为参数引用。
func (c Coeff) IsRef() bool { return c.ref != "" }

// RefID 返回引用的参数 ID；常量系数返回空串。
func (c Coeff) RefID() string { return c.ref }

// Constant 返回常量值；参数引用返回 (0, false)——引用请先 Resolve。
func (c Coeff) Constant() (float64, bool) { return c.val, !c.IsRef() }

// Resolve 用参数快照求值。常量直接返回；引用必须在 theta 中存在。
func (c Coeff) Resolve(theta ParamPoint) (float64, error) {
	if c.ref == "" {
		return c.val, nil
	}
	v, ok := theta[c.ref]
	if !ok {
		return 0, fmt.Errorf("qdl: 参数引用 %q 未在 ParamPoint 中提供", c.ref)
	}
	return v, nil
}

// UnmarshalYAML 实现 YAML 双态解码（Intent §2.1 Coeff = float | ParamRef）：
// 数值标量 → 常量；字符串 → 参数引用。其余形态（映射/序列/空串引用）拒绝。
func (c *Coeff) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var f float64
	if err := unmarshal(&f); err == nil {
		*c = Const(f)
		return nil
	}
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("qdl: Coeff 必须是数值常量或参数引用字符串")
	}
	if s == "" {
		return fmt.Errorf("qdl: Coeff 参数引用不能为空串")
	}
	*c = Ref(s)
	return nil
}

// MarshalYAML 输出双态的规范形：常量 → 数值，引用 → 字符串。
func (c Coeff) MarshalYAML() (interface{}, error) {
	if c.ref != "" {
		return c.ref, nil
	}
	return c.val, nil
}
