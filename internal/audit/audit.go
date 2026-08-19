// Package audit 是端到端审计管线（ROADMAP B6）：把「真实份额审计」组装成
// 一份可读报告——Claude Code JSONL 入账 → 观测提取 → 在线点估计 + 离线后验
// → gauge 读数（等价 API 美元）+ 残差归因表。
//
// 深接口分层：本包是纯编排层——解析归 collect、扣减归 semantics、存储与
// 对账归 ledger、估计归 estimate。编排不改任何一层的结果，只决定执行顺序
// 与报告形态，故端到端测试即各层契约的集成验收。
package audit

import (
	"fmt"
	"math"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/collect"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/estimate"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/ledger"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
	"github.com/Cloudbird-Software/Use-up-Plan/internal/semantics"
)

// IngestClaude 把 collect 解析出的会话计量记录转成 ChargeEvent 追加进事件库
// （Intent §3.3：dims 原始物理量 + 按当时 θ 的 bucket_deltas + theta_version，
// θ 重估后可用新 θ 重放同一请求流）。扣减用 EXACT 记账口径。
// 追加前保证时间升序（replayer 的硬前提）；turns 乱序时报错而非静默错排。
func IngestClaude(store ledger.Store, spec *qdl.PlanSpec, turns []collect.ClaudeTurn,
	theta qdl.ParamPoint, thetaVersion string, channelID string) (int, error) {
	if store == nil || spec == nil {
		return 0, fmt.Errorf("audit: IngestClaude(nil)")
	}
	if channelID == "" {
		channelID = "claude_code"
	}
	last := time.Time{}
	for i, tu := range turns {
		if tu.Ts.Before(last) {
			return i, fmt.Errorf("audit: 第 %d 条记录时间 %s 早于前一条（必须升序）", i, tu.Ts)
		}
		last = tu.Ts
		req := &semantics.Request{ChannelID: channelID, Model: tu.Model, Dims: tu.Dims}
		deltas, err := semantics.Charge(spec, req, theta, semantics.ChargeModeExact)
		if err != nil {
			return i, fmt.Errorf("audit: 请求 %s 扣减: %w", tu.MsgID, err)
		}
		id := tu.MsgID
		if id == "" { // 老格式日志可能缺 message.id：回退序号，保证可配对重放
			id = fmt.Sprintf("claude-%d", i)
		}
		if _, err := store.Append(tu.Ts, &ledger.ChargeEvent{
			RequestID: id, PlanID: spec.ID, ChannelID: channelID, Model: tu.Model,
			Dims: tu.Dims, BucketDeltas: deltas, ThetaVersion: thetaVersion,
		}); err != nil {
			return i, fmt.Errorf("audit: 入账 %s: %w", id, err)
		}
	}
	return len(turns), nil
}

// ThetaFromPrior 从 spec 参数先验组装完整 θ 快照（中位数口径：
// lognormal→exp(mu)、normal→mu、uniform→域中心、point→值、discrete→
// 概率最大值）。用途：首次导入历史日志时还没有任何估计——bucket_deltas
// 按先验中心记录；θ 重估后 Recompute 重放用新 θ 重算，存量 deltas 只作
// 历史对照（Intent §3.3 的 theta_version 配对语义）。
func ThetaFromPrior(spec *qdl.PlanSpec) qdl.ParamPoint {
	theta := qdl.ParamPoint{}
	if spec == nil {
		return theta
	}
	for i := range spec.Parameters {
		p := &spec.Parameters[i]
		theta[p.ID] = priorCenter(&p.Prior)
	}
	return theta
}

// priorCenter 单参数先验的中心值。未知形状回退 0（loader 封闭集校验
// 保证不会走到）。
func priorCenter(d *qdl.Distribution) float64 {
	switch d.Kind {
	case qdl.DistLognormal:
		return math.Exp(d.Params["mu"])
	case qdl.DistNormal:
		return d.Params["mu"]
	case qdl.DistUniform:
		return (d.Params["low"] + d.Params["high"]) / 2
	case qdl.DistPoint:
		return d.Params["value"]
	case qdl.DistDiscrete:
		best, bestP := 0.0, -1.0
		for i, v := range d.Values {
			if p := d.Probs[i]; p > bestP {
				best, bestP = v, p
			}
		}
		return best
	}
	return 0
}

// Options 是一次审计的全部输入。Base 是 gauge 基线（frozen 参数的值，
// 通常来自价目表锚定）；Theta0 是在线估计起点（缺省参数自动从先验补）。
type Options struct {
	Spec   *qdl.PlanSpec
	Store  ledger.Store
	Base   qdl.ParamPoint
	Theta0 qdl.ParamPoint
	// SkipPosterior 跳过离线后验（在线模式 / 观测太少时）。
	SkipPosterior bool
	Posterior     estimate.PosteriorOptions
}

// Report 是一份审计报告的结构化产物；Render 负责人类可读形态。
type Report struct {
	PlanID      string
	GeneratedAt time.Time
	Online      *estimate.Result
	Posterior   *estimate.PosteriorResult // SkipPosterior 时为 nil
	Reads       []estimate.GaugeRead      // gauge 可解释读数（等价 API 美元等）
	Recon       *ledger.Report            // 残差归因表
	NObs        int                       // 参与拟合的观测点数
}

// Run 跑完整审计管线：在线 MLE（warm-start 起点 Theta0）→ 离线后验
// （从 MLE 起步——链从后验众数附近起步，预热只学形状不爬坡）→ gauge
// 读数 → 对账归因。观测不足时 Estimate 自然返回（先验兜底），
// 后验是否跳过由调用方决定。
func Run(opts Options) (*Report, error) {
	if opts.Spec == nil || opts.Store == nil {
		return nil, fmt.Errorf("audit: Run(nil)")
	}
	ds, err := estimate.ExtractDataset(opts.Spec, opts.Store, estimate.ExtractOptions{})
	if err != nil {
		return nil, fmt.Errorf("audit: 提取观测集: %w", err)
	}
	online, err := estimate.Estimate(ds, opts.Base, opts.Theta0, estimate.EstimateOptions{})
	if err != nil {
		return nil, fmt.Errorf("audit: 在线估计: %w", err)
	}
	rep := &Report{
		PlanID: opts.Spec.ID, GeneratedAt: time.Now().UTC(),
		Online: online, Reads: estimate.GaugeSummary(opts.Spec, online.Theta),
		NObs: online.NObs,
	}
	if !opts.SkipPosterior {
		post, err := estimate.SamplePosterior(ds, opts.Base, online.Theta, opts.Posterior)
		if err != nil {
			return nil, fmt.Errorf("audit: 离线后验: %w", err)
		}
		rep.Posterior = post
	}
	recon, err := ledger.Reconcile(opts.Spec, opts.Store, online.Theta)
	if err != nil {
		return nil, fmt.Errorf("audit: 对账: %w", err)
	}
	rep.Recon = recon
	return rep, nil
}
