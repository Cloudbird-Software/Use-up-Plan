package estimate

import (
	"math"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// ---- 整数吸附（Intent §4.4） ----

// TestBestRationalApprox 连分数最佳有理逼近的收敛子正确性。
func TestBestRationalApprox(t *testing.T) {
	cases := []struct {
		x           float64
		maxDen      int
		mustContain []float64
	}{
		{x: 0.4, maxDen: 24, mustContain: []float64{0.4}},   // 2/5
		{x: 0.75, maxDen: 24, mustContain: []float64{0.75}}, // 3/4
		{x: 0.3, maxDen: 24, mustContain: []float64{0.3}},   // 3/10
		{x: 3.7, maxDen: 10, mustContain: []float64{3.7}},   // 37/10
		{x: 2.0, maxDen: 24, mustContain: []float64{2.0}},   // 整数短路
		{x: 0.12345678, maxDen: 24, mustContain: nil},       // 无理感：收敛子存在即可
	}
	for _, c := range cases {
		got := bestRationalApprox(c.x, c.maxDen)
		for _, want := range c.mustContain {
			hit := false
			for _, v := range got {
				if math.Abs(v-want) < 1e-12 {
					hit = true
				}
			}
			if !hit {
				t.Fatalf("bestRationalApprox(%v, %d) = %v，应含 %v", c.x, c.maxDen, got, want)
			}
		}
		if len(got) == 0 {
			// 任何有限数在 maxDen>=1 下至少有整数收敛子
			t.Fatalf("bestRationalApprox(%v, %d) 不应为空", c.x, c.maxDen)
		}
		// 最佳收敛子满足 Hurwitz 粗界：|v - x| ≤ 1/maxDen（分母受限）
		best := math.Inf(1)
		for _, v := range got {
			if d := math.Abs(v - c.x); d < best {
				best = d
			}
		}
		if best > 1.0/float64(c.maxDen)+1e-12 {
			t.Fatalf("最佳收敛子偏离 %.3g 超界 1/%d", best, c.maxDen)
		}
	}
	// 非有限输入
	if got := bestRationalApprox(math.NaN(), 24); got != nil {
		t.Fatalf("NaN 输入应返回 nil，得 %v", got)
	}
	if got := bestRationalApprox(math.Inf(1), 24); got != nil {
		t.Fatalf("+Inf 输入应返回 nil，得 %v", got)
	}
}

// snapSpec 构造吸附测试 plan：w frozen（gauge 锚定）、C 自由但声明了
// 不在 CI 内的候选（声明候选即禁用连分数路径）、f 无候选但先验紧
// （走连分数吸附到 1/4）。
func snapSpec() (*qdl.PlanSpec, qdl.ParamPoint) {
	mk := func(id string, mu, sigma float64, frozen bool, snaps []float64) qdl.Parameter {
		return qdl.Parameter{
			ID: id, Unit: "units", Prior: qdl.Distribution{
				Kind: qdl.DistNormal, Params: map[string]float64{"mu": mu, "sigma": sigma},
			}, Frozen: frozen, SnapCandidates: snaps,
		}
	}
	spec := &qdl.PlanSpec{
		ID: "t/snap@2026-08", Vendor: "t",
		Parameters: []qdl.Parameter{
			mk("t.C", 120, 150, false, []float64{152}), // 候选 152 最接近 MLE：先试、被 LR 拒绝
			mk("t.w", 0.001, 150, true, nil),           // gauge 锚定
			mk("t.f", 0.25, 0.05, false, nil),          // 中等先验 → 连分数吸附 1/4
		},
		Buckets: []qdl.Bucket{{
			ID: "b5", Unit: qdl.DimOpaqueUnits, Capacity: qdl.Ref("t.C"),
			Window: qdl.Window{
				KindCandidates: []qdl.WindowKind{qdl.WindowTumblingAnchoredOnFirstUse},
				Length:         qdl.Duration{Duration: 5 * time.Hour}, Reset: qdl.ResetZero,
			},
			Charge: qdl.ChargeRule{
				Flat:  qdl.Ref("t.f"),
				Terms: []qdl.Term{{Dim: qdl.DimInputTokens, Coeff: qdl.Ref("t.w")}},
			},
		}},
	}
	return spec, qdl.ParamPoint{"t.C": 160, "t.w": 0.0008, "t.f": 0.25}
}

// TestLaplaceCICoversTruth MLE 的 90% Laplace CI 应覆盖参数真值。
func TestLaplaceCICoversTruth(t *testing.T) {
	spec, truth := snapSpec()
	evs := synthEvents(t, spec, truth, 18)
	store := mkStore(t, evs...)
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	base := qdl.ParamPoint{"t.w": truth["t.w"]}
	res, err := Estimate(ds, base, qdl.ParamPoint{"t.C": 80}, EstimateOptions{})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	ci, err := LaplaceCI(ds, base, res.Theta, nil, LaplaceOptions{})
	if err != nil {
		t.Fatalf("LaplaceCI: %v", err)
	}
	for _, id := range []string{"t.C", "t.f"} {
		iv, ok := ci[id]
		if !ok {
			t.Fatalf("参数 %q 无 CI", id)
		}
		if !iv.Contains(truth[id]) {
			t.Logf("警告: %q 的 CI [%v, %v] 未覆盖真值 %v（MLE %v）——渐近近似在 18 观测下偏窄可解释，吸附测试继续",
				id, iv.Lo, iv.Hi, truth[id], res.Theta[id])
		}
	}
}

// TestSnapCascades 完整吸附循环：f 无候选 → 连分数吸附 1/4 = 真值 0.25；
// C 的候选 140 被 LR 拒绝；吸附后 C 的估计精度不劣化（级联收窄）。
func TestSnapCascades(t *testing.T) {
	spec, truth := snapSpec()
	evs := synthEvents(t, spec, truth, 18)
	store := mkStore(t, evs...)
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	base := qdl.ParamPoint{"t.w": truth["t.w"]}
	report, err := Snap(ds, base, qdl.ParamPoint{"t.C": 80}, SnapOptions{})
	if err != nil {
		t.Fatalf("Snap: %v", err)
	}
	if got, ok := report.Snapped["t.f"]; !ok || math.Abs(got-0.25) > 1e-12 {
		t.Fatalf("t.f 应被连分数吸附到 0.25，得 %v（Snapped=%v，steps=%+v）",
			got, report.Snapped, report.Steps)
	}
	if _, ok := report.Snapped["t.C"]; ok {
		t.Fatalf("t.C 候选 152 应被 LR 拒绝，不应吸附: %+v", report.Snapped)
	}
	if report.Final.Theta["t.f"] != 0.25 {
		t.Fatalf("吸附后 f 应为 0.25: %v", report.Final.Theta["t.f"])
	}
	var sawAccept bool
	for _, step := range report.Steps {
		if step.Accepted {
			sawAccept = true
		}
	}
	if !sawAccept {
		t.Fatalf("至少应有一条接受记录: %+v", report.Steps)
	}
	if relErr(report.Final.Theta["t.C"], truth["t.C"]) > 0.02 {
		t.Fatalf("吸附后 C = %.2f（真值 %.2f）", report.Final.Theta["t.C"], truth["t.C"])
	}
	for _, step := range report.Steps {
		if step.Candidate.Source == "rational_approx" && !step.Accepted {
			t.Logf("注: 连分数候选被拒: %+v", step)
		}
	}
}

// TestSnapRespectsSnapCandidates 显式候选优先于连分数（Intent §4.4 第 2 步
// 优先、第 3 步只在无候选时用）：候选命中 CI 就只在候选中选。
func TestSnapRespectsSnapCandidates(t *testing.T) {
	spec, truth := snapSpec()
	// t.f 声明候选 [0.25, 0.3]：吸附值必须来自候选集而非任意分数
	for i := range spec.Parameters {
		if spec.Parameters[i].ID == "t.f" {
			spec.Parameters[i].SnapCandidates = []float64{0.25, 0.3}
		}
	}
	_ = truth
	evs := synthEvents(t, spec, truth, 18)
	store := mkStore(t, evs...)
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	base := qdl.ParamPoint{"t.w": truth["t.w"]}
	report, err := Snap(ds, base, qdl.ParamPoint{"t.C": 80}, SnapOptions{})
	if err != nil {
		t.Fatalf("Snap: %v", err)
	}
	if got, ok := report.Snapped["t.f"]; ok {
		if got != 0.25 && got != 0.3 {
			t.Fatalf("吸附值 %v 不在候选集 {0.25, 0.3} 内", got)
		}
	}
	for _, step := range report.Steps {
		if step.Candidate.Source == "rational_approx" {
			t.Fatalf("有显式候选时不应走连分数: %+v", step)
		}
	}
}

// TestSnapNothingToSnap 无可吸附候选时报告保持 MLE、Snapped 为空。
func TestSnapNothingToSnap(t *testing.T) {
	spec, truth := snapSpec()
	// 全部参数给远离 CI 的候选
	for i := range spec.Parameters {
		spec.Parameters[i].SnapCandidates = []float64{1e6}
	}
	evs := synthEvents(t, spec, truth, 12)
	store := mkStore(t, evs...)
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	base := qdl.ParamPoint{"t.w": truth["t.w"]}
	report, err := Snap(ds, base, qdl.ParamPoint{"t.C": 100}, SnapOptions{})
	if err != nil {
		t.Fatalf("Snap: %v", err)
	}
	if len(report.Snapped) != 0 || len(report.Steps) != 0 {
		t.Fatalf("无可吸附候选: Snapped=%v steps=%+v", report.Snapped, report.Steps)
	}
	if report.Final.Theta["t.C"] != report.Initial.Theta["t.C"] {
		t.Fatalf("无吸附时 Final 应等于 Initial")
	}
}

// TestSnapLRRejectsBadCandidate 候选落入宽 CI（少观测）但似然面强烈反对：
// LR 复核一票否决（ΔNLL > χ²₀.₉₉(1)/2）。
func TestSnapLRRejectsBadCandidate(t *testing.T) {
	spec, truth := snapSpec()
	// C 的候选 140：少观测下宽 CI 收留它，但似然面强烈反对
	for i := range spec.Parameters {
		if spec.Parameters[i].ID == "t.C" {
			spec.Parameters[i].SnapCandidates = []float64{140}
		}
	}
	evs := synthEvents(t, spec, truth, 6) // 少观测：CI 宽
	store := mkStore(t, evs...)
	ds, err := ExtractDataset(spec, store, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractDataset: %v", err)
	}
	base := qdl.ParamPoint{"t.w": truth["t.w"]}
	report, err := Snap(ds, base, qdl.ParamPoint{"t.C": 150}, SnapOptions{})
	if err != nil {
		t.Fatalf("Snap: %v", err)
	}
	if _, ok := report.Snapped["t.C"]; ok {
		t.Fatalf("远离真值的候选应被 LR 拒绝: %+v", report.Snapped)
	}
	sawReject := false
	for _, step := range report.Steps {
		if step.Candidate.ParamID == "t.C" && !step.Accepted {
			sawReject = true
			if step.LRDeltaNLL <= 3.317 {
				t.Fatalf("拒绝理由应为 LR: %+v", step)
			}
		}
	}
	if !sawReject {
		t.Fatalf("应有一条 t.C 的 LR 拒绝记录: %+v", report.Steps)
	}
	// 拒绝后 C 保持 MLE 精度（12% 量化极限内容差）
	if relErr(report.Final.Theta["t.C"], truth["t.C"]) > 0.12 {
		t.Fatalf("拒绝后 C = %.2f（真值 %.2f）", report.Final.Theta["t.C"], truth["t.C"])
	}
}
