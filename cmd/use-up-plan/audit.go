package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/audit"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/collect"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/estimate"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/ledger"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// runAudit 实现 `use-up-plan audit`：端到端审计报告（ROADMAP B6）——
// Claude Code JSONL 入账 → 在线估计 + 离线后验 → gauge 读数（等价 API
// 美元）+ 残差归因表。退出码：0 正常；2 用法错误；1 运行错误。
func runAudit(args []string) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	planPath := fs.String("plan", "", "QDL 计划文件路径（必填）")
	ledgerPath := fs.String("ledger", "", "事件库 JSONL 路径（必填，不存在则创建）")
	claudeDir := fs.String("claude-dir", "", "Claude Code 会话日志根目录（默认 ~/.claude/projects；空串跳过导入）")
	outPath := fs.String("out", "", "报告输出文件（默认 stdout）")
	skipPosterior := fs.Bool("skip-posterior", false, "跳过离线后验（纯在线模式）")
	seed := fs.Uint64("seed", 1, "离线后验 PCG 种子（确定性复现）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *planPath == "" || *ledgerPath == "" {
		fmt.Fprintln(os.Stderr, "audit: -plan 与 -ledger 必填")
		return 2
	}

	spec, err := qdl.Load(*planPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit: 加载计划: %v\n", err)
		return 1
	}
	store, err := ledger.NewJSONLStore(*ledgerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit: 打开事件库: %v\n", err)
		return 1
	}
	defer store.Close()

	// 历史日志导入（可选）：首次导入用先验中心作 θ 快照——存量 deltas
	// 只作历史对照，θ 重估后 Recompute 重放会重算（theta_version 配对语义）。
	if *claudeDir != "" {
		dir := *claudeDir
		if dir == "default" {
			if dir, err = collect.DefaultClaudeProjectsDir(); err != nil {
				fmt.Fprintf(os.Stderr, "audit: 定位会话日志目录: %v\n", err)
				return 1
			}
		}
		turns, err := collect.LoadClaudeLogs(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "audit: 解析会话日志: %v\n", err)
			return 1
		}
		theta := audit.ThetaFromPrior(spec)
		n, err := audit.IngestClaude(store, spec, turns, theta, priorVersionTag(), "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "audit: 入账 %d 条后失败: %v\n", n, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "audit: 已入账 %d 条 Claude Code 请求\n", n)
	}

	// gauge 基线：frozen 参数的先验值（价目表锚定），其余参数自由估计。
	prior := audit.ThetaFromPrior(spec)
	base := qdl.ParamPoint{}
	for i := range spec.Parameters {
		if spec.Parameters[i].Frozen {
			base[spec.Parameters[i].ID] = prior[spec.Parameters[i].ID]
		}
	}
	rep, err := audit.Run(audit.Options{
		Spec: spec, Store: store, Base: base, Theta0: prior,
		SkipPosterior: *skipPosterior,
		Posterior:     estimate.PosteriorOptions{Seed: *seed},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit: %v\n", err)
		return 1
	}

	out := os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "audit: 创建报告文件: %v\n", err)
			return 1
		}
		defer f.Close()
		out = f
	}
	if err := rep.Render(out); err != nil {
		fmt.Fprintf(os.Stderr, "audit: 渲染报告: %v\n", err)
		return 1
	}
	return 0
}

// priorVersionTag 生成 theta_version 标签（导入时刻 + prior 标记）。
func priorVersionTag() string {
	return "prior@" + time.Now().UTC().Format("20060102T150405Z")
}
