package audit

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/collect"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// seedPlan 加载仓库根 qdl/plans/ 下的种子 plan（跨模块 golden：种子文件
// 是真实 plan 契约面，审计管线必须在其上可运行或给出可诊断错误——
// 评审发现 B6 端到端只测了合成 spec，真实 plan 上的失败因此漏网）。
func seedPlan(t *testing.T, rel ...string) *qdl.PlanSpec {
	t.Helper()
	parts := append([]string{"..", "..", "qdl", "plans"}, rel...)
	spec, err := qdl.Load(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("加载种子 plan: %v", err)
	}
	return spec
}

func smokeTurns(model string) []collect.ClaudeTurn {
	base := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	var turns []collect.ClaudeTurn
	for i := 0; i < 5; i++ {
		turns = append(turns, collect.ClaudeTurn{
			Ts: base.Add(time.Duration(i) * time.Minute), MsgID: "smoke-" + model + "-" + string(rune('a'+i)),
			Model: model,
			Dims: map[qdl.Dim]float64{
				qdl.DimInputTokens: 1000, qdl.DimCacheWriteTokens: 200,
				qdl.DimCacheReadTokens: 5000, qdl.DimOutputTokens: 300,
			},
		})
	}
	return turns
}

// TestSeedPlanAuditSmokeKnownAnchor 锚点已知的种子 plan（zai：首用起锚窗）
// 必须端到端走通：入账 → 在线估计 → gauge 读数 → 对账。
func TestSeedPlanAuditSmokeKnownAnchor(t *testing.T) {
	spec := seedPlan(t, "zai", "glm-coding-max@2026-08.qdl.yaml")
	store := mkStore(t)
	if _, err := IngestClaude(store, spec, smokeTurns("glm-5.1"), ThetaFromPrior(spec), "v0", "glm_anthropic_compat"); err != nil {
		t.Fatalf("IngestClaude: %v", err)
	}
	rep, err := Run(Options{Spec: spec, Store: store, SkipPosterior: true, Theta0: ThetaFromPrior(spec)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep == nil || rep.Recon == nil {
		t.Fatalf("报告不完整: %+v", rep)
	}
}

// TestSeedPlanAuditSmokeUnknownAnchor anthropic/max20 的周窗 anchor_utc=UNKNOWN
// （待 C3 结构辨识写回）。当前管线在其上必然失败——但失败必须是可诊断的：
// 错误信息指认「锚点未知」与桶 ID，而不是「初始点目标值 +Inf」天书。
// C3 落地写回锚点后，本测试应翻转为断言成功。
func TestSeedPlanAuditSmokeUnknownAnchor(t *testing.T) {
	spec := seedPlan(t, "anthropic", "max20@2026-08.qdl.yaml")
	store := mkStore(t)
	if _, err := IngestClaude(store, spec, smokeTurns("claude-sonnet-4-6"), ThetaFromPrior(spec), "v0", "claude_code_oauth"); err != nil {
		t.Fatalf("IngestClaude: %v", err)
	}
	_, err := Run(Options{Spec: spec, Store: store, SkipPosterior: true, Theta0: ThetaFromPrior(spec)})
	if err == nil {
		t.Fatal("锚点未知时 Run 应报错（待 C3 写回锚点后本断言翻转为成功）")
	}
	if !strings.Contains(err.Error(), "锚点未知") || !strings.Contains(err.Error(), "b_7d") {
		t.Fatalf("错误必须可诊断（指认锚点与桶 ID）: %v", err)
	}
}
