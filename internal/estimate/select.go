package estimate

import (
	"fmt"
	"math"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/ledger"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// StructureChoice 是一个结构未知量的模型选择结果（Intent §4.3）：
// 枚举候选结构 → 同一数据集上拟合 → BIC 打分 → softmax 后验。
type StructureChoice struct {
	BucketID  string             // 结构未知量所属桶
	Field     string             // 结构字段（当前仅 "window.kind"）
	Posterior map[string]float64 // 候选 -> 后验概率（softmax 输出，和为 1）
	Scores    map[string]float64 // 候选 -> BIC 打分（越大越好；logL_hat - ½k·ln n）
	NParams   int                // 自由参数个数 k（候选间参数集相同时相等）
	NObs      int                // 打分用观测数 n
}

// SelectOptions 是结构选择的运行参数。
type SelectOptions struct {
	Estimate EstimateOptions // 传给每个候选的在线点估计（warm-start 复用）
}

// SelectStructure 对 spec 中每个窗口带多候选（len(KindCandidates) > 1）
// 的桶做 BIC 模型选择（Intent §4.3）：
//
//	score(cand) = logL_hat(cand) - ½·k·ln(n)
//	Posterior   = softmax(score)
//
// score 即 BIC 口径的边际似然近似（logZ ≈ logL_hat - ½k·ln n）；候选间
// 参数集相同时惩罚项相消，后验比退化为似然比——窗口 kind 候选不改变
// 参数集，正是这种情形。打分只用纯似然（不含先验）：结构候选共享同一
// 参数先验，把先验计入会重复一次。
//
// 每个候选用同一 theta0 warm-start、同一观测集拟合——唯一差异是
// KindPosterior 钉到单候选（semantics.advance 按它分派），保证比较公平。
// 确定性：候选按 KindCandidates 声明序遍历，softmax 数值稳定（log-sum-exp）。
func SelectStructure(ds *Dataset, base qdl.ParamPoint, theta0 qdl.ParamPoint, opts SelectOptions) ([]StructureChoice, error) {
	if ds == nil || ds.Spec == nil {
		return nil, fmt.Errorf("estimate: SelectStructure(nil)")
	}
	n := len(ds.Obs)
	if n == 0 {
		return nil, fmt.Errorf("estimate: SelectStructure 无观测——结构选择没有证据（先跑采集或导入）")
	}
	var choices []StructureChoice
	for bi := range ds.Spec.Buckets {
		b := &ds.Spec.Buckets[bi]
		if len(b.Window.KindCandidates) <= 1 {
			continue // 单候选：无结构未知量
		}
		ch := StructureChoice{
			BucketID: b.ID, Field: "window.kind", NObs: n,
			Scores: map[string]float64{}, Posterior: map[string]float64{},
		}
		// 候选按声明序打分（确定性）；重复候选只打一次分。
		seen := map[string]bool{}
		var order []string
		for _, cand := range b.Window.KindCandidates {
			if seen[string(cand)] {
				continue
			}
			seen[string(cand)] = true
			order = append(order, string(cand))
			dsC := &Dataset{Spec: cloneSpecWithKind(ds.Spec, bi, cand), Store: ds.Store, Obs: ds.Obs}
			res, err := Estimate(dsC, base, theta0, opts.Estimate)
			if err != nil {
				return nil, fmt.Errorf("estimate: 结构候选 %s/%s 拟合失败: %w", b.ID, cand, err)
			}
			k := len(res.FreeIDs)
			ch.NParams = k
			ch.Scores[string(cand)] = res.LogLikelihood - 0.5*float64(k)*math.Log(float64(n))
		}
		softmaxInto(order, ch.Scores, ch.Posterior)
		choices = append(choices, ch)
	}
	return choices, nil
}

// StructEvents 把选择结果转成 StructureUpdateEvent 载荷（Intent §3.3：
// 结构判定也进事件流）。不落库——事件写入是调用方的职责，与本包
// 「纯估计、不碰存储」的分层一致（与 ParamUpdates 同约定）。
// PosteriorBefore 取自 spec 现有 KindPosterior（nil = 首次判定）。
func StructEvents(spec *qdl.PlanSpec, choices []StructureChoice) ([]*ledger.StructureUpdateEvent, error) {
	if spec == nil {
		return nil, fmt.Errorf("estimate: StructEvents(nil spec)")
	}
	events := make([]*ledger.StructureUpdateEvent, 0, len(choices))
	for _, ch := range choices {
		var before map[string]float64
		for i := range spec.Buckets {
			if spec.Buckets[i].ID != ch.BucketID {
				continue
			}
			kp := spec.Buckets[i].Window.KindPosterior
			if len(kp) > 0 {
				before = map[string]float64{}
				for k, v := range kp {
					before[string(k)] = v
				}
			}
		}
		ev := &ledger.StructureUpdateEvent{
			PlanID: spec.ID, BucketID: ch.BucketID, Field: ch.Field,
			PosteriorBefore: before, PosteriorAfter: ch.Posterior,
		}
		if err := ev.Validate(); err != nil {
			return nil, fmt.Errorf("estimate: 结构事件 %s/%s: %w", ch.BucketID, ch.Field, err)
		}
		events = append(events, ev)
	}
	return events, nil
}

// cloneSpecWithKind 浅拷贝 spec 并把第 bucketIdx 桶的窗口钉到单候选。
// 桶切片必须复制一层：Window 是桶结构体的值字段，原地改会污染共享
// 底层数组（原 spec 的候选列表会被静默改写）。
func cloneSpecWithKind(spec *qdl.PlanSpec, bucketIdx int, kind qdl.WindowKind) *qdl.PlanSpec {
	cp := *spec
	cp.Buckets = append([]qdl.Bucket(nil), spec.Buckets...)
	cp.Buckets[bucketIdx].Window.KindPosterior = map[qdl.WindowKind]float64{kind: 1.0}
	return &cp
}

// softmaxInto 把 scores（log 空间）按 order 顺序归一成概率写入 out。
// log-sum-exp 数值稳定；全 -Inf（全部候选的似然都退化）时退化为均匀
// 分布——「无证据区分」而非报错，调用方看 Posterior 的平坦度即知。
func softmaxInto(order []string, scores, out map[string]float64) {
	m := math.Inf(-1)
	for _, k := range order {
		if scores[k] > m {
			m = scores[k]
		}
	}
	sum := 0.0
	for _, k := range order {
		var e float64
		if math.IsInf(m, -1) {
			e = 1 // 全 -Inf：均匀
		} else {
			e = math.Exp(scores[k] - m)
		}
		out[k] = e
		sum += e
	}
	for _, k := range order {
		out[k] /= sum
	}
}
