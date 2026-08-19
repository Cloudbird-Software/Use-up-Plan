// Package ledger 实现 Intent §3.3 的事件溯源：append-only 事件流是唯一事实源，
// 桶余量、参数后验、结构信念全部可从事件流重建。θ 会被反复重估，只有存下
// 逐请求的原始物理量与「当时用的 θ 版本」，才能用新 θ 重放旧请求流——这是
// 参数辨识迭代收敛、反事实分析、漂移检测三件事的共同地基。
//
// 深接口分层：本包只定义事件形状与存储契约（校验 + 脱敏 + JSONL 追加），
// 不做重放（B2 ledger/replay）、不做归因（B2 ledger/reconcile）。
package ledger

import (
	"fmt"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// EventType 是事件类型的封闭集合（Intent §3.3 六种）。
type EventType string

const (
	EventCharge          EventType = "charge"           // 成功扣减
	EventObservation     EventType = "observation"      // 观测入账（三通道来源）
	EventWallHit         EventType = "wall_hit"         // 撞墙（最高价值辨识数据）
	EventResetObserved   EventType = "reset_observed"   // 观测到桶重置
	EventParamUpdate     EventType = "param_update"     // 参数后验更新
	EventStructureUpdate EventType = "structure_update" // 结构信念更新（窗型/粒度等）
)

// Payload 是六种事件负载的标记接口。构造用各类型的指针（*ChargeEvent 等），
// 存储边界统一做 Validate + 脱敏。
type Payload interface {
	payload()
	Validate() error
}

// ChargeEvent 记录一次成功扣减（Intent §3.3）。
//
// 不变量：Dims 存原始物理量（真实 token 数），绝不存已加权结果——重放的前提；
// BucketDeltas 是「按当时 θ」计算的扣减，与 ThetaVersion 配对，θ 重估后可用
// 新 θ 重放同一请求流。
type ChargeEvent struct {
	RequestID    string              `json:"request_id"`
	PlanID       string              `json:"plan_id"`
	ChannelID    string              `json:"channel_id"`
	Model        string              `json:"model"`
	Effort       string              `json:"effort,omitempty"`
	Dims         map[qdl.Dim]float64 `json:"dims"`          // 原始物理量（真实 token 数）
	BucketDeltas map[string]float64  `json:"bucket_deltas"` // bucket_id -> 扣减量（按当时 θ）
	ThetaVersion string              `json:"theta_version"` // 用了哪个参数快照
}

func (*ChargeEvent) payload() {}

// Validate 拒绝缺身份（无法配对重放）与负量（原始物理量不为负）。
func (e *ChargeEvent) Validate() error {
	if e.RequestID == "" || e.PlanID == "" || e.ThetaVersion == "" {
		return fmt.Errorf("ledger: ChargeEvent 缺 request_id/plan_id/theta_version（无法配对重放）")
	}
	for d, v := range e.Dims {
		if d == "" {
			return fmt.Errorf("ledger: ChargeEvent 含空维度名")
		}
		if v < 0 {
			return fmt.Errorf("ledger: ChargeEvent 维度 %s 为负 %v（Dims 只存原始物理量）", d, v)
		}
	}
	for b, v := range e.BucketDeltas {
		if b == "" {
			return fmt.Errorf("ledger: ChargeEvent 含空 bucket_id")
		}
		if v < 0 {
			return fmt.Errorf("ledger: ChargeEvent 桶 %s 扣减为负 %v", b, v)
		}
	}
	return nil
}

// ObservationEvent 记录一条来自三通道（响应头/usage endpoint/网页 DOM）的观测。
// RawValue 保真存原始字符串（"93"、"2026-08-20T12:00:00Z"），语义解释归
// estimate/reconcile 层按 Semantic 分派——存储层不做有损解析。
type ObservationEvent struct {
	PlanID       string           `json:"plan_id"`
	BucketID     string           `json:"bucket_id"`
	Semantic     qdl.Semantic     `json:"semantic"`
	RawValue     string           `json:"raw_value"`
	Quantization qdl.Quantization `json:"quantization"`
	Source       qdl.ObsSource    `json:"source"`
	Trust        float64          `json:"trust"` // 0..1，观测噪声权重
	ChannelID    string           `json:"channel_id,omitempty"`
}

func (*ObservationEvent) payload() {}

// Validate 拒绝缺定位信息与越界 trust。
func (e *ObservationEvent) Validate() error {
	if e.PlanID == "" || e.BucketID == "" {
		return fmt.Errorf("ledger: ObservationEvent 缺 plan_id/bucket_id")
	}
	if e.Semantic == "" || e.Source == "" {
		return fmt.Errorf("ledger: ObservationEvent 缺 semantic/source")
	}
	if e.RawValue == "" {
		return fmt.Errorf("ledger: ObservationEvent 缺 raw_value")
	}
	if e.Quantization.Kind == "" {
		return fmt.Errorf("ledger: ObservationEvent 缺 quantization.kind（缺省应为 unknown）")
	}
	if e.Trust < 0 || e.Trust > 1 {
		return fmt.Errorf("ledger: ObservationEvent trust=%v 越界 [0,1]", e.Trust)
	}
	return nil
}

// WallHitEvent 记录撞墙（请求被 429/配额拒绝）。LedgerSnapshot 是撞墙时刻的
// 完整累积账本——它就是那条「Σwx = C」的方程，是最宝贵的辨识数据，
// 每一个都要完整保留（Intent §3.3 原文要求，永不删除）。
type WallHitEvent struct {
	PlanID         string              `json:"plan_id"`
	BucketID       string              `json:"bucket_id"`
	RequestID      string              `json:"request_id,omitempty"` // 撞墙的请求（与 ChargeEvent 配对定界 C）
	ErrorBody      string              `json:"error_body"`           // 存储前自动脱敏
	ResetHint      *time.Time          `json:"reset_hint,omitempty"` // 错误体给出的重置时刻（若有）
	LedgerSnapshot map[qdl.Dim]float64 `json:"ledger_snapshot"`      // 撞墙时的完整累积账本（原始物理量）
}

func (*WallHitEvent) payload() {}

// Validate 要求账本快照非空——空的 WallHitEvent 丢掉了唯一的高价值信息。
func (e *WallHitEvent) Validate() error {
	if e.PlanID == "" || e.BucketID == "" {
		return fmt.Errorf("ledger: WallHitEvent 缺 plan_id/bucket_id")
	}
	if len(e.LedgerSnapshot) == 0 {
		return fmt.Errorf("ledger: WallHitEvent 缺 ledger_snapshot（Σwx=C 的方程，必须完整保留）")
	}
	for d, v := range e.LedgerSnapshot {
		if d == "" {
			return fmt.Errorf("ledger: WallHitEvent 账本含空维度名")
		}
		if v < 0 {
			return fmt.Errorf("ledger: WallHitEvent 账本维度 %s 为负 %v", d, v)
		}
	}
	return nil
}

// ResetObservedEvent 记录「观测到桶重置」：prev_u 与 ledger 推算不符时的
// 结构证据（窗口结构判断错误的信号，Intent §3.4 归因表最后一行）。
type ResetObservedEvent struct {
	PlanID          string     `json:"plan_id"`
	BucketID        string     `json:"bucket_id"`
	PrevU           float64    `json:"prev_u"`            // 重置前观测/账面值
	NewU            float64    `json:"new_u"`             // 重置后观测值
	ResetAtReported *time.Time `json:"reset_at_reported"` // 厂商报告的重置时刻（可空）
}

func (*ResetObservedEvent) payload() {}

// Validate 只要求可定位。
func (e *ResetObservedEvent) Validate() error {
	if e.PlanID == "" || e.BucketID == "" {
		return fmt.Errorf("ledger: ResetObservedEvent 缺 plan_id/bucket_id")
	}
	return nil
}

// ParamUpdateEvent 记录参数后验更新（在线点估计 / 离线后验 / 吸附共用）。
// EvidenceIDs 指认支撑该更新的证据事件（Observation/WallHit 的 seq）。
type ParamUpdateEvent struct {
	ParamID         string            `json:"param_id"`
	PosteriorBefore *qdl.Distribution `json:"posterior_before,omitempty"` // nil = 从先验起步
	PosteriorAfter  qdl.Distribution  `json:"posterior_after"`
	EvidenceIDs     []int64           `json:"evidence_ids,omitempty"` // 支撑证据的事件 seq
	Reason          string            `json:"reason,omitempty"`       // online | offline | snap | drift_reset
}

func (*ParamUpdateEvent) payload() {}

// Validate 要求目标参数可指认、前后分布形状合法。
func (e *ParamUpdateEvent) Validate() error {
	if e.ParamID == "" {
		return fmt.Errorf("ledger: ParamUpdateEvent 缺 param_id")
	}
	if err := e.PosteriorAfter.Validate(); err != nil {
		return fmt.Errorf("ledger: ParamUpdateEvent posterior_after: %w", err)
	}
	if e.PosteriorBefore != nil {
		if err := e.PosteriorBefore.Validate(); err != nil {
			return fmt.Errorf("ledger: ParamUpdateEvent posterior_before: %w", err)
		}
	}
	return nil
}

// StructureUpdateEvent 记录结构信念更新：窗型候选、计量粒度（turn vs request）
// 等离散结构量的后验。Posterior 是 candidate -> 概率，和为 1。
type StructureUpdateEvent struct {
	PlanID          string             `json:"plan_id"`
	BucketID        string             `json:"bucket_id"`
	Field           string             `json:"field"` // 如 "window.kind" / "charge.granularity"
	PosteriorBefore map[string]float64 `json:"posterior_before,omitempty"`
	PosteriorAfter  map[string]float64 `json:"posterior_after"`
}

func (*StructureUpdateEvent) payload() {}

// Validate 要求概率合法且和为 1（softmax 输出的天然不变量）。
func (e *StructureUpdateEvent) Validate() error {
	if e.PlanID == "" || e.BucketID == "" || e.Field == "" {
		return fmt.Errorf("ledger: StructureUpdateEvent 缺 plan_id/bucket_id/field")
	}
	if err := checkProbVector(e.PosteriorAfter); err != nil {
		return fmt.Errorf("ledger: StructureUpdateEvent posterior_after: %w", err)
	}
	if e.PosteriorBefore != nil {
		if err := checkProbVector(e.PosteriorBefore); err != nil {
			return fmt.Errorf("ledger: StructureUpdateEvent posterior_before: %w", err)
		}
	}
	return nil
}

// checkProbVector 校验离散概率向量：分量 ∈ [0,1]，非空，和为 1（±1e-6）。
func checkProbVector(p map[string]float64) error {
	if len(p) == 0 {
		return fmt.Errorf("概率向量不能为空")
	}
	sum := 0.0
	for c, v := range p {
		if c == "" {
			return fmt.Errorf("含空候选名")
		}
		if v < 0 || v > 1 {
			return fmt.Errorf("候选 %s 概率 %v 越界 [0,1]", c, v)
		}
		sum += v
	}
	if sum < 1-1e-6 || sum > 1+1e-6 {
		return fmt.Errorf("概率和 %v ≠ 1", sum)
	}
	return nil
}

// Event 是 JSONL 信封：序号（存储分配，严格递增）+ 时间戳 + 类型 + 六选一负载。
// 恰好一个负载非 nil 且与 Type 一致——由 Validate 强制。
type Event struct {
	Seq  int64     `json:"seq"`
	Ts   time.Time `json:"ts"`
	Type EventType `json:"type"`

	Charge          *ChargeEvent          `json:"charge,omitempty"`
	Observation     *ObservationEvent     `json:"observation,omitempty"`
	WallHit         *WallHitEvent         `json:"wall_hit,omitempty"`
	ResetObserved   *ResetObservedEvent   `json:"reset_observed,omitempty"`
	ParamUpdate     *ParamUpdateEvent     `json:"param_update,omitempty"`
	StructureUpdate *StructureUpdateEvent `json:"structure_update,omitempty"`
}

// Payload 返回事件的负载（六选一）。
func (ev Event) Payload() Payload {
	switch {
	case ev.Charge != nil:
		return ev.Charge
	case ev.Observation != nil:
		return ev.Observation
	case ev.WallHit != nil:
		return ev.WallHit
	case ev.ResetObserved != nil:
		return ev.ResetObserved
	case ev.ParamUpdate != nil:
		return ev.ParamUpdate
	case ev.StructureUpdate != nil:
		return ev.StructureUpdate
	}
	return nil
}

// Validate 校验信封一致性：恰好一个负载、Type 与负载匹配、负载自身合法。
func (ev Event) Validate() error {
	p := ev.Payload()
	if p == nil {
		return fmt.Errorf("ledger: 事件 seq=%d 无负载", ev.Seq)
	}
	var want EventType
	switch p.(type) {
	case *ChargeEvent:
		want = EventCharge
	case *ObservationEvent:
		want = EventObservation
	case *WallHitEvent:
		want = EventWallHit
	case *ResetObservedEvent:
		want = EventResetObserved
	case *ParamUpdateEvent:
		want = EventParamUpdate
	case *StructureUpdateEvent:
		want = EventStructureUpdate
	}
	if ev.Type != want {
		return fmt.Errorf("ledger: 事件 seq=%d type=%s 与负载 %s 不匹配", ev.Seq, ev.Type, want)
	}
	if ev.Seq <= 0 {
		return fmt.Errorf("ledger: 事件 seq=%d 非正", ev.Seq)
	}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("ledger: 事件 seq=%d: %w", ev.Seq, err)
	}
	return nil
}
