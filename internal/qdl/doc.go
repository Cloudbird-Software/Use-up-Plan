// Package qdl 实现 Quota Description Language（额度描述语言，Intent.md §1–§2）：
// 用一份可版本化的 YAML 把厂商订阅计划的计量语义写成机器可读的模型，将「额度规则」
// 从写死的配置常量升级为带先验/后验的可辨识参数体系。
//
// 核心概念（AI/新人从本表开始）：
//
//	Dim        计量维度分类学：计数维 / token 细分维 / 时间维 / 货币维 / 不透明维
//	InstantDim 瞬时约束维（并发、上下文峰值——不进桶，进 AdmissionPolicy）
//	Parameter  待估参数（一等公民）：prior / posterior / provenance / snap / frozen
//	Coeff      「常量或 ParamRef」双态系数，所有数值槽位的统一类型
//	Window     窗口语义；结构未知 ⇒ kind_candidates + kind_posterior，禁止写死单值
//	Scope      桶挂在谁身上（含 cross_product_pool 跨产品共享池）
//	Quantize   量化（ceil/floor 到 step）；EXACT 记账与 LINEAR_EV 规划的分界线
//	ChargeRule 分段线性扣减函数（flat + 加权项 + 倍率 + floor + 桶级量化）
//	Bucket     一个额度桶 = Window × Scope × ChargeRule × 观测绑定 × 溢出瀑布
//	PlanSpec   一份厂商计划 = 桶集 + 通道 + 参数表 + 标度规范(gauge) + 风险档案
//
// 包不变量（loader 强制执法）：
//   - 一切 ParamRef 必须能在 PlanSpec.Parameters 中解析
//   - SPILL_TO_PAYG 必须 requires_explicit_enable=true（防一夜烧穿钱包的安全契约）
//   - vendor_doc 来源参数永不 frozen（公开数字全部不可信，只配当先验）
//   - 本包零 IO、零网络、零副作用；PlanSpec 加载后不可变
package qdl
