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
- internal/ledger（B2）：事件流重放与残差归因——`Replayer` 增量重放
  （Advance 到事件时刻 → 应用事件效果；观测不动账本，账实不符正是
  reconcile 要检测的信号），双口径 `Replay`：AsRecorded（存量入账）/
  Recompute（当前 θ 重算，参数辨识与反事实分析入口）；`Reconcile`
  残差归因（Intent §3.4 归因表工程化）：量化噪声 / 外生消耗 / 系数漂移
  （CUSUM 变点）/ 未建模 flat / 结构错判（观测重置但账本有存量）/ 负偏 /
  数据不足 / 未解释，八类封闭分类 + 逐桶证据字符串。
- internal/estimate（B3）：Intent §4 参数辨识落地——量化似然
  `P(y|μ)=Φ((y+s/2-μ)/σ)-Φ((y-s/2-μ)/σ)`（尾区渐近展开保数值稳定）、
  精确计数似然、撞墙似然（`1[Σwx≥C]·(1-ε)+ε`）、五类先验密度；
  `ExtractDataset` 观测提取 + `Predict(θ)`（Recompute + LinearEV 重放，
  「用新 θ 重放旧请求流」的辨识循环本体）；`Estimate` 在线点估计
  （L-BFGS + MoreThuente 强 Wolfe + FunctionConverge 早停，线搜索卡窄谷
  降级返回至今最优点）。新依赖 gonum.org/v1/gonum（BSD-3-Clause）。
- internal/estimate（B4）：Intent §4.2/§4.4 尺度规范与整数吸附——耦合组
  探测（并查集找共享尺度自由度的参数集合）、标度规范校验（每个组至少一个
  frozen 参数）、可解释读数（容量等价美元/倍率偏离/缓存折扣证据）；整数
  吸附（Laplace 近似 90% CI + 候选/连分数逼近 + 似然比复核 χ²₀.₉₉(1)/2=3.317）。
- qdl/semantics 新增 `model_family` 作用域层级与模型族前缀匹配——支撑
  Claude Sonnet/Opus 周限专用窗；`ChargeOne` 倍率改为乘在 `(flat+Σ)` 整体，
  per-request 桶（flat=1、terms 空）的模型倍率由此生效。

### Fixed

- 估计器逐位确定性：`logPosterior` 先验项改按参数 ID 排序求和（map 迭代序
  随机 + 浮点加法不可结合 → ULP 级差异 → 优化器在量化似然窄谷走不同
  线搜索路径，CI 上 TestEstimateWarmStart 间歇性失败即此症状）；
  `globBest` 等长 pattern 平手改取字典序最小（原随 map 迭代序随机）。
  修复前先写复现测试（TestLogPosteriorDeterministic：200 键宽量级
  先验和，同进程 500 次调用必须逐位一致）。

### Changed

- 按 languages.yaml 应用层默认政策选定语言为 Go：移除 TypeScript 脚手架，落地 Go 工具链
  （go.mod、cmd/use-up-plan、tools/archlint 架构门禁），Makefile 目标改为 Go 等价实现
  （check 目标不变），dependabot 切换 npm → gomod。
- CI 语言面收口（ADR-0028）：check job `runtime: node` → `runtime: go`（go-version 1.25.1）；
  push 面 deps-audit 由 npm audit 换 govulncheck@v1.7.0。
- 治理收口：REPOS.yaml 申报入图（.github PR #77，ADR-0024）；ADR-0028 记录 Go 语言基线决策；
  首批依赖提案获 owner 批准（2026-08-19）。
