package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestMultiChart(t *testing.T) {
	var series []SeriesData
	for i := 0; i < 8; i++ {
		series = append(series, SeriesData{Label: "GPU " + string(rune('0'+i)), Values: []float64{1, 2, float64(i)}, Color: lipgloss.Color("#FFFFFF")})
	}
	series[7].Values = nil
	out := RenderMultiTimeSeriesChart(MultiChartOptions{Title: "Power", Unit: "W", Width: 60, Height: 5, Series: series})
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w != 60 {
			t.Fatalf("line is %d wide, want 60: %q", w, line)
		}
	}
	if !strings.Contains(out, "GPU 7: --") {
		t.Error("a series without data should read -- in the legend")
	}
	if !strings.Contains(out, "GPU 0: 0.0 W") {
		t.Error("legend lacks the latest value")
	}
}

func TestWrapEntries(t *testing.T) {
	if got := wrapEntries([]string{"aaaa", "bbbb", "cccc"}, 11); len(got) != 2 || got[0] != "aaaa   bbbb" {
		t.Errorf("got %q", got)
	}
}
