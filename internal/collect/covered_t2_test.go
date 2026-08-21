package collect

import "testing"

func TestCoveredT2(t *testing.T) {
	if got := CoveredT2(5); got != 30 {
		t.Fatalf("CoveredT2(5) = %d, want 30", got)
	}
}
