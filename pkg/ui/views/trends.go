package views

import (
	"fmt"
	"math"
	"strings"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/components"
	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"
	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	"charm.land/lipgloss/v2"
)

// RenderTrends draws the time-series history.
//
// This is the old Dashboard/Graphs mode, now a tab of its own. It is opt-in
// because charts are expensive in rows -- five of them filled a 50-row terminal
// entirely -- and trend is a peripheral signal, not the thing an operator opens
// the tool to read.
func RenderTrends(gpus []*xpu.GPUCombined, selectedIdx int, width int, height int) string {
	boxWidth := width - 6
	if boxWidth < 30 {
		boxWidth = 30
	}
	if len(gpus) == 0 {
		return lipgloss.NewStyle().Foreground(styles.ColorMuted).Render("Discovering GPUs...")
	}

	if len(gpus) == 1 || (selectedIdx >= 0 && selectedIdx < len(gpus)) {
		idx := selectedIdx
		if idx < 0 || idx >= len(gpus) {
			idx = 0
		}
		return renderGraphsView(gpus[idx], boxWidth, height)
	}
	switch StyleFor(len(gpus)) {
	case StylePerCard:
		return renderSmallMultiples(gpus, boxWidth, height)
	case StyleStripes:
		if len(gpus) == 2 {
			return renderOverlaidDualGraphs(gpus, boxWidth, height)
		}
		return renderOverlaidMultiGraphs(gpus, boxWidth, height, true)
	}
	return renderOverlaidMultiGraphs(gpus, boxWidth, height, false)
}

// TrendsStyle is how Trends shows several cards.
type TrendsStyle int

const (
	StyleAuto    TrendsStyle = iota // by card count: see StyleFor
	StyleStripes                    // one chart per metric, filled, alternating colours where cards overlap
	StyleLines                      // one chart per metric, a line per card
	StylePerCard                    // a small graph per card, shaded by value
)

// Style is the active setting; v cycles it.
var Style = StyleAuto

var styleNames = map[TrendsStyle]string{StyleStripes: "stripes", StyleLines: "lines", StylePerCard: "per card"}

func (s TrendsStyle) String() string { return styleNames[s] }

// ParseTrendsStyle reads a saved setting; anything unknown is auto.
func ParseTrendsStyle(s string) TrendsStyle {
	for k, v := range styleNames {
		if v == s {
			return k
		}
	}
	return StyleAuto
}

// StyleFor is the style n cards are drawn in. Without a choice it is the one
// that reads best at that count: stripes for two (past two, the area every
// card shares turns into a weave of colours), lines for three, a graph per
// card from four.
func StyleFor(n int) TrendsStyle {
	if Style != StyleAuto {
		return Style
	}
	switch {
	case n <= 2:
		return StyleStripes
	case n == 3:
		return StyleLines
	}
	return StylePerCard
}

// NextStyle is the style v moves to from the one n cards are drawn in now.
func NextStyle(n int) TrendsStyle {
	return map[TrendsStyle]TrendsStyle{StyleStripes: StyleLines, StyleLines: StylePerCard, StylePerCard: StyleStripes}[StyleFor(n)]
}

// trendMetric is one Trends chart: its title, unit, ceiling and series.
type trendMetric struct {
	title, unit string
	top         float64
	values      func(*xpu.GPUCombined) []float64
}

func trendMetrics(gpus []*xpu.GPUCombined) []trendMetric {
	maxOf := func(f func(*xpu.GPUCombined) float64) float64 {
		m := 0.0
		for _, g := range gpus {
			m = math.Max(m, f(g))
		}
		return m
	}
	var fans, clocks [][]float64
	for _, g := range gpus {
		fans = append(fans, g.History.FanRPM.Values())
		clocks = append(clocks, g.History.Frequency.Values())
	}
	clockTop := maxOf(func(g *xpu.GPUCombined) float64 { return g.Info.MaxFrequencyMHz })
	if clockTop <= 0 {
		clockTop = math.Max(1000, math.Ceil(seriesPeak(clocks...)*1.1/100)*100)
	}
	// Grouped by what each answers, read left to right then down (two per
	// row when wide): is it working and how fast; what it costs and how hard
	// the cooling works; memory fill and traffic; temperatures.
	return []trendMetric{
		{"Compute", "%", 100, func(g *xpu.GPUCombined) []float64 { return g.History.Compute.Values() }},
		{"Clock", "MHz", clockTop, func(g *xpu.GPUCombined) []float64 { return g.History.Frequency.Values() }},
		{"Power Draw", "W", maxOf(PowerLimitW), func(g *xpu.GPUCombined) []float64 { return g.History.PowerW.Values() }},
		{"Fan Speed", "RPM", fanScale(fans...), func(g *xpu.GPUCombined) []float64 { return g.History.FanRPM.Values() }},
		{"VRAM Utilization", "%", 100, func(g *xpu.GPUCombined) []float64 { return g.History.MemoryUtil.Values() }},
		{"Memory Bandwidth", "%", 100, func(g *xpu.GPUCombined) []float64 { return g.History.Bandwidth.Values() }},
		{"Core Temperature", "°C", maxOf(CoreTempScale), func(g *xpu.GPUCombined) []float64 { return reported(g.History.CoreTemp.Values()) }},
		{"Memory Temperature", "°C", maxOf(MemTempScale), func(g *xpu.GPUCombined) []float64 { return reported(g.History.MemoryTemp.Values()) }},
	}
}

// layoutCharts lays charts out two to a row when wide, one per row otherwise,
// with a blank row between rows. Every chart is shown; the viewport scrolls.
func layoutCharts(charts []string, twoColumn bool) string {
	var rows []string
	step := 1
	if twoColumn {
		step = 2
	}
	for i := 0; i < len(charts); i += step {
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		if step == 2 && i+1 < len(charts) {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, charts[i], "  ", charts[i+1]))
		} else {
			rows = append(rows, charts[i])
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// chartLayout is the chart width, whether charts sit two to a row, and a
// chart height that fits the rows of charts in height where it can.
func chartLayout(boxWidth, height, n int) (chartW int, twoColumn bool, chartH int) {
	twoColumn = boxWidth >= 95
	chartW = boxWidth
	rows := n
	if twoColumn {
		chartW = (boxWidth - 4) / 2
		rows = (n + 1) / 2
	}
	// Each chart spends 6 rows on border, header (a legend may wrap to a
	// second row), axis and time labels, plus the blank row between rows.
	chartH = clamp((height-rows)/rows-6, minChartH, maxChartH)
	return chartW, twoColumn, chartH
}

// Plot rows per chart: charts grow to fill the view but never shrink below
// minChartH (32 braille dots); the view scrolls instead.
const (
	minChartH = 8
	maxChartH = 16
)

// renderSmallMultiples gives each metric a box holding one small filled
// graph per card, shaded by value, btop style. It scales to many cards where
// an overlay cannot.
func renderSmallMultiples(gpus []*xpu.GPUCombined, boxWidth int, height int) string {
	for _, g := range gpus {
		if g.History == nil {
			return styles.CardStyle.Width(boxWidth + 2).Render(styles.MutedText.Render("No history yet."))
		}
	}
	twoColumn := boxWidth >= 95
	chartW := boxWidth
	if twoColumn {
		chartW = (boxWidth - 4) / 2
	}
	// height is the view's own (header and footer are outside it). Boxes
	// stack in a column with a blank row between; each spends 4 rows on
	// border, title and time axis.
	perColumn := len(trendMetrics(gpus))
	if twoColumn {
		perColumn = (perColumn + 1) / 2
	}
	perBox := max(height-(perColumn-1), 12)/perColumn - 4
	rows := clamp(perBox/len(gpus), 1, 4)

	labelW := 0
	for _, g := range gpus {
		labelW = max(labelW, lipgloss.Width(g.Info.Label()))
	}
	const valueW = 10

	var boxes []string
	for _, m := range trendMetrics(gpus) {
		inner := chartW - styles.CardStyle.GetHorizontalFrameSize()
		graphW := max(inner-labelW-valueW-2, 10)
		title := components.FitHeader(styles.BoldText.Render(m.title),
			styles.MutedText.Render(fmt.Sprintf("0–%s %s", trimFloat(m.top), m.unit)), inner)
		lines := []string{title}
		for _, g := range gpus {
			vals := m.values(g)
			value := styles.MutedText.Render("--")
			if len(vals) > 0 {
				value = styles.RegularText.Render(fmt.Sprintf("%.0f %s", vals[len(vals)-1], m.unit))
			}
			graph := components.RenderMiniGraph(vals, 0, m.top, graphW, rows, 0, styles.Heat)
			label := lipgloss.NewStyle().Bold(true).Foreground(accentFor(g.Info.GPUNumber())).Render(g.Info.Label())
			for r, gl := range graph {
				left, right := strings.Repeat(" ", labelW), ""
				if r == len(graph)-1 { // the bottom row, where every graph starts
					left = label + strings.Repeat(" ", labelW-lipgloss.Width(label))
					right = value
				}
				gl += strings.Repeat(" ", graphW-lipgloss.Width(gl))
				lines = append(lines, left+" "+gl+" "+strings.Repeat(" ", max(valueW-lipgloss.Width(right), 0))+right)
			}
		}
		axis := styles.SubtleText
		lines = append(lines, strings.Repeat(" ", labelW+1)+axis.Render("-60s")+
			strings.Repeat(" ", max(graphW-7, 1))+axis.Render("now"))
		boxes = append(boxes, styles.CardStyle.Width(chartW).Render(strings.Join(lines, "\n")))
	}

	return layoutCharts(boxes, twoColumn)
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

// trimFloat prints 275 as "275" and 99.5 as "99.5".
func trimFloat(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", v), "0"), ".")
}

// renderOverlaidMultiGraphs puts every card on one chart per metric, as
// striped filled areas (fill) or lines.
func renderOverlaidMultiGraphs(gpus []*xpu.GPUCombined, boxWidth int, height int, fill bool) string {
	for _, g := range gpus {
		if g.History == nil {
			return styles.CardStyle.Width(boxWidth + 2).Render(styles.MutedText.Render("No history yet."))
		}
	}
	metrics := trendMetrics(gpus)
	chartW, twoColumn, _ := chartLayout(boxWidth, height, len(metrics))
	zero := 0.0
	render := func(chartH int) []string {
		var charts []string
		for _, m := range metrics {
			top := m.top
			series := make([]components.SeriesData, len(gpus))
			for i, g := range gpus {
				series[i] = components.SeriesData{Label: g.Info.Label(), Values: m.values(g), Color: accentFor(g.Info.GPUNumber())}
			}
			charts = append(charts, components.RenderMultiTimeSeriesChart(components.MultiChartOptions{
				Title: m.title, Unit: m.unit, Width: chartW, Height: chartH,
				FixedMin: &zero, FixedMax: &top, Series: series, Fill: fill,
			}))
		}
		return charts
	}
	// Legends wrap to as many rows as the card count needs, so measure the
	// tallest box at the minimum height and give the plots what is left.
	const minH = 4 // measuring height only
	charts := render(minH)
	overhead := 0
	for _, c := range charts {
		overhead = max(overhead, lipgloss.Height(c)-minH)
	}
	rows := len(metrics)
	if twoColumn {
		rows = (rows + 1) / 2
	}
	charts = render(clamp((height-(rows-1))/rows-overhead, minChartH, maxChartH))
	return layoutCharts(charts, twoColumn)
}

// renderGraphsView is one card's full charts, with axes and min/avg/max: the
// view when a single card is focused or installed.
func renderGraphsView(g *xpu.GPUCombined, boxWidth int, height int) string {
	gpuColor := accentFor(g.Info.GPUNumber())
	if g.History == nil {
		return styles.CardStyle.Width(boxWidth + 2).Render(styles.MutedText.Render("No history yet."))
	}

	// The header is budgeted: the full name and bus address ran to 75 columns,
	// and lipgloss pads every line to the widest, so at 60 columns the whole
	// Trends frame was dragged off-screen.
	headerBadge := lipgloss.NewStyle().Bold(true).Background(gpuColor).Foreground(styles.ColorBg).Padding(0, 1).Render(g.Info.Label())
	room := boxWidth - 4 - lipgloss.Width(headerBadge) - 2
	parts := []string{headerBadge}
	if room > 12 {
		parts = append(parts, "  ", styles.BoldText.Render(truncate(g.Info.DeviceName, room)))
		room -= lipgloss.Width(parts[len(parts)-1]) + 2
	}
	if bdf := fmt.Sprintf("[%s | %s]", g.Info.PciBdf, g.Info.DrmDevice); room >= lipgloss.Width(bdf)+2 {
		parts = append(parts, "  ", styles.SubtleText.Render(bdf))
	}
	topRow := lipgloss.JoinHorizontal(lipgloss.Center, parts...)

	metrics := trendMetrics([]*xpu.GPUCombined{g})
	chartW, twoColumn, chartH := chartLayout(boxWidth, height-2, len(metrics))
	zero := 0.0
	var charts []string
	for _, m := range metrics {
		top := m.top
		charts = append(charts, components.RenderTimeSeriesChart(m.values(g), components.ChartOptions{
			Title: g.Info.Label() + " " + m.title, Unit: m.unit, Width: chartW, Height: chartH,
			Color: gpuColor, FixedMin: &zero, FixedMax: &top,
		}))
	}
	return lipgloss.JoinVertical(lipgloss.Left, topRow, "", layoutCharts(charts, twoColumn))
}

// renderOverlaidDualGraphs is the overlay for exactly two cards: both filled,
// striped where they overlap.
func renderOverlaidDualGraphs(gpus []*xpu.GPUCombined, boxWidth int, height int) string {
	g0, g1 := gpus[0], gpus[1]
	if g0.History == nil || g1.History == nil {
		return styles.CardStyle.Width(boxWidth + 2).Render(styles.MutedText.Render("No history yet."))
	}
	metrics := trendMetrics(gpus)
	chartW, twoColumn, chartH := chartLayout(boxWidth, height, len(metrics))
	zero := 0.0
	var charts []string
	for _, m := range metrics {
		top := m.top
		charts = append(charts, components.RenderDualTimeSeriesChart(components.DualChartOptions{
			Title: m.title, Unit: m.unit, Width: chartW, Height: chartH, FixedMin: &zero, FixedMax: &top,
			Series0: components.SeriesData{Label: g0.Info.Label(), Values: m.values(g0), Color: accentFor(g0.Info.GPUNumber())},
			Series1: components.SeriesData{Label: g1.Info.Label(), Values: m.values(g1), Color: accentFor(g1.Info.GPUNumber())},
		}))
	}
	return layoutCharts(charts, twoColumn)
}

// seriesPeak is the highest value across series.
func seriesPeak(series ...[]float64) float64 {
	peak := 0.0
	for _, vs := range series {
		for _, v := range vs {
			peak = math.Max(peak, v)
		}
	}
	return peak
}

// fanScale is the fan chart's ceiling: the highest speed seen plus headroom,
// rounded up to 500 RPM. Cards publish no maximum fan speed.
func fanScale(series ...[]float64) float64 {
	peak := 0.0
	for _, vs := range series {
		for _, v := range vs {
			peak = math.Max(peak, v)
		}
	}
	return math.Max(1000, math.Ceil(peak*1.1/500)*500)
}

// reported drops a temperature history that is all zeros: 0 °C is a card
// without that sensor (the A40 has no VRAM sensor), not a reading.
func reported(vs []float64) []float64 {
	for _, v := range vs {
		if v != 0 {
			return vs
		}
	}
	return nil
}
