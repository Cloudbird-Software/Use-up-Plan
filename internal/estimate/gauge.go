package estimate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// 耦合组探测（Intent §0.3 尺度不可辨识性的工程面）：只观测百分比时
// y = 100·(Σ_d w_d·q_d(x_d) + flat)/C，对任意 λ>0 参数 (λw, λflat, λC)
// 给出相同观测——同一条扣减路径上的全部系数与容量共享一个尺度自由度。
// 探测方式：桶 → 参数集合（容量 + 扣减规则的全部 Coeff 引用 + 外生消耗率），
// 共享参数的桶传递合并（并查集），连通分量即耦合组。
//
// gauge fixing（Intent §4.2）：每个耦合组内至少一个参数 frozen（通常是
// 锚定价目表的权重 w），尺度自由度被消除，组内其余参数全部可辨识。

// unionFind 是参数 ID 上的并查集（路径压缩 + 按秩合并）。
type unionFind struct {
	parent map[string]string
	rank   map[string]int
}

func newUnionFind() *unionFind {
	return &unionFind{parent: map[string]string{}, rank: map[string]int{}}
}

func (u *unionFind) add(x string) {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
	}
}

func (u *unionFind) find(x string) string {
	u.add(x)
	root := x
	for u.parent[root] != root {
		root = u.parent[root]
	}
	for u.parent[x] != root { // 路径压缩
		u.parent[x], x = root, u.parent[x]
	}
	return root
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
	if u.rank[ra] == u.rank[rb] {
		u.rank[ra]++
	}
}

// bucketParams 收集一个桶的扣减路径引用的全部参数 ID。
func bucketParams(b *qdl.Bucket) []string {
	ids := []string{}
	add := func(c qdl.Coeff) {
		if c.IsRef() {
			ids = append(ids, c.RefID())
		}
	}
	add(b.Capacity)
	add(b.Charge.Flat)
	add(b.Charge.Floor)
	for i := range b.Charge.Terms {
		add(b.Charge.Terms[i].Coeff)
	}
	for _, pat := range sortedCoeffKeys(b.Charge.ModelMultiplier) {
		add(b.Charge.ModelMultiplier[pat])
	}
	for _, e := range sortedCoeffKeys(b.Charge.EffortMultiplier) {
		add(b.Charge.EffortMultiplier[e])
	}
	if b.ExogenousRateParam != "" {
		ids = append(ids, b.ExogenousRateParam)
	}
	return ids
}

func sortedCoeffKeys(m map[string]qdl.Coeff) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// CouplingGroup 是一组共享尺度自由度的参数及其涉及的桶。
type CouplingGroup struct {
	ParamIDs  []string // 组内参数（spec.Parameters 声明序）
	BucketIDs []string // 涉及的桶（spec.Buckets 声明序）
}

// CouplingGroups 探测 spec 的参数耦合组：共享一个桶扣减路径的参数同组，
// 共享参数的桶传递合并。未被任何桶引用的参数不进组（无尺度问题）。
// 返回按组内最小参数下标排序，确定性。
func CouplingGroups(spec *qdl.PlanSpec) []CouplingGroup {
	if spec == nil {
		return nil
	}
	uf := newUnionFind()
	paramIdx := map[string]int{}
	for i := range spec.Parameters {
		paramIdx[spec.Parameters[i].ID] = i
		uf.add(spec.Parameters[i].ID)
	}
	bucketOf := map[string][]string{} // 参数根 → 桶列表
	for i := range spec.Buckets {
		b := &spec.Buckets[i]
		ps := bucketParams(b)
		for _, p := range ps {
			if _, ok := paramIdx[p]; !ok {
				continue // 校验保证存在；防御重复探测未知引用
			}
			for _, q := range ps {
				if _, ok := paramIdx[q]; ok {
					uf.union(p, q)
				}
			}
			root := uf.find(p)
			found := false
			for _, bb := range bucketOf[root] {
				if bb == b.ID {
					found = true
					break
				}
			}
			if !found {
				bucketOf[root] = append(bucketOf[root], b.ID)
			}
		}
	}
	// 根 → 组
	rootParams := map[string][]string{}
	rootBuckets := map[string][]string{}
	for i := range spec.Parameters {
		id := spec.Parameters[i].ID
		root := uf.find(id)
		if _, ok := bucketOf[root]; !ok {
			continue // 未被桶引用的参数不构成耦合组
		}
		rootParams[root] = append(rootParams[root], id)
	}
	for root, buckets := range bucketOf {
		rootBuckets[root] = buckets
	}
	groups := make([]CouplingGroup, 0, len(rootParams))
	for root, params := range rootParams {
		sort.Strings(rootBuckets[root])
		groups = append(groups, CouplingGroup{ParamIDs: params, BucketIDs: rootBuckets[root]})
	}
	sort.Slice(groups, func(i, j int) bool {
		return strings.Join(groups[i].ParamIDs, ",") < strings.Join(groups[j].ParamIDs, ",")
	})
	return groups
}

// GaugeProblem 描述一个缺 gauge 锚定的耦合组（尺度不可辨识）。
type GaugeProblem struct {
	Group   CouplingGroup
	Frozen  []string // 组内已 frozen 的参数（空 = 完全未锚定）
	Message string   // 人类可读诊断
}

// ValidateGauge 检查每个耦合组至少一个 frozen 参数（Intent §4.2 gauge
// fixing 的充分条件）。未锚定的组返回 GaugeProblem 列表（空列表 = 通过）。
// 建议文案按 Intent §0.3 的规范化选择：把 token 维权重锚定到官方按量
// 价目表，C 的单位自动变成等价 API 美元。
func ValidateGauge(spec *qdl.PlanSpec) []GaugeProblem {
	if spec == nil {
		return nil
	}
	frozen := map[string]bool{}
	for i := range spec.Parameters {
		if spec.Parameters[i].Frozen {
			frozen[spec.Parameters[i].ID] = true
		}
	}
	var problems []GaugeProblem
	for _, g := range CouplingGroups(spec) {
		var fz []string
		for _, id := range g.ParamIDs {
			if frozen[id] {
				fz = append(fz, id)
			}
		}
		if len(fz) == 0 {
			problems = append(problems, GaugeProblem{
				Group:  g,
				Frozen: fz,
				Message: fmt.Sprintf(
					"耦合组 [%s]（桶 %v）内无 frozen 参数：尺度不可辨识——(λw, λflat, λC) 给出相同观测。"+
						"把一个权重锚定到官方价目表（provenance: gauge + frozen: true），"+
						"容量单位即等价 API 美元（Intent §0.3/§4.2）",
					strings.Join(g.ParamIDs, ", "), g.BucketIDs),
			})
		}
	}
	return problems
}

// GaugeRead 是 gauge 锚定后的一条可解释读数（Intent §4.2 的核心输出：
// C 的等价美元价值、mult 的相对偏离、缓存折扣证据——都是可直接行动的结论）。
type GaugeRead struct {
	Kind           string // "capacity_usd_equiv" | "multiplier_deviation" | "weight_ratio"
	Subject        string // 桶 ID 或参数 ID
	Value          float64
	Interpretation string // 人类可读结论
}

// GaugeSummary 产出 gauge 锚定参数快照的可解释读数表：
//   - 容量 → 「等价 API 美元」读数（gauge mode = anchor_to_vendor_ratecard 时）
//   - 模型倍率 → 「相对 API 价差的偏离倍数」（>1 = 订阅内额外惩罚）
//   - 权重比 → cache_read 等折扣证据（w_cache/w_input vs API 价目比）
func GaugeSummary(spec *qdl.PlanSpec, theta qdl.ParamPoint) []GaugeRead {
	if spec == nil {
		return nil
	}
	var reads []GaugeRead
	anchored := spec.Gauge.Mode == "anchor_to_vendor_ratecard" && len(spec.Gauge.RatecardUSDPerUnit) > 0
	for i := range spec.Buckets {
		b := &spec.Buckets[i]
		c, err := b.Capacity.Resolve(theta)
		if err != nil || c <= 0 {
			continue
		}
		if anchored {
			reads = append(reads, GaugeRead{
				Kind: "capacity_usd_equiv", Subject: b.ID, Value: c,
				Interpretation: fmt.Sprintf("桶 %s 用尽 = %.2f 等价 API 美元", b.ID, c),
			})
		}
		// 模型倍率读数：>1 的倍率是「订阅内额外惩罚」的定量证据
		for _, pat := range sortedCoeffKeys(b.Charge.ModelMultiplier) {
			m, err := b.Charge.ModelMultiplier[pat].Resolve(theta)
			if err != nil {
				continue
			}
			interp := fmt.Sprintf("模型 %q 倍率 %.2f（相对 gauge 锚定价的偏离倍数）", pat, m)
			if m > 1.05 {
				interp += "——订阅内被额外惩罚，权重超出 API 价差，厂商在劝退该模型的定量证据"
			} else if m < 0.95 {
				interp += "——订阅内相对优待"
			}
			reads = append(reads, GaugeRead{Kind: "multiplier_deviation", Subject: pat, Value: m, Interpretation: interp})
		}
	}
	// 权重比读数：cache_read 折扣证据（w_cache_read/w_input 对照价目比）
	if anchored {
		for i := range spec.Buckets {
			b := &spec.Buckets[i]
			var wIn, wCache float64
			hasIn, hasCache := false, false
			for j := range b.Charge.Terms {
				w, err := b.Charge.Terms[j].Coeff.Resolve(theta)
				if err != nil {
					continue
				}
				switch b.Charge.Terms[j].Dim {
				case qdl.DimInputTokens:
					wIn, hasIn = w, true
				case qdl.DimCacheReadTokens:
					wCache, hasCache = w, true
				}
			}
			if !hasIn || !hasCache || wIn <= 0 {
				continue
			}
			ratio := wCache / wIn
			apiIn := spec.Gauge.RatecardUSDPerUnit[qdl.DimInputTokens]
			apiCache := spec.Gauge.RatecardUSDPerUnit[qdl.DimCacheReadTokens]
			interp := fmt.Sprintf("桶 %s 的 cache_read 权重比 %.3f", b.ID, ratio)
			if apiIn > 0 && apiCache > 0 {
				interp += fmt.Sprintf("（API 价目比 %.3f）", apiCache/apiIn)
				if ratio > apiCache/apiIn*1.05 {
					interp += "——订阅不给足缓存折扣，缓存优化收益打折，缓存密集任务应移走"
				} else if ratio < apiCache/apiIn*0.95 {
					interp += "——订阅缓存折扣优于 API"
				}
			} else if ratio > 0.95 {
				interp += "——订阅内缓存按全价计，缓存优化在这里白做"
			}
			reads = append(reads, GaugeRead{Kind: "weight_ratio", Subject: b.ID, Value: ratio, Interpretation: interp})
		}
	}
	return reads
}
