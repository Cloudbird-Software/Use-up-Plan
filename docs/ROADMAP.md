# Use-up-Plan 开发规划（ROADMAP）

> 版本：v2（2026-08-19）｜维护：随 Phase 推进更新｜上游输入：[Intent.md](../Intent.md) §9、
> [ARCHITECTURE.md](ARCHITECTURE.md) 模块映射、治理 policy/languages.yaml + policy/testing.yaml。

## 1. 现状基线（T1 = 2026-08-19）

已就绪：

- Go 骨架：`go.mod`（go 1.25.1）、`cmd/use-up-plan` 入口、`tools/archlint` 架构门禁（GO-3/MOD-1/MOD-5）、
  `internal/qdl` 模块章程；`make check` 本地全绿；零第三方依赖。
- 文档：README（项目定位）、ARCHITECTURE（语言决策/模块映射/依赖提案）、本规划。
- 治理收口（T0 阻塞项已全部消除）：
  1. ci.yml check job 已切 `runtime: go`（go-version 1.25.1）；push 面 deps-audit 已由
     npm audit 换 govulncheck@v1.7.0（本仓首个正式 PR，ADR-0028）。
  2. adr-required 引用 ADR-0028（Go 语言基线，agent-registry/decisions）；建仓申报与
     bootstrap 直推豁免见 ADR-0024（agent-registry PR #36 / .github PR #77）。
  3. governance/REPOS.yaml 已申报本仓（GM-4，.github PR #77）。
  4. 依赖提案（goccy/go-yaml / gonum / errcheck / goleak）已获 owner 批准（2026-08-19），
     清单与状态见 ARCHITECTURE.md；随对应 Phase PR 落地引入。

非阻塞遗留（owner 个人安全卫生，与本仓开发无关）：撤销曾在对话/日志中泄露的个人 PAT。

## 2. 规划原则

1. **PR 纪律**（AGENTS.md 硬规则 5）：一个 PR 一个模块/一件事，diff < 400 行；模块落地三件套——
   archlint 登记（MOD-5）+ 模块 AGENTS.md + 边界契约测试。
2. **离线先行**（Intent §8.3）：Ledger/Estimator 正确性先在离线历史日志上验证，网关最后接——
   避免同时调试「路由抖动」和「参数不收敛」两个纠缠问题。
3. **先 Codex 后 Anthropic**（Intent §5.1）：Codex 响应头为一位小数百分比（量化步长 0.1 vs 1.0），
   参数收敛快约 10 倍，先用它验证辨识管线正确性。
4. **每阶段独立可停**：Phase 2 结束即拥有「真实额度审计」核心产出；Phase 4 起产出调度节省。
5. **测试政策映射**（policy/testing.yaml）：

| 政策项 | 本仓落点 | 状态 |
| ------ | -------- | ---- |
| T-01 unit/property/golden | 各模块 `go test` + 不变量断言 + golden fixtures | 持续 |
| T-02 race（go） | `make test` 已带 `-race` | 已生效 |
| T-04 fuzz 种子 | 解析器/加载器：qdl loader、collect headers、locallog | 对应 PR 落地 |
| T-09 differential/golden | QDL 加载 golden、charge EXACT vs LINEAR_EV 差分、replay 重放对比 | Phase 0–1 |
| G-07 状态模型测试 | `advance()` 可组合性（BucketState 是典型 complex_state_entity） | Phase 0 |
| T-10 mutation 周跑 | go-mutesting（score<60% 模块告警） | Phase 1 后引入 |
| T-06 license_scan | 依赖落地时 | 随首批依赖 |

## 3. 里程碑总表

| Phase | 目标 | PR 序列 | 核心交付 | 验收要点 | 外部依赖 |
| ----- | ---- | ------- | -------- | -------- | -------- |
| 0 | QDL 类型 + 语义内核 | A1–A5 | PlanSpec 类型/加载校验；advance/charge/admit 纯函数 | 3 个种子 plan 过校验；可组合性 property test；零网络 | go-yaml（A2 起） |
| 1 | Ledger + 离线辨识 | B1–B6 | append-only 事件库；量化似然估计器；残差归因 | C_5h/C_7d 后验 90% 区间 < 中位数 40%；注入故障归因正确 | gonum |
| 2 | 结构辨识 | C1–C3 | 窗口语义/计量粒度判定 + 探针剧本库 | 合成数据判定后验 > 0.9 | — |
| 3 | 观测三通道 + 凭证 | D1–D4 | 响应头/endpoint/本地日志采集；凭证加密与 refresh | 三通道入账；凭证失效优雅降级（NEEDS_HUMAN） | web 采集选型（D4） |
| 4 | LP + 影子价格 | E1–E3 | LP 求解与对偶价；等价美元/折扣率报表 | 50 桶×50 类求解 < 100ms；反事实重放节省报告 | LP solver 选型（E1） |
| 5 | 在线路由 | F1–F3 | Route/Report 服务；网关 hook；瞬切+hysteresis | 路由 p99 < 2ms；SPILL_TO_PAYG guard 演练 | 网关选型 |
| 6 | eval + 漂移监控 | G1–G2 | 私有题库跑分；CUSUM + 文档语义 diff 2×2 表 | 注入系数变更 24h 内告警 | eval 框架选型 |

## 4. PR 级拆解

### Phase 0 —— QDL + 语义内核

| PR | 内容 | 关键点 |
| -- | ---- | ---- |
| A1 | `internal/qdl` 核心类型（Dim/Window/Scope/ChargeRule/Bucket/Parameter/PlanSpec/gauge） | 零依赖纯类型；archlint 登记；Coeff=常量或 ParamRef 的双态类型 |
| A2 | `internal/qdl` YAML 加载器 + 校验 | ParamRef 可解析性；SPILL_TO_PAYG 必须 requires_explicit_enable（缺省拒绝）；vendor_doc 永不 frozen；fuzz 种子 + 首批 golden fixtures |
| A3 | `internal/semantics` advance() | 按 WindowKind 分派；可组合性 advance(advance(s,a,b),b,c)==advance(s,a,c) 用随机时间序列做 property test（G-07） |
| A4 | `internal/semantics` charge() 双模式 + admit() 三态 | EXACT（记账）vs LINEAR_EV（规划）差分测试；DENY_ADMISSION/DENY_QUOTA/ALLOW_WITH_RISK |
| A5 | `qdl/plans/` 3 个种子 plan 文件 | Anthropic Max20（不透明分+共享池）/ GLM per-request（结构套利对照）/ 免费档模板（多维硬桶）；加载 golden |

验收（Intent §9.2 Phase 0）：全部通过 + `make check` 绿 + 零网络调用。

### Phase 1 —— Ledger + 离线辨识（价值密度最高）

| PR | 内容 | 关键点 |
| -- | ---- | ---- |
| B1 | `internal/ledger` 事件类型 + append-only 存储 | AR-7：事件→数据层 JSONL 起步（SQL 迁移后置到 Phase 3+ 按数据层政策提案）；ChargeEvent.dims 存原始物理量 |
| B2 | replay + reconcile | theta_version 重放；残差归因表（量化噪声/外生消耗/系数漂移/结构错判四类模式） |
| B3 | `internal/estimate` 量化似然 + 在线点估计 | gonum optimize（BFGS 族 + 边界）；warm-start 增量更新 |
| B4 | gauge fixing + 整数吸附 | 价目表锚定（frozen）；snap_candidates 吸附 + 连分数逼近（PSLQ 的 Go 等价简化） |
| B5 | 离线后验 | Go 无 NUTS：Laplace 近似或自实现 Metropolis-Hastings（选型注记见 §5）；在线/离线写同一 ParamUpdateEvent 流 |
| B6 | 端到端首份审计报告 | Claude Code JSONL → 转换器 → 估计管线 → C_5h/C_7d「等价 API 美元」报告 |

验收：后验 90% 区间 < 中位数 40%；人工注入「权重改变/外生消耗/未建模 flat」三类故障，归因表全部命中。

### Phase 2 —— 结构辨识（产出永久性知识）

| PR | 内容 | 关键点 |
| -- | ---- | ---- |
| C1 | 结构模型选择 | 候选枚举 + BIC/边际似然打分；kind_posterior 更新 |
| C2 | `internal/probe` 剧本库 + runner | 剧本 YAML 格式（判别式/数据需求/预期信号）；runner 先 dry-run 回放模式，不自动发真实请求 |
| C3 | 确定性判别式 | resets_at 恒定性（anchored vs sliding）；断崖 vs 阶梯；turn/request 步进计数；null 字段出现性 |

验收：合成数据上五类结构问题（5h 窗类型/周窗锚定/prompt 粒度/RPM 桶类型/共池）判定后验 > 0.9。

### Phase 3 —— 观测三通道 + 凭证

| PR | 内容 | 关键点 |
| -- | ---- | ---- |
| D1 | `internal/collect` headers 声明式解析 | ObsBinding 映射表驱动；全量原始头存档（契约要点）；fuzz |
| D2 | usage endpoint 轮询 + locallog 解析 | 3 分钟轮询；Claude Code JSONL / Codex 日志解析器（fuzz） |
| D3 | `internal/cred` 加密存储 + refresh + health | age 加密静态存储；refresh token 自维护回写；NEEDS_HUMAN 告警 + 从可用集移除 |
| D4 | web 采集层 | 选型 spike 后落地（§5）；一账号一 profile 一固定出口 IP 硬约束 |

验收：三通道数据入 ledger 且对账；凭证失效不影响其他账号流量。

### Phase 4 —— LP + 影子价格

| PR | 内容 | 关键点 |
| -- | ---- | ---- |
| E1 | LP 求解器选型 spike | 三候选对比（§5）+ 提案审批；50 桶×50 类基准 < 100ms 门槛 |
| E2 | `internal/planner` LP 构造 + 对偶提取 + shadow_price 表 | charge(LINEAR_EV) 生成消耗系数；低分位数有效容量（α 可调） |
| E3 | `internal/value` 报表 | 等价美元价值/利用率/折扣率；「当前策略实现价值 vs 最优潜在价值」双数 |

验收：反事实重放（历史流量 × LP 路由 vs 实际路由）节省报告产出。

### Phase 5 —— 在线路由

| PR | 内容 | 关键点 |
| -- | ---- | ---- |
| F1 | `internal/route` Route/Report 服务 | 先 unix socket + JSON（gRPC 选型另提）；决策 < 1ms 查表路径 |
| F2 | 网关接入 | Bifrost（Go）vs LiteLLM hook（Python 生态）选型提案 |
| F3 | 瞬切 + hysteresis + 缓存亲和 | fallbacks 链；score 差 > 10% 才切换；缓存亲和奖励项 |

验收：路由 p99 < 2ms；免费档中断瞬切成功率 > 99%；SPILL_TO_PAYG guard 主动打满演练。

### Phase 6 —— eval + 漂移监控（持续）

| PR | 内容 | 关键点 |
| -- | ---- | ---- |
| G1 | `internal/evals` 私有题库 + 跑分 | 20–50 题真实工作抽样 + 自动判分；quality_scores 进 LP 价值函数 |
| G2 | `internal/estimate/drift` | CUSUM/Page-Hinkley；文档语义 diff（抽数字+标签配对）；2×2 交叉表（文档变×参数漂移） |

## 5. 选型待决清单（spike 后按依赖流程提案）

| # | 决策点 | 候选 | 当前倾向与理由 |
| - | ------ | ---- | -------------- |
| S1 | LP 求解器（Phase 4） | HiGHS cgo 绑定 / 纯 Go 自实现 revised simplex / Python sidecar（highspy） | 规模小（≈2500 变量）+ 需要对偶值；cgo 供应链与跨平台成本 vs sidecar 运维复杂度，E1 spike 定 |
| S2 | 离线后验（Phase 1） | Laplace 近似 / 自实现 Metropolis-Hastings | Go 无 numpyro/NUTS 等价物；量化似然下 MH 通常够用；B5 内做对比实验 |
| S3 | web 采集（Phase 3） | playwright sidecar（Python/Node）/ go-rod | 优先 refresh token 通道降低网页依赖（Intent §5.2），web 是末通道，选型可推迟 |
| S4 | 路由协议（Phase 5） | unix socket JSON / gRPC | 单机部署 JSON 足够；多机或网关旁路部署再上 gRPC |
| S5 | 事件存储升级 | JSONL → SQL（Timescale） | 数据层政策 storage:sql、禁重 ORM；Phase 3 后事件量上来再迁移 |

## 6. 风险登记册

| # | 风险 | 等级 | 缓解 |
| - | ---- | ---- | ---- |
| R1 | Go 数值/优化生态缺口（NUTS、生产级 LP） | 中 | S1/S2 选型 spike 前置；必要时受控 sidecar，应用层主体仍 Go |
| R2 | ci.yml 未切 runtime，CI 全红（阻塞合流） | 已消除 | T1 已收口：check 切 `runtime: go`、deps-audit 换 govulncheck@v1.7.0（ADR-0028） |
| R3 | 厂商接口/语义漂移（percent 精度、响应头变更） | 高（业务固有） | 全量原始头存档 + 结构探针剧本 + CUSUM；这是产品核心能力而非副作用 |
| R4 | ToS/封号（订阅 OAuth 驱动 swarm） | 中（业务固有） | risk 字段进价值模型（Intent §10.2）；支付方式/IP 拓扑隔离；多厂商分散 |
| R5 | 供应链（本系统将集中持有全部凭证） | 中 | 最小依赖 + 版本锁定 + govulncheck（CI push 面）+ 凭证 age 加密 + 不暴露公网 |
| R6 | 单 PR 超 400 行（Phase 1 估计器复杂度） | 低 | 已拆 6 个 PR；仍超限的按参数族再拆 |
| R7 | archlint 仅审计 `go list` 当前构建上下文可见的包，平台限定 / 自定义 build tag 代码不进检查面（评审 #1 已含 TestImports 收口） | 低 | 仓库当前无平台限定包（纯跨平台标准库）；首个平台限定包落地时演进为文件级 go/parser 解析 |

