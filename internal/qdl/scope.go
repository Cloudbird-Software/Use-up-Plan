package qdl

// ScopeLevel 是桶的挂靠层级。CROSS_PRODUCT_POOL 必须显式建模：Claude 的额度在
// Claude Code / claude.ai / Desktop 之间共享，手动开个网页聊天就在偷 swarm 的额度。
type ScopeLevel string

const (
	ScopeAccount          ScopeLevel = "account"
	ScopeOrganization     ScopeLevel = "organization"
	ScopeWorkspace        ScopeLevel = "workspace"
	ScopeCredential       ScopeLevel = "credential"
	ScopeSubscription     ScopeLevel = "subscription"
	ScopeCrossProductPool ScopeLevel = "cross_product_pool"
)

// Scope 描述桶挂在谁身上。Models 为 nil 表示全部模型。
type Scope struct {
	Level              ScopeLevel
	Models             []string // nil = 全部
	ModelFamilies      []string
	EffortTiers        []string
	Channels           []string
	Endpoints          []string
	PoolID             string   // cross_product_pool 的共享标识
	SharedWithProducts []string // ["claude.ai","desktop","cowork"]：外生消耗来源
}
