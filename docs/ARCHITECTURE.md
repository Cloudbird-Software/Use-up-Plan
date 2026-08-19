# 架构纪律（每个模块都必须遵守）

> 从 AGENTS.md 拆出以省上下文。新建模块、动模块边界、review 时读。

## 语言决策（new_repo 首 PR 要求，policy/languages.yaml）

- 应用层：**Go**（组织默认）。TypeScript 仅限 frontend-isomorphic，本仓不适用——模板 TS 脚手架已移除。
- llm_prompt 层：当前无内嵌 prompt；未来若有，按政策走 BAML / Python 并独立目录隔离。
- data 层（SQL，禁重 ORM）与 infrastructure 层（terraform / compose）均未落地，落地时按政策执行。
- 语言更换 = 重新立项（languages.yaml `language_change`）。

## Go 版架构纪律

1. **每个模块一个 public entry**：`internal/<模块>/` 根包是唯一对外面；跨模块只能 import 对方根包，
   禁止深入子包。`make arch`（tools/archlint）机器执法。
2. **根包不导出内部实现类型**——Go 包可见性天然强制一半（未导出即不可见）；review 仍查导出面是否过宽。
3. **每个模块目录一份 `AGENTS.md`**：写清该模块负责什么、不变量是什么、禁止做什么、如何独立验证。
4. **契约测试在模块边界**，实现细节内部自由。模块内大改而外部测试不动。
5. **模块大小上限 3000 行**。超过就拆——一个模块必须能被 agent 一次性完整读完。
6. **生成代码进独立目录**（`*.gen.go`、`gen/`），禁止手改。
7. **接口设计标准**：一个 LLM 能否仅凭函数签名 + 一行注释零样本正确使用？否 => 接口太浅，重做。
8. **测试优先级**：行为不变量用 property-based（`go test`，随机化输入 + 不变量断言），关键输出用
   golden test（T-09）。先写不变量，再写实现（T-01/T-04：解析器必须带 fuzz 种子）。

## 模块映射（Intent.md §9.1 → Go 布局）

| Intent 目录 | Go 包               | 职责                                         | 落地阶段 |
| ----------- | ------------------- | -------------------------------------------- | -------- |
| qdl/        | `internal/qdl`      | QDL 类型 + YAML 加载校验（最底层，零依赖）    | Phase 0  |
| semantics/  | `internal/semantics`| advance / charge / admit / replay（纯函数内核）| Phase 0 |
| ledger/     | `internal/ledger`   | append-only 事件存储 + 状态重建 + 残差归因     | Phase 1  |
| estimate/   | `internal/estimate` | 量化似然、在线点估计、离线后验、整数吸附、结构选择、漂移 | Phase 1–2 |
| probe/      | `internal/probe`    | 结构探针剧本库 + 执行器                        | Phase 2  |
| collect/    | `internal/collect`  | 响应头 / usage endpoint / 本地日志 / 网页采集  | Phase 3  |
| cred/       | `internal/cred`     | 凭证加密存储 + refresh + 健康度                | Phase 3  |
| plan/       | `internal/planner`  | LP 构造求解 + 影子价格表                       | Phase 4  |
| value/      | `internal/value`    | 等价美元换算 + 折扣率 / 利用率报表             | Phase 4  |
| route/      | `internal/route`    | 路由服务 + 网关 hook                           | Phase 5  |
| evals/      | `internal/evals`    | 私有题库跑分                                  | Phase 6  |
| ops/        | `ops/`（非 Go 包）  | docker-compose、迁移、仪表盘                   | 按需     |
| —           | `cmd/use-up-plan`   | 服务入口（GO-3：main 仅在 cmd/ 与 tools/）     | 已落地   |
| —           | `tools/archlint`    | 架构边界门禁                                   | 已落地   |

边界规则登记表在 [tools/archlint/main.go](../tools/archlint/main.go) 的 `internalModuleRules`；
**模块落地 PR 必须同步登记（MOD-5），否则 `make arch` 失败**。

## 依赖规则与提案（审批前零第三方依赖）

新增依赖前先列「依赖名 / 用途 / 许可证 / 是否能用标准库替代」等人批（approver：CODEOWNERS owner）；
禁止引入 AGPL / GPL-3.0 / SSPL 的库。当前 = **零第三方依赖**（纯标准库）。已批准待落地
（2026-08-19 owner 授权，随对应 Phase PR 引入）：

| 名称                       | 用途                        | 许可证        | 标准库可否替代                    |
| -------------------------- | --------------------------- | ------------- | --------------------------------- |
| github.com/goccy/go-yaml   | QDL YAML 解析               | MIT           | 否（标准库无 YAML）               |
| gonum.org/v1/gonum         | 数值优化 / 统计（辨识层）    | BSD-3-Clause  | 否（标准库无数值栈）              |
| github.com/kisielk/errcheck| GO-2 errcheck 门禁          | MIT           | 否（go vet 不覆盖未检查错误）     |
| go.uber.org/goleak         | T-03 goroutine 泄漏检测     | MIT           | 否（长驻进程落地时启用）          |

选型备注：gopkg.in/yaml.v3 已归档停维，故提案 goccy/go-yaml（活跃、纯 Go）。
LP 求解器（Phase 4 前）与贝叶斯 NUTS 选型另行提案——Go 生态无 highspy / numpyro 等价物，
需评估 HiGHS cgo 绑定 vs 纯 Go 实现并附对比，届时决定。

## 治理收口记录（2026-08-19，原「已知人工待办」全部消除）

1. ci.yml：check job 已切 `runtime: go`（go-version "1.25.1"）；push 面 deps-audit 已由
   npm audit 换 govulncheck@v1.7.0——决策记录见 ADR-0028（agent-registry/decisions）。
2. adr-required：首个 C1 面 PR 引用 ADR-0028（Go 语言基线）；建仓申报与 bootstrap 直推
   豁免登记见 ADR-0024（agent-registry PR #36 / .github PR #77）。
3. governance/REPOS.yaml 已申报本仓（GM-4，.github PR #77）。
4. 依赖提案已获 owner 批准（2026-08-19）：goccy/go-yaml（MIT）、gonum.org/v1/gonum
   （BSD-3-Clause）、errcheck（MIT）、goleak（MIT）——按 Phase 落地引入。

## Phase 0 验收标准（开发起点，来自 Intent.md §9.2）

- 手写 3 个 plan 的 QDL（Anthropic Max / per-request 计量国产档 / 免费档模板）并通过校验
- `advance` 可组合性（advance(advance(s,a,b),b,c) == advance(s,a,c)）property test 通过
- `charge` 的 EXACT / LINEAR_EV 双模式在合成数据上差值符合预期
- 全部零网络调用，`make check` 全绿
