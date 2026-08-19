package ledger

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// AttributionKind 是残差归因的封闭分类（Intent §3.4 归因表的工程化）。
// 诊断中枢：比任何 dashboard 都有用——它回答「预测和观测为什么对不上」。
type AttributionKind string

const (
	AttributionQuantNoise    AttributionKind = "quantization_noise"  // 零均值、幅度 ≈ 量化步长/2：正常
	AttributionExogenous     AttributionKind = "exogenous_drain"     // 持续正偏：外生消耗 / θ 低估 / 未建模维
	AttributionDrift         AttributionKind = "coefficient_drift"   // 阶跃变点：厂商改了系数 → CUSUM 告警
	AttributionUnmodeledFlat AttributionKind = "unmodeled_flat"      // 残差随请求数增长：未建模 flat/floor/quantize
	AttributionStructure     AttributionKind = "structure_misjudged" // 观测重置但账本未重置：窗口结构判断错误
	AttributionNegativeBias  AttributionKind = "negative_bias"       // 持续负偏：θ 高估（候选证据）
	AttributionInsufficient  AttributionKind = "insufficient_data"   // 观测不足，无法归因
	AttributionUnexplained   AttributionKind = "unexplained"         // 以上都不是：留给人工
)

// ResidualPoint 是一次对账的残差样本：观测百分比 vs 账本预测百分比。
type ResidualPoint struct {
	Seq              int64
	T                time.Time
	ObservedPct      float64
	PredictedPct     float64
	Residual         float64 // observed - predicted
	ChargesSincePrev int     // 距上一条观测之间的 charge 事件数（flat 检测用）
}

// BucketReport 是单桶的对账报告。
type BucketReport struct {
	BucketID       string
	N              int     // 有效 pct 观测数
	Step           float64 // 量化步长（pct 点；0 = exact）
	MeanResidual   float64
	MaxAbsResidual float64
	Attribution    AttributionKind
	Evidence       string          // 人类可读的归因证据
	Residuals      []ResidualPoint // 诊断明细
	ResetMismatch  int             // 观测重置但账本未重置的次数（结构错判证据）
	WallHits       int
	ParseSkipped   int // 无法解析的观测数
}

// Report 是整份对账报告（每桶一条）。
type Report struct {
	PlanID      string
	Buckets     []*BucketReport
	GeneratedAt time.Time
}

// Reconcile 跑一次完整对账（Intent §3.4）：用当前 θ 重放事件流（Recompute
// 口径——诊断的是「当前模型」与观测的差距），对每条 pct 观测计算残差并归因。
// 返回的 Report 是诊断中枢的输入，不是告警本身（CUSUM 告警在 estimate/drift）。
func Reconcile(spec *qdl.PlanSpec, store Store, theta qdl.ParamPoint) (*Report, error) {
	r, err := NewReplayer(spec, ReplayOptions{Theta: theta, Mode: ReplayRecompute})
	if err != nil {
		return nil, err
	}
	rep := &Report{PlanID: spec.ID, GeneratedAt: time.Now().UTC()}

	// 桶容量（预测分母）与逐桶累计状态
	capacity := map[string]float64{}
	reports := map[string]*BucketReport{}
	lastCharges := map[string]int{}
	for i := range spec.Buckets {
		b := &spec.Buckets[i]
		c, err := b.Capacity.Resolve(theta)
		if err != nil {
			return nil, fmt.Errorf("ledger: 对账解析桶 %q 容量: %w", b.ID, err)
		}
		if c <= 0 {
			return nil, fmt.Errorf("ledger: 桶 %q 容量 %v 非正——对账需要有效 C_hat", b.ID, c)
		}
		capacity[b.ID] = c
		reports[b.ID] = &BucketReport{BucketID: b.ID}
	}

	err = store.Iterate(func(ev Event) error {
		switch p := ev.Payload().(type) {
		case *ObservationEvent:
			if p.PlanID != spec.ID {
				return nil
			}
			if err := r.Apply(ev); err != nil {
				return err
			}
			br := reports[p.BucketID]
			if br == nil {
				return nil // 观测指向未知桶：不属本 spec 的诊断面
			}
			obs, ok := observedPct(p)
			if !ok {
				br.ParseSkipped++
				return nil
			}
			st, _ := r.BucketStateAt(p.BucketID)
			pred := 100 * st.U / capacity[p.BucketID]
			if br.N == 0 && p.Quantization.Kind != "" {
				br.Step = quantStepOf(p.Quantization)
			}
			s, _ := r.BucketStatAt(p.BucketID)
			br.Residuals = append(br.Residuals, ResidualPoint{
				Seq: ev.Seq, T: ev.Ts, ObservedPct: obs, PredictedPct: pred,
				Residual: obs - pred, ChargesSincePrev: s.Charges - lastCharges[p.BucketID],
			})
			lastCharges[p.BucketID] = s.Charges
			br.N++
		case *ResetObservedEvent:
			if p.PlanID != spec.ID {
				return nil
			}
			if err := r.Apply(ev); err != nil {
				return err
			}
			br := reports[p.BucketID]
			if br == nil {
				return nil
			}
			// 结构错判证据：厂商观测到重置（NewU≈0），但我们的账本还有可观存量
			// ——窗口语义判断错误（Intent §3.4 归因表最后一行）。
			st, _ := r.BucketStateAt(p.BucketID)
			if st.U > 0.05*capacity[p.BucketID] && p.NewU < 0.05*capacity[p.BucketID] {
				br.ResetMismatch++
			}
		default:
			return r.Apply(ev)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ledger: 对账中断: %w", err)
	}

	ids := make([]string, 0, len(reports))
	for id := range reports {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		br := reports[id]
		if s, ok := r.BucketStatAt(id); ok {
			br.WallHits = s.WallHits
		}
		classify(br)
		if br.N == 0 && br.ResetMismatch == 0 && br.WallHits == 0 && br.ParseSkipped == 0 {
			continue // 该桶无任何对账面：不进报告
		}
		rep.Buckets = append(rep.Buckets, br)
	}
	return rep, nil
}

// observedPct 按语义把原始观测值解析为已用百分比。只处理百分比语义；
// 绝对量语义（used_abs 等）需要 limit 配对，属 collect 层职责，此处跳过。
func observedPct(o *ObservationEvent) (float64, bool) {
	v, err := strconv.ParseFloat(o.RawValue, 64)
	if err != nil {
		return 0, false
	}
	switch o.Semantic {
	case qdl.SemUsedPct:
		return v, true
	case qdl.SemRemainingPct:
		return 100 - v, true
	default:
		return 0, false
	}
}

// quantStepOf 把观测量化描述折算为 pct 步长（似然模型的 s）。
func quantStepOf(q qdl.Quantization) float64 {
	switch q.Kind {
	case "integer":
		return 1.0
	case "decimals":
		if q.Decimals != nil && *q.Decimals > 0 {
			return math.Pow(10, float64(-*q.Decimals))
		}
		return 1.0
	default: // exact / unknown：无量化借口，只留数值余量
		return 0.0
	}
}

// classify 对单桶报告做残差归因（Intent §3.4 表的确定性规则，阈值保守：
// 宁可 unexplained 交人工，不可把噪声说成故障）。
func classify(br *BucketReport) {
	if br.ResetMismatch > 0 {
		br.Attribution = AttributionStructure
		br.Evidence = fmt.Sprintf("观测重置但账本未重置 %d 次——窗口结构判断错误，触发结构重选", br.ResetMismatch)
		return
	}
	if br.N < 3 {
		br.Attribution = AttributionInsufficient
		br.Evidence = fmt.Sprintf("仅 %d 条有效观测（另跳过 %d 条无法解析）", br.N, br.ParseSkipped)
		return
	}
	rs := br.Residuals
	sum, maxAbs := 0.0, 0.0
	for _, p := range rs {
		sum += p.Residual
		if a := math.Abs(p.Residual); a > maxAbs {
			maxAbs = a
		}
	}
	mean := sum / float64(len(rs))
	br.MeanResidual, br.MaxAbsResidual = mean, maxAbs
	tol := math.Max(br.Step/2, 0.5) // 半步长 + 数值余量

	// ① 零均值且幅度在量化容差内 → 正常量化噪声
	if math.Abs(mean) <= tol/2 && maxAbs <= tol {
		br.Attribution = AttributionQuantNoise
		br.Evidence = fmt.Sprintf("残差零均值（%+.2f）且最大幅度 %.2f ≤ 容差 %.2f（半步长）——量化噪声", mean, maxAbs, tol)
		return
	}
	// ② 阶跃变点：前后段均值差远超容差 → 厂商改系数
	if k, before, after := bestChangepoint(rs); k >= 0 && math.Abs(after-before) > 3*tol {
		br.Attribution = AttributionDrift
		br.Evidence = fmt.Sprintf("变点 %s：前段均值 %+.2f → 后段 %+.2f（差 %.2f > 3×容差）——厂商改了系数，触发 CUSUM 告警与局部重估",
			rs[k].T.Format(time.RFC3339), before, after, math.Abs(after-before))
		return
	}
	// ③ 残差与请求数强相关且正偏 → 未建模 flat/floor/quantize（小请求偏大）
	if corr, slope := corrWithCharges(rs); corr >= 0.7 && mean > tol && slope > 0 {
		br.Attribution = AttributionUnmodeledFlat
		br.Evidence = fmt.Sprintf("残差与区间请求数相关 r=%.2f、每请求斜率 %+.3f——存在未建模的 flat/floor/quantize", corr, slope)
		return
	}
	// ④ 持续偏置：正 = 外生消耗或 θ 低估；负 = θ 高估候选
	if mean > tol {
		br.Attribution = AttributionExogenous
		br.Evidence = fmt.Sprintf("持续正偏 %+.2f（容差 %.2f）——exogenous_drain（手动使用）或 θ 低估或未建模桶维", mean, tol)
		return
	}
	if mean < -tol {
		br.Attribution = AttributionNegativeBias
		br.Evidence = fmt.Sprintf("持续负偏 %+.2f（容差 %.2f）——θ 高估候选（容量/权重偏大）", mean, tol)
		return
	}
	br.Attribution = AttributionUnexplained
	br.Evidence = fmt.Sprintf("均值 %+.2f 在容差内但最大幅度 %.2f 超容差 %.2f——模式不明，交人工", mean, maxAbs, tol)
}

// bestChangepoint 找前后段均值差最大的分割点（两侧各 ≥3 样本）。
// 返回 (分割下标 k：rs[k] 起属后段, 前段均值, 后段均值)；无有效分割返回 (-1,0,0)。
func bestChangepoint(rs []ResidualPoint) (int, float64, float64) {
	n := len(rs)
	bestK, bestBefore, bestAfter := -1, 0.0, 0.0
	bestDiff := 0.0
	for k := 3; k <= n-3; k++ {
		before, after := 0.0, 0.0
		for i := 0; i < k; i++ {
			before += rs[i].Residual
		}
		for i := k; i < n; i++ {
			after += rs[i].Residual
		}
		before, after = before/float64(k), after/float64(n-k)
		if d := math.Abs(after - before); d > bestDiff {
			bestDiff, bestK, bestBefore, bestAfter = d, k, before, after
		}
	}
	return bestK, bestBefore, bestAfter
}

// corrWithCharges 计算残差与区间请求数的 Pearson 相关与最小二乘斜率。
// 请求数无方差（全部相同）时相关无定义，返回 (0,0)。
func corrWithCharges(rs []ResidualPoint) (float64, float64) {
	n := float64(len(rs))
	if len(rs) < 3 {
		return 0, 0
	}
	var sx, sy, sxx, syy, sxy float64
	for _, p := range rs {
		sx += float64(p.ChargesSincePrev)
		sy += p.Residual
		sxx += float64(p.ChargesSincePrev) * float64(p.ChargesSincePrev)
		syy += p.Residual * p.Residual
		sxy += float64(p.ChargesSincePrev) * p.Residual
	}
	cov := sxy - sx*sy/n
	varX := sxx - sx*sx/n
	varY := syy - sy*sy/n
	if varX <= 1e-12 || varY <= 1e-12 {
		return 0, 0
	}
	r := cov / math.Sqrt(varX*varY)
	slope := cov / varX
	return r, slope
}
