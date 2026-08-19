package audit

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/estimate"
)

// Render 输出人类可读审计报告。结构五段：头（数据面规模）→ 参数后验
// （90% 可信区间，Phase 1 验收主口径）→ gauge 读数（等价 API 美元——
// 「真实份额审计」的核心产出）→ 对账归因（预测 vs 观测为什么对不上）→
// 诊断（链健康度）。行宽对齐人类阅读，不是机器格式——机器消费用结构体。
func (rep *Report) Render(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "══ Use-up-Plan 审计报告：%s ══\n", rep.PlanID)
	fmt.Fprintf(&b, "生成于 %s · 观测点 %d · 在线估计 %d 次求值（%s）\n\n",
		rep.GeneratedAt.Format("2006-01-02 15:04:05 MST"), rep.NObs,
		rep.Online.FuncEvals, onelineStatus(rep.Online))

	// ── 参数后验（90% 可信区间）──
	if rep.Posterior != nil {
		ids := append([]string(nil), rep.Posterior.FreeIDs...)
		sort.Strings(ids)
		b.WriteString("── 参数后验（90% 可信区间）──\n")
		for _, id := range ids {
			s := rep.Posterior.Summary[id]
			fmt.Fprintf(&b, "  %-28s 中位 %-12.4g  [Q05 %-10.4g, Q95 %-10.4g]  ESS %.0f\n",
				id, s.Q50, s.Q05, s.Q95, rep.Posterior.ESS[id])
		}
		fmt.Fprintf(&b, "  采样诊断：接受率 %.2f（健康带 0.05–0.6）\n\n", rep.Posterior.AcceptRate)
	} else {
		b.WriteString("── 参数点估计（离线后验未运行）──\n")
		ids := append([]string(nil), rep.Online.FreeIDs...)
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(&b, "  %-28s %-.6g\n", id, rep.Online.Theta[id])
		}
		b.WriteString("\n")
	}

	// ── gauge 读数（等价 API 美元）──
	if len(rep.Reads) > 0 {
		b.WriteString("── gauge 读数（gauge = 等价 API 美元锚定）──\n")
		for _, r := range rep.Reads {
			fmt.Fprintf(&b, "  %s\n", r.Interpretation)
		}
		b.WriteString("\n")
	}

	// ── 对账归因表 ──
	if rep.Recon != nil && len(rep.Recon.Buckets) > 0 {
		b.WriteString("── 残差归因（预测 vs 观测为什么对不上）──\n")
		for _, br := range rep.Recon.Buckets {
			fmt.Fprintf(&b, "  桶 %-14s %s：观测 %d 条，均值残差 %+.2f pct\n    证据：%s\n",
				br.BucketID, br.Attribution, br.N, br.MeanResidual, br.Evidence)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// onelineStatus 压缩优化器终止状态为一行短语。
func onelineStatus(r *estimate.Result) string {
	if r.Converged {
		return "已收敛"
	}
	return "迭代上限截断: " + r.Status
}
