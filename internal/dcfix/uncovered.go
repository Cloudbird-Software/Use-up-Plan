package dcfix

// T1 负向注入（.github #88 临时）：20 行无测试源码——预期 diff-coverage 红
func UncoveredA(x int) int {
	y := x * 2
	z := y + 1
	w := z * 3
	v := w - 4
	u := v / 2
	t := u + 5
	s := t * 6
	r := s - 7
	q := r + 8
	p := q * 9
	return p
}

func UncoveredB(x int) int {
	a := x + 1
	b := a + 2
	c := b + 3
	d := c + 4
	e := d + 5
	f := e + 6
	g := f + 7
	h := g + 8
	i := h + 9
	return i
}
