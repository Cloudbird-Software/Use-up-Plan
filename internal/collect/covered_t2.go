package collect

// T2 正向注入（.github #88 临时，测后即删）：新源码 + 覆盖全部新行的测试——预期 diff-coverage 绿
func CoveredT2(x int) int {
	y := x + 10
	z := y * 2
	return z
}
