# internal/probe

结构探针剧本库（Intent §4.3）：把「最重要的结构问题有不需要统计的
确定性判别式」工程化为可执行资产。每个 plan 接入时跑一遍，总成本几十次
请求，收益是整个模型的正确性——结构错了，数值再准也没用。

## 职责

- `playbook.go`：剧本类型（问题/候选/证据需求/判别式声明）+ 封闭集校验
  （cost、semantic、判别式 kind）
- `load.go`：YAML 严格加载（未知字段报错）+ `Builtins()`——playbooks/
  目录 embed 进二进制，按 ID 排序确定性暴露
- `playbooks/*.yaml`：六个种子剧本，逐条对应 Intent §4.3 判别式表：
  5h 窗语义 / 周窗锚定 / prompt 粒度 / RPM 桶类型 / null 字段语义 / 共池
- `runner.go`：`DryRun`——在事件库上回放剧本的证据需求（glob 匹配桶 +
  semantic 匹配观测），评估样本数与时间跨度的充分性，产出证据序列
  （判别式 C3 的统一输入面）。不发真实请求（ROADMAP C2：dry-run 先行）

## 不变量（违反 = bug）

1. 剧本是纯声明：不含执行逻辑；判定算法在 C3 判别式（按 Discriminator.Kind
   分派），采集行动归 collect——三层各自可独立测试。
2. `EvidenceSample.RawValue` 保真存原始串，语义解释归判别式（与
   ledger.ObservationEvent 同哲学：存储层不做有损解析）。
3. `Builtins()` 按 ID 排序、`DryRun` 按 needs 声明序报告——确定性遍历。
4. 判别式 kind 的封闭集与剧本资产必须同步演进：新增 kind 不落资产、或
   资产用未实现 kind，`TestBuiltinsCoverIntentTable` 会抓前者、Validate
   抓后者。
5. 剧本 YAML 用严格模式解码：schema 演进必须显式，静默忽略新字段会让
   旧二进制跑出语义漂移的判定。

## 验证

- `go test -race ./internal/probe/...`
- 内置剧本全量加载校验 + Intent §4.3 表六行覆盖 + 封闭集拒绝面 +
  dry-run 充分性两个方向（ready / insufficient）+ glob/plan/语义三重过滤
