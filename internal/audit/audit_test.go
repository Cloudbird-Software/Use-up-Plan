package audit

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/collect"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/estimate"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/ledger"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// ---- 测试脚手架：带 gauge 锚定的可辨识 plan ----

// auditSpec 与 estimate 测试同形（单 5h 桶、w frozen 作 gauge 锚、C/f 自由），
// 但带 ratecard 锚定声明——GaugeSummary 才会产出「等价 API 美元」读数，
// 这是 B6 审计报告的核心输出。
func auditSpec() (*qdl.PlanSpec, qdl.ParamPoint) {
	spec := &qdl.PlanSpec{
		ID: "t/audit@2026-08", Vendor: "t",
		Gauge: qdl.CalibrationGauge{
			Mode: "anchor_to_vendor_ratecard",
			RatecardUSDPerUnit: map[qdl.Dim]float64{
				qdl.DimInputTokens: 0.0008, // 锚定价 = 真值 w（理想锚定）
			},
		},
		Parameters: []qdl.Parameter{
			{ID: "t.C", Unit: "usd_equivalent", Prior: qdl.Distribution{
				Kind: qdl.DistNormal, Params: map[string]float64{"mu": 120, "sigma": 150}}},
			{ID: "t.w", Unit: "usd_per_token", Prior: qdl.Point(0.0008), Frozen: true,
				Provenance: qdl.Provenance("gauge")},
			{ID: "t.f", Unit: "usd_equivalent", Prior: qdl.Distribution{
				Kind: qdl.DistNormal, Params: map[string]float64{"mu": 0.1, "sigma": 150}}},
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
	return spec, qdl.ParamPoint{"t.C": 160, "t.w": 0.0008, "t.f": 0.3}
}

// synthTurns 生成确定性的请求序列（伪随机 token 数，无 rng 依赖）。
// 90 条 × 3min = 267min，全部落在同一 5h 窗内（不触发 reset），
// 累计消耗 ≈ 66% C——量化观测信息量充足的区间。
func synthTurns() []collect.ClaudeTurn {
	base := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	turns := make([]collect.ClaudeTurn, 90)
	for i := range turns {
		tokens := float64(200 + (i*37)%1800)
		turns[i] = collect.ClaudeTurn{
			Ts:    base.Add(time.Duration(i) * 3 * time.Minute),
			MsgID: "msg_" + strconv.Itoa(i), Model: "claude-sonnet-4-6",
			Dims: map[qdl.Dim]float64{qdl.DimInputTokens: tokens},
		}
	}
	return turns
}

// fullTheta 补全 θ 快照（IngestClaude 计算 EXACT 扣减需要全部参数）。
func fullTheta(truth qdl.ParamPoint) qdl.ParamPoint {
	return qdl.ParamPoint{"t.C": truth["t.C"], "t.w": truth["t.w"], "t.f": truth["t.f"]}
}

// mkStore 建临时事件库。
func mkStore(t *testing.T) ledger.Store {
	t.Helper()
	s, err := ledger.NewJSONLStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// obsAfter 用真值口径（EXACT 重放）算第 k 条请求后的已用百分比并取整。
func obsAfter(turns []collect.ClaudeTurn, truth qdl.ParamPoint, k int) float64 {
	U := 0.0
	for i := 0; i <= k; i++ {
		U += truth["t.f"] + truth["t.w"]*turns[i].Dims[qdl.DimInputTokens]
	}
	return math.Round(100 * U / truth["t.C"])
}

// ingestInterleaved 按统一时间轴交错入账：连续 turn 段成批 IngestClaude，
// 观测单独 Append——append-only 事件流的 seq 序必须与时间序一致
// （replayer 的硬前提），这就是增量导入的真实形态。
func ingestInterleaved(t *testing.T, store ledger.Store, spec *qdl.PlanSpec,
	turns []collect.ClaudeTurn, truth qdl.ParamPoint) {
	t.Helper()
	base := fullTheta(truth)
	var batch []collect.ClaudeTurn
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if _, err := IngestClaude(store, spec, batch, base, "v1", ""); err != nil {
			t.Fatalf("IngestClaude: %v", err)
		}
		batch = nil
	}
	for i, tu := range turns {
		batch = append(batch, tu)
		if i%5 == 4 { // 每 5 条请求后一条 usage endpoint 观测
			flush()
			y := obsAfter(turns, truth, i)
			if _, err := store.Append(tu.Ts.Add(time.Minute), &ledger.ObservationEvent{
				PlanID: spec.ID, BucketID: "b5", Semantic: qdl.SemUsedPct,
				RawValue:     strings.TrimSpace(formatFloat(y)),
				Quantization: qdl.Quantization{Kind: "integer"},
				Source:       qdl.ObsUsageEndpoint, Trust: 0.95,
			}); err != nil {
				t.Fatalf("Append 观测: %v", err)
			}
		}
	}
	flush()
}

// formatFloat 整数值不带小数点（与真实 usage endpoint 响应一致）。
func formatFloat(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ---- IngestClaude 单元 ----

// TestIngestClaudeOutOfOrderFails 乱序 turns 显式报错（时间轴是重放地基）。
func TestIngestClaudeOutOfOrderFails(t *testing.T) {
	spec, _ := auditSpec()
	store := mkStore(t)
	turns := synthTurns()
	turns[3], turns[7] = turns[7], turns[3]
	if _, err := IngestClaude(store, spec, turns[:10], qdl.ParamPoint{"t.w": 0.0008}, "v1", ""); err == nil {
		t.Fatal("乱序 turns 应报错")
	}
}

// TestIngestClaudeDeltasExact 入账的 bucket_deltas 是 EXACT 记账口径
// （flat + w·tokens），且 dims 原始物理量原样保留。
func TestIngestClaudeDeltasExact(t *testing.T) {
	spec, truth := auditSpec()
	store := mkStore(t)
	turns := synthTurns()[:3]
	n, err := IngestClaude(store, spec, turns, fullTheta(truth), "v1", "claude_code")
	if err != nil || n != 3 {
		t.Fatalf("IngestClaude: n=%d err=%v", n, err)
	}
	i := 0
	err = store.Iterate(func(ev ledger.Event) error {
		if ev.Charge == nil {
			t.Fatalf("应只有 charge 事件，得 %s", ev.Type)
		}
		tu := turns[i]
		want := truth["t.f"] + truth["t.w"]*tu.Dims[qdl.DimInputTokens]
		got := ev.Charge.BucketDeltas["b5"]
		if math.Abs(got-want) > 1e-12 {
			t.Fatalf("第 %d 条扣减 %v ≠ EXACT 口径 %v", i, got, want)
		}
		if ev.Charge.RequestID != tu.MsgID || ev.Charge.Model != tu.Model {
			t.Fatalf("身份字段丢失: %+v", ev.Charge)
		}
		if ev.Ts != tu.Ts {
			t.Fatalf("时间戳 %v ≠ %v", ev.Ts, tu.Ts)
		}
		i++
		return nil
	})
	if err != nil || i != 3 {
		t.Fatalf("遍历: i=%d err=%v", i, err)
	}
}

// ---- 端到端 ----

// TestRunEndToEnd B6 主验收：合成请求流 + usage endpoint 观测 →
// 在线估计 + 离线后验 → C 的 90% 区间覆盖真值 → gauge 读数给出
// 「等价 API 美元」→ 对账归因为量化噪声（管线各层契约的集成验证）。
func TestRunEndToEnd(t *testing.T) {
	spec, truth := auditSpec()
	turns := synthTurns()
	store := mkStore(t)
	ingestInterleaved(t, store, spec, turns, truth)

	rep, err := Run(Options{
		Spec: spec, Store: store,
		Base:   qdl.ParamPoint{"t.w": truth["t.w"]},
		Theta0: qdl.ParamPoint{"t.C": 80}, // 初值错一半
		Posterior: estimate.PosteriorOptions{
			Seed: 7, BurnIn: 600, Samples: 400, Thin: 6,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.NObs != 18 {
		t.Fatalf("观测点数 %d ≠ 18", rep.NObs)
	}
	// C 的 90% 可信区间覆盖真值（Phase 1 验收口径）
	s := rep.Posterior.Summary["t.C"]
	if s.Q05 > truth["t.C"] || s.Q95 < truth["t.C"] {
		t.Fatalf("C 的 90%% 区间 [%.1f, %.1f] 未覆盖真值 %.1f", s.Q05, s.Q95, truth["t.C"])
	}
	// gauge 读数：容量 = 等价 API 美元（值 = 估计的 C_hat，非先验）
	found := false
	for _, r := range rep.Reads {
		if r.Kind == "capacity_usd_equiv" && r.Subject == "b5" {
			found = true
			if relErr(r.Value, truth["t.C"]) > 0.15 {
				t.Fatalf("等价美元读数 %.2f 偏离真值 %.2f 超 15%%", r.Value, truth["t.C"])
			}
		}
	}
	if !found {
		t.Fatalf("缺 capacity_usd_equiv 读数: %+v", rep.Reads)
	}
	// 对账归因：合成数据的残差只应来自整数取整 → 量化噪声
	if len(rep.Recon.Buckets) != 1 || rep.Recon.Buckets[0].Attribution != ledger.AttributionQuantNoise {
		t.Fatalf("归因应为量化噪声: %+v", rep.Recon.Buckets)
	}
}

// TestRunSkipPosterior 在线模式（跳过后验）可用，报告渲染不炸。
func TestRunSkipPosterior(t *testing.T) {
	spec, truth := auditSpec()
	turns := synthTurns()[:20]
	store := mkStore(t)
	ingestInterleaved(t, store, spec, turns, truth)
	rep, err := Run(Options{
		Spec: spec, Store: store, SkipPosterior: true,
		Base: qdl.ParamPoint{"t.w": truth["t.w"]}, Theta0: qdl.ParamPoint{"t.C": 100},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Posterior != nil {
		t.Fatal("SkipPosterior 时后验应为 nil")
	}
	var b strings.Builder
	if err := rep.Render(&b); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(b.String(), "离线后验未运行") {
		t.Fatalf("在线模式报告形态错误:\n%s", b.String())
	}
}

// TestRenderShape 报告渲染：三段关键内容（后验区间 / 等价美元 / 归因表）。
func TestRenderShape(t *testing.T) {
	spec, truth := auditSpec()
	turns := synthTurns()
	store := mkStore(t)
	ingestInterleaved(t, store, spec, turns, truth)
	rep, err := Run(Options{
		Spec: spec, Store: store,
		Base:   qdl.ParamPoint{"t.w": truth["t.w"]},
		Theta0: qdl.ParamPoint{"t.C": 80},
		Posterior: estimate.PosteriorOptions{
			Seed: 7, BurnIn: 400, Samples: 200, Thin: 6,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var b strings.Builder
	if err := rep.Render(&b); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := b.String()
	for _, want := range []string{
		"审计报告", "t.C", "等价 API 美元", "残差归因", "b5", "quantization_noise",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("报告缺关键段 %q:\n%s", want, out)
		}
	}
}

// TestIngestFromJSONLFile 全链路：磁盘 JSONL → collect 解析 → 入账 → 审计。
func TestIngestFromJSONLFile(t *testing.T) {
	spec, truth := auditSpec()
	turns := synthTurns()
	// 写成 Claude Code 会话日志形状（只含计量必需字段）
	var b strings.Builder
	for _, tu := range turns {
		b.WriteString(`{"type":"assistant","timestamp":"` + tu.Ts.UTC().Format(time.RFC3339Nano) +
			`","message":{"id":"` + tu.MsgID + `","model":"` + tu.Model +
			`","usage":{"input_tokens":` + formatFloat(tu.Dims[qdl.DimInputTokens]) +
			`,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}` + "\n")
	}
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "session.jsonl"), b.String()); err != nil {
		t.Fatal(err)
	}
	parsed, err := collect.LoadClaudeLogs(dir)
	if err != nil {
		t.Fatalf("LoadClaudeLogs: %v", err)
	}
	if len(parsed) != len(turns) {
		t.Fatalf("解析 %d 条 ≠ 生成 %d 条", len(parsed), len(turns))
	}
	store := mkStore(t)
	if _, err := IngestClaude(store, spec, parsed, fullTheta(truth), "v1", ""); err != nil {
		t.Fatalf("IngestClaude: %v", err)
	}
	n := 0
	_ = store.Iterate(func(ev ledger.Event) error {
		if ev.Charge != nil {
			n++
		}
		return nil
	})
	if n != len(turns) {
		t.Fatalf("入账 %d 条 ≠ %d", n, len(turns))
	}
}

func relErr(got, want float64) float64 {
	return math.Abs(got-want) / math.Abs(want)
}
