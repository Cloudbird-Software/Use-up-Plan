package estimate

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// ---- 离线后验（B5） ----

// postOptsCI 是 CI 验收用采样参数（默认档的缩小版，保持诊断有效性）。
// Thin=6：量化似然的平谷让链粘滞，抽稀不足时 ESS 掉得快。
func postOptsCI() PosteriorOptions {
	return PosteriorOptions{Seed: 7, BurnIn: 600, Samples: 400, Thin: 6}
}

// TestPosteriorCITight Phase 1 验收口径（ROADMAP B 行）：
// C 的后验 90% 可信区间宽度 < 中位数的 40%，且区间覆盖真值。
// w 锚定（frozen gauge）保证尺度可辨识；theta0 用在线 MLE warm-start——
// 在线/离线两档估计器在链起点衔接。
func TestPosteriorCITight(t *testing.T) {
	spec, truth := estSpec(true, false) // w frozen（gauge），C/f 自由
	evs := synthEvents(t, spec, truth, 30)
	store := mkStore(t, evs...)
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	base := qdl.ParamPoint{"t.w": truth["t.w"]}
	mle, err := Estimate(ds, base, qdl.ParamPoint{"t.C": 80}, EstimateOptions{})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	res, err := SamplePosterior(ds, base, mle.Theta, postOptsCI())
	if err != nil {
		t.Fatalf("SamplePosterior: %v", err)
	}

	s := res.Summary["t.C"]
	lo, hi := s.Q05, s.Q95
	if lo > truth["t.C"] || hi < truth["t.C"] {
		t.Fatalf("90%% 可信区间 [%.1f, %.1f] 未覆盖真值 %.1f", lo, hi, truth["t.C"])
	}
	width := hi - lo
	if width/s.Q50 >= 0.4 {
		t.Fatalf("区间宽度 %.1f / 中位数 %.1f = %.1f%% ≥ 40%%（Phase 1 验收线）",
			width, s.Q50, 100*width/s.Q50)
	}
	// 诊断健康度：接受率与有效样本量
	if res.AcceptRate < 0.05 || res.AcceptRate > 0.6 {
		t.Fatalf("接受率 %.2f 超出健康带 [0.05, 0.6]", res.AcceptRate)
	}
	for _, id := range res.FreeIDs {
		if res.ESS[id] < 30 {
			t.Fatalf("参数 %q ESS=%.0f 过低（链混合不足）", id, res.ESS[id])
		}
	}
	// f 的区间也应覆盖真值（flat 与 C 由请求规模/token 规模的不同组合区分）
	sf := res.Summary["t.f"]
	if sf.Q05 > truth["t.f"] || sf.Q95 < truth["t.f"] {
		t.Fatalf("f 区间 [%.3f, %.3f] 未覆盖真值 %.3f", sf.Q05, sf.Q95, truth["t.f"])
	}
}

// TestLaplaceVsPosterior 选型对比实验（ROADMAP S2：Laplace 近似 vs 自实现
// MH）：两个口径的 90% 区间都覆盖真值，宽度同量级（差 5×以内）——
// 量化似然的窄谷接近二次，Laplace 足够吸附准入用；MH 提供审计级
// 全量后验（偏度/尾部由样本直接读出）。
func TestLaplaceVsPosterior(t *testing.T) {
	spec, truth := estSpec(true, false)
	evs := synthEvents(t, spec, truth, 30)
	store := mkStore(t, evs...)
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	base := qdl.ParamPoint{"t.w": truth["t.w"]}
	mle, err := Estimate(ds, base, qdl.ParamPoint{"t.C": 80}, EstimateOptions{})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	ci, err := LaplaceCI(ds, base, mle.Theta, nil, LaplaceOptions{})
	if err != nil {
		t.Fatalf("LaplaceCI: %v", err)
	}
	res, err := SamplePosterior(ds, base, mle.Theta, postOptsCI())
	if err != nil {
		t.Fatalf("SamplePosterior: %v", err)
	}
	lap := ci["t.C"]
	mc := res.Summary["t.C"]
	if !lap.Contains(truth["t.C"]) {
		t.Fatalf("Laplace 区间 [%.1f, %.1f] 未覆盖真值", lap.Lo, lap.Hi)
	}
	lapW, mcW := lap.Hi-lap.Lo, mc.Q95-mc.Q05
	if mcW < 0.2*lapW || mcW > 5*lapW {
		t.Fatalf("两口径宽度失配：Laplace=%.1f MH=%.1f", lapW, mcW)
	}
}

// TestPosteriorDeterministic 同种子逐位复现（审计可复现的硬要求）；
// 换种子样本应不同（防止「确定性」退化为常量链）。
func TestPosteriorDeterministic(t *testing.T) {
	spec, truth := estSpec(true, false)
	evs := synthEvents(t, spec, truth, 12)
	store := mkStore(t, evs...)
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	base := qdl.ParamPoint{"t.w": truth["t.w"]}
	opts := PosteriorOptions{Seed: 3, BurnIn: 300, Samples: 200, Thin: 2}
	r1, err := SamplePosterior(ds, base, qdl.ParamPoint{"t.C": 100}, opts)
	if err != nil {
		t.Fatalf("SamplePosterior#1: %v", err)
	}
	r2, err := SamplePosterior(ds, base, qdl.ParamPoint{"t.C": 100}, opts)
	if err != nil {
		t.Fatalf("SamplePosterior#2: %v", err)
	}
	if r1.Summary["t.C"] != r2.Summary["t.C"] || r1.AcceptRate != r2.AcceptRate {
		t.Fatalf("同种子应逐位复现: %+v vs %+v", r1.Summary["t.C"], r2.Summary["t.C"])
	}
	opts.Seed = 4
	r3, err := SamplePosterior(ds, base, qdl.ParamPoint{"t.C": 100}, opts)
	if err != nil {
		t.Fatalf("SamplePosterior#3: %v", err)
	}
	if r3.Summary["t.C"] == r1.Summary["t.C"] {
		t.Fatalf("不同种子产生了相同样本（疑似常量链）")
	}
}

// TestParamUpdatesFromPosterior 离线估计写入与在线同一 ParamUpdateEvent 流
// （Intent §4.6）：reason=offline、正支持参数摘要为 lognormal（中位数口径）、
// PosteriorBefore 回填先验、证据 seq 与数据集一致、负载可通过 ledger 校验。
func TestParamUpdatesFromPosterior(t *testing.T) {
	spec, truth := estSpec(true, false)
	evs := synthEvents(t, spec, truth, 12)
	store := mkStore(t, evs...)
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	base := qdl.ParamPoint{"t.w": truth["t.w"]}
	res, err := SamplePosterior(ds, base, qdl.ParamPoint{"t.C": 100},
		PosteriorOptions{Seed: 5, BurnIn: 300, Samples: 200, Thin: 2})
	if err != nil {
		t.Fatalf("SamplePosterior: %v", err)
	}
	evUpdates := res.ParamUpdates(ds.EvidenceIDs())
	if len(evUpdates) != len(res.FreeIDs) {
		t.Fatalf("应每个自由参数一条事件: %d vs %d", len(evUpdates), len(res.FreeIDs))
	}
	for _, ev := range evUpdates {
		if err := ev.Validate(); err != nil {
			t.Fatalf("ParamUpdateEvent %q 校验失败: %v", ev.ParamID, err)
		}
		if ev.Reason != "offline" {
			t.Fatalf("reason 应为 offline: %q", ev.Reason)
		}
		if ev.PosteriorAfter.Kind != qdl.DistLognormal {
			t.Fatalf("正支持参数 %q 应摘要为 lognormal: %v", ev.ParamID, ev.PosteriorAfter.Kind)
		}
		med := math.Exp(ev.PosteriorAfter.Params["mu"])
		s := res.Summary[ev.ParamID]
		if math.Abs(med-s.Q50) > 1e-9+1e-6*math.Abs(s.Q50) {
			t.Fatalf("lognormal 中位数 %.6f 应与样本中位数 %.6f 一致", med, s.Q50)
		}
		if ev.PosteriorBefore == nil || ev.PosteriorBefore.Kind != qdl.DistNormal {
			t.Fatalf("PosteriorBefore 应回填先验（normal）: %+v", ev.PosteriorBefore)
		}
	}
	// 证据 seq 一致
	want := ds.EvidenceIDs()
	if len(evUpdates[0].EvidenceIDs) != len(want) {
		t.Fatalf("证据指认数量不符: %d vs %d", len(evUpdates[0].EvidenceIDs), len(want))
	}
	for i, id := range want {
		if evUpdates[0].EvidenceIDs[i] != id {
			t.Fatalf("证据 seq[%d]: %d vs %d", i, evUpdates[0].EvidenceIDs[i], id)
		}
	}
}

// TestPosteriorAllFrozen 无自由参数时离线采样应报错（恒等问题无后验可言）。
func TestPosteriorAllFrozen(t *testing.T) {
	spec, truth := estSpec(true, true)
	evs := synthEvents(t, spec, truth, 6)
	store := mkStore(t, evs...)
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	base := qdl.ParamPoint{"t.C": truth["t.C"], "t.w": truth["t.w"]}
	// f 也冻结（spec 里 f 恒自由，经 Freeze 临时退出）
	opts := PosteriorOptions{Freeze: map[string]float64{"t.f": truth["t.f"]}}
	if _, err := SamplePosterior(ds, base, base, opts); err == nil {
		t.Fatalf("全 frozen 时应报错")
	}
}

// TestESSFunction Geyer ESS 的两个锚点：iid 样本 ESS≈N，
// 强自相关链（AR(1) φ=0.99）ESS 远小于 N。
func TestESSFunction(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 22))
	iid := make([]float64, 2000)
	for i := range iid {
		iid[i] = rng.NormFloat64()
	}
	if e := ess(iid); e < 1000 {
		t.Fatalf("iid 链 ESS=%.0f 应接近 N=2000", e)
	}
	ar := make([]float64, 2000)
	x := 0.0
	for i := range ar {
		x = 0.99*x + rng.NormFloat64()
		ar[i] = x
	}
	if e := ess(ar); e > 200 {
		t.Fatalf("AR(1) φ=0.99 链 ESS=%.0f 应远小于 N=2000", e)
	}
}
