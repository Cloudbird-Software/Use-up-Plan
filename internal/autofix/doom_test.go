package autofix

import "testing"

// T1 全链路注入（.github #93 临时，测后即删）：确定性失败 ×3 次
func TestAlwaysFailV3(t *testing.T) {
	t.Fatal("doomed by design v3 (#93 T1)")
}
