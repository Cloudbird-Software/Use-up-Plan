// Command use-up-plan 是 LLM 订阅额度「智能调度 + 真实份额审计」系统的服务入口。
//
// 当前状态：初始化完成，Phase 0（QDL 类型与语义内核）尚未开始。
// 需求与全量设计见仓库根 Intent.md；模块布局与依赖提案见 docs/ARCHITECTURE.md。
package main

import (
	"fmt"
	"os"
)

// version 由发布构建注入：go build -ldflags "-X main.version=v0.1.0"。
var version = "dev"

const usage = `use-up-plan —— LLM 订阅额度调度与审计系统（开发中）

用法：
  use-up-plan version   打印版本
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println("use-up-plan", version)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
