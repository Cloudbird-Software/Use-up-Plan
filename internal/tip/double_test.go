package tip

import "testing"

func TestDouble(t *testing.T) {
	if got := Double(3); got != 6 {
		t.Errorf("Double(3) = %d, want 6", got)
	}
	if got := Double(-4); got != -8 {
		t.Errorf("Double(-4) = %d, want -8", got)
	}
}
