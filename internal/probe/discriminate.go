// discriminate.go 实现 C3 确定性判别式：消费 DryRun 提取的证据序列，
// 产出结构判定结论。Intent §4.3：「最重要的几个结构问题有不需要统计的
// 确定性判别式，优先用」——每个判别式输出一个归一化 finding（封闭集）+
// 置信度，finding 经剧本声明的 mapping 映射到结构候选。
//
// 判别式不做数值估计（那是 estimate 的职责），只回答二值/三值的结构问题；
// 证据不足时如实返回不可判定（Candidate 为空），绝不硬猜。
package probe

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// 判别式归一化 finding 封闭集（按 Kind 分组，见 findingsByKind）。
const (
	FindingConstant        = "constant"          // resets_at 恒定（锚定窗）
	FindingShifting        = "shifting"          // resets_at 随消耗滑动（true sliding）
	FindingCliff           = "cliff"             // 断崖（整窗归零 / 边界跳满）
	FindingStair           = "stair"             // 阶梯（渐进衰减 / 连续回补）
	FindingTurn            = "turn"              // 每 turn 计 1
	FindingRequest         = "request"           // 每 request 计 1
	FindingStep            = "step"              // 每 step 计 1（最细粒度）
	FindingAppearsAfterUse = "appears_after_use" // 字段在消耗后出现（窗存在）
	FindingStaysNull       = "stays_null"        // 消耗后仍 null（窗不存在）
	FindingSynchronized    = "synchronized"      // 双桶同步上涨（共池）
	FindingIndependent     = "independent"       // 孤立上涨（独立池）
)

// Verdict 是单个判别式对证据的判定：finding 是算法封闭集内的归一化结论，
// Candidate 是经剧本 mapping 翻译后的结构候选（不可判定时为空）。
// Confidence ∈ [0,1] 是该判别式对 finding 的置信度——确定性判别式的
// 「置信」指信号干净程度（断崖与次大回补动作的分离度等），不是贝叶斯后验。
type Verdict struct {
	DiscriminatorID string
	Kind            string
	Finding         string  // 归一化结论（findingsByKind 封闭集内）
	Candidate       string  // mapping 翻译后的候选；"" = 不可判定
	Confidence      float64 // 0 = 无证据；1 = 信号完全干净
	Evidence        string  // 人类可读的证据摘要（审计口径）
}

// Conclusion 是剧本级聚合结论：把各判别式的置信度当作其分布在
// 「候选 vs 不可判定」上的质量，归一化成后验。
type Conclusion struct {
	PlaybookID   string
	Candidate    string // 得票最高的候选；"" = 全部判别式不可判定
	Confidence   float64
	Posterior    map[string]float64 // 候选 → 归一化后验
	Inconclusive float64            // 落入「不可判定」的质量份额
}

// Options 是判别式的实验设计参数：确定性算法需要少量「实验怎么做的」
// 上下文，这些不是证据（证据在事件库里），是实验协议的声明。
type Options struct {
	// StepsPerTurn 是 step_counting 实验中单个 turn 含的 request 数
	// （Intent §4.3：「发 1 个含 5 次工具调用的 turn」→ 5）。0 → 缺省 5。
	StepsPerTurn int
	// UsageConfirmed 声明 null_presence 实验期间确认发生过目标桶消耗
	//（「用 Opus 消耗一点」）。全 null 且无消耗确认时无法区分
	// 「窗不存在」与「仍未使用」，判别式必须如实返回不可判定。
	UsageConfirmed bool
}

// Discriminate 按剧本声明的判别式逐个执行（声明序，确定性）。
// 证据不足不报错——返回不可判定 verdict，缺口写在 Evidence 里。
func Discriminate(pb *Playbook, series map[qdl.Semantic]*EvidenceSeries, opts Options) ([]Verdict, error) {
	if pb == nil {
		return nil, fmt.Errorf("probe: Discriminate(nil)")
	}
	out := make([]Verdict, 0, len(pb.Discriminators))
	for _, d := range pb.Discriminators {
		var v Verdict
		switch d.Kind {
		case "resets_at_constancy":
			v = discriminateResetsAtConstancy(d, series)
		case "cliff_vs_stair":
			v = discriminateCliffVsStair(d, series)
		case "step_counting":
			v = discriminateStepCounting(d, series, opts)
		case "null_presence":
			v = discriminateNullPresence(d, series, opts)
		case "pool_sync":
			v = discriminatePoolSync(d, series)
		default:
			return nil, fmt.Errorf("probe: 判别式 %s kind %q 未实现（封闭集与实现脱节）", d.ID, d.Kind)
		}
		v.DiscriminatorID = d.ID
		v.Kind = d.Kind
		if v.Finding != "" {
			if cand, ok := d.Mapping[v.Finding]; ok {
				v.Candidate = cand
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// Conclude 把判别式 verdict 聚合成剧本级结论。每个判别式把 1.0 的
// 质量分给其候选（Confidence）与「不可判定」（1-Confidence）；
// 候选后验 = 该候选得分 / 总质量。平票取字典序最小（确定性）。
func Conclude(pb *Playbook, verdicts []Verdict) Conclusion {
	if pb == nil {
		return Conclusion{Posterior: map[string]float64{}}
	}
	c := Conclusion{PlaybookID: pb.ID, Posterior: map[string]float64{}}
	scores := map[string]float64{}
	var sink float64
	for _, v := range verdicts {
		if v.Candidate != "" && v.Confidence > 0 {
			scores[v.Candidate] += v.Confidence
			sink += 1 - v.Confidence
		} else {
			sink++ // 不可判定判别式的全部质量
		}
	}
	var total float64
	for _, s := range scores {
		total += s
	}
	total += sink
	if total <= 0 {
		return c
	}
	c.Inconclusive = sink / total
	for cand, s := range scores {
		c.Posterior[cand] = s / total
	}
	cands := make([]string, 0, len(scores))
	for cand := range scores {
		cands = append(cands, cand)
	}
	sort.Strings(cands)
	for _, cand := range cands {
		if c.Candidate == "" || scores[cand] > scores[c.Candidate] {
			c.Candidate = cand
			c.Confidence = c.Posterior[cand]
		}
	}
	return c
}

// ─── 判别式阈值（相对量优先：桶容量跨计划差几个数量级，绝对阈值会误判） ───

// cliffQualFrac 是断崖幅度占序列值域的最低占比（整窗重置的单步动作
// ≈ 全值域）；cliffQualMin 是绝对下限（值域极小时防退化）。落点检查
// （used 归零 / remaining 跳满）用值域的 10% 做容差，兼顾整数量化噪声。
const (
	cliffQualFrac  = 0.7 // 断崖幅度占值域的最低占比（整窗重置 ≈ 全值域单步）
	cliffQualMin   = 5.0
	landingFrac    = 0.1
	shiftFloorFrac = 0.3 // 判「跳变前用量仍显著」的下限（占值域比例）
)

// ─── resets_at_constancy ───
//
// 窗内 resets_at 恒定 → 锚定窗（anchored tumbling / 账号锚定）；
// resets_at 在无整窗重置的情况下随消耗滑动 → true sliding。
// 判定单元是「跳变」：相邻两次 resets_at 观测值不同。每次跳变检查
// 用量序列在跳变时刻附近是否发生断崖（高用量 → 归零）：
//   - 跳变与断崖重合 → 窗口边界（锚定语义）
//   - 跳变后用量仍高 → 旧用量滑出窗口（sliding 语义）
func discriminateResetsAtConstancy(d Discriminator, series map[qdl.Semantic]*EvidenceSeries) Verdict {
	v := Verdict{}
	reset := series[qdl.SemResetAtISO]
	if reset == nil {
		if s := series[qdl.SemResetAtEpochMS]; s != nil {
			reset = s
		}
	}
	if reset == nil || len(reset.Samples) < 2 {
		v.Evidence = "resets_at 观测不足（<2 个样本），无法判定恒定性"
		return v
	}
	rs := seriesSorted(reset)
	// used 序列可能多桶（共池场景）——扁平化成单时间线不影响
	// 「跳变时刻前后用量」的查询语义（resets_at 观测本身是窗级的）。
	var used []levelSample
	if s := series[qdl.SemUsedPct]; s != nil {
		for _, ls := range bucketLevels(s) {
			used = append(used, ls...)
		}
		sort.Slice(used, func(i, j int) bool { return used[i].ts.Before(used[j].ts) })
	}
	// 值域（落点/显著性的相对阈值基准）。
	rangeUsed := levelRange(used)
	tol := 2 * medianGap(used)
	if tol < time.Minute {
		tol = time.Minute
	}
	type shiftClass int
	const (
		shiftUnknown shiftClass = iota
		shiftBoundary
		shiftMidWindow
	)
	var classes []shiftClass
	var parsed []time.Time
	for i := 1; i < len(rs); i++ {
		t0, ok0 := parseResetAt(rs[i-1].RawValue)
		t1, ok1 := parseResetAt(rs[i].RawValue)
		if !ok0 || !ok1 || !t1.After(t0) {
			continue
		}
		parsed = append(parsed, t1)
		cls := shiftUnknown
		if len(used) > 0 {
			before, bok := nearestBeforeWithin(used, rs[i].Ts, tol)
			after, aok := nearestAfterWithin(used, rs[i].Ts, tol)
			if bok && aok {
				floor := math.Max(shiftFloorFrac*rangeUsed, 2)
				zero := math.Max(landingFrac*rangeUsed, 2)
				switch {
				case after >= floor:
					cls = shiftMidWindow // 跳变后用量仍高：旧用量滑出
				case before >= floor && after <= zero:
					cls = shiftBoundary // 高用量 → 归零：窗口边界
				}
			}
		}
		classes = append(classes, cls)
	}
	if len(classes) == 0 {
		v.Finding = FindingConstant
		v.Confidence = 0.5
		v.Evidence = "观测期内 resets_at 无前移跳变（未覆盖窗边界），恒定性证据不完整"
		return v
	}
	boundary, mid, known := 0, 0, 0
	for _, cls := range classes {
		switch cls {
		case shiftBoundary:
			boundary++
			known++
		case shiftMidWindow:
			mid++
			known++
		}
	}
	if known == 0 {
		v.Evidence = "跳变时刻附近无可用量观测（跳变既非边界也非滑出的证据都没有）"
		return v
	}
	if mid > 0 {
		v.Finding = FindingShifting
		v.Confidence = float64(mid) / float64(known)
		v.Evidence = fmt.Sprintf("%d 次跳变中 %d 次发生后用量仍高（旧用量滑出窗口 → sliding）", len(classes), mid)
		return v
	}
	v.Finding = FindingConstant
	v.Confidence = float64(boundary) / float64(known)
	v.Evidence = fmt.Sprintf("%d 次跳变全部与整窗重置断崖重合（锚定窗边界）", boundary)
	if anchor, n, m := modalAnchor(parsed); n > 0 {
		v.Evidence += fmt.Sprintf("；重置时刻模态 %s（%d/%d 个 reset 值）", anchor, n, m)
	}
	return v
}

// modalAnchor 汇总 reset 值的星期几+时刻模态（周窗锚点的反推依据）。
// reset_at 是厂商宣告的绝对时刻，其星期几/时刻语义在原时区里最稳——
// RFC3339 自带偏移，这里取解析后的墙上时钟做模态。
func modalAnchor(parsed []time.Time) (string, int, int) {
	if len(parsed) == 0 {
		return "", 0, 0
	}
	counts := map[string]int{}
	exact := map[string]string{}
	for _, t := range parsed {
		k := fmt.Sprintf("%s %02d:%02d", t.Weekday(), t.Hour(), t.Minute())
		counts[k]++
		exact[k] = t.Format(time.RFC3339)
	}
	best, bestN := "", 0
	for k, n := range counts {
		if n > bestN || (n == bestN && k < best) {
			best, bestN = k, n
		}
	}
	return best + " @ " + exact[best], bestN, len(parsed)
}

// ─── cliff_vs_stair ───
//
// 回补方向的单步动作幅度分布是双峰的：整窗边界（tumbling 归零 / 离散窗
// 跳满）是孤立大量级动作；滑出衰减（sliding）与 token bucket 连续回补
// 是小步阶梯。判据（相对值域，桶容量跨计划差数量级）：
//
//	cliff ⇔ ∃ 回补动作幅度 ≥ max(0.6·range, 5) 且落点贴极端
//	        （used_pct 整窗归零落 ≤ min+0.1·range；remaining_abs 边界
//	         跳满落 ≥ max−0.1·range）
//	stair ⇔ 有回补动作但无一达标
//
// 置信度 = 信号分离度（次大动作 / 断崖幅度，或最大动作 / 达标线，平方收敛）。
func discriminateCliffVsStair(d Discriminator, series map[qdl.Semantic]*EvidenceSeries) Verdict {
	v := Verdict{}
	// 输入面：used_pct（利用率断崖 vs 阶梯衰减）或 remaining_abs
	//（RPM：边界跳满 vs 连续回补）。回补方向前者为下降、后者为上升。
	var levels map[string][]levelSample
	var onRise bool
	switch {
	case series[qdl.SemUsedPct] != nil:
		levels = bucketLevels(series[qdl.SemUsedPct])
		onRise = false
	case series[qdl.SemRemainingAbs] != nil:
		levels = bucketLevels(series[qdl.SemRemainingAbs])
		onRise = true
	default:
		v.Evidence = "无 used_pct / remaining_abs 序列，断崖判别式没有输入"
		return v
	}
	type move struct{ from, to float64 }
	var moves []move
	all := flattenLevels(levels)
	rng := levelRange(all)
	if rng <= 0 {
		v.Evidence = "序列值域为 0（用量恒定），无回补动作可判"
		return v
	}
	lo, hi := all[0].v, all[0].v
	for _, s := range all {
		lo = math.Min(lo, s.v)
		hi = math.Max(hi, s.v)
	}
	for _, ls := range levels {
		for i := 1; i < len(ls); i++ {
			if (onRise && ls[i].v > ls[i-1].v) || (!onRise && ls[i].v < ls[i-1].v) {
				moves = append(moves, move{from: ls[i-1].v, to: ls[i].v})
			}
		}
	}
	if len(moves) == 0 {
		v.Evidence = "观测期内无回补方向动作（无窗口到期/回补信号）"
		return v
	}
	qualMag := math.Max(cliffQualFrac*rng, cliffQualMin)
	landing := math.Max(landingFrac*rng, 2)
	var cliffMove, runnerUp float64
	hasCliff := false
	for _, m := range moves {
		mag := m.to - m.from
		if !onRise {
			mag = m.from - m.to
		}
		landsExtreme := m.to <= lo+landing // used：归零；remaining：跳到近满（对称取 hi 侧）
		if onRise {
			landsExtreme = m.to >= hi-landing
		}
		if mag >= qualMag && landsExtreme {
			hasCliff = true
			cliffMove = math.Max(cliffMove, mag)
		} else {
			runnerUp = math.Max(runnerUp, mag)
		}
	}
	if hasCliff {
		v.Finding = FindingCliff
		v.Confidence = clamp01(1 - math.Pow(runnerUp/cliffMove, 2))
		v.Evidence = fmt.Sprintf("断崖幅度 %.1f（达标线 %.1f），次大回补动作 %.1f",
			cliffMove, qualMag, runnerUp)
		return v
	}
	maxMag := runnerUp // 无达标断崖时 runnerUp 即最大动作
	v.Finding = FindingStair
	v.Confidence = clamp01(1 - math.Pow(maxMag/qualMag, 2))
	v.Evidence = fmt.Sprintf("最大单步回补 %.1f < 达标线 %.1f（阶梯式渐进）", maxMag, qualMag)
	return v
}

// ─── step_counting ───
//
// prompt 粒度（Intent §4.3 第 4 行）：实验发 1 个含 S 个 request 的 turn，
// 看 used_abs 涨多少：1 → turn 计量；S → request 计量；2S → step 计量
// （请求与工具往返各计一步的代理预测）。匹配容差兼顾整数量化。
func discriminateStepCounting(d Discriminator, series map[qdl.Semantic]*EvidenceSeries, opts Options) Verdict {
	v := Verdict{}
	s := series[qdl.SemUsedAbs]
	if s == nil || len(s.Samples) < 2 {
		v.Evidence = "used_abs 观测不足（<2），无法测步进"
		return v
	}
	S := opts.StepsPerTurn
	if S <= 0 {
		S = 5 // Intent §4.3 实验设计：1 turn 含 5 次工具调用
	}
	// 实验应在单桶单窗内完成（剧本 min_span 5m）：取样本最多的桶
	//（平票字典序），delta = 末样本 − 首样本。
	levels := bucketLevels(s)
	bucketIDs := make([]string, 0, len(levels))
	for id := range levels {
		bucketIDs = append(bucketIDs, id)
	}
	sort.Strings(bucketIDs)
	pick := ""
	for _, id := range bucketIDs {
		if pick == "" || len(levels[id]) > len(levels[pick]) {
			pick = id
		}
	}
	ls := levels[pick]
	delta := ls[len(ls)-1].v - ls[0].v
	type pred struct {
		finding string
		want    float64
	}
	preds := []pred{
		{FindingTurn, 1},
		{FindingRequest, float64(S)},
		{FindingStep, float64(2 * S)},
	}
	best, bestDist := "", math.Inf(1)
	for _, p := range preds {
		dist := math.Abs(delta - p.want)
		tol := math.Max(1, 0.15*p.want)
		if dist <= tol && dist < bestDist {
			best, bestDist = p.finding, dist
		}
	}
	if best == "" {
		v.Evidence = fmt.Sprintf("单 turn 配额增量 %.1f 不匹配任何粒度假设（turn=1 / request=%d / step=%d）",
			delta, S, 2*S)
		return v
	}
	v.Finding = best
	// 置信度按相对偏差：整数量化下精确命中 → 1.0，差 1 单位按预测值的
	// 比例折损（delta 偏离预测越远，该粒度假说越可疑）。
	for _, p := range preds {
		if p.finding == best {
			v.Confidence = clamp01(1 - bestDist/p.want)
		}
	}
	v.Evidence = fmt.Sprintf("单 turn（含 %d request）配额增量 %.1f", S, delta)
	return v
}

// ─── null_presence ───
//
// Intent §4.3 第 5 行（seven_day_opus 语义）：字段出现即窗存在（unused
// 不报）；用了一点仍全 null 才能判窗不存在——且需要实验协议确认消耗
// 确实发生过，否则「仍未使用」与「窗不存在」不可分。
func discriminateNullPresence(d Discriminator, series map[qdl.Semantic]*EvidenceSeries, opts Options) Verdict {
	v := Verdict{}
	s := series[qdl.SemUsedPct]
	if s == nil || len(s.Samples) == 0 {
		v.Evidence = "无观测样本，null 语义无法判定"
		return v
	}
	ss := seriesSorted(s)
	sawNull, sawNumeric, sawTransition := false, false, false
	for i := range ss {
		val, isNull := parseSample(ss[i].RawValue)
		if isNull {
			if sawNumeric {
				sawTransition = true // numeric → null：字段消失（异常但记录）
			}
			sawNull = true
			continue
		}
		if math.IsNaN(val) {
			continue // 无法解析的非空值：跳过，不算出现
		}
		if sawNull {
			sawTransition = true // null → numeric：消耗后出现（判别式的教科书信号）
		}
		sawNumeric = true
	}
	switch {
	case sawNumeric:
		v.Finding = FindingAppearsAfterUse
		if sawTransition {
			v.Confidence = 0.98
			v.Evidence = "观测到 null → 数值转换：字段在消耗后出现，窗存在（未使用不报）"
		} else {
			v.Confidence = 0.9
			v.Evidence = "字段全程有数值（观测期内非 null）：窗存在"
		}
	case opts.UsageConfirmed:
		v.Finding = FindingStaysNull
		v.Confidence = 0.95
		v.Evidence = "消耗确认发生后字段仍全 null：窗不存在（可从参数空间删除该桶容量）"
	default:
		v.Evidence = "字段全 null 且无消耗确认：无法区分「窗不存在」与「仍未使用」"
	}
	return v
}

// ─── pool_sync ───
//
// Intent §4.3 第 6 行（共池判定）：只用某模型族期间，专桶与总桶是否
// 同步上涨。一个孤立上涨（专桶涨、总桶平）足以证伪共池——共池语义下
// 专桶消耗必然同时抬总桶。反向（全部同步）只能证一致，置信度按同步
// 比例计。专桶/总桶配对从桶 ID 前缀推断（b_7d 与 b_7d_opus）。
func discriminatePoolSync(d Discriminator, series map[qdl.Semantic]*EvidenceSeries) Verdict {
	v := Verdict{}
	s := series[qdl.SemUsedPct]
	if s == nil || len(s.Samples) == 0 {
		v.Evidence = "无 used_pct 观测，共池判定没有输入"
		return v
	}
	levels := bucketLevels(s)
	ids := make([]string, 0, len(levels))
	for id := range levels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) < 2 {
		v.Evidence = fmt.Sprintf("只观测到 %d 个桶（glob 命中面内需总桶+专桶两个序列）", len(ids))
		return v
	}
	general, special, pairs := "", "", 0
	for _, g := range ids {
		for _, sp := range ids {
			if sp != g && strings.HasPrefix(sp, g) {
				pairs++
				if pairs == 1 {
					general, special = g, sp
				}
			}
		}
	}
	if pairs != 1 {
		v.Evidence = "桶 ID 前缀配对不唯一（需形如 b_7d 与 b_7d_opus 的总桶/专桶对）"
		return v
	}
	gs, ss := levels[general], levels[special]
	tol := 2 * medianGap(gs)
	if tol < time.Minute {
		tol = time.Minute
	}
	var specialRises, coincident, isolated int
	for i := 1; i < len(ss); i++ {
		if ss[i].v-ss[i-1].v < 1 {
			continue // 非上涨（回补/持平）不构成消耗信号
		}
		specialRises++
		lo, hi := ss[i-1].ts.Add(-tol), ss[i].ts.Add(tol)
		if hasRiseWithin(gs, lo, hi) {
			coincident++
		} else {
			isolated++
		}
	}
	if specialRises == 0 {
		v.Evidence = "专桶观测期内无上涨（无消耗信号，共池判定无证据）"
		return v
	}
	if isolated > 0 {
		v.Finding = FindingIndependent
		v.Confidence = float64(isolated) / float64(specialRises)
		v.Evidence = fmt.Sprintf("专桶 %d 次上涨中 %d 次总桶持平（孤立上涨证伪共池 → 独立池）",
			specialRises, isolated)
		return v
	}
	v.Finding = FindingSynchronized
	v.Confidence = float64(coincident) / float64(specialRises)
	v.Evidence = fmt.Sprintf("专桶 %d 次上涨全部与总桶同步（%s ↔ %s → 共池）",
		specialRises, general, special)
	return v
}

// hasRiseWithin 报告序列在 [lo, hi] 内是否存在相邻上涨（≥1 单位）。
func hasRiseWithin(ls []levelSample, lo, hi time.Time) bool {
	for i := 1; i < len(ls); i++ {
		if ls[i].v-ls[i-1].v < 1 {
			continue
		}
		if !ls[i-1].ts.Before(lo) && !ls[i].ts.After(hi) {
			return true
		}
	}
	return false
}

// ─── 共用小工具 ───

type levelSample struct {
	ts time.Time
	v  float64
}

// parseSample 解析原始观测值：null 族（空串/null/nil/none）返回
// (NaN, true)；数值返回 (v, false)；其余垃圾返回 (NaN, false)。
// RawValue 保真是存储层契约，语义解释归这里。
func parseSample(raw string) (float64, bool) {
	s := strings.TrimSpace(raw)
	switch strings.ToLower(s) {
	case "", "null", "nil", "none":
		return math.NaN(), true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN(), false
	}
	return v, false
}

// parseResetAt 解析 resets_at 原始值：ISO 字符串或 epoch 毫秒。
func parseResetAt(raw string) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), true
	}
	return time.Time{}, false
}

// seriesSorted 返回按（时间, 桶）排序的样本副本——DryRun 产出本就有序，
// 这里防御独立构造的序列（判别式的输入面不假设调用方排序）。
func seriesSorted(s *EvidenceSeries) []EvidenceSample {
	out := append([]EvidenceSample(nil), s.Samples...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ts.Equal(out[j].Ts) {
			return out[i].BucketID < out[j].BucketID
		}
		return out[i].Ts.Before(out[j].Ts)
	})
	return out
}

// bucketLevels 把证据序列按桶分组、剔除 null/不可解析值、桶内按时间排序。
func bucketLevels(s *EvidenceSeries) map[string][]levelSample {
	m := map[string][]levelSample{}
	for _, smp := range seriesSorted(s) {
		v, isNull := parseSample(smp.RawValue)
		if isNull || math.IsNaN(v) {
			continue
		}
		m[smp.BucketID] = append(m[smp.BucketID], levelSample{ts: smp.Ts, v: v})
	}
	for _, ls := range m {
		sort.Slice(ls, func(i, j int) bool { return ls[i].ts.Before(ls[j].ts) })
	}
	return m
}

func flattenLevels(m map[string][]levelSample) []levelSample {
	var out []levelSample
	for _, ls := range m {
		out = append(out, ls...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ts.Before(out[j].ts) })
	return out
}

// levelRange 是序列值域（max-min）——相对阈值的基准。
func levelRange(ls []levelSample) float64 {
	if len(ls) == 0 {
		return 0
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range ls {
		lo = math.Min(lo, s.v)
		hi = math.Max(hi, s.v)
	}
	return hi - lo
}

// medianGap 是相邻样本间隔的中位数（时间对齐容差的基准）。
func medianGap(ls []levelSample) time.Duration {
	if len(ls) < 2 {
		return 0
	}
	gaps := make([]float64, 0, len(ls)-1)
	for i := 1; i < len(ls); i++ {
		gaps = append(gaps, ls[i].ts.Sub(ls[i-1].ts).Seconds())
	}
	sort.Float64s(gaps)
	return time.Duration(gaps[len(gaps)/2] * float64(time.Second))
}

// nearestBeforeWithin 取严格早于 t 的最近样本（恰在 t 时刻的样本归
// 「之后」——边界时刻的观测值通常是边界后的新值）。
func nearestBeforeWithin(ls []levelSample, t time.Time, tol time.Duration) (float64, bool) {
	best := -1
	for i := range ls {
		if !ls[i].ts.Before(t) {
			break
		}
		best = i
	}
	if best < 0 || t.Sub(ls[best].ts) > tol {
		return 0, false
	}
	return ls[best].v, true
}

func nearestAfterWithin(ls []levelSample, t time.Time, tol time.Duration) (float64, bool) {
	for i := range ls {
		if ls[i].ts.Before(t) {
			continue
		}
		if ls[i].ts.Sub(t) > tol {
			return 0, false
		}
		return ls[i].v, true
	}
	return 0, false
}

func clamp01(x float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	return math.Max(0, math.Min(1, x))
}
