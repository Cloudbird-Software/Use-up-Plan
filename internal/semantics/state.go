// Package semantics 实现 Intent §3 的形式语义内核：给定 QDL + 请求流，
// 状态如何演化。三个核心函数（advance / charge / admit）全部是纯函数，
// 可确定性重放——这是参数辨识与反事实分析的共同地基。
//
// 分层（深接口）：qdl 类型（Coeff 双态、AnchorUTC 字符串）在这里先经
// ResolveBucket 求值成纯几何参数（ResolvedBucket），advance 只做时间几何，
// 不碰 Coeff 解析。charge/admit 在同层复用 ResolvedBucket。
package semantics

import (
	"time"
)

// Delta 是一笔历史扣减（sliding_exact 窗口的逐笔明细）。
type Delta struct {
	T  time.Time // 扣减时刻
	DU float64   // 该笔扣减量（桶单位）
}

// BucketState 是单个桶的运行状态（Intent §3.1）。值类型：advance 返回新值，
// 不原地修改（纯函数重放的基石）。
type BucketState struct {
	U       float64    // 当前累积消耗（桶单位）；负值 = rollover 结转余量
	Anchor  *time.Time // 窗口锚点（tumbling_anchored_on_first_use 用；nil = 未启动）
	Ledger  []Delta    // 逐笔明细（sliding_exact 用；其余窗型可不维护）
	ResetAt *time.Time // 下次重置时刻（来自观测或推算；不参与 advance 几何）
}

// SystemState 是全系统状态（Intent §3.1）。参数分布部分（param_id ->
// Distribution）归 estimate 模块管理，此结构只承载桶状态——θ 以 ParamPoint
// 快照形式在各函数间显式传递，杜绝隐藏共享。
type SystemState struct {
	Buckets map[string]BucketState // bucket_id -> 状态
}
