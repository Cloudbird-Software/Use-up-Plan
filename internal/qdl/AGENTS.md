# internal/qdl —— Quota Description Language

## 负责什么

厂商订阅计划（PlanSpec）的 Go 类型与 `*.qdl.yaml` 加载校验：计量维度分类学、窗口分类学、
ChargeRule（分段线性 + 量化）、作用域（含 CROSS_PRODUCT_POOL / MODEL_FAMILY）、可观测性绑定、
溢出瀑布、参数体系（prior/posterior/provenance/snap_candidates/frozen）、calibration gauge。
种子计划资产在仓库根 `qdl/plans/`（anthropic/max20、zai/glm-coding-max、free/_template），
是本模块与 semantics 的共同契约面——两侧各有 golden 准入测试。
规格来源：Intent.md §1–§2。

## 不变量

- 所有系数字段可为「常量或 ParamRef」；ParamRef 必须能在 PlanSpec.parameters 中解析（loader 校验）
- Window 结构未知 ⇒ kind_candidates + kind_posterior，禁止写死单值 kind
- SPILL_TO_PAYG 必须 requires_explicit_enable=true（安全契约，loader 层拒绝缺省）
- 厂商声称值（vendor_doc 来源）永不 frozen；只有 gauge 锚定参数可 frozen
- 加载器纯函数、零网络、零副作用；PlanSpec 加载后不可变
- 序列化往返稳定：`LoadBytes(Marshal(s))` 语义等价；空集合（如 per-request 桶的
  `terms: []`）与缺省收敛到同一规范形

## 禁止

- 引入网络/IO 依赖（纯解析层），也禁止依赖其他 internal 模块（QDL 是最底层）

## 如何验证

`go test -race ./internal/qdl/...`；加载器落地时必须带 fuzz 种子（T-04）与 golden fixtures（T-09）。
新增种子 plan 必须登记 internal/qdl/plans_test.go 的 seedPlans 表（加载 + 往返 golden）。
