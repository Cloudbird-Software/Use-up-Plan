package estimate

import (
	"math"
	"testing"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// ---- 估计器逐位确定性（审计可复现的硬要求） ----

// TestLogPosteriorDeterministic 复现：logPosterior 的先验项曾按 map 迭代序
// 求和——Go 每次迭代顺序随机，浮点加法不可结合 → 同一输入在同进程内产生
// ULP 级差异 → 优化器在量化似然窄谷里走不同线搜索路径（CI 上
// TestEstimateWarmStart 的 41 vs 100 次求值即此症状）。修复 = 按 key 排序
// 求和。宽量级（1e-9..1e9）+ 多 key 让任何换序几乎必然改变舍入。
func TestLogPosteriorDeterministic(t *testing.T) {
	theta := qdl.ParamPoint{}
	priors := map[string]*qdl.Distribution{}
	for i := 0; i < 200; i++ {
		id := "p." + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676%26))
		v := math.Pow(10, -9+float64(i%19))
		theta[id] = v
		priors[id] = &qdl.Distribution{
			Kind: qdl.DistNormal, Params: map[string]float64{"mu": v * 1.0000001, "sigma": v},
		}
	}
	obs := []ObsPoint{{BucketID: "b", Kind: ObsPct, Y: 50, Step: 1, Sigma: 0.5}}
	mus := []float64{50}
	first, err := logPosterior(mus, obs, theta, priors)
	if err != nil {
		t.Fatalf("logPosterior: %v", err)
	}
	for k := 0; k < 500; k++ {
		got, err := logPosterior(mus, obs, theta, priors)
		if err != nil {
			t.Fatalf("logPosterior: %v", err)
		}
		if got != first {
			t.Fatalf("logPosterior 非确定：第 %d 次调用 %b ≠ 首次 %b（先验求和序随机）", k, got, first)
		}
	}
}
