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
- internal/semantics（A5）：`Charge` 扣减双模式——EXACT（记账，ceil/floor/max
  精确应用）与 LINEAR_EV（规划，量化取期望、max 取线性上界，仿射可进 LP），
  外加 `ChargeUpperBound` 严格上界；`Admit` 准入三态（DENY_ADMISSION /
  DENY_QUOTA（带 retry_after）/ ALLOW_WITH_RISK（p_break）> ALLOW），含
  瞬时约束（并发/上下文峰值/模型清单）与 glob 最长 pattern 倍率匹配。
- qdl/plans 种子计划（A6）：Anthropic Max20（不透明单元锚定价目表 + 共享池 +
  模型族专用窗）、GLM Coding Max（per-request 计量，token 边际成本为 0 的
  结构套利形态）、免费档模板（RPM/RPD/TPM 多维硬桶 + 高中断率通道）；
  golden 加载契约（`TestSeedPlansLoad`/`RoundTrip`）与语义层行为测试
  （桶命中 / per-request 扣减 / 三态准入）。
- internal/ledger（B1）：Intent §3.3 事件溯源落地——六种事件类型
  （charge / observation / wall_hit / reset_observed / param_update /
  structure_update）+ JSONL 信封；`Store` 深接口与 `JSONLStore` 文件实现
  （AR-7：JSONL 起步，SQL 后置 Phase 3+）：追加写 + fsync、重开恢复续号、
  崩溃半写残行截断、中间坏行报错；存储边界统一负载校验与凭证脱敏
  （`Sanitize`：api key / bearer / JWT / AWS / GitHub PAT / key=value）。
  ChargeEvent.Dims 只存原始物理量、WallHitEvent.LedgerSnapshot 强制非空
  （Σwx=C 方程）由校验强制。
- qdl/semantics 新增 `model_family` 作用域层级与模型族前缀匹配——支撑
  Claude Sonnet/Opus 周限专用窗；`ChargeOne` 倍率改为乘在 `(flat+Σ)` 整体，
  per-request 桶（flat=1、terms 空）的模型倍率由此生效。

### Changed

- 按 languages.yaml 应用层默认政策选定语言为 Go：移除 TypeScript 脚手架，落地 Go 工具链
  （go.mod、cmd/use-up-plan、tools/archlint 架构门禁），Makefile 目标改为 Go 等价实现
  （check 目标不变），dependabot 切换 npm → gomod。
- CI 语言面收口（ADR-0028）：check job `runtime: node` → `runtime: go`（go-version 1.25.1）；
  push 面 deps-audit 由 npm audit 换 govulncheck@v1.7.0。
- 治理收口：REPOS.yaml 申报入图（.github PR #77，ADR-0024）；ADR-0028 记录 Go 语言基线决策；
  首批依赖提案获 owner 批准（2026-08-19）。
