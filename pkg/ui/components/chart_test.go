package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// A chart is exactly opt.Width wide and, when the header fits on one row,
// Height plus five rows tall: header, the plot, the axis, the time labels and
// the two border rows. The plot used to be sized one column too wide for the
// frame, so lipgloss wrapped every braille row onto a line of its own and the
// chart came out twice as tall with its lines out of place.
func TestChartsFitTheirFrame(t *testing.T) {
	values := make([]float64, 60)
	for i := range values {
		values[i] = float64(i%20) * 5
	}
	zero, hundred := 0.0, 100.0

	for _, width := range []int{30, 40, 55, 72, 100, 140} {
		for _, height := range []int{4, 5, 7, 8} {
			single := RenderTimeSeriesChart(values, ChartOptions{
				Title: "Power Draw", Unit: "W", Width: width, Height: height,
				Color: lipgloss.Color("#00F5FF"), FixedMin: &zero, FixedMax: &hundred,
			})
			dual := RenderDualTimeSeriesChart(DualChartOptions{
				Title: "Memory Temperature", Unit: "°C", Width: width, Height: height,
				FixedMin: &zero, FixedMax: &hundred,
				Series0: SeriesData{Label: "GPU 0", Values: values, Color: lipgloss.Color("#00F5FF")},
				Series1: SeriesData{Label: "GPU 1", Values: values, Color: lipgloss.Color("#E056FD")},
			})
			for name, out := range map[string]string{"single": single, "dual": dual} {
				lines := strings.Split(out, "\n")
				for i, l := range lines {
					if w := lipgloss.Width(l); w != width {
						t.Errorf("%s %dx%d: line %d is %d wide, want %d: %q", name, width, height, i, w, width, plain(l))
					}
				}
				// The header may take two rows when the legend does not fit
				// beside the title; never more, and never a wrapped plot row.
				if n := len(lines); n != height+5 && n != height+6 {
					t.Errorf("%s %dx%d: %d rows, want %d (or %d with a two-row header)", name, width, height, n, height+5, height+6)
				}
				for _, l := range lines {
					p := plain(l)
					if strings.Contains(p, "⣿") && !strings.Contains(p, "┤") && !strings.HasPrefix(p, "│       │") {
						t.Errorf("%s %dx%d: a plot row lost its axis label, it wrapped: %q", name, width, height, p)
					}
				}
			}
		}
	}
}
