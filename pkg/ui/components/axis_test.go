package components

import (
	"strings"
	"testing"
)

func TestYAxisRoundLabels(t *testing.T) {
	cases := []struct {
		lo, hi float64
		h      int
		want   []string
	}{
		{0, 100, 8, []string{"100", "50", "0"}},
		{0, 2800, 8, []string{"2000", "1000", "0"}},
		{0, 1500, 8, []string{"1500", "1000", "500", "0"}},
		{0, 100, 16, []string{"100", "80", "60", "40", "20", "0"}},
		{0, 1, 8, []string{"1.0", "0.5", "0.0"}},
		{0, 100, 12, []string{"100", "80", "60", "40", "20", "0"}},
	}
	for _, c := range cases {
		labels, _ := yAxis(c.lo, c.hi, c.h)
		var got []string
		for _, l := range labels {
			if v := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(l, "│"), "┤")); v != "" {
				got = append(got, v)
			}
			if w := len([]rune(l)); w != axisPrefixWidth {
				t.Fatalf("%v: prefix %q is %d wide", c, l, w)
			}
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%v..%v h=%d: got %v, want %v", c.lo, c.hi, c.h, got, c.want)
		}
	}
}
