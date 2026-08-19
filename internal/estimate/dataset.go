package estimate

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/ledger"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/semantics"
)

// ObsKind 是观测点的封闭分类（Intent §4.1 三种似然面）。
type ObsKind string

const (
	ObsPct   ObsKind = "pct"   // 量化百分比观测（不透明桶主战场）
	ObsExact ObsKind = "exact" // 精确计数观测（免费档 RPM、credits）
	ObsWall  ObsKind = "wall"  // 撞墙观测（信息量最大：夹逼 C）
)

// ObsPoint 是一个可拟合的观测点：预测值 μ(θ) 由 Dataset.Produce 按重放
// 口径计算，观测值 y 与精度（步长 s、噪声 σ）在此静态描述。两者在
// logPosterior 里配对。
type ObsPoint struct {
	BucketID string
	Kind     ObsKind
	Y        float64 // pct 语义为已用百分比；exact 语义为绝对已用量；wall 忽略
	Step     float64 // 量化步长（QuantizedLogProb 的 s；0 = 无量化）
	Sigma    float64 // 观测噪声（由 trust 折算）
	Hit      bool    // wall 语义：是否撞墙
	Eps      float64 // wall 语义：误报率
	Seq      int64   // 证据事件 seq（ParamUpdateEvent.EvidenceIDs 用）
	T        time.Time
}

// ExtractOptions 控制观测噪声的折算。噪声模型：σ = base / trust——
// trust=1 只剩基础噪声（attribution lag 错位），低信任通道按比例放大。
type ExtractOptions struct {
	BaseSigmaPct float64 // pct 观测基础噪声（默认 0.5 个百分点）
	BaseSigmaAbs float64 // exact 观测基础噪声（默认 0.25 计数单位）
	WallEps      float64 // 撞墙误报率（默认 0.05；429 可能来自并发等其他约束）
}

func (o ExtractOptions) withDefaults() ExtractOptions {
	if o.BaseSigmaPct <= 0 {
		o.BaseSigmaPct = 0.5
	}
	if o.BaseSigmaAbs <= 0 {
		o.BaseSigmaAbs = 0.25
	}
	if o.WallEps <= 0 || o.WallEps >= 1 {
		o.WallEps = 0.05
	}
	return o
}

// Dataset 是参数辨识的原料：spec + 观测点序列 + 事件存储引用。
// 预测面（θ → μ_j）不是静态字段——每次求值都要重放事件流（Recompute +
// LinearEV 口径），这正是 Intent「用新 θ 重放旧请求流」的辨识循环本体。
type Dataset struct {
	Spec  *qdl.PlanSpec
	Store ledger.Store
	Obs   []ObsPoint
}

// ExtractDataset 遍历事件流，把百分比/精确计数观测与撞墙事件收进观测集。
// used_pct/remaining_pct 进 ObsPct；used_abs 进 ObsExact；wall_hit 进 ObsWall。
// remaining_abs / reset 类语义不进（需要 limit 配对或属结构证据，归 collect/结构层）。
func ExtractDataset(spec *qdl.PlanSpec, store ledger.Store, opts ExtractOptions) (*Dataset, error) {
	if spec == nil || store == nil {
		return nil, fmt.Errorf("estimate: ExtractDataset(nil)")
	}
	opts = opts.withDefaults()
	ds := &Dataset{Spec: spec, Store: store}
	err := store.Iterate(func(ev ledger.Event) error {
		switch p := ev.Payload().(type) {
		case *ledger.ObservationEvent:
			if p.PlanID != spec.ID {
				return nil
			}
			v, err := strconv.ParseFloat(p.RawValue, 64)
			if err != nil {
				return nil // 保真存储的原始串解析不了：跳过（reconcile 已计数）
			}
			trust := p.Trust
			if trust <= 0 {
				trust = 0.05
			}
			switch p.Semantic {
			case qdl.SemUsedPct, qdl.SemRemainingPct:
				y := v
				if p.Semantic == qdl.SemRemainingPct {
					y = 100 - v
				}
				ds.Obs = append(ds.Obs, ObsPoint{
					BucketID: p.BucketID, Kind: ObsPct, Y: y,
					Step: quantStep(p.Quantization), Sigma: opts.BaseSigmaPct / trust,
					Seq: ev.Seq, T: ev.Ts,
				})
			case qdl.SemUsedAbs:
				ds.Obs = append(ds.Obs, ObsPoint{
					BucketID: p.BucketID, Kind: ObsExact, Y: v,
					Sigma: opts.BaseSigmaAbs / trust,
					Seq:   ev.Seq, T: ev.Ts,
				})
			}
		case *ledger.WallHitEvent:
			if p.PlanID != spec.ID {
				return nil
			}
			ds.Obs = append(ds.Obs, ObsPoint{
				BucketID: p.BucketID, Kind: ObsWall, Hit: true, Eps: opts.WallEps,
				Seq: ev.Seq, T: ev.Ts,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("estimate: 提取观测集中断: %w", err)
	}
	return ds, nil
}

// Predict 在给定 θ 下产出与 Obs 一一对应的预测值序列（Recompute + LinearEV
// 重放）。pct / wall 观测 → 100·U/C；exact 观测 → U。墙的 μ 是撞墙时刻的
// 累计预测（墙请求不扣减——这正是夹逼 C 的下界方程）。
func (ds *Dataset) Predict(theta qdl.ParamPoint) ([]float64, error) {
	r, err := ledger.NewReplayer(ds.Spec, ledger.ReplayOptions{
		Theta: theta, Mode: ledger.ReplayRecompute, ChargeMode: semantics.ChargeModeLinearEV,
	})
	if err != nil {
		return nil, err
	}
	capacity := map[string]float64{}
	for i := range ds.Spec.Buckets {
		b := &ds.Spec.Buckets[i]
		c, err := b.Capacity.Resolve(theta)
		if err != nil {
			return nil, fmt.Errorf("estimate: 解析桶 %q 容量: %w", b.ID, err)
		}
		if c <= 0 {
			return nil, fmt.Errorf("estimate: 桶 %q 容量 %v 非正", b.ID, c)
		}
		capacity[b.ID] = c
	}
	mus := make([]float64, len(ds.Obs))
	idxBySeq := make(map[int64]int, len(ds.Obs))
	for i, o := range ds.Obs {
		idxBySeq[o.Seq] = i
	}
	err = ds.Store.Iterate(func(ev ledger.Event) error {
		if err := r.Apply(ev); err != nil {
			return err
		}
		i, ok := idxBySeq[ev.Seq]
		if !ok {
			return nil
		}
		o := ds.Obs[i]
		st, ok := r.BucketStateAt(o.BucketID)
		if !ok {
			return fmt.Errorf("estimate: 观测指向 spec 未知桶 %q", o.BucketID)
		}
		switch o.Kind {
		case ObsPct, ObsWall:
			mus[i] = 100 * st.U / capacity[o.BucketID]
		case ObsExact:
			mus[i] = st.U
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("estimate: 预测重放中断: %w", err)
	}
	return mus, nil
}

// quantStep 把观测量化描述折算为步长（与 ledger.quantStepOf 同规则；
// estimate 不依赖 ledger 内部符号，故独立实现）。
func quantStep(q qdl.Quantization) float64 {
	switch q.Kind {
	case "integer":
		return 1.0
	case "decimals":
		if q.Decimals != nil && *q.Decimals > 0 {
			step := 1.0
			for i := 0; i < *q.Decimals; i++ {
				step /= 10
			}
			return step
		}
		return 1.0
	default: // exact / unknown：无量化
		return 0.0
	}
}
