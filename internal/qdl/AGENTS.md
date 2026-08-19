# internal/qdl —— Quota Description Language

## 负责什么

厂商订阅计划（PlanSpec）的 Go 类型与 `*.qdl.yaml` 加载校验：计量维度分类学、窗口分类学、
ChargeRule（分段线性 + 量化）、作用域（含 CROSS_PRODUCT_POOL）、可观测性绑定、溢出瀑布、
参数体系（prior/posterior/provenance/snap_candidates/frozen）、calibration gauge。
规格来源：Intent.md §1–§2。

## 不变量

- 所有系数字段可为「常量或 ParamRef」；ParamRef 必须能在 PlanSpec.parameters 中解析（loader 校验）
- Window 结构未知 ⇒ kind_candidates + kind_posterior，禁止写死单值 kind
- SPILL_TO_PAYG 必须 requires_explicit_enable=true（安全契约，loader 层拒绝缺省）
- 厂商声称值（vendor_doc 来源）永不 frozen；只有 gauge 锚定参数可 frozen
- 加载器纯函数、零网络、零副作用；PlanSpec 加载后不可变

## 禁止

- 引入网络/IO 依赖（纯解析层），也禁止依赖其他 internal 模块（QDL 是最底层）

## 如何验证

`go test -race ./internal/qdl/...`；加载器落地时必须带 fuzz 种子（T-04）与 golden fixtures（T-09）。
