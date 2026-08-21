package autofix

import "testing"

// T1 全链路注入（.github #93 临时，测后即删）：确定性失败 ×3 次
func TestAlwaysFailV2(t *testing.T) {
	t.Fatal("doomed by design v2 (#93 T1)")
}
