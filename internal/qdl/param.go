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
	Params        map[string]float64 `yaml:"params"` // point: value / normal|lognormal: mu,sigma / uniform: low,high
	Values        []float64          `yaml:"values"` // 数值型离散分布
	Probs         []float64          `yaml:"probs"`
	Categories    []string           `yaml:"categories"` // 类别型离散分布（结构辨识用，如 prompt 粒度 turn/request/step）
	CategoryProbs []float64          `yaml:"category_probs"`
}

// Point 构造确定值分布。
func Point(v float64) Distribution {
	return Distribution{Kind: DistPoint, Params: map[string]float64{"value": v}}
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
	SnapCandidates []float64   `yaml:"snap_candidates"`
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
