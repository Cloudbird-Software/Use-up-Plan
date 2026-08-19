package qdl

import "math"

// QuantizeMode 是量化模式。量化是 EXACT 记账与 LINEAR_EV 规划分界的核心：
// 每请求 ceil 到 1k token 而平均请求 300 token 时，实际消耗是名义的 3.3 倍——
// 这正是「LP 说还有 5% 余量、实际已经撞墙」诡异 bug 的来源（Intent §3.2）。
type QuantizeMode string

const (
	QuantizeNone  QuantizeMode = "none"
	QuantizeCeil  QuantizeMode = "ceil"
	QuantizeFloor QuantizeMode = "floor"
	QuantizeRound QuantizeMode = "round"
)

// Quantize 是量化规则（维度级或桶级）。
type Quantize struct {
	Mode QuantizeMode `yaml:"mode"`
	Step float64      `yaml:"step"`
}

// Apply 对 x 应用量化。Mode 为 none 或 Step<=0 时原样返回；
// 其余按 x/step 取整后回乘。
func (q Quantize) Apply(x float64) float64 {
	if q.Mode == QuantizeNone || q.Step <= 0 {
		return x
	}
	n := x / q.Step
	switch q.Mode {
	case QuantizeCeil:
		n = math.Ceil(n)
	case QuantizeFloor:
		n = math.Floor(n)
	case QuantizeRound:
		n = math.Round(n)
	}
	return n * q.Step
}

// UnmarshalYAML 应用 Pydantic 等价缺省（Intent §2.1 Quantize()）：mode 缺省 none、
// step 缺省 1。
func (q *Quantize) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Mode QuantizeMode `yaml:"mode"`
		Step *float64     `yaml:"step"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	q.Mode = raw.Mode
	if q.Mode == "" {
		q.Mode = QuantizeNone
	}
	q.Step = 1
	if raw.Step != nil {
		q.Step = *raw.Step
	}
	return nil
}
