package ledger

import (
	"fmt"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/semantics"
)

// ReplayMode 是重放的两种口径（Intent §3.3：用新 θ 重放旧请求流）：
//
//	AsRecorded 事件里存的 bucket_deltas 原样入账（历史真相口径）
//	Recompute  用给定 θ 对事件的原始 dims 重新计算扣减（当前模型口径）
//
// Recompute 是参数辨识迭代收敛、反事实分析、漂移检测的入口；AsRecorded
// 用于核对「当时到底怎么扣的」。两者的 θ 都必须完整——AsRecorded 也需要
// θ 解析容量/回补率等窗口几何参数。
type ReplayMode int

const (
	ReplayAsRecorded ReplayMode = iota
	ReplayRecompute
)

// ReplayOptions 是重放的全部输入。
type ReplayOptions struct {
	Theta qdl.ParamPoint // 参数快照（两种模式都必须完整）
	Mode  ReplayMode
}

// BucketStat 是重放过程中单桶的累计统计。
type BucketStat struct {
	Charges    int     // 命中该桶的 charge 事件数
	TotalDelta float64 // 累计扣减（重放口径：AsRecorded 用存量、Recompute 用新算）
	WallHits   int     // 撞墙次数
	Resets     int     // 观测到的重置次数
}

// ReplayResult 是一次全量重放的产物。
type ReplayResult struct {
	State  semantics.SystemState // 终态（每桶 U/Anchor/Ledger）
	Stats  map[string]BucketStat
	Events int // 处理的事件总数（含跳过的非本 plan 事件）
}

// Replayer 把事件流增量重放到状态：Advance 到事件时刻 → 应用事件效果。
// 事件按序号升序喂入（Iterate 保证）；时间倒流是显式错误。
// Observation/ResetObserved 不改账本状态（观测是证据不是账）——账实不符
// 正是 reconcile 要检测的信号，重放绝不能悄悄抹平它。
type Replayer struct {
	spec     *qdl.PlanSpec
	opts     ReplayOptions
	resolved map[string]semantics.ResolvedBucket // 桶几何（按 theta 求值一次）
	state    semantics.SystemState
	stats    map[string]*BucketStat
	lastT    time.Time
	started  bool
}

// NewReplayer 构造重放器并求值全部桶几何。
func NewReplayer(spec *qdl.PlanSpec, opts ReplayOptions) (*Replayer, error) {
	if spec == nil {
		return nil, fmt.Errorf("ledger: NewReplayer(nil spec)")
	}
	r := &Replayer{
		spec:     spec,
		opts:     opts,
		resolved: map[string]semantics.ResolvedBucket{},
		state:    semantics.SystemState{Buckets: map[string]semantics.BucketState{}},
		stats:    map[string]*BucketStat{},
	}
	for i := range spec.Buckets {
		b := &spec.Buckets[i]
		rb, err := semantics.ResolveBucket(b, opts.Theta)
		if err != nil {
			return nil, fmt.Errorf("ledger: 重放器求值桶 %q: %w", b.ID, err)
		}
		r.resolved[b.ID] = rb
		r.state.Buckets[b.ID] = semantics.BucketState{}
		r.stats[b.ID] = &BucketStat{}
	}
	return r, nil
}

// Apply 处理单个事件：非本 plan 的事件跳过；时间倒流报错。
func (r *Replayer) Apply(ev Event) error {
	switch p := ev.Payload().(type) {
	case *ChargeEvent:
		if p.PlanID != r.spec.ID {
			return nil
		}
		if err := r.advanceTo(ev.Ts); err != nil {
			return err
		}
		deltas, err := r.deltasFor(p)
		if err != nil {
			return err
		}
		for bID, du := range deltas {
			st, ok := r.state.Buckets[bID]
			if !ok {
				return fmt.Errorf("ledger: 事件 seq=%d 扣减未知桶 %q（spec %s）", ev.Seq, bID, r.spec.ID)
			}
			rb := r.resolved[bID]
			if rb.Kind == qdl.WindowTumblingAnchoredOnFirstUse && st.Anchor == nil {
				t := ev.Ts // 起锚：第一次消耗落在窗内才开窗
				st.Anchor = &t
			}
			st.U += du
			if rb.Kind == qdl.WindowSlidingExact {
				st.Ledger = append(st.Ledger, semantics.Delta{T: ev.Ts, DU: du})
			}
			r.state.Buckets[bID] = st
			s := r.stats[bID]
			s.Charges++
			s.TotalDelta += du
		}
	case *ObservationEvent:
		if p.PlanID != r.spec.ID {
			return nil
		}
		return r.advanceTo(ev.Ts) // 观测不动账本，只推进时间
	case *WallHitEvent:
		if p.PlanID != r.spec.ID {
			return nil
		}
		if err := r.advanceTo(ev.Ts); err != nil {
			return err
		}
		if s, ok := r.stats[p.BucketID]; ok {
			s.WallHits++
		}
	case *ResetObservedEvent:
		if p.PlanID != r.spec.ID {
			return nil
		}
		if err := r.advanceTo(ev.Ts); err != nil {
			return err
		}
		if s, ok := r.stats[p.BucketID]; ok {
			s.Resets++
		}
	}
	return nil
}

// State 返回当前状态快照（值拷贝；Ledger 切片共享底层数组，调用方勿改）。
func (r *Replayer) State() semantics.SystemState { return r.state }

// Stats 返回累计统计的拷贝。
func (r *Replayer) Stats() map[string]BucketStat {
	out := make(map[string]BucketStat, len(r.stats))
	for k, v := range r.stats {
		out[k] = *v
	}
	return out
}

// BucketStateAt 返回桶当前状态（重放推进到已喂入的最后一个事件时刻）。
func (r *Replayer) BucketStateAt(bID string) (semantics.BucketState, bool) {
	st, ok := r.state.Buckets[bID]
	return st, ok
}

// BucketStatAt 返回桶累计统计的拷贝。
func (r *Replayer) BucketStatAt(bID string) (BucketStat, bool) {
	s, ok := r.stats[bID]
	if !ok {
		return BucketStat{}, false
	}
	return *s, true
}

// advanceTo 把全部桶推进到 t（纯函数逐桶替换）。
func (r *Replayer) advanceTo(t time.Time) error {
	if !r.started {
		r.lastT, r.started = t, true
		return nil
	}
	if t.Before(r.lastT) {
		return fmt.Errorf("ledger: 事件时间倒流 %s → %s（事件必须按时间升序喂入）", r.lastT, t)
	}
	for bID, st := range r.state.Buckets {
		rb := r.resolved[bID]
		next, err := semantics.Advance(st, &rb, r.lastT, t)
		if err != nil {
			return fmt.Errorf("ledger: 桶 %q 推进到 %s: %w", bID, t, err)
		}
		r.state.Buckets[bID] = next
	}
	r.lastT = t
	return nil
}

// deltasFor 取事件的扣减：AsRecorded 用存量，Recompute 用 θ 重算。
func (r *Replayer) deltasFor(e *ChargeEvent) (map[string]float64, error) {
	if r.opts.Mode == ReplayAsRecorded {
		if len(e.BucketDeltas) == 0 {
			return nil, fmt.Errorf(
				"ledger: charge seq 缺 bucket_deltas（AsRecorded 模式必须有存量扣减）")
		}
		return e.BucketDeltas, nil
	}
	req := &semantics.Request{
		ChannelID: e.ChannelID,
		Model:     e.Model,
		Effort:    e.Effort,
		Dims:      e.Dims,
	}
	deltas, err := semantics.Charge(r.spec, req, r.opts.Theta, semantics.ChargeModeExact)
	if err != nil {
		return nil, fmt.Errorf("ledger: 重算请求 %s 扣减: %w", e.RequestID, err)
	}
	return deltas, nil
}

// Replay 全量重放事件库：spec 的桶状态从零重建。
func Replay(spec *qdl.PlanSpec, store Store, opts ReplayOptions) (*ReplayResult, error) {
	r, err := NewReplayer(spec, opts)
	if err != nil {
		return nil, err
	}
	res := &ReplayResult{State: semantics.SystemState{}, Stats: map[string]BucketStat{}}
	err = store.Iterate(func(ev Event) error {
		res.Events++
		return r.Apply(ev)
	})
	if err != nil {
		return nil, fmt.Errorf("ledger: 重放中断于第 %d 个事件: %w", res.Events, err)
	}
	res.State = r.State()
	res.Stats = r.Stats()
	return res, nil
}
