package qdl

import (
	"path"
	"sort"
)

// Linearization 声明规划期（LP）对该扣减规则采用的线性近似方式（Intent §1.6 契约）：
// 规划用线性近似、记账用精确形式，两者的差值累积为 linearization_residual。
type Linearization string

const (
	LinearExactLinear Linearization = "exact_linear"   // 本身线性（无量化/floor），两模式一致
	LinearExpectedEV  Linearization = "expected_value" // 量化替换为期望值
	LinearUpperBound  Linearization = "upper_bound"    // 线性上界（保守）
)

// Term 是扣减函数的一个加权项：w_dim × q_dim(x_dim)。
type Term struct {
	Dim      Dim      // 计量维度（token 细分维权重不同，故逐维一项）
	Coeff    Coeff    // 权重（常量或 ParamRef——一切系数皆可待估）
	Quantize Quantize // 维度级量化（ceil to 1000 tokens 之类）
}

// ChargeRule 是分段线性 + 量化的扣减函数（Intent §1.6，刻意限制表达力换取 LP 可解性）：
//
//	charge_b(request) = max( flat + m(model)·e(effort)·Σ_d w_d·q_d(x_d), floor ) 再做桶级量化
//
// 不支持任意非线性与跨请求状态依赖（缓存命中经 cache_read_tokens 维显式表达）。
// 这是结构套利的机器可读来源：terms 满而 flat=0 是 per-token 桶；terms 空而 flat=1
// 是 per-request 桶（大上下文任务在那里的边际 token 成本为 0）。
type ChargeRule struct {
	Flat             Coeff            // 每请求固定扣（per-request 桶为 1，terms 空）
	Terms            []Term           // 加权项
	ModelMultiplier  map[string]Coeff // 模型 ID / glob → 倍率（"claude-opus-*"）
	EffortMultiplier map[string]Coeff // 努力级 → 倍率（thinking budget / reasoning effort）
	Floor            Coeff            // 每请求最低扣
	Quantize         Quantize         // 桶级量化
	Linearization    Linearization    // 规划期线性近似声明（契约要求显式）
}

// MultiplierFor 返回模型命中的模型倍率。多个 pattern 命中时取最长 pattern（更具体者）。
// 返回 ok=false 表示无倍率（= 1.0）。
func (r *ChargeRule) MultiplierFor(model string) (Coeff, bool) {
	return matchPattern(r.ModelMultiplier, model)
}

// EffortFor 返回努力级命中的倍率。
func (r *ChargeRule) EffortFor(effort string) (Coeff, bool) {
	return matchPattern(r.EffortMultiplier, effort)
}

// matchPattern 在 pattern→Coeff 表里找匹配 model 的条目，取最长 pattern。
func matchPattern(table map[string]Coeff, model string) (Coeff, bool) {
	var best Coeff
	bestPat := ""
	for pat, c := range table {
		ok, err := path.Match(pat, model)
		if err != nil || !ok {
			continue
		}
		if len(pat) > len(bestPat) {
			best, bestPat = c, pat
		}
	}
	if bestPat == "" {
		return Coeff{}, false
	}
	return best, true
}

// sortedKeys 供确定性遍历（测试与序列化稳定性）。
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
