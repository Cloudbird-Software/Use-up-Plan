# Use-up-Plan

LLM 订阅额度「智能调度 + 真实份额审计」系统：把各厂商订阅 plan 的额度语义形式化为机器可读的
QDL（Quota Description Language），用真实请求账本辨识厂商不公开的计量规则（容量 / 权重 / 窗口结构），
再用 LP 影子价格把任务流调度到最合适的额度桶——把每个 plan 用尽，并输出「等价 API 美元」口径的真实价值审计。

需求与全量设计（本体论、QDL schema、形式语义、系统契约、分阶段路线）见 [Intent.md](Intent.md)。

## 核心思路

- 厂商额度规则 = **结构未知**（窗口类型 / 作用域 / 计量粒度）+ **数值未知**（容量 C、权重 w），
  全部建模为带先验的可学习参数，而非写死的常量；
- 以官方 API 价目表作 **calibration gauge** 打破 (w, C) 的尺度不可辨识性——容量单位自动成为
  「等价 API 美元」，即「把 plan 用尽 = 买了多少美元的 API」；
- **append-only Ledger**（每次请求的每维原始消耗 + 撞墙时刻完整账本快照）是审计与参数辨识的数据基石；
- 调度 = 定期解小 LP 得各桶**影子价格**，在线路由退化为查表打分；结构套利是自然解
  （per-request 桶吃大上下文长输出任务，per-token 桶吃高频短请求）。

## 语言与治理

- 应用层语言：**Go**——[languages.yaml](https://github.com/Cloudbird-Software/.github/blob/main/governance/policy/languages.yaml)
  应用层默认；TypeScript 仅限前端同构场景，本项目不适用（模板 TS 脚手架已移除）。
- 本仓继承组织全套护栏：CI gate（hygiene / check / deps / adr-required）、gitleaks、zizmor、
  dependabot、CODEOWNERS。
- 依赖准入：先提案（名称 / 用途 / 许可证 / 标准库可否替代）待人批，禁 AGPL / GPL-3.0 / SSPL；
  当前为**零第三方依赖**，Phase 0 前待批清单见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## Makefile 接口（所有语言统一，CI 只认这个）

| 目标         | 作用                                            |
| ------------ | ----------------------------------------------- |
| `make setup` | 安装依赖（`go mod download`）                    |
| `make fmt`   | 格式化（gofmt）                                  |
| `make lint`  | gofmt 零 diff（GO-1）+ `go vet ./...`            |
| `make arch`  | 架构边界检查（`tools/archlint`：GO-3 / MOD-1 / MOD-5） |
| `make test`  | `go test -race ./...`（T-02 竞态检测）           |
| `make build` | `go build ./...`                                 |
| `make check` | lint + arch + test，**提交前必须全绿**           |

## CI 结构

- `hygiene`：密钥扫描（gitleaks）、大文件/凭据文件拦截、zizmor Actions 审计
- `check`：`make setup && make check`（check.yml@v1，`runtime: go`）
- `deps`：依赖漏洞 + 许可证审查（PR 时，读 go.sum）
- `adr-required`：C1 脚手架面（`.github/`、AGENTS.md、Makefile、docs/ 等）变更的 PR 须引用有效 ADR
- `gate`：聚合门（组织 ruleset 的唯一必需 check）

工作流实现在 [CI-Workflows](https://github.com/Cloudbird-Software/CI-Workflows)，本仓只引用 `@v1`。

## 开发路线（详见 Intent.md §9.2）

| 阶段      | 内容                                | 验收要点                                                     |
| --------- | ----------------------------------- | ------------------------------------------------------------ |
| Phase 0   | QDL 类型 + 语义内核（advance/charge/admit） | 3 个 plan QDL 过校验；advance 可组合性 property test；零网络 |
| Phase 1   | Ledger + 离线辨识（喂历史日志）      | 容量参数后验 90% 区间 < 中位数 40%；残差归因表分类注入故障   |
| Phase 2   | 结构辨识剧本                        | 窗口类型 / 计量粒度判定后验 > 0.9                            |
| Phase 3   | 观测三通道 + 凭证管理                | 响应头全量入账；凭证 refresh + NEEDS_HUMAN 优雅降级          |
| Phase 4   | LP + 影子价格                        | 求解 < 100ms；反事实重放验证节省显著                         |
| Phase 5   | 在线路由（网关接入）                 | 路由 p99 < 2ms；SPILL_TO_PAYG guard 验证                     |
| Phase 6   | 私有 eval + 漂移监控                 | 质量分进 LP 价值函数；CUSUM 24h 内告警                       |

## 安全与合规红线

- 厂商凭证（OAuth token / API key / cookie）**零明文入仓**：运行时 sops/age 加密存储，仓库只留
  `.env.example` 占位（治理 AR-3）。
- 网关与控制面只监听 127.0.0.1 / 私网；依赖锁定版本并校验哈希（LiteLLM 类供应链前车之鉴）。
- 订阅 OAuth 驱动第三方 swarm 处于多数厂商 ToS 灰色/违规区间：`ban_hazard_monthly` 等风险参数
  显式进入价值模型（Intent.md §10），不做口头风险评估。
