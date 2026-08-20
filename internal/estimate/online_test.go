package estimate

import (
	"testing"

	"gonum.org/v1/gonum/optimize"
)

// TestStatusConvergedMapping 回归：卡死降级路径上 gonum 返回的 status 是
// Failure（数值 8 > 0），旧实现 res.Converged = out.Status > 0 会把降级
// 误报为收敛——与 online.go 降级分支注释及本模块 AGENTS.md 不变量 5
// （降级返回至今最优点、如实报告）直接矛盾。
func TestStatusConvergedMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		st   optimize.Status
		want bool
	}{
		{"线搜索失败降级", optimize.ErrLinesearcherFailure, optimize.Failure, false},
		{"无进展降级", optimize.ErrNoProgress, optimize.Failure, false},
		{"函数收敛判收敛", nil, optimize.FunctionConvergence, true},
		{"方法收敛判收敛", nil, optimize.MethodConverge, true},
		{"迭代上限截断非收敛", nil, optimize.IterationLimit, false},
		{"未终止非收敛", nil, optimize.NotTerminated, false},
	}
	for _, c := range cases {
		if got := statusConverged(c.err, c.st); got != c.want {
			t.Errorf("%s: statusConverged(%v, %v) = %v, want %v", c.name, c.err, c.st, got, c.want)
		}
	}
}
