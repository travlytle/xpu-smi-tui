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
	var fans [][]float64
	for _, g := range gpus {
		fans = append(fans, g.History.FanRPM.Values())
	}
	return []trendMetric{
		{"Power Draw", "W", maxOf(PowerLimitW), func(g *xpu.GPUCombined) []float64 { return g.History.PowerW.Values() }},
		{"Core Temperature", "°C", maxOf(CoreTempScale), func(g *xpu.GPUCombined) []float64 { return reported(g.History.CoreTemp.Values()) }},
		{"VRAM Utilization", "%", 100, func(g *xpu.GPUCombined) []float64 { return g.History.MemoryUtil.Values() }},
		{"Fan Speed", "RPM", fanScale(fans...), func(g *xpu.GPUCombined) []float64 { return g.History.FanRPM.Values() }},
		{"Memory Temperature", "°C", maxOf(MemTempScale), func(g *xpu.GPUCombined) []float64 { return reported(g.History.MemoryTemp.Values()) }},
	}
}

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
	// height is the view's own (header and footer are outside it). Three
	// boxes stack in a column with a blank row between; each spends 4 rows
	// on border, title and time axis.
	perBox := max(height-2, 12)/3 - 4
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

	if twoColumn {
		return lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.JoinHorizontal(lipgloss.Top, boxes[0], "  ", boxes[1]), "",
			lipgloss.JoinHorizontal(lipgloss.Top, boxes[2], "  ", boxes[3]), "",
			boxes[4])
	}
	return lipgloss.JoinVertical(lipgloss.Left, boxes[0], "", boxes[1], "", boxes[2], "", boxes[3], "", boxes[4])
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
	twoColumn := boxWidth >= 95
	chartW := boxWidth
	if twoColumn {
		chartW = (boxWidth - 4) / 2
	}
	availH := max(height-7, 12)
	chartH := 4
	switch {
	case availH >= 30 && height >= 48:
		chartH = 8
	case availH >= 30:
		chartH = 7
	case availH >= 22:
		chartH = 6
	}

	series := func(values func(*xpu.GPUCombined) []float64) []components.SeriesData {
		out := make([]components.SeriesData, len(gpus))
		for i, g := range gpus {
			out[i] = components.SeriesData{Label: g.Info.Label(), Values: values(g), Color: accentFor(g.Info.GPUNumber())}
		}
		return out
	}
	maxOf := func(f func(*xpu.GPUCombined) float64) float64 {
		m := 0.0
		for _, g := range gpus {
			m = math.Max(m, f(g))
		}
		return m
	}
	zero, pct := 0.0, 100.0
	chart := func(title, unit string, top float64, values func(*xpu.GPUCombined) []float64) string {
		return components.RenderMultiTimeSeriesChart(components.MultiChartOptions{
			Title: title, Unit: unit, Width: chartW, Height: chartH,
			FixedMin: &zero, FixedMax: &top, Series: series(values), Fill: fill,
		})
	}
	var fans [][]float64
	for _, g := range gpus {
		fans = append(fans, g.History.FanRPM.Values())
	}

	power := chart("Power Draw", "W", maxOf(PowerLimitW), func(g *xpu.GPUCombined) []float64 { return g.History.PowerW.Values() })
	core := chart("Core Temperature", "°C", maxOf(CoreTempScale), func(g *xpu.GPUCombined) []float64 { return reported(g.History.CoreTemp.Values()) })
	vram := chart("VRAM Utilization", "%", pct, func(g *xpu.GPUCombined) []float64 { return g.History.MemoryUtil.Values() })
	fan := chart("Fan Speed", "RPM", fanScale(fans...), func(g *xpu.GPUCombined) []float64 { return g.History.FanRPM.Values() })
	mem := chart("Memory Temperature", "°C", maxOf(MemTempScale), func(g *xpu.GPUCombined) []float64 { return reported(g.History.MemoryTemp.Values()) })

	if twoColumn {
		return lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.JoinHorizontal(lipgloss.Top, power, "  ", core), "",
			lipgloss.JoinHorizontal(lipgloss.Top, vram, "  ", fan), "",
			mem)
	}
	return lipgloss.JoinVertical(lipgloss.Left, power, "", core, "", vram, "", fan, "", mem)
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

	twoColumn := boxWidth >= 95
	chartW := boxWidth
	if twoColumn {
		chartW = (boxWidth - 4) / 2
	}

	availH := height - 7
	if availH < 12 {
		availH = 12
	}

	chartH := 5
	if availH >= 30 {
		chartH = 7
		if height >= 48 {
			chartH = 8
		}
	} else if availH >= 22 {
		chartH = 6
	} else {
		chartH = 4
	}

	limitW := PowerLimitW(g)
	zero := 0.0

	powerChart := components.RenderTimeSeriesChart(g.History.PowerW.Values(), components.ChartOptions{
		Title:    g.Info.Label() + " Power Draw",
		Unit:     "W",
		Width:    chartW,
		Height:   chartH,
		Color:    gpuColor,
		FixedMin: &zero,
		FixedMax: &limitW,
	})

	tempMax := CoreTempScale(g)
	coreTempChart := components.RenderTimeSeriesChart(g.History.CoreTemp.Values(), components.ChartOptions{
		Title:    g.Info.Label() + " Core Temperature",
		Unit:     "°C",
		Width:    chartW,
		Height:   chartH,
		Color:    gpuColor,
		FixedMin: &zero,
		FixedMax: &tempMax,
	})

	pctMax := 100.0
	vramChart := components.RenderTimeSeriesChart(g.History.MemoryUtil.Values(), components.ChartOptions{
		Title:    g.Info.Label() + " VRAM Utilization",
		Unit:     "%",
		Width:    chartW,
		Height:   chartH,
		Color:    gpuColor,
		FixedMin: &zero,
		FixedMax: &pctMax,
	})

	fanMax := fanScale(g.History.FanRPM.Values())
	fanChart := components.RenderTimeSeriesChart(g.History.FanRPM.Values(), components.ChartOptions{
		Title:    g.Info.Label() + " Fan Speed",
		Unit:     "RPM",
		Width:    chartW,
		Height:   chartH,
		Color:    gpuColor,
		FixedMin: &zero,
		FixedMax: &fanMax,
	})

	memTempMax := MemTempScale(g) // the sensor's own crit; 95 pegged the chart ten degrees early
	memTempChart := components.RenderTimeSeriesChart(g.History.MemoryTemp.Values(), components.ChartOptions{
		Title:    g.Info.Label() + " Memory Temperature",
		Unit:     "°C",
		Width:    chartW,
		Height:   chartH,
		Color:    gpuColor,
		FixedMin: &zero,
		FixedMax: &memTempMax,
	})

	var chartLayout string
	if twoColumn {
		row1 := lipgloss.JoinHorizontal(lipgloss.Top, powerChart, "  ", coreTempChart)
		row2 := lipgloss.JoinHorizontal(lipgloss.Top, vramChart, "  ", fanChart)
		if availH >= 28 {
			row3 := lipgloss.JoinHorizontal(lipgloss.Top, memTempChart)
			chartLayout = lipgloss.JoinVertical(lipgloss.Left, row1, "", row2, "", row3)
		} else {
			chartLayout = lipgloss.JoinVertical(lipgloss.Left, row1, "", row2)
		}
	} else {
		if availH >= 36 {
			chartLayout = lipgloss.JoinVertical(lipgloss.Left, powerChart, "", coreTempChart, "", vramChart, "", fanChart, "", memTempChart)
		} else if availH >= 24 {
			chartLayout = lipgloss.JoinVertical(lipgloss.Left, powerChart, "", coreTempChart, "", vramChart)
		} else {
			chartLayout = lipgloss.JoinVertical(lipgloss.Left, powerChart, "", coreTempChart)
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, topRow, "", chartLayout)
}

// renderOverlaidDualGraphs is the overlay for exactly two cards: both filled,
// striped where they overlap.
func renderOverlaidDualGraphs(gpus []*xpu.GPUCombined, boxWidth int, height int) string {
	g0 := gpus[0]
	g1 := gpus[1]
	if g0.History == nil || g1.History == nil {
		return styles.CardStyle.Width(boxWidth + 2).Render(styles.MutedText.Render("No history yet."))
	}

	twoColumn := boxWidth >= 95
	chartW := boxWidth
	if twoColumn {
		chartW = (boxWidth - 4) / 2
	}

	availH := height - 7
	if availH < 12 {
		availH = 12
	}

	chartH := 5
	if availH >= 30 {
		chartH = 7
		if height >= 48 {
			chartH = 8
		}
	} else if availH >= 22 {
		chartH = 6
	} else {
		chartH = 4
	}

	zero := 0.0

	colorGpu0 := styles.ColorGPU0 // Neon Cyan (#00F5FF)
	colorGpu1 := styles.ColorGPU1 // Electric Violet (#E056FD)

	// 1. Power Draw (W)
	limitW := math.Max(PowerLimitW(g0), PowerLimitW(g1))
	powerChart := components.RenderDualTimeSeriesChart(components.DualChartOptions{
		Title:    "Power Draw",
		Unit:     "W",
		Width:    chartW,
		Height:   chartH,
		FixedMin: &zero,
		FixedMax: &limitW,
		Series0: components.SeriesData{
			Label:  g0.Info.Label(),
			Values: g0.History.PowerW.Values(),
			Color:  colorGpu0,
		},
		Series1: components.SeriesData{
			Label:  g1.Info.Label(),
			Values: g1.History.PowerW.Values(),
			Color:  colorGpu1,
		},
	})

	// 2. Core Temperature (°C)
	tempMax := math.Max(CoreTempScale(g0), CoreTempScale(g1))
	coreTempChart := components.RenderDualTimeSeriesChart(components.DualChartOptions{
		Title:    "Core Temperature",
		Unit:     "°C",
		Width:    chartW,
		Height:   chartH,
		FixedMin: &zero,
		FixedMax: &tempMax,
		Series0: components.SeriesData{
			Label:  g0.Info.Label(),
			Values: g0.History.CoreTemp.Values(),
			Color:  colorGpu0,
		},
		Series1: components.SeriesData{
			Label:  g1.Info.Label(),
			Values: g1.History.CoreTemp.Values(),
			Color:  colorGpu1,
		},
	})

	// 3. VRAM Utilization (%)
	pctMax := 100.0
	vramChart := components.RenderDualTimeSeriesChart(components.DualChartOptions{
		Title:    "VRAM Utilization",
		Unit:     "%",
		Width:    chartW,
		Height:   chartH,
		FixedMin: &zero,
		FixedMax: &pctMax,
		Series0: components.SeriesData{
			Label:  g0.Info.Label(),
			Values: g0.History.MemoryUtil.Values(),
			Color:  colorGpu0,
		},
		Series1: components.SeriesData{
			Label:  g1.Info.Label(),
			Values: g1.History.MemoryUtil.Values(),
			Color:  colorGpu1,
		},
	})

	// 4. Fan Speed (RPM)
	fanMax := fanScale(g0.History.FanRPM.Values(), g1.History.FanRPM.Values())
	fanChart := components.RenderDualTimeSeriesChart(components.DualChartOptions{
		Title:    "Fan Speed",
		Unit:     "RPM",
		Width:    chartW,
		Height:   chartH,
		FixedMin: &zero,
		FixedMax: &fanMax,
		Series0: components.SeriesData{
			Label:  g0.Info.Label(),
			Values: g0.History.FanRPM.Values(),
			Color:  colorGpu0,
		},
		Series1: components.SeriesData{
			Label:  g1.Info.Label(),
			Values: g1.History.FanRPM.Values(),
			Color:  colorGpu1,
		},
	})

	// 5. Memory Temperature (°C)
	memMax := math.Max(MemTempScale(g0), MemTempScale(g1))
	memTempChart := components.RenderDualTimeSeriesChart(components.DualChartOptions{
		Title:    "Memory Temperature",
		Unit:     "°C",
		Width:    chartW,
		Height:   chartH,
		FixedMin: &zero,
		FixedMax: &memMax,
		Series0: components.SeriesData{
			Label:  g0.Info.Label(),
			Values: g0.History.MemoryTemp.Values(),
			Color:  colorGpu0,
		},
		Series1: components.SeriesData{
			Label:  g1.Info.Label(),
			Values: g1.History.MemoryTemp.Values(),
			Color:  colorGpu1,
		},
	})

	if twoColumn {
		row1 := lipgloss.JoinHorizontal(lipgloss.Top, powerChart, "  ", coreTempChart)
		row2 := lipgloss.JoinHorizontal(lipgloss.Top, vramChart, "  ", fanChart)
		if availH >= 28 {
			row3 := lipgloss.JoinHorizontal(lipgloss.Top, memTempChart)
			return lipgloss.JoinVertical(lipgloss.Left, row1, "", row2, "", row3)
		}
		return lipgloss.JoinVertical(lipgloss.Left, row1, "", row2)
	}

	if availH >= 36 {
		return lipgloss.JoinVertical(lipgloss.Left, powerChart, "", coreTempChart, "", vramChart, "", fanChart, "", memTempChart)
	} else if availH >= 24 {
		return lipgloss.JoinVertical(lipgloss.Left, powerChart, "", coreTempChart, "", vramChart)
	}
	return lipgloss.JoinVertical(lipgloss.Left, powerChart, "", coreTempChart)
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
