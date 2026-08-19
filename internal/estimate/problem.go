package estimate

import (
	"fmt"
	"math"

	"github.com/Cloudbird-Software/Use-up-Plan/internal/qdl"
)

// ParamSpace 是可优化参数空间：spec 的全部非 frozen 参数 + 边界变换。
//
// 优化在无界 z 空间进行（gonum optimize 无原生 box 约束），物理值 θ 由
// 变换映射回去：双侧界用 logit、单侧界用 exp、无界用恒等。这保证任何
// 迭代点都落在物理可行域（容量/权重永不为负），先验不需要越界惩罚。
type ParamSpace struct {
	IDs     []string // 自由参数 ID（spec.Parameters 声明序，确定性）
	Bounds  map[string][2]float64
	Priors  map[string]*qdl.Distribution
	Kind    map[string]paramTransform
	BaseIDs []string // frozen 参数 ID（CompleteTheta 用）
}

type paramTransform int

const (
	transformIdentity paramTransform = iota // x = z
	transformLower                          // x = lb + e^z
	transformUpper                          // x = ub - e^z
	transformLogit                          // x = lb + (ub-lb)·σ(z)
)

// NewParamSpace 提取 spec 的自由参数并推导变换：
// 显式 bounds 优先；无 bounds 时按先验的支持域决定——
//   - normal 且 mu>0：计量参数正性（Intent 域内容量/权重/倍率/flat 皆
//     非负，负值会让 Predict 落入非法区）→ exp 变换保正（lb=0）
//   - normal 且 mu<=0、point、discrete、无先验：先验明确覆盖非正域或
//     不设假设 → 恒等（正性由 NLL 的 +Inf 悬崖兜底）
//   - lognormal：定义域正 → exp 变换（lb=0）
//
// extraFrozen 把额外参数临时退出自由空间（B4 整数吸附后的重拟合：
// 吸附值由调用方合入 base，参数不再进 IDs；吸附的级联效应——自由度
// 每减一，其余参数的 CI 收窄一轮——由 Snap 的逐个吸附循环实现）。
//
// 变换是往返安全的唯一保证：θ 贴近界外时 physToZ 会把值钳回界内，
// ToZ∘FromZ 恒等（TestParamSpaceRoundTrip 验收）。
func NewParamSpace(spec *qdl.PlanSpec, extraFrozen map[string]float64) (*ParamSpace, error) {
	if spec == nil {
		return nil, fmt.Errorf("estimate: NewParamSpace(nil spec)")
	}
	if extraFrozen == nil {
		extraFrozen = map[string]float64{}
	}
	ps := &ParamSpace{
		Bounds: map[string][2]float64{},
		Priors: map[string]*qdl.Distribution{},
		Kind:   map[string]paramTransform{},
	}
	for i := range spec.Parameters {
		p := &spec.Parameters[i]
		// 类别型结构参数（discrete + categories，如 prompt_granularity）
		// 不是数值量：无 float64 表示、不进 θ、PriorLogProb 对其恒 -Inf。
		// 它们的辨识走结构选择（select.go 的 BIC 面 / probe 的确定性判别式），
		// 进数值自由空间只会让整个估计爆掉。
		if p.Prior.Kind == qdl.DistDiscrete && len(p.Prior.Categories) > 0 {
			continue
		}
		if p.Frozen || containsKey(extraFrozen, p.ID) {
			ps.BaseIDs = append(ps.BaseIDs, p.ID)
			continue
		}
		ps.IDs = append(ps.IDs, p.ID)
		ps.Priors[p.ID] = &p.Prior
		lb, ub := p.Bounds[0], p.Bounds[1]
		switch {
		case lb != nil && ub != nil:
			if *lb >= *ub {
				return nil, fmt.Errorf("estimate: 参数 %q bounds [%v,%v] 下界不小于上界", p.ID, *lb, *ub)
			}
			ps.Bounds[p.ID] = [2]float64{*lb, *ub}
			ps.Kind[p.ID] = transformLogit
		case lb != nil:
			ps.Bounds[p.ID] = [2]float64{*lb, math.Inf(1)}
			ps.Kind[p.ID] = transformLower
		case ub != nil:
			ps.Bounds[p.ID] = [2]float64{math.Inf(-1), *ub}
			ps.Kind[p.ID] = transformUpper
		case p.Prior.Kind == qdl.DistNormal && p.Prior.Params["mu"] > 0,
			p.Prior.Kind == qdl.DistLognormal:
			// 正性计量参数：下界 0，exp 变换（theta0 钳到界内）
			ps.Bounds[p.ID] = [2]float64{0, math.Inf(1)}
			ps.Kind[p.ID] = transformLower
		default:
			// normal(mu<=0)/point/discrete/无先验：先验覆盖非正域或不设假设
			ps.Kind[p.ID] = transformIdentity
		}
	}
	return ps, nil
}

// N 返回自由参数个数。
func (ps *ParamSpace) N() int { return len(ps.IDs) }

// containsKey 报告 map 是否含键（map[string]float64 的集合用法）。
func containsKey(m map[string]float64, k string) bool {
	_, ok := m[k]
	return ok
}

// ToZ 把物理参数映射到无界优化空间。θ 缺失的参数由 priorInitial 补。
func (ps *ParamSpace) ToZ(theta qdl.ParamPoint) []float64 {
	z := make([]float64, ps.N())
	for i, id := range ps.IDs {
		x, ok := theta[id]
		if !ok {
			x = ps.priorInitial(id)
		}
		z[i] = ps.physToZ(id, x)
	}
	return z
}

// FromZ 把无界 z 映射回物理参数（只含自由参数；frozen 值见 CompleteTheta）。
func (ps *ParamSpace) FromZ(z []float64) qdl.ParamPoint {
	theta := qdl.ParamPoint{}
	for i, id := range ps.IDs {
		theta[id] = ps.zToPhys(id, z[i])
	}
	return theta
}

// CompleteTheta 合并自由参数与 base（frozen/常量）：free 覆盖 base。
// Predict/Charge 需要完整 θ——frozen 参数从 base 来。
func (ps *ParamSpace) CompleteTheta(free, base qdl.ParamPoint) qdl.ParamPoint {
	out := qdl.ParamPoint{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range free {
		out[k] = v
	}
	return out
}

// priorInitial 从先验取初始值（中位数口径）：normal→mu、lognormal→e^mu、
// uniform→(lo+hi)/2、point→value、discrete→最大概率值、未知→1。
func (ps *ParamSpace) priorInitial(id string) float64 {
	d := ps.Priors[id]
	if d == nil {
		return 1
	}
	p := func(k string) float64 { return d.Params[k] }
	switch d.Kind {
	case qdl.DistNormal:
		return p("mu")
	case qdl.DistLognormal:
		return math.Exp(p("mu"))
	case qdl.DistUniform:
		return (p("low") + p("high")) / 2
	case qdl.DistPoint:
		return p("value")
	case qdl.DistDiscrete:
		best, bestP := 1.0, -1.0
		for i, v := range d.Values {
			if i < len(d.Probs) && d.Probs[i] > bestP {
				best, bestP = v, d.Probs[i]
			}
		}
		return best
	}
	return 1
}

// physToZ 单参数物理值 → z（逆变换）。
func (ps *ParamSpace) physToZ(id string, x float64) float64 {
	switch ps.Kind[id] {
	case transformLower:
		lb := ps.Bounds[id][0]
		d := x - lb
		if d <= 1e-12 {
			return -27.6 // log(1e-12)：贴边时给个有限深值
		}
		return math.Log(d)
	case transformUpper:
		ub := ps.Bounds[id][1]
		d := ub - x
		if d <= 1e-12 {
			return -27.6
		}
		return math.Log(d)
	case transformLogit:
		lb, ub := ps.Bounds[id][0], ps.Bounds[id][1]
		t := (x - lb) / (ub - lb)
		t = math.Min(math.Max(t, 1e-9), 1-1e-9)
		return math.Log(t/(1-t)) + 0
	default:
		return x
	}
}

// zToPhys 单参数 z → 物理值。
func (ps *ParamSpace) zToPhys(id string, z float64) float64 {
	switch ps.Kind[id] {
	case transformLower:
		return ps.Bounds[id][0] + math.Exp(z)
	case transformUpper:
		return ps.Bounds[id][1] - math.Exp(z)
	case transformLogit:
		lb, ub := ps.Bounds[id][0], ps.Bounds[id][1]
		t := 1 / (1 + math.Exp(-z))
		return lb + (ub-lb)*t
	default:
		return z
	}
}

// Problem 是在线点估计的完整优化问题：数据集 + 参数空间 + frozen 基线
// + 参考点重参数化（u 空间 = zRef + S·u；当前 S=I，结构留给 B4 gauge
// fixing 挂接）。
type Problem struct {
	DS    *Dataset
	Space *ParamSpace
	Base  qdl.ParamPoint // frozen 参数与显式常量的取值来源
	zRef  []float64      // 参考点（theta0 的 z 表示）
	scale []float64      // 每维缩放因子（当前恒 1；z = zRef + S·u）
}

// NewProblem 组装优化问题。extraFrozen 是吸附后临时冻结的参数集合
// （值必须已合入 base）。
//
// 优化直接在 z 空间进行（u ≡ z，scale 恒 1）：gonum 的 InitDirection 已把
// 首步归一为 1/‖g‖，无需额外缩放；早期实验中按 1/|g| 缩放反而把单位步长
// 压到 z 的 1e-5 量级，线搜索被迫无限倍增步长后撞悬崖失败。zRef/scale
// 结构保留——吸附重拟合用「冻结 + warm-start」而非重参数化实现级联。
func NewProblem(ds *Dataset, base qdl.ParamPoint, theta0 qdl.ParamPoint, extraFrozen map[string]float64) (*Problem, error) {
	if ds == nil {
		return nil, fmt.Errorf("estimate: NewProblem(nil dataset)")
	}
	space, err := NewParamSpace(ds.Spec, extraFrozen)
	if err != nil {
		return nil, err
	}
	if base == nil {
		base = qdl.ParamPoint{}
	}
	p := &Problem{DS: ds, Space: space, Base: base}
	p.zRef = space.ToZ(theta0)
	n := len(p.zRef)
	p.scale = make([]float64, n)
	for i := range p.scale {
		p.scale[i] = 1
	}
	return p, nil
}

// uToZ 把优化变量 u 映射回 z 空间（z = zRef + S·u）。u 比 z 短时缺省 0
// （u=nil 即参考点自身）。
func (p *Problem) uToZ(u []float64) []float64 {
	z := make([]float64, len(p.zRef))
	for i := range z {
		if i < len(u) {
			z[i] = p.zRef[i] + p.scale[i]*u[i]
		} else {
			z[i] = p.zRef[i]
		}
	}
	return z
}

// ZRef 返回参考点 z（诊断用）。
func (p *Problem) ZRef() []float64 { return append([]float64(nil), p.zRef...) }

// ScaleDebug 返回缩放因子（诊断用）。
func (p *Problem) ScaleDebug() []float64 { return append([]float64(nil), p.scale...) }

// NLL 是负对数后验（u 空间，含 preconditioning 缩放）。预测失败
// （θ 探索到非法区）返回 +Inf 而非 error——优化器需要连续的目标面，
// 非法点的统一语义是「无限差」。
func (p *Problem) NLL(u []float64) float64 {
	return p.nllZ(p.uToZ(u))
}

// NLLGrad 是 NLL 的数值梯度（u 空间；链式法则 = z 空间梯度 × 缩放）。
// 签名遵循 gonum optimize.Problem 的约定：Grad(grad, x)——梯度缓冲在前、
// 求值点在后。解析梯度要穿过 LinearEV 扣减的倍率/权重/容量复合，维护
// 成本高于收益；参数量 20~100、数据集千级事件下数值梯度足够
// （Intent §4.6 在线估计是毫秒级 warm-start 场景）。一侧为 +Inf 时退到
// 单侧差分——置 0 会破坏梯度与目标的一致性，BFGS 线搜索会以 non-descent 拒绝。
func (p *Problem) NLLGrad(grad, u []float64) {
	gz := make([]float64, len(p.zRef))
	p.nllZGrad(p.uToZ(u), gz)
	for i := range grad {
		if i < len(gz) {
			grad[i] = gz[i] * p.scale[i]
		}
	}
}

// nllZ 是 z 空间的负对数后验（内部口径）。
func (p *Problem) nllZ(z []float64) float64 {
	theta := p.Space.CompleteTheta(p.Space.FromZ(z), p.Base)
	mus, err := p.DS.Predict(theta)
	if err != nil {
		return math.Inf(1)
	}
	lp, err := logPosterior(mus, p.DS.Obs, theta, p.Space.Priors)
	if err != nil || math.IsNaN(lp) || math.IsInf(lp, 0) {
		return math.Inf(1)
	}
	return -lp
}

// nllZGrad 是 z 空间的数值梯度（中心差分 + 越界侧单侧差分兜底）。
func (p *Problem) nllZGrad(z, grad []float64) {
	f0 := p.nllZ(z)
	for i := range z {
		h := 1e-5 * math.Max(1, math.Abs(z[i]))
		zp := append([]float64(nil), z...)
		zm := append([]float64(nil), z...)
		zp[i] += h
		zm[i] -= h
		fp, fm := p.nllZ(zp), p.nllZ(zm)
		bad := func(f float64) bool { return math.IsInf(f, 1) || math.IsNaN(f) }
		switch {
		case !bad(fp) && !bad(fm):
			grad[i] = (fp - fm) / (2 * h)
		case !bad(fp) && !math.IsInf(f0, 1) && !math.IsNaN(f0):
			grad[i] = (fp - f0) / h
		case !bad(fm) && !math.IsInf(f0, 1) && !math.IsNaN(f0):
			grad[i] = (f0 - fm) / h
		default:
			grad[i] = 0
		}
	}
}
