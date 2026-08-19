package probe

import (
	"fmt"
	"sort"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/ledger"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// Status 是 dry-run 对单个剧本的数据充分性结论。
type Status string

const (
	StatusReady        Status = "ready"        // 证据齐全，判别式可判定（C3 接入）
	StatusInsufficient Status = "insufficient" // 证据不足：缺哪个语义/多少样本/多少跨度，报告里明说
)

// NeedReport 是单项证据需求的满足情况。
type NeedReport struct {
	Semantic  qdl.Semantic
	Have      int           // 实有样本数
	Want      int           // 需求下限
	SpanHave  time.Duration // 实有首末样本时间跨度
	SpanWant  time.Duration // 需求跨度下限
	Buckets   []string      // 命中样本的桶（去重排序）
	Satisfied bool
}

// EvidenceSample 是提取出的一条证据（保真原始串，语义解释归判别式）。
type EvidenceSample struct {
	Ts       time.Time
	BucketID string
	RawValue string
}

// EvidenceSeries 是按语义归组、按时间升序的证据序列——判别式（C3）的
// 统一输入面。同一个剧本可声明多种语义需求（如 used_pct + reset_at_iso）。
type EvidenceSeries struct {
	Semantic qdl.Semantic
	Samples  []EvidenceSample
}

// DryRunResult 是剧本在事件库上的 dry-run 评估：证据提取 + 充分性判定。
// 不做结构结论——结论是判别式（C3）的职责；dry-run 回答「数据够不够、
// 缺什么」（ROADMAP C2：runner 先 dry-run 回放模式，不自动发真实请求）。
type DryRunResult struct {
	PlaybookID string
	PlanID     string
	Status     Status
	Needs      []NeedReport
	Series     map[qdl.Semantic]*EvidenceSeries
}

// DryRun 在事件库上回放剧本的证据需求：收集命中桶（glob）+ 语义匹配的
// 观测，评估每项 needs 的样本数与时间跨度。planID 过滤事件流（多 plan
// 共库时剧本只看自己的 plan）。
func DryRun(store ledger.Store, pb *Playbook, planID string) (*DryRunResult, error) {
	if store == nil || pb == nil {
		return nil, fmt.Errorf("probe: DryRun(nil)")
	}
	if planID == "" {
		return nil, fmt.Errorf("probe: DryRun 缺 planID（多 plan 共库时无法定位证据）")
	}
	res := &DryRunResult{
		PlaybookID: pb.ID, PlanID: planID,
		Status: StatusInsufficient,
		Series: map[qdl.Semantic]*EvidenceSeries{},
	}
	for _, n := range pb.Needs {
		res.Series[n.Semantic] = &EvidenceSeries{Semantic: n.Semantic}
	}
	buckets := map[string]bool{}
	err := store.Iterate(func(ev ledger.Event) error {
		obs, ok := ev.Payload().(*ledger.ObservationEvent)
		if !ok || obs.PlanID != planID {
			return nil
		}
		series, ok := res.Series[obs.Semantic]
		if !ok || !pb.MatchBucket(obs.BucketID) {
			return nil
		}
		series.Samples = append(series.Samples, EvidenceSample{
			Ts: ev.Ts, BucketID: obs.BucketID, RawValue: obs.RawValue,
		})
		buckets[obs.BucketID] = true
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("probe: DryRun 遍历事件流中断: %w", err)
	}
	allOK := true
	for _, n := range pb.Needs {
		rep := NeedReport{Semantic: n.Semantic, Want: n.MinCount, SpanWant: n.MinSpan}
		s := res.Series[n.Semantic]
		rep.Have = len(s.Samples)
		if rep.Have > 0 {
			rep.SpanHave = s.Samples[rep.Have-1].Ts.Sub(s.Samples[0].Ts)
		}
		seen := map[string]bool{}
		for _, smp := range s.Samples {
			if !seen[smp.BucketID] {
				seen[smp.BucketID] = true
				rep.Buckets = append(rep.Buckets, smp.BucketID)
			}
		}
		sort.Strings(rep.Buckets)
		rep.Satisfied = rep.Have >= n.MinCount && rep.SpanHave >= n.MinSpan
		if !rep.Satisfied {
			allOK = false
		}
		res.Needs = append(res.Needs, rep)
	}
	if allOK {
		res.Status = StatusReady
	}
	return res, nil
}

// Missing 是人类可读的缺口描述（采集调度器的行动输入：去哪个通道、
// 还差多少样本/跨度）。
func (r *DryRunResult) Missing() []string {
	var out []string
	for _, n := range r.Needs {
		if n.Satisfied {
			continue
		}
		s := fmt.Sprintf("semantic %s: 样本 %d/%d", n.Semantic, n.Have, n.Want)
		if n.SpanHave < n.SpanWant {
			s += fmt.Sprintf("，跨度 %s/%s", n.SpanHave, n.SpanWant)
		}
		out = append(out, s)
	}
	return out
}
