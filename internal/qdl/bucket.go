package qdl

// OverflowAction 是桶满后的动作（Intent §1.7 溢出瀑布，有序列表）。
type OverflowAction string

const (
	OverflowSpillToBucket      OverflowAction = "spill_to_bucket" // 溢到下一个桶
	OverflowSpillToPAYG        OverflowAction = "spill_to_payg"   // 溢到按量付费——必须显式开启！
	OverflowHardBlock          OverflowAction = "hard_block"
	OverflowHardBlockResetHint OverflowAction = "hard_block_with_reset_hint"
	OverflowDegradeModel       OverflowAction = "degrade_model"
	OverflowDegradeSpeed       OverflowAction = "degrade_speed"
	OverflowTruncateContext    OverflowAction = "truncate_context"
	OverflowQueue              OverflowAction = "queue"
	OverflowSilentQualityDrop  OverflowAction = "silent_quality_drop" // 最阴险：只能靠私有 eval 检测
)

// OverflowStep 是溢出瀑布的一步。
type OverflowStep struct {
	Action                 OverflowAction `yaml:"action"`
	Target                 string         `yaml:"target"` // spill/degrade 的目标桶或模型
	Factor                 *float64       `yaml:"factor"`
	MaxWaitS               *int           `yaml:"max_wait_s"`               // queue 的最大等待
	RequiresExplicitEnable bool           `yaml:"requires_explicit_enable"` // PAYG 必须 true（loader 缺省拒绝——防一夜烧穿钱包的安全契约）
}

// Bucket 是一个额度桶：一次请求命中的是 BucketSet（多桶同时扣减），不是单桶。
type Bucket struct {
	ID                 string         `yaml:"id"`
	Unit               Dim            `yaml:"unit"`     // 桶的计量单位
	Capacity           Coeff          `yaml:"capacity"` // 常为 ParamRef（未知容量是辨识对象）
	Window             Window         `yaml:"window"`
	Scope              Scope          `yaml:"scope"`
	Charge             ChargeRule     `yaml:"charge"`
	Observability      []ObsBinding   `yaml:"observability"`
	Overflow           []OverflowStep `yaml:"overflow"`
	ExogenousDrain     bool           `yaml:"exogenous_drain"`      // 会被系统外的你本人消耗（网页/桌面端偷额度）
	ExogenousRateParam string         `yaml:"exogenous_rate_param"` // 外生消耗率 ParamRef（带先验的隐变量，不当模型误差）
	Notes              string         `yaml:"notes"`
}
