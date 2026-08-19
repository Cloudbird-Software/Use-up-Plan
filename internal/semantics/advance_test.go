package semantics

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// base 是测试通用的时间基准。
var base = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// mkBucket 构造一个最小可解析的 qdl.Bucket（容量常量、无参数引用）。
func mkBucket(id string, kind qdl.WindowKind, length time.Duration) *qdl.Bucket {
	return &qdl.Bucket{
		ID:       id,
		Unit:     qdl.DimOpaqueUnits,
		Capacity: qdl.Const(100),
		Window: qdl.Window{
			KindCandidates: []qdl.WindowKind{kind},
			Length:         qdl.Duration{Duration: length},
		},
		Scope:  qdl.Scope{Level: qdl.ScopeAccount},
		Charge: qdl.ChargeRule{Flat: qdl.Const(0), Linearization: qdl.LinearExactLinear},
	}
}

// mkResolved 直接构造 ResolvedBucket（绕过 qdl 层，聚焦 advance 几何）。
func mkResolved(kind qdl.WindowKind, length time.Duration) *ResolvedBucket {
	return &ResolvedBucket{
		ID: "b", Kind: kind, Length: length,
		Capacity: 100, Reset: qdl.ResetZero,
	}
}

func mustAdvance(t *testing.T, s BucketState, rb *ResolvedBucket, from, to time.Time) BucketState {
	t.Helper()
	got, err := Advance(s, rb, from, to)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	return got
}

// --- 单元测试：逐窗型验证 Intent §3.2 的伪代码语义 ---

func TestAdvanceTumblingAnchoredOnFirstUse(t *testing.T) {
	rb := mkResolved(qdl.WindowTumblingAnchoredOnFirstUse, 5*time.Hour)
	anchor := base.Add(-3 * time.Hour)
	s := BucketState{U: 42, Anchor: &anchor}

	// 窗内：u 不变
	if got := mustAdvance(t, s, rb, base, base.Add(time.Hour)); got.U != 42 || got.Anchor == nil {
		t.Fatalf("窗内应不变: %+v", got)
	}
	// 过期（t_to >= anchor+5h）：整体归零、重新起锚
	got := mustAdvance(t, s, rb, base, base.Add(3*time.Hour))
	if got.U != 0 || got.Anchor != nil {
		t.Fatalf("过期应归零并清锚: %+v", got)
	}
	// 未启动/已重置（anchor nil）：u 原样保留（重置时 rollover 结转是负值，
	// 此处清零会破坏可组合性——负结转的保留见专测）
	s2 := BucketState{U: -20} // 已重置 + rollover 结转
	if got := mustAdvance(t, s2, rb, base, base.Add(time.Hour)); got.U != -20 || got.Anchor != nil {
		t.Fatalf("anchor nil 应原样保留: %+v", got)
	}
}

func TestAdvanceAccountAnchored(t *testing.T) {
	rb := mkResolved(qdl.WindowTumblingAccountAnchored, 24*time.Hour)
	rb.Anchor0, rb.HasAnchor = base.Add(-48*time.Hour), true // 重置时刻：base-48h, base-24h, base, ...
	s := BucketState{U: 42}

	// 周期内：不变
	if got := mustAdvance(t, s, rb, base.Add(time.Hour), base.Add(2*time.Hour)); got.U != 42 {
		t.Fatalf("周期内应不变: %+v", got)
	}
	// 跨一个重置时刻：归零
	if got := mustAdvance(t, s, rb, base.Add(-time.Hour), base.Add(time.Hour)); got.U != 0 {
		t.Fatalf("跨重置时刻应归零: %+v", got)
	}
	// 跨两个重置时刻（rollover 语义见专测；zero 下等价一次归零）
	if got := mustAdvance(t, s, rb, base.Add(-25*time.Hour), base.Add(time.Hour)); got.U != 0 {
		t.Fatalf("跨两个重置时刻应归零: %+v", got)
	}
}

func TestAdvanceSlidingExact(t *testing.T) {
	rb := mkResolved(qdl.WindowSlidingExact, 1*time.Hour)
	ledger := []Delta{
		{T: base.Add(-90 * time.Minute), DU: 10}, // 推进后过期
		{T: base.Add(-30 * time.Minute), DU: 20}, // 仍在窗内
		{T: base.Add(-61 * time.Minute), DU: 5},  // 恰好过期边界外
	}
	s := BucketState{U: 35, Ledger: ledger}
	got := mustAdvance(t, s, rb, base.Add(-time.Hour), base)
	if got.U != 20 || len(got.Ledger) != 1 || got.Ledger[0].DU != 20 {
		t.Fatalf("sliding 应逐笔过期: u=%v ledger=%+v", got.U, got.Ledger)
	}
}

func TestAdvanceTokenBucket(t *testing.T) {
	rb := mkResolved(qdl.WindowTokenBucketContinuous, 0)
	rb.RefillRate = 2 // 2 单位/秒
	s := BucketState{U: 10}
	got := mustAdvance(t, s, rb, base, base.Add(3*time.Second))
	if got.U != 4 {
		t.Fatalf("回补 2/s×3s: %+v", got)
	}
	// 回补不低于 0
	s.U = 3
	if got := mustAdvance(t, s, rb, base, base.Add(10*time.Second)); got.U != 0 {
		t.Fatalf("回补下限 0: %+v", got)
	}
}

func TestAdvanceOneShotExpiring(t *testing.T) {
	rb := mkResolved(qdl.WindowOneShotExpiring, 0)
	exp := base.Add(24 * time.Hour)
	rb.ExpiresAt = &exp
	s := BucketState{U: 30}
	if got := mustAdvance(t, s, rb, base, base.Add(23*time.Hour)); got.U != 30 {
		t.Fatalf("过期前不变: %+v", got)
	}
	if got := mustAdvance(t, s, rb, base, base.Add(25*time.Hour)); got.U != 100 {
		t.Fatalf("过期后置满（额度作废）: %+v", got)
	}
}

func TestAdvanceNever(t *testing.T) {
	rb := mkResolved(qdl.WindowNever, 0)
	s := BucketState{U: 42}
	if got := mustAdvance(t, s, rb, base, base.Add(100*time.Hour)); got.U != 42 {
		t.Fatalf("never 不重置: %+v", got)
	}
}

func TestAdvanceRollover(t *testing.T) {
	rb := mkResolved(qdl.WindowTumblingAccountAnchored, 24*time.Hour)
	rb.Anchor0, rb.HasAnchor = base.Add(-48*time.Hour), true
	rb.Reset = qdl.ResetRolloverCapped
	rb.RolloverCapMultiple = 0.5 // 结转上限 50

	// u=80 → 结转 min(100-80, 50) = 20 → u = -20
	s := BucketState{U: 80}
	if got := mustAdvance(t, s, rb, base.Add(-time.Hour), base.Add(time.Hour)); got.U != -20 {
		t.Fatalf("rollover_capped 结转 20: %+v", got)
	}
	// 跨两个重置时刻无消耗：结转累积 min(100+20,50)=50 → u=-50
	// （这正是 reset 必须逐周期执行的原因——单次归零违反可组合性）
	if got := mustAdvance(t, s, rb, base.Add(-25*time.Hour), base.Add(time.Hour)); got.U != -50 {
		t.Fatalf("两个周期结转饱和 50: %+v", got)
	}
	// 超扣（u > capacity）：结转截断为 0
	s.U = 130
	if got := mustAdvance(t, s, rb, base.Add(-time.Hour), base.Add(time.Hour)); got.U != 0 {
		t.Fatalf("超扣不倒贴: %+v", got)
	}
}

func TestAdvanceErrors(t *testing.T) {
	// 锚点未知
	rb := mkResolved(qdl.WindowTumblingAccountAnchored, 24*time.Hour)
	if _, err := Advance(BucketState{U: 1}, rb, base, base.Add(time.Hour)); err == nil {
		t.Fatal("锚点未知应报错")
	}
	// 时间倒流
	rb2 := mkResolved(qdl.WindowNever, 0)
	if _, err := Advance(BucketState{}, rb2, base, base.Add(-time.Hour)); err == nil {
		t.Fatal("t_from > t_to 应报错")
	}
	// one_shot 缺 expires_at
	rb3 := mkResolved(qdl.WindowOneShotExpiring, 0)
	if _, err := Advance(BucketState{}, rb3, base, base.Add(time.Hour)); err == nil {
		t.Fatal("one_shot 缺 expires_at 应报错")
	}
	// 幂等
	s := BucketState{U: 7}
	if got := mustAdvance(t, s, rb2, base, base); !reflect.DeepEqual(got, s) {
		t.Fatalf("t_from == t_to 应原样返回: %+v", got)
	}
}

func TestResolveBucket(t *testing.T) {
	// 周内锚点：WED 20:00 → Unix 纪元（周四）之后首个周三 20:00 = 1970-01-07T20:00Z
	b := mkBucket("b", qdl.WindowTumblingAccountAnchored, 7*24*time.Hour)
	b.Window.AnchorUTC = "WED 20:00"
	rb, err := ResolveBucket(b, nil)
	if err != nil {
		t.Fatalf("ResolveBucket: %v", err)
	}
	want := time.Unix(0, 0).UTC().AddDate(0, 0, 6).Add(20 * time.Hour) // 纪元周四+6天=周三
	if !rb.Anchor0.Equal(want) || !rb.HasAnchor {
		t.Fatalf("WED 20:00 锚点: got %v want %v", rb.Anchor0, want)
	}
	// RFC3339 锚点
	b.Window.AnchorUTC = "2026-08-01T00:00:00Z"
	rb, err = ResolveBucket(b, nil)
	if err != nil || !rb.Anchor0.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("RFC3339 锚点: %+v err=%v", rb.Anchor0, err)
	}
	// UNKNOWN 锚点：ResolveBucket 成功（结构性未知），Advance 显式失败
	b.Window.AnchorUTC = "UNKNOWN"
	if _, err := ResolveBucket(b, nil); err == nil {
		t.Fatal("UNKNOWN 锚点应在 ResolveBucket 报错")
	}
	// 容量 ParamRef 解析
	b2 := mkBucket("b2", qdl.WindowNever, 0)
	b2.Capacity = qdl.Ref("p.C")
	if _, err := ResolveBucket(b2, nil); err == nil {
		t.Fatal("缺参数应报错")
	}
	rb2, err := ResolveBucket(b2, qdl.ParamPoint{"p.C": 250})
	if err != nil || rb2.Capacity != 250 {
		t.Fatalf("ParamRef 容量: %+v err=%v", rb2, err)
	}
	// 病态窗长
	b3 := mkBucket("b3", qdl.WindowSlidingExact, 0)
	if _, err := ResolveBucket(b3, nil); err == nil {
		t.Fatal("sliding 零窗长应报错")
	}
}

// --- 可组合性 property test（Intent §3.2 契约：强制可任意粒度重放） ---
//
// advance(advance(s,a,b),b,c) == advance(s,a,c)
// 对每个窗型随机生成 (state, a, b, c) 反复验证（Go 侧用固定种子随机，
// 等价 hypothesis 的 @given 策略；失败即违反重放契约）。
//
// 浮点注记：U 是浮点累积量，token_bucket 的 r·Δt 乘加在浮点下不严格结合
// （r(b-a)+r(c-b) ≠ r(c-a)，相对误差 ~1e-16）。可组合性对 U 按 1e-9 相对
// 容差检验；Anchor/Ledger 等结构字段严格相等。

func TestAdvanceComposability(t *testing.T) {
	rng := rand.New(rand.NewSource(20260801))
	kinds := []qdl.WindowKind{
		qdl.WindowTumblingAnchoredOnFirstUse,
		qdl.WindowTumblingAccountAnchored,
		qdl.WindowTumblingCalendar,
		qdl.WindowBillingCycle,
		qdl.WindowSlidingExact,
		qdl.WindowTokenBucketContinuous,
		qdl.WindowOneShotExpiring,
		qdl.WindowNever,
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			for i := 0; i < 400; i++ {
				rb := randResolved(rng, kind)
				s := randState(rng, rb)
				a := base.Add(time.Duration(rng.Intn(3600)) * time.Second)
				b := a.Add(time.Duration(rng.Intn(7200)) * time.Second)
				c := b.Add(time.Duration(rng.Intn(7200)) * time.Second)

				twoStep, err1 := Advance(s, rb, a, b)
				if err1 != nil {
					continue // 随机状态对该窗型非法（如无锚），跳过
				}
				twoStep, err2 := Advance(twoStep, rb, b, c)
				if err2 != nil {
					continue
				}
				oneStep, err3 := Advance(s, rb, a, c)
				if err3 != nil {
					t.Fatalf("两步成功但一步失败: %v", err3)
				}
				if !statesEquivalent(twoStep, oneStep) {
					t.Fatalf("违反可组合性:\n窗型=%s\n两步: %+v\n一步: %+v", kind, twoStep, oneStep)
				}
			}
		})
	}
}

// statesEquivalent 比较两个状态的可组合性等价：结构字段严格相等，
// U 允许 1e-9 相对容差（浮点乘加非结合的固有噪声）。
func statesEquivalent(a, b BucketState) bool {
	if !reflect.DeepEqual(a.Anchor, b.Anchor) {
		return false
	}
	if !reflect.DeepEqual(a.ResetAt, b.ResetAt) {
		return false
	}
	if len(a.Ledger) != len(b.Ledger) {
		return false
	}
	for i := range a.Ledger {
		if !a.Ledger[i].T.Equal(b.Ledger[i].T) || a.Ledger[i].DU != b.Ledger[i].DU {
			return false
		}
	}
	scale := math.Abs(a.U) + math.Abs(b.U)
	if scale == 0 {
		return true
	}
	return math.Abs(a.U-b.U)/scale <= 1e-9
}

// randResolved 随机生成合法 ResolvedBucket（窗型特定字段补齐）。
func randResolved(rng *rand.Rand, kind qdl.WindowKind) *ResolvedBucket {
	rb := &ResolvedBucket{
		ID:                  "b",
		Kind:                kind,
		Length:              time.Duration(600+rng.Intn(3600)) * time.Second,
		Capacity:            50 + rng.Float64()*150,
		Reset:               qdl.ResetPolicy([4]string{"zero", "refill_to_full", "rollover_capped", "rollover_uncapped"}[rng.Intn(4)]),
		RolloverCapMultiple: []float64{0.5, 1, 2}[rng.Intn(3)],
		RefillRate:          rng.Float64() * 0.01,
	}
	switch kind {
	case qdl.WindowTumblingAccountAnchored, qdl.WindowTumblingCalendar, qdl.WindowBillingCycle:
		rb.HasAnchor = true
		// 锚点覆盖 a 之前的若干周期（含 a 落在 anchor0 之前的反向情形）
		rb.Anchor0 = base.Add(-time.Duration(rng.Intn(3)+1) * rb.Length)
		if rng.Intn(4) == 0 {
			rb.Anchor0 = base.Add(time.Duration(rng.Intn(2)+1) * rb.Length)
		}
	case qdl.WindowOneShotExpiring:
		exp := base.Add(time.Duration(rng.Intn(7200)) * time.Second)
		rb.ExpiresAt = &exp
	}
	return rb
}

// randState 随机生成与窗型相容的初始状态（含锚 nil / ledger 边界情形）。
func randState(rng *rand.Rand, rb *ResolvedBucket) BucketState {
	s := BucketState{U: rng.Float64() * rb.Capacity}
	switch rb.Kind {
	case qdl.WindowTumblingAnchoredOnFirstUse:
		if rng.Intn(4) > 0 {
			anchor := base.Add(-time.Duration(rng.Intn(int(rb.Length/time.Second)+1)) * time.Second)
			s.Anchor = &anchor
		}
	case qdl.WindowSlidingExact:
		n := rng.Intn(5)
		for i := 0; i < n; i++ {
			s.Ledger = append(s.Ledger, Delta{
				T:  base.Add(-time.Duration(rng.Intn(int(2*rb.Length/time.Second)+1)) * time.Second),
				DU: rng.Float64() * 10,
			})
		}
	}
	return s
}
