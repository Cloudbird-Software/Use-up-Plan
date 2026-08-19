package qdl

import (
	"testing"

	"github.com/goccy/go-yaml"
)

// Coeff 双态 YAML 解码：数值 → 常量；字符串 → 参数引用。
func TestCoeffYAMLDecode(t *testing.T) {
	type holder struct {
		Const Coeff `yaml:"const"`
		Ref   Coeff `yaml:"ref"`
		Int   Coeff `yaml:"int"`
		Sci   Coeff `yaml:"sci"`
	}
	var h holder
	src := []byte("const: 1.5\nref: anthropic.max20.C_5h\nint: 5\nsci: 3.0e-6\n")
	if err := yaml.UnmarshalWithOptions(src, &h, yaml.Strict()); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if v, ok := h.Const.Constant(); !ok || v != 1.5 {
		t.Errorf("数值应解码为常量: (%v, %v)", v, ok)
	}
	if !h.Ref.IsRef() || h.Ref.RefID() != "anthropic.max20.C_5h" {
		t.Errorf("字符串应解码为引用: %+v", h.Ref)
	}
	if v, ok := h.Int.Constant(); !ok || v != 5 {
		t.Errorf("整数标量: (%v, %v)", v, ok)
	}
	if v, ok := h.Sci.Constant(); !ok || v != 3e-6 {
		t.Errorf("科学计数法: (%v, %v)", v, ok)
	}
}

// Coeff 非法形态：映射 / 序列 / 空串引用。
func TestCoeffYAMLBadShape(t *testing.T) {
	for name, src := range map[string]string{
		"映射":   "c: {a: 1}\n",
		"序列":   "c: [1, 2]\n",
		"空串引用": "c: \"\"\n",
	} {
		type holder struct {
			C Coeff `yaml:"c"`
		}
		var h holder
		if err := yaml.UnmarshalWithOptions([]byte(src), &h, yaml.Strict()); err == nil {
			t.Errorf("%s 形态应报错", name)
		}
	}
}

// Coeff 序列化往返：常量 → 数值，引用 → 字符串，回读后语义不变。
func TestCoeffYAMLMarshalRoundTrip(t *testing.T) {
	type holder struct {
		A Coeff `yaml:"a"`
		B Coeff `yaml:"b"`
	}
	h := holder{A: Const(3e-6), B: Ref("p.w")}
	out, err := yaml.MarshalWithOptions(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back holder
	if err := yaml.UnmarshalWithOptions(out, &back, yaml.Strict()); err != nil {
		t.Fatalf("回读: %v", err)
	}
	if v, _ := back.A.Constant(); v != 3e-6 || back.A.IsRef() {
		t.Errorf("常量往返: %+v", back.A)
	}
	if !back.B.IsRef() || back.B.RefID() != "p.w" {
		t.Errorf("引用往返: %+v", back.B)
	}
}
