package qdl

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// goldenYAML 供多个测试与 fuzz 种子复用。
func goldenYAML(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden.qdl.yaml"))
	if err != nil {
		t.Fatalf("读 golden fixture: %v", err)
	}
	return b
}

// TestLoadGoldenSpotChecks 加载黄金样本并抽查关键结构（$ref 复制、双态 Coeff、
// ISO 时长、缺省回填、大写 overflow 规范化）。
func TestLoadGoldenSpotChecks(t *testing.T) {
	spec, err := LoadBytes(goldenYAML(t))
	if err != nil {
		t.Fatalf("golden 加载失败: %v", err)
	}
	if spec.ID != "t/golden@2026-08" || spec.Period != "month" {
		t.Fatalf("基本信息: %+v", spec)
	}
	b5h := spec.Bucket("b_5h")
	if b5h == nil {
		t.Fatal("桶 b_5h 缺失")
	}
	// ISO 8601 时长 + 后验 MAP
	if b5h.Window.Length.Duration != 5*time.Hour {
		t.Fatalf("PT5H: %v", b5h.Window.Length.Duration)
	}
	if b5h.Window.Kind() != WindowTumblingAnchoredOnFirstUse {
		t.Fatalf("MAP 窗型: %v", b5h.Window.Kind())
	}
	if b5h.Window.Reset != ResetZero {
		t.Fatalf("reset 显式 zero: %v", b5h.Window.Reset)
	}
	// 双态 Coeff
	if !b5h.Capacity.IsRef() || b5h.Capacity.RefID() != "t.golden.C_5h" {
		t.Fatalf("capacity 应为引用: %+v", b5h.Capacity)
	}
	in := b5h.Charge.Terms[0]
	if v, ok := in.Coeff.Constant(); !ok || v != 3e-6 {
		t.Fatalf("term[0] 应为常量 3e-6: (%v,%v)", v, ok)
	}
	cr := b5h.Charge.Terms[1]
	if !cr.Coeff.IsRef() || cr.Coeff.RefID() != "t.golden.cache_ratio" {
		t.Fatalf("term[1] 应为引用: %+v", cr.Coeff)
	}
	// 维度级量化缺省：term[0]/term[1] 未声明 quantize → none/1
	if in.Quantize.Mode != QuantizeNone || in.Quantize.Step != 1 {
		t.Fatalf("term[0] quantize 缺省: %+v", in.Quantize)
	}
	if cr.Quantize.Mode != QuantizeNone || cr.Quantize.Step != 1 {
		t.Fatalf("term[1] quantize 缺省: %+v", cr.Quantize)
	}
	out := b5h.Charge.Terms[2]
	if out.Quantize.Mode != QuantizeCeil || out.Quantize.Step != 1000 {
		t.Fatalf("term[2] quantize 显式 ceil/1000: %+v", out.Quantize)
	}
	// 桶级量化缺省 none/1；linearization 显式 expected_value
	if b5h.Charge.Quantize.Mode != QuantizeNone || b5h.Charge.Quantize.Step != 1 {
		t.Fatalf("桶级 quantize 缺省: %+v", b5h.Charge.Quantize)
	}
	if b5h.Charge.Linearization != LinearExpectedEV {
		t.Fatalf("linearization: %v", b5h.Charge.Linearization)
	}
	if v, ok := b5h.Charge.Flat.Constant(); !ok || v != 0 {
		t.Fatalf("flat 缺省 Const(0): (%v, %v)", v, ok)
	}
	// 大写 overflow 规范化为小写
	if b5h.Overflow[0].Action != OverflowSpillToBucket || b5h.Overflow[1].Action != OverflowHardBlockResetHint {
		t.Fatalf("overflow 规范化: %+v", b5h.Overflow)
	}
	// 观测缺省：trust 未声明 → 1.0；quantization 未声明 → unknown
	hdr := b5h.Observability[1]
	if hdr.Trust != 1.0 || hdr.Quantization.Kind != "unknown" {
		t.Fatalf("观测缺省: trust=%v quant=%+v", hdr.Trust, hdr.Quantization)
	}
	// $ref：b_7d 的 charge 应与 b_5h 完全一致
	b7d := spec.Bucket("b_7d")
	if !reflect.DeepEqual(b7d.Charge, b5h.Charge) {
		t.Fatalf("$ref 展开后 charge 应一致:\n%+v\nvs\n%+v", b7d.Charge, b5h.Charge)
	}
	// $ref 拷贝独立性：改 b_7d 不得影响 b_5h（深拷贝，非共享）
	if len(b7d.Charge.Terms) > 0 {
		b7d.Charge.Terms[0].Dim = Dim("mutated")
		if spec.Bucket("b_5h").Charge.Terms[0].Dim == Dim("mutated") {
			t.Fatal("$ref 目标被共享，深拷贝失效")
		}
	}
	// 类别型离散先验
	gran := spec.Param("t.golden.granularity")
	if gran == nil || len(gran.Prior.Categories) != 2 || gran.Prior.CategoryProbs[1] != 0.5 {
		t.Fatalf("类别型离散先验: %+v", gran.Prior)
	}
	// gauge 价目锚定
	if spec.Gauge.RatecardUSDPerUnit[DimInputTokens] != 3e-6 {
		t.Fatalf("ratecard: %+v", spec.Gauge.RatecardUSDPerUnit)
	}
	// admission 瞬时约束（自定义 key）
	ch := spec.Channel("chan_oauth")
	if got := ch.Admission.Limits[InstantConcurrency]; !got.IsRef() {
		if v, ok := got.Constant(); !ok || v != 5 {
			t.Fatalf("concurrency=5: (%v,%v)", v, ok)
		}
	} else {
		t.Fatal("concurrency 应为常量")
	}
	if spec.Channel("chan_oauth").Reliability.InterruptionGranularity != "between_requests" {
		t.Fatal("interruption_granularity 缺省 between_requests")
	}
	// grants + risk 大写规范化
	if spec.Grants[0].Kind != "base" || !spec.Grants[0].Amount.IsRef() {
		t.Fatalf("grant: %+v", spec.Grants[0])
	}
	if spec.Risk.TOSViolationClass != "grey" {
		t.Fatalf("GREY 应规范化为 grey: %q", spec.Risk.TOSViolationClass)
	}
	// b_extra：PAYG 显式开启（安全契约）+ capacity 常量
	be := spec.Bucket("b_extra")
	if v, ok := be.Capacity.Constant(); !ok || v != 50 {
		t.Fatalf("b_extra capacity=50: (%v,%v)", v, ok)
	}
	if be.Overflow[0].Action != OverflowSpillToPAYG || !be.Overflow[0].RequiresExplicitEnable {
		t.Fatalf("PAYG 契约: %+v", be.Overflow[0])
	}
	// 缺省 provenance：t.golden.cache_ratio 未声明 → assumed
	if spec.Param("t.golden.cache_ratio").Provenance != ProvenanceAssumed {
		t.Fatal("provenance 缺省 assumed")
	}
}

// TestLoadGoldenRoundTrip Load → Marshal → Load 必须语义等价（往返稳定性）。
func TestLoadGoldenRoundTrip(t *testing.T) {
	spec, err := LoadBytes(goldenYAML(t))
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	out, err := Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	spec2, err := LoadBytes(out)
	if err != nil {
		t.Fatalf("回读: %v\n---\n%s", err, out)
	}
	if !reflect.DeepEqual(spec, spec2) {
		t.Fatalf("往返后不等价\n旧: %+v\n新: %+v", spec, spec2)
	}
}

// TestLoadFile 走文件路径入口。
func TestLoadFile(t *testing.T) {
	if _, err := Load(filepath.Join("testdata", "golden.qdl.yaml")); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Load(filepath.Join("testdata", "不存在的文件.yaml")); err == nil {
		t.Fatal("读不存在的文件应报错")
	}
}

// TestLoadErrors 负面用例：未知字段、未知枚举、坏 $ref、PAYG 契约、
// 坏分布、坏 Coeff 形态。
func TestLoadErrors(t *testing.T) {
	base, err := LoadBytes(goldenYAML(t))
	if err != nil {
		t.Fatalf("golden 加载: %v", err)
	}
	_ = base
	cases := []struct {
		name string
		mut  func(string) string
		want string
	}{
		{"未知顶层字段", func(s string) string { return s + "\nunknown_top: 1\n" }, "解析 PlanSpec"},
		{"未知桶字段", func(s string) string { return strings.Replace(s, "  unit: opaque_units", "  unt: opaque_units", 1) }, "解析 PlanSpec"},
		{"未知枚举 period", func(s string) string {
			return strings.Replace(s, "period: month", "period: fortnight", 1)
		}, "period"},
		{"未知窗型候选", func(s string) string {
			return strings.Replace(s, "kind_candidates: [tumbling_anchored_on_first_use, sliding_exact]",
				"kind_candidates: [wat]", 1)
		}, "kind_candidates"},
		{"坏分布概率", func(s string) string {
			return strings.Replace(s, "categories: [turn, request], probs: [0.5, 0.5]",
				"categories: [turn, request], probs: [0.9, 0.5]", 1)
		}, "概率和"},
		{"PAYG 未显式开启", func(s string) string {
			return strings.Replace(s, "{action: SPILL_TO_PAYG, requires_explicit_enable: true}",
				"{action: SPILL_TO_PAYG}", 1)
		}, "requires_explicit_enable"},
		{"未知参数引用", func(s string) string {
			return strings.Replace(s, "coeff: t.golden.cache_ratio", "coeff: t.golden.nope", 1)
		}, "未知参数"},
		{"Coeff 非法形态", func(s string) string {
			return strings.Replace(s, "capacity: t.golden.C_5h", "capacity: {a: 1}", 1)
		}, "Coeff"},
		{"坏时长", func(s string) string {
			return strings.Replace(s, "length: PT5H", "length: PX", 1)
		}, "时长"},
		{"空溢出动作", func(s string) string {
			return strings.Replace(s, "        target: b_extra", "        target: b_extra\n        action: \"\"", 1)
		}, "action"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(c.mut(string(goldenYAML(t)))))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("期望错误含 %q, got: %v", c.want, err)
			}
		})
	}
}

// TestLoadRefErrors $ref 专列：坏路径、外部引用、兄弟键、自引用环。
func TestLoadRefErrors(t *testing.T) {
	src := `id: t/x@2026-08
vendor: t
plan_name: X
period: month
spec_version: qdl/1.0
effective_from: 2026-08-01T00:00:00Z
parameters:
  - id: t.C
    unit: usd
    prior: {kind: point, params: {value: 1}}
buckets:
  - id: b1
    unit: opaque_units
    capacity: t.C
    window: {kind_candidates: [never]}
    scope: {level: account}
    charge: CHARGE_REF
channels:
  - id: c1
    protocol: openai_chat
    auth: api_key
`
	cases := []struct {
		name, ref, want string
	}{
		{"坏路径", `{"$ref": "#/buckets/nope/charge"}`, "路径不存在"},
		{"外部引用", `{"$ref": "https://example.com/x.yaml#/charge"}`, "仅支持文档内"},
		{"标量中断", `{"$ref": "#/vendor/charge"}`, "标量处中断"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(strings.Replace(src, "CHARGE_REF", c.ref, 1)))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("期望错误含 %q, got: %v", c.want, err)
			}
		})
	}
	t.Run("兄弟键", func(t *testing.T) {
		_, err := LoadBytes([]byte(strings.Replace(src, "CHARGE_REF", `{"$ref": "#/buckets/b1/window", flat: 1}`, 1)))
		if err == nil || !strings.Contains(err.Error(), "$ref") {
			t.Fatalf("期望 $ref 错误, got: %v", err)
		}
	})
	t.Run("自引用环", func(t *testing.T) {
		loop := strings.Replace(src, "CHARGE_REF", `{"$ref": "#/buckets"}`, 1)
		loop = strings.Replace(loop, "charge: {\"$ref\": \"#/buckets\"}", "charge: {\"$ref\": \"#/buckets\"}", 1)
		_, err := LoadBytes([]byte(loop))
		if err == nil || !strings.Contains(err.Error(), "展开") {
			t.Fatalf("期望深度上限错误, got: %v", err)
		}
	})
	t.Run("链式引用", func(t *testing.T) {
		chained := strings.Replace(src, "CHARGE_REF", `{"$ref": "#/buckets/0/charge_alias"}`, 1)
		chained = strings.Replace(chained, "channels:",
			"    charge_alias: {flat: 1}\nchannels:", 1)
		// charge_alias 在桶内，但桶 strict 解码会拒绝未知字段——改放 parameters 前的自定义键不可行；
		// 此用例改为：b1.charge 引用 b2.charge，b2.charge 引用 b3.charge（链）。
		chained = `id: t/x@2026-08
vendor: t
plan_name: X
period: month
spec_version: qdl/1.0
effective_from: 2026-08-01T00:00:00Z
parameters:
  - id: t.C
    unit: usd
    prior: {kind: point, params: {value: 1}}
buckets:
  - id: b1
    unit: opaque_units
    capacity: t.C
    window: {kind_candidates: [never]}
    scope: {level: account}
    charge: {"$ref": "#/buckets/b2/charge"}
  - id: b2
    unit: opaque_units
    capacity: t.C
    window: {kind_candidates: [never]}
    scope: {level: account}
    charge: {"$ref": "#/buckets/b3/charge"}
  - id: b3
    unit: opaque_units
    capacity: t.C
    window: {kind_candidates: [never]}
    scope: {level: account}
    charge: {flat: 1, terms: [{dim: input_tokens, coeff: 2}]}
channels:
  - id: c1
    protocol: openai_chat
    auth: api_key
`
		spec, err := LoadBytes([]byte(chained))
		if err != nil {
			t.Fatalf("链式 $ref 应展开: %v", err)
		}
		if v, ok := spec.Bucket("b1").Charge.Terms[0].Coeff.Constant(); !ok || v != 2 {
			t.Fatalf("链式展开后 b1 应得 b3 的 charge: (%v,%v)", v, ok)
		}
	})
}

// FuzzLoadBytes 任意输入不 panic；成功加载者必须可序列化并再次加载（T-04）。
func FuzzLoadBytes(f *testing.F) {
	f.Add(goldenBytesForFuzz())
	f.Add([]byte("id: x"))
	f.Add([]byte("[1, 2, 3]"))
	f.Add([]byte("a: {b: {c: {}}}"))
	f.Add([]byte("charge: {\"$ref\": \"#/self\"}\nself: {\"$ref\": \"#/charge\"}"))
	f.Fuzz(func(t *testing.T, data []byte) {
		spec, err := LoadBytes(data)
		if err != nil {
			return
		}
		out, merr := Marshal(spec)
		if merr != nil {
			t.Fatalf("加载成功但 Marshal 失败: %v", merr)
		}
		if _, err := LoadBytes(out); err != nil {
			t.Fatalf("往返失败: %v\n%s", err, out)
		}
	})
}

// goldenBytesForFuzz 读 golden fixture 作为 fuzz 种子（独立函数以便初始化错误清晰）。
func goldenBytesForFuzz() []byte {
	b, err := os.ReadFile(filepath.Join("testdata", "golden.qdl.yaml"))
	if err != nil {
		return []byte("id: t/fallback@2026-08\nvendor: t\nplan_name: F\nperiod: month\nspec_version: q/1\neffective_from: 2026-08-01T00:00:00Z\nparameters:\n  - id: t.C\n    unit: u\n    prior: {kind: point, params: {value: 1}}\nbuckets:\n  - id: b1\n    unit: opaque_units\n    capacity: t.C\n    window: {kind_candidates: [never]}\n    scope: {level: account}\nchannels:\n  - id: c1\n    protocol: openai_chat\n    auth: api_key\n")
	}
	return b
}
