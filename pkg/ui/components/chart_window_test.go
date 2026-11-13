package components

import "testing"

func TestSampleXSpansWidth(t *testing.T) {
	if x := sampleX(0, 60, 60, 120); x != 0 {
		t.Errorf("oldest of full window at %d, want 0", x)
	}
	if x := sampleX(59, 60, 60, 120); x != 119 {
		t.Errorf("newest at %d, want 119", x)
	}
	if x := sampleX(0, 30, 60, 120); x < 55 || x > 65 {
		t.Errorf("half window starts at %d, want mid-plot", x)
	}
}
