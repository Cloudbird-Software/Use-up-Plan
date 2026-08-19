# Changelog

本文件记录对外可见的变更。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

## [Unreleased]

### Added

- 初始模板工程（CI gate / hygiene / dependabot / automerge 全套护栏）。
- docs/ROADMAP.md：Phase 0–6 开发规划（PR 序列 / 验收标准 / 选型待决 / 风险登记）。
- archlint MOD-1 执法面收口（评审 #1）：_test.go 的 import（TestImports / XTestImports）
  纳入深导入检查；build tag 盲区登记为 ROADMAP R7 已知限制。
- internal/qdl：QDL 类型系统落地（A1/A2）——维度分类学、Coeff 双态系数、ISO 8601
  Duration、窗口/作用域/扣减规则/观测绑定/通道/PlanSpec 与安全契约校验。
- internal/qdl 加载器（A3）：`Load`/`LoadBytes`/`Marshal`——YAML 严格解码、文档内
  `$ref` 展开（JSON pointer 扩展 + 深拷贝防共享）、缺省规范化、封闭集校验、
  黄金样本往返测试与 fuzz（goccy/go-yaml 落地）。
- 全类型 YAML 序列化契约：往返稳定（`LoadBytes(Marshal(s))` 语义等价），空集合
  `omitempty` 收敛，类别型离散分布 `probs`+`categories` 归一到 `CategoryProbs`。
- internal/semantics（A4）：`ResolveBucket`（qdl→纯几何参数求值层）与
  `Advance`（时间推进纯函数，八窗型分派 + ResetPolicy 归位）；可组合性
  `advance(advance(s,a,b),b,c) == advance(s,a,c)` 由固定种子 property test
  强制（U 浮点 1e-9 相对容差）。

### Changed

- 按 languages.yaml 应用层默认政策选定语言为 Go：移除 TypeScript 脚手架，落地 Go 工具链
  （go.mod、cmd/use-up-plan、tools/archlint 架构门禁），Makefile 目标改为 Go 等价实现
  （check 目标不变），dependabot 切换 npm → gomod。
- CI 语言面收口（ADR-0028）：check job `runtime: node` → `runtime: go`（go-version 1.25.1）；
  push 面 deps-audit 由 npm audit 换 govulncheck@v1.7.0。
- 治理收口：REPOS.yaml 申报入图（.github PR #77，ADR-0024）；ADR-0028 记录 Go 语言基线决策；
  首批依赖提案获 owner 批准（2026-08-19）。
