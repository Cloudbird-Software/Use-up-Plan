// Package probe 实现 Intent §4.3 的结构探针剧本库：把「最重要的结构问题
// 有不需要统计的确定性判别式」这张表工程化为可执行资产。每个 plan 接入时
// 跑一遍，总成本几十次请求，收益是整个模型的正确性——结构错了，数值再准
// 也没用。
//
// 深接口分层：剧本是纯声明（数据需求 + 判别式规格）；runner 的 dry-run
// 只读事件库评估「证据够不够」（ROADMAP C2 明确：不自动发真实请求，
// 真实执行器后置于观测三通道就绪）。判定算法（C3 判别式）消费 dry-run
// 提取的证据序列。
package probe

import (
	"fmt"
	"path"
	"time"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// Cost 是剧本的执行成本类别。Intent 难点 2(d)：只在周期末尾用即将过期
// 清零的残余额度做探针——那部分额度的边际成本本来就是零，这让持续审计
// 从奢侈品变成免费品，是架构的一等公民而非事后优化。
type Cost string

const (
	CostPassive       Cost = "passive"        // 纯被动观测，零请求
	CostResidualQuota Cost = "residual_quota" // 周期末尾即将清零的残余额度（边际成本 0）
	CostSmallRequests Cost = "small_requests" // 少量真实请求（接入时跑一遍）
)

// validCosts 是 Cost 的封闭集。
var validCosts = map[Cost]bool{
	CostPassive: true, CostResidualQuota: true, CostSmallRequests: true,
}

// validSemantics 是剧本可声明的观测语义封闭集（与 qdl.Semantic 逐一对齐）。
var validSemantics = map[qdl.Semantic]bool{
	qdl.SemUsedPct: true, qdl.SemRemainingPct: true, qdl.SemUsedAbs: true,
	qdl.SemRemainingAbs: true, qdl.SemLimitAbs: true, qdl.SemResetAtEpochMS: true,
	qdl.SemResetAtISO: true, qdl.SemResetAfterS: true, qdl.SemWindowMinutes: true,
	qdl.SemReason: true, qdl.SemPlanType: true,
}

// EvidenceNeed 声明剧本判定所需的一类证据：观测语义 + 最少样本数 +
// 最少时间跨度（判别式对数据形状的最低要求——如 resets_at 恒定性
// 需要跨两个以上窗口边界才有区分力）。
type EvidenceNeed struct {
	Semantic qdl.Semantic  `yaml:"semantic"`
	MinCount int           `yaml:"min_count"`
	MinSpan  time.Duration `yaml:"min_span"` // 0 = 无跨度要求
}

// Discriminator 声明一个确定性判别式（Intent §4.3：「不需要统计的判别式，
// 优先用」）。Kind 是 C3 实现的算法封闭集，此处只做登记与校验。
type Discriminator struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description"`
	Kind        string `yaml:"kind"` // resets_at_constancy | cliff_vs_stair | step_counting | null_presence | pool_sync
}

// validDiscriminatorKinds 是判别式算法的封闭集（C3 逐个落地）。
var validDiscriminatorKinds = map[string]bool{
	"resets_at_constancy": true, // resets_at 恒定性（anchored vs sliding / 周窗锚定）
	"cliff_vs_stair":      true, // utilization 断崖归零 vs 阶梯衰减
	"step_counting":       true, // turn/request 步进计数（prompt 粒度）
	"null_presence":       true, // null 字段出现性（seven_day_opus 语义）
	"pool_sync":           true, // 双桶同步上涨（共池判定）
}

// Playbook 是一个结构探针剧本：一个问题 + 候选结论 + 证据需求 + 判别式
// 规格。YAML 资产见 playbooks/（embed 进二进制，Builtins() 暴露）。
type Playbook struct {
	ID             string          `yaml:"id"`
	Question       string          `yaml:"question"`
	Structure      string          `yaml:"structure"`   // window.kind | charge.granularity | scope.pool | field.presence
	BucketGlob     string          `yaml:"bucket_glob"` // 适用桶的 glob（如 "b_5h*"）
	Candidates     []string        `yaml:"candidates"`
	Cost           Cost            `yaml:"cost"`
	Risk           string          `yaml:"risk,omitempty"` // tos / ban 备注
	Needs          []EvidenceNeed  `yaml:"needs"`
	Discriminators []Discriminator `yaml:"discriminators"`
	Notes          string          `yaml:"notes,omitempty"`
}

// Validate 强制剧本的结构完整性：ID/问题/候选/需求/判别式非空，
// Cost 与 Semantic 与判别式 Kind 都在封闭集内，glob 语法合法。
// 剧本是接入新 plan 时的执行依据，坏剧本静默通过 = 结构判定跑空。
func (p *Playbook) Validate() error {
	if p.ID == "" || p.Question == "" {
		return fmt.Errorf("probe: 剧本缺 id/question")
	}
	if p.BucketGlob == "" {
		return fmt.Errorf("probe: 剧本 %s 缺 bucket_glob", p.ID)
	}
	if _, err := path.Match(p.BucketGlob, ""); err != nil {
		return fmt.Errorf("probe: 剧本 %s bucket_glob %q 语法非法: %w", p.ID, p.BucketGlob, err)
	}
	if len(p.Candidates) < 2 {
		return fmt.Errorf("probe: 剧本 %s 候选结论少于 2 个（结构问题至少两个互斥假设）", p.ID)
	}
	if !validCosts[p.Cost] {
		return fmt.Errorf("probe: 剧本 %s cost %q 不在封闭集 {passive, residual_quota, small_requests}", p.ID, p.Cost)
	}
	if len(p.Needs) == 0 {
		return fmt.Errorf("probe: 剧本 %s 无证据需求（判别式没有输入）", p.ID)
	}
	for i, n := range p.Needs {
		if !validSemantics[n.Semantic] {
			return fmt.Errorf("probe: 剧本 %s needs[%d] semantic %q 不在观测语义封闭集", p.ID, i, n.Semantic)
		}
		if n.MinCount <= 0 {
			return fmt.Errorf("probe: 剧本 %s needs[%d] min_count 必须 ≥ 1", p.ID, i)
		}
		if n.MinSpan < 0 {
			return fmt.Errorf("probe: 剧本 %s needs[%d] min_span 不能为负", p.ID, i)
		}
	}
	if len(p.Discriminators) == 0 {
		return fmt.Errorf("probe: 剧本 %s 无判别式声明", p.ID)
	}
	for i, d := range p.Discriminators {
		if d.ID == "" || d.Description == "" {
			return fmt.Errorf("probe: 剧本 %s discriminators[%d] 缺 id/description", p.ID, i)
		}
		if !validDiscriminatorKinds[d.Kind] {
			return fmt.Errorf("probe: 剧本 %s 判别式 %s kind %q 不在封闭集", p.ID, d.ID, d.Kind)
		}
	}
	return nil
}

// MatchBucket 报告桶 ID 是否命中剧本的 glob。
func (p *Playbook) MatchBucket(bucketID string) bool {
	ok, err := path.Match(p.BucketGlob, bucketID)
	return err == nil && ok
}
