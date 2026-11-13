package components

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

var sgrSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return sgrSeq.ReplaceAllString(s, "") }

// brailleLevel reports how many of a braille cell's four dot rows are filled,
// counting up from the bottom, for the column-fill patterns the canvas draws.
func brailleLevel(r rune) int {
	bits := int(r - 0x2800)
	rows := [4]int{0x40 | 0x80, 0x04 | 0x20, 0x02 | 0x10, 0x01 | 0x08} // bottom to top
	level := 0
	for i, mask := range rows {
		if bits&mask != 0 {
			level = i + 1
		}
	}
	return level
}

// A value must be placed against the bounds it is drawn with. The renderer used
// to subtract the data's own minimum while dividing by the bounds' span, so any
// steady reading -- 250 W on a 0-275 W scale -- drew along the bottom.
func TestSparklineUsesFixedBounds(t *testing.T) {
	flat := func(v float64) []float64 {
		out := make([]float64, 40)
		for i := range out {
			out[i] = v
		}
		return out
	}

	high := []rune(plain(RenderSparkline(flat(250), 10, lipgloss.Color("#fff"), 0, 275)))
	low := []rune(plain(RenderSparkline(flat(20), 10, lipgloss.Color("#fff"), 0, 275)))

	if got := brailleLevel(high[len(high)-1]); got != 4 {
		t.Errorf("250 of 275 drew at level %d of 4, want the top", got)
	}
	if got := brailleLevel(low[len(low)-1]); got > 1 {
		t.Errorf("20 of 275 drew at level %d of 4, want the floor", got)
	}
}

// Readings that differ must not draw identically.
func TestSparklineDistinguishesValues(t *testing.T) {
	series := func(v float64) string {
		vals := make([]float64, 40)
		for i := range vals {
			vals[i] = v
		}
		return plain(RenderSparkline(vals, 12, lipgloss.Color("#fff"), 0, 100))
	}
	if series(90) == series(10) {
		t.Error("90% and 10% rendered the same sparkline")
	}
	if !strings.ContainsRune(series(90), '⣿') {
		t.Errorf("90%% should fill cells to the top: %q", series(90))
	}
}
