# internal/semantics —— 形式语义内核

## 负责什么

Intent §3 的三个核心纯函数中 `advance`（时间推进）的完整实现，及 charge/admit
共用的求值层：`ResolveBucket` 把 qdl.Bucket + θ 折算成纯几何参数
（ResolvedBucket），`Advance` 按窗型分派推进 BucketState。规格来源：Intent §3.1–§3.2。

## 不变量

- **可组合性（最高契约）**：`advance(advance(s,a,b),b,c) == advance(s,a,c)`
  ——property test 强制；因此跨多个重置时刻必须逐周期归位（rollover 结转
  按周期累积），anchor 归 nil 后绝不能再清 u（会抹掉负结转）
- Advance 纯函数：不修改入参、无 IO；浮点下 U 的可组合性是 1e-9 相对容差
  近似（token bucket 的 r·Δt 乘加非结合，固有噪声）
- 锚点未知（anchor_utc=UNKNOWN）是显式错误，不是静默缺省——待 estimate
  反推后写回 spec
- 时间倒流（t_from > t_to）报错；t_from == t_to 幂等原样返回

## 禁止

- 在此模块解析 Coeff（求值统一走 ResolveBucket，advance 只做时间几何）
- 引入网络/存储依赖；依赖仅限 internal/qdl 根包

## 如何验证

`go test -race ./internal/semantics/...`；窗型语义改动必须保持
TestAdvanceComposability 全绿（重放契约回归）。
