# internal/semantics —— 形式语义内核

## 负责什么

Intent §3 的三个核心纯函数全部落地：

- `Advance`（时间推进）：`ResolveBucket` 把 qdl.Bucket + θ 折算成纯几何参数
  （ResolvedBucket），`Advance` 按窗型分派推进 BucketState
- `Charge`（扣减，双模式）：`ResolveCharge` 折算 ChargeRule → `ChargeOne` 代数
  ——EXACT（记账：ceil/floor/max 精确应用）与 LINEAR_EV（规划：量化取期望
  x+s/2、max 取线性上界 flat+floor，仿射可进 LP）；`ChargeUpperBound` 给出
  EXACT 的严格上界（admit 风险评估用）
- `Admit`（准入三态）：DENY_ADMISSION（瞬时约束：并发/上下文峰值/模型清单）
  > DENY_QUOTA（桶满，带 retry_after）> ALLOW_WITH_RISK（EV..UB 均匀假设下的
  p_break）> ALLOW

规格来源：Intent §3.1–§3.2。

## 不变量

- **可组合性（最高契约）**：`advance(advance(s,a,b),b,c) == advance(s,a,c)`
  ——property test 强制；因此跨多个重置时刻必须逐周期归位（rollover 结转
  按周期累积），anchor 归 nil 后绝不能再清 u（会抹掉负结转）
- Advance 纯函数：不修改入参、无 IO；浮点下 U 的可组合性是 1e-9 相对容差
  近似（token bucket 的 r·Δt 乘加非结合，固有噪声）
- **EV ≤ EXACT ≤ UB 序**（量化凸性）：LINEAR_EV 只给规划器、EXACT 只给记账器，
  两者差累积成 linearization_residual——ledger reconcile 的核心归因对象
- Dims 必须存原始物理量（真实 token 数），不存已加权结果（重放前提）
- 锚点未知（anchor_utc=UNKNOWN）是显式错误，不是静默缺省——待 estimate
  反推后写回 spec
- 时间倒流（t_from > t_to）报错；t_from == t_to 幂等原样返回

## 禁止

- 在此模块解析 Coeff（求值统一走 Resolve* 层，advance/charge 只做几何/代数）
- 引入网络/存储依赖；依赖仅限 internal/qdl 根包
- admit 不做时间推进（桶状态必须已 advance 到 actx.Now——调用方职责）

## 如何验证

`go test -race ./internal/semantics/...`；窗型语义改动必须保持
TestAdvanceComposability 全绿（重放契约回归）；扣减改动必须保持
TestChargeExactVsLinearEV 的 EV ≤ EXACT ≤ UB 序与三态边界测试全绿。

