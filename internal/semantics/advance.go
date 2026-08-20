package semantics

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// ResolvedBucket 是 advance 所需的全部 spec 求值结果：把 Coeff 双态系数、
// AnchorUTC 字符串等「待估/待解析」信息一次性折算成纯几何参数。之后
// advance 不再依赖 qdl 类型——时间几何与参数估计彻底解耦。
type ResolvedBucket struct {
	ID     string
	Kind   qdl.WindowKind
	Length time.Duration // 窗长；token_bucket/never 可为零

	// Anchor0 是周期锚定类（tumbling_account_anchored / tumbling_calendar /
	// billing_cycle）的锚点序列起点：重置时刻 = Anchor0 + n·Length (n≥0)。
	// HasAnchor=false 表示锚未知（如 anchor_utc: "UNKNOWN"），此类桶无法
	// 推进周期归零，Advance 返回显式错误（待 estimate 从 resets_at 序列
	// 反推后写回 spec）。
	Anchor0   time.Time
	HasAnchor bool

	ExpiresAt  *time.Time // one_shot_expiring 的绝对过期时刻
	RefillRate float64    // token_bucket_continuous：单位/秒
	Capacity   float64    // one_shot 置满值 / rollover 结转基数

	Reset               qdl.ResetPolicy
	RolloverCapMultiple float64 // rollover_capped 的 k
}

// ResolveBucket 把 qdl.Bucket + 参数快照 θ 折算成 ResolvedBucket。
// 一切 ParamRef 必须能在 theta 中解析；锚点未知是显式错误（不是静默缺省）。
func ResolveBucket(b *qdl.Bucket, theta qdl.ParamPoint) (ResolvedBucket, error) {
	if b == nil {
		return ResolvedBucket{}, fmt.Errorf("semantics: ResolveBucket(nil)")
	}
	rb := ResolvedBucket{
		ID:                  b.ID,
		Kind:                b.Window.Kind(),
		Length:              b.Window.Length.Duration,
		Reset:               b.Window.Reset,
		RolloverCapMultiple: 1,
	}
	var err error
	if rb.RefillRate, err = b.Window.RefillRate.Resolve(theta); err != nil {
		return ResolvedBucket{}, fmt.Errorf("semantics: 桶 %q refill_rate: %w", b.ID, err)
	}
	if rb.Capacity, err = b.Capacity.Resolve(theta); err != nil {
		return ResolvedBucket{}, fmt.Errorf("semantics: 桶 %q capacity: %w", b.ID, err)
	}
	if b.Window.RolloverCapMultiple != nil {
		rb.RolloverCapMultiple = *b.Window.RolloverCapMultiple
	}
	rb.ExpiresAt = b.Window.ExpiresAt

	switch rb.Kind {
	case qdl.WindowTumblingAccountAnchored, qdl.WindowTumblingCalendar, qdl.WindowBillingCycle:
		if rb.Length <= 0 {
			return ResolvedBucket{}, fmt.Errorf(
				"semantics: 桶 %q 周期锚定窗型 %q 的 length=%v 必须为正", b.ID, rb.Kind, rb.Length)
		}
	case qdl.WindowSlidingExact:
		if rb.Length <= 0 {
			return ResolvedBucket{}, fmt.Errorf(
				"semantics: 桶 %q sliding_exact 的 length=%v 必须为正", b.ID, rb.Length)
		}
	}
	switch rb.Kind {
	case qdl.WindowTumblingAccountAnchored, qdl.WindowBillingCycle:
		anchor, err := parseAnchorUTC(b.Window.AnchorUTC)
		if err != nil {
			return ResolvedBucket{}, fmt.Errorf("semantics: 桶 %q: %w", b.ID, err)
		}
		rb.Anchor0, rb.HasAnchor = anchor, true
	case qdl.WindowTumblingCalendar:
		// 日历窗锚定到 UTC 零点（calendar_align 空或 utc_midnight）；
		// local 对齐需要时区信息，spec 层尚未承载，显式拒绝。
		switch b.Window.CalendarAlign {
		case "", "utc_midnight":
			rb.Anchor0, rb.HasAnchor = time.Unix(0, 0).UTC(), true
		default:
			return ResolvedBucket{}, fmt.Errorf(
				"semantics: 桶 %q calendar_align %q 未支持（当前仅 utc_midnight）", b.ID, b.Window.CalendarAlign)
		}
	}
	return rb, nil
}

// parseAnchorUTC 解析 anchor_utc（Intent §1.4）：
//
//	RFC3339 完整时刻  "2026-08-05T20:00:00Z" → 该时刻即锚点
//	周内时刻         "WED 20:00"            → Unix 纪元（周四）之后第一个该时刻
//	"UNKNOWN"/空     → 错误：锚点待辨识，显式失败优于错误几何
func parseAnchorUTC(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "UNKNOWN") {
		return time.Time{}, fmt.Errorf("锚点未知（anchor_utc=%q），无法做周期归零推进", s)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	weekday := map[string]time.Weekday{
		"SUN": time.Sunday, "MON": time.Monday, "TUE": time.Tuesday, "WED": time.Wednesday,
		"THU": time.Thursday, "FRI": time.Friday, "SAT": time.Saturday,
	}
	fields := strings.Fields(strings.ToUpper(s))
	if len(fields) != 2 {
		return time.Time{}, fmt.Errorf("anchor_utc %q 既非 RFC3339 亦非 \"WD HH:MM\" 形态", s)
	}
	wd, ok := weekday[fields[0]]
	if !ok {
		return time.Time{}, fmt.Errorf("anchor_utc %q 的星期缩写未知（合法：SUN..SAT）", s)
	}
	hm := strings.Split(fields[1], ":")
	if len(hm) != 2 {
		return time.Time{}, fmt.Errorf("anchor_utc %q 的时刻应为 HH:MM", s)
	}
	h, err1 := strconv.Atoi(hm[0])
	m, err2 := strconv.Atoi(hm[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return time.Time{}, fmt.Errorf("anchor_utc %q 的时刻 HH:MM 越界", s)
	}
	// Unix 纪元 1970-01-01 是周四：首个锚点 = 纪元起 (wd-Thursday+7)%7 天后。
	epoch := time.Unix(0, 0).UTC()
	daysAhead := (int(wd) - int(time.Thursday) + 7) % 7
	return epoch.AddDate(0, 0, daysAhead).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute), nil
}

// Advance 是时间推进纯函数（Intent §3.2）：把状态从 tFrom 推进到 tTo，
// 按 ResolvedBucket.Kind 分派窗语义。契约：可组合
// advance(advance(s,a,b),b,c) == advance(s,a,c)（property test 强制），
// 因此任意粒度重放历史都得到同一状态。
func Advance(s BucketState, rb *ResolvedBucket, tFrom, tTo time.Time) (BucketState, error) {
	if rb == nil {
		return BucketState{}, fmt.Errorf("semantics: Advance(nil spec)")
	}
	if tTo.Before(tFrom) {
		return BucketState{}, fmt.Errorf("semantics: 桶 %q t_from=%s 晚于 t_to=%s", rb.ID, tFrom, tTo)
	}
	if tFrom.Equal(tTo) {
		return s, nil // 幂等
	}
	switch rb.Kind {
	case qdl.WindowTumblingAnchoredOnFirstUse:
		if s.Anchor == nil {
			// 未启动（u 恒 0）或已重置（u 已按 ResetPolicy 归位，rollover 下为
			// 负结转）——两种情形都无需推进。此处绝不能写 u=0：那会把重置时
			// 计算出的 rollover 结转清掉，破坏可组合性。
		} else if !tTo.Before(s.Anchor.Add(rb.Length)) {
			resetBucket(&s, rb) // ★ 整体归零，下一次请求重新起锚
		}
	case qdl.WindowTumblingAccountAnchored, qdl.WindowTumblingCalendar, qdl.WindowBillingCycle:
		if !rb.HasAnchor {
			return BucketState{}, fmt.Errorf(
				"semantics: 桶 %q 锚点未知（anchor_utc=UNKNOWN），周期归零无法推进——待辨识写回", rb.ID)
		}
		// 逐个跨越的重置时刻都归位：rollover 结转按周期累积（跨两个重置时刻
		// 无消耗 ⇒ 结转两次），单次归零会违反可组合性。
		kFrom, kTo := kOf(rb, tFrom), kOf(rb, tTo)
		if kTo-kFrom > maxResetSteps {
			return BucketState{}, fmt.Errorf(
				"semantics: 桶 %q 一次推进跨越 %d 个重置时刻（窗长 %v 过短或时间跨度异常）",
				rb.ID, kTo-kFrom, rb.Length)
		}
		for k := kFrom + 1; k <= kTo; k++ {
			resetBucket(&s, rb)
		}
	case qdl.WindowSlidingExact:
		s.Ledger = pruneLedger(s.Ledger, tTo, rb.Length)
		s.U = sumLedger(s.Ledger)
	case qdl.WindowTokenBucketContinuous:
		s.U = max0(s.U - rb.RefillRate*tTo.Sub(tFrom).Seconds())
	case qdl.WindowOneShotExpiring:
		if rb.ExpiresAt == nil {
			return BucketState{}, fmt.Errorf("semantics: 桶 %q one_shot_expiring 缺 expires_at", rb.ID)
		}
		if tTo.After(*rb.ExpiresAt) {
			s.U = rb.Capacity // 额度作废，视为耗尽
		}
	case qdl.WindowNever:
		// 纯余额，不重置：u 不变
	default:
		return BucketState{}, fmt.Errorf("semantics: 桶 %q 窗型 %q 未实现", rb.ID, rb.Kind)
	}
	return s, nil
}

// kOf 返回 t 所处的周期序号：floor((t - anchor0) / length)。
// anchor0 起第一个周期为 0；anchor0 之前为负（Go 整除向零截断，需修正）。
func kOf(rb *ResolvedBucket, t time.Time) int64 {
	d := t.Sub(rb.Anchor0)
	if d < 0 {
		return int64(-((-d + rb.Length - 1) / rb.Length)) // -ceil(|d|/length)
	}
	return int64(d / rb.Length)
}

// maxResetSteps 是单次 Advance 允许跨越的重置时刻数上限（防病态窗长：
// 正常 plan 最短周期为分钟级，百万次重置已是数据异常的显式失败）。
const maxResetSteps = 1 << 20

// resetBucket 在重置时刻按 ResetPolicy 归位（Intent §1.4 / §3.2）：
// zero/refill_to_full 归零；rollover 系结转为负消耗（负 u = 结转余量）。
func resetBucket(s *BucketState, rb *ResolvedBucket) {
	switch rb.Reset {
	case qdl.ResetRolloverCapped:
		carry := rb.Capacity - s.U
		capCarry := rb.RolloverCapMultiple * rb.Capacity
		s.U = -minNonNeg(carry, capCarry)
	case qdl.ResetRolloverUncapped:
		// 无上限结转：每跨一个重置时刻减一整个周期容量，剩余/欠额全额滚存
		// （负 u = 结转余量，正 u = 欠额）。线性形式天然逐周期累积且保持
		// 可组合性；截断到单周期容量会退化成 capped k=1。
		s.U -= rb.Capacity
	default: // zero / refill_to_full
		s.U = 0
	}
	s.Anchor = nil
}

// pruneLedger 修剪 sliding 明细：只保留 tTo-length < t 的条目（恰好等于
// 窗长的过期——与 Intent 伪代码 Σ{Δu : t_to - t < length} 一致）。
func pruneLedger(ledger []Delta, tTo time.Time, length time.Duration) []Delta {
	cutoff := tTo.Add(-length)
	kept := ledger[:0:0]
	for _, d := range ledger {
		if d.T.After(cutoff) { // t > cutoff ⇔ tTo - t < length
			kept = append(kept, d)
		}
	}
	return kept
}

// sumLedger 对明细求和（sliding 的 u 恒等于在窗明细之和）。
func sumLedger(ledger []Delta) float64 {
	sum := 0.0
	for _, d := range ledger {
		sum += d.DU
	}
	return sum
}

func max0(x float64) float64 {
	if x < 0 {
		return 0
	}
	return x
}

// minNonNeg 取两值较小者并截断到非负（超扣时结转为 0，不倒贴）。
func minNonNeg(a, b float64) float64 {
	if a < 0 || b < 0 {
		return 0
	}
	if a < b {
		return a
	}
	return b
}
