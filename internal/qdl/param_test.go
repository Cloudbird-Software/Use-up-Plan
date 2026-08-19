package qdl

import "testing"

func TestCoeffResolve(t *testing.T) {
	theta := ParamPoint{"w_in": 3e-6}

	if got, err := Const(1.5).Resolve(theta); err != nil || got != 1.5 {
		t.Fatalf("常量解析: got %v, err %v", got, err)
	}
	if got, err := Ref("w_in").Resolve(theta); err != nil || got != 3e-6 {
		t.Fatalf("引用解析: got %v, err %v", got, err)
	}
	if _, err := Ref("missing").Resolve(theta); err == nil {
		t.Fatal("缺失引用必须报错")
	}
	c := Const(2)
	if c.IsRef() || c.RefID() != "" {
		t.Fatal("常量的 IsRef/RefID 语义错误")
	}
	if v, ok := Ref("x").Constant(); ok || v != 0 {
		t.Fatal("引用的 Constant 必须返回 (0,false)")
	}
}

func TestDimValid(t *testing.T) {
	for _, d := range []Dim{DimRequests, DimOpaqueUnits, DimCacheReadTokens, DimCharacters} {
		if !d.Valid() {
			t.Errorf("已知维度 %q 应合法", d)
		}
	}
	if Dim("nope").Valid() {
		t.Error("未知维度不应合法")
	}
	if !InstantConcurrency.Valid() || InstantDim("nope").Valid() {
		t.Error("InstantDim 校验错误")
	}
}

func TestQuantizeApply(t *testing.T) {
	cases := []struct {
		q    Quantize
		x, w float64
	}{
		{Quantize{}, 123.4, 123.4},                              // none
		{Quantize{Mode: QuantizeCeil, Step: 1000}, 300, 1000},   // ceil 效应
		{Quantize{Mode: QuantizeFloor, Step: 1000}, 1300, 1000}, // floor
		{Quantize{Mode: QuantizeRound, Step: 0.5}, 1.3, 1.5},    // round
		{Quantize{Mode: QuantizeCeil, Step: 0}, 300, 300},       // step<=0 退化为 none
	}
	for i, c := range cases {
		if got := c.q.Apply(c.x); got != c.w {
			t.Errorf("case %d: Apply(%v)=%v, want %v", i, c.x, got, c.w)
		}
	}
}

func TestWindowKindMAP(t *testing.T) {
	w := &Window{
		KindCandidates: []WindowKind{WindowTumblingAnchoredOnFirstUse, WindowSlidingExact},
		KindPosterior:  map[WindowKind]float64{WindowTumblingAnchoredOnFirstUse: 0.4, WindowSlidingExact: 0.6},
	}
	if w.Kind() != WindowSlidingExact {
		t.Fatalf("MAP 估计应取后验最大者, got %v", w.Kind())
	}
	w.KindPosterior = nil
	if w.Kind() != WindowTumblingAnchoredOnFirstUse {
		t.Fatalf("无后验时应取候选首位, got %v", w.Kind())
	}
}
