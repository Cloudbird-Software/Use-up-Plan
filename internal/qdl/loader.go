package qdl

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// loader 把 `*.qdl.yaml` 变成加载后不可变的 PlanSpec。管线四段（深接口，
// 每段单一职责，错误文案统一 "qdl:" 前缀 + 定位）：
//
//	YAML bytes
//	  → ① 泛型树 + $ref 展开（expandRefs：文档内语义指针，见下）
//	  → ② 严格解码（yaml.Strict 拒未知字段；类型自带缺省的 UnmarshalYAML）
//	  → ③ 缺省规范化（normalizeDefaults：provenance/gauge.mode/tos_class 等）
//	  → ④ PlanSpec.Validate（安全契约唯一执法点：引用可解析、PAYG 契约、封闭集）
//
// $ref 语义（Intent §2.2 示例采用）：`{"$ref": "#/buckets/b_5h/charge"}`。
// 路径是 JSON pointer 的扩展——数组段既接受数字下标，也接受按元素 "id"
// 字段匹配（`buckets/b_5h` 即「buckets 里 id == b_5h 的那个」）。仅支持
// 文档内引用；引用目标深拷贝后替换，支持链式引用（深度上限防环）。

// maxRefDepth 是 $ref 链式展开的深度上限（防自引用环）。
const maxRefDepth = 32

// Load 读取路径并加载 PlanSpec（LoadBytes 是纯函数核心，本函数仅包一层文件读）。
func Load(path string) (*PlanSpec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("qdl: 读文件 %q: %w", path, err)
	}
	spec, err := LoadBytes(b)
	if err != nil {
		return nil, fmt.Errorf("qdl: %s: %w", path, err)
	}
	return spec, nil
}

// LoadBytes 是加载管线本体：纯函数、零网络、零副作用。
func LoadBytes(b []byte) (*PlanSpec, error) {
	var tree any
	if err := yaml.Unmarshal(b, &tree); err != nil {
		return nil, fmt.Errorf("qdl: 解析 YAML: %w", err)
	}
	expanded, err := expandRefs(tree, tree, 0)
	if err != nil {
		return nil, err
	}
	resolved, err := yaml.Marshal(expanded)
	if err != nil {
		return nil, fmt.Errorf("qdl: 序列化 $ref 展开结果: %w", err)
	}
	var spec PlanSpec
	if err := yaml.UnmarshalWithOptions(resolved, &spec, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("qdl: 解析 PlanSpec（含缺省回填）: %w", err)
	}
	normalizeDefaults(&spec)
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

// Marshal 输出 PlanSpec 的规范 YAML（往返稳定：LoadBytes(Marshal(s)) 语义等价）。
// 不做 Validate——序列化一个进行中的（尚未合法的）spec 是合法需求。
func Marshal(p *PlanSpec) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("qdl: Marshal(nil)")
	}
	return yaml.MarshalWithOptions(p)
}

// normalizeDefaults 补齐字段级 UnmarshalYAML 覆盖不到的顶层缺省
// （等价 Intent §2.1 Pydantic 默认值）。
//
// 注意 Quantize：goccy 对「结构体字段键缺失」的场景不调用自定义
// UnmarshalYAML（零值直接透传），所以 term 未写 quantize、charge 未写
// quantize 时缺省必须在这里补——字段级 UnmarshalYAML 只覆盖「键存在」
// 的情况。Step<=0 与 none 语义等价（Apply 原样返回），一并归一到 1，
// 保证 LoadBytes(Marshal(s)) 往返稳定。
func normalizeDefaults(p *PlanSpec) {
	for i := range p.Buckets {
		charge := &p.Buckets[i].Charge
		normQuantize(&charge.Quantize)
		for j := range charge.Terms {
			normQuantize(&charge.Terms[j].Quantize)
		}
	}
	for i := range p.Parameters {
		prm := &p.Parameters[i]
		if prm.Provenance == "" {
			prm.Provenance = ProvenanceAssumed
		}
		if prm.Drift != nil && prm.Drift.Detector == "" {
			prm.Drift.Detector = "cusum"
		}
	}
	if p.Gauge.Mode == "" {
		p.Gauge.Mode = "anchor_to_vendor_ratecard"
	}
	p.Risk.TOSViolationClass = strings.ToLower(p.Risk.TOSViolationClass)
	if p.Risk.TOSViolationClass == "" {
		p.Risk.TOSViolationClass = "none"
	}
	for i := range p.Channels {
		rel := &p.Channels[i].Reliability
		if rel.InterruptionGranularity == "" {
			rel.InterruptionGranularity = "between_requests"
		}
	}
}

// normQuantize 回填 Quantize 缺省：mode 空 → none，step<=0 → 1。
func normQuantize(q *Quantize) {
	if q.Mode == "" {
		q.Mode = QuantizeNone
	}
	if q.Step <= 0 {
		q.Step = 1
	}
}

// expandRefs 自顶向下遍历泛型树，把单键 {"$ref": "#/..."} 映射替换为目标
// 子树的深拷贝（拷贝防多处引用共享可变节点），并对拷贝继续展开（链式引用）。
func expandRefs(node, root any, depth int) (any, error) {
	if depth > maxRefDepth {
		return nil, fmt.Errorf("qdl: $ref 链式展开超过 %d 层（疑似自引用环）", maxRefDepth)
	}
	switch v := node.(type) {
	case map[string]any:
		if ref, ok := singleRef(v); ok {
			target, err := lookupPointer(root, ref)
			if err != nil {
				return nil, err
			}
			return expandRefs(deepCopy(target), root, depth+1)
		}
		if _, has := v["$ref"]; has {
			return nil, fmt.Errorf("qdl: 带兄弟键的 $ref 不支持（$ref 必须是映射的唯一键）")
		}
		for k, val := range v {
			nv, err := expandRefs(val, root, depth)
			if err != nil {
				return nil, err
			}
			v[k] = nv
		}
		return v, nil
	case []any:
		for i, val := range v {
			nv, err := expandRefs(val, root, depth)
			if err != nil {
				return nil, err
			}
			v[i] = nv
		}
		return v, nil
	default:
		return node, nil
	}
}

// singleRef 判定映射是否恰为 {"$ref": "<文档内指针>"}。
func singleRef(m map[string]any) (string, bool) {
	if len(m) != 1 {
		return "", false
	}
	s, ok := m["$ref"].(string)
	return s, ok
}

// lookupPointer 从 root 解析 "#/a/b/c" 语义指针。数组段先按数字下标、
// 不中再按元素 "id" 字段匹配；映射段按键直取。
func lookupPointer(root any, ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("qdl: 仅支持文档内 $ref（须 #/ 开头）: %q", ref)
	}
	segs := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	cur := root
	for _, seg := range segs {
		switch n := cur.(type) {
		case map[string]any:
			v, ok := n[seg]
			if !ok {
				return nil, fmt.Errorf("qdl: $ref %q 路径不存在（键 %q 缺失）", ref, seg)
			}
			cur = v
		case []any:
			if idx, err := strconv.Atoi(seg); err == nil && idx >= 0 && idx < len(n) {
				cur = n[idx]
				continue
			}
			found := false
			for _, item := range n {
				im, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if id, _ := im["id"].(string); id == seg {
					cur, found = item, true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("qdl: $ref %q 路径不存在（数组无下标 %q 亦无 id=%q 的元素）", ref, seg, seg)
			}
		default:
			return nil, fmt.Errorf("qdl: $ref %q 在标量处中断（段 %q）", ref, seg)
		}
	}
	return cur, nil
}

// deepCopy 深拷贝泛型树（仅 map/slice/标量三类节点）。
func deepCopy(node any) any {
	switch v := node.(type) {
	case map[string]any:
		m := make(map[string]any, len(v))
		for k, val := range v {
			m[k] = deepCopy(val)
		}
		return m
	case []any:
		s := make([]any, len(v))
		for i, val := range v {
			s[i] = deepCopy(val)
		}
		return s
	default:
		return v
	}
}
