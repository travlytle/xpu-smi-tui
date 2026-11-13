package views

import (
	"fmt"
	"math"

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
	if len(gpus) == 2 {
		return renderOverlaidDualGraphs(gpus, boxWidth, height)
	}
	// Three or more: the overlay is drawn for exactly two series, so each card
	// gets its own block, stacked; the viewport scrolls them.
	blocks := make([]string, 0, len(gpus))
	for _, g := range gpus {
		blocks = append(blocks, renderGraphsView(g, boxWidth, height))
	}
	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}

// -----------------------------------------------------------------------------
// 1. GRAPHS VIEW (Default View - Overlaid curves or single-GPU)
// -----------------------------------------------------------------------------

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

// renderOverlaidDualGraphs overlays GPU 0 and GPU 1 together on the same chart per metric
func renderOverlaidDualGraphs(gpus []*xpu.GPUCombined, boxWidth int, height int) string {
	// Exactly two cards reach here; RenderTrends stacks single-card blocks otherwise.
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

// -----------------------------------------------------------------------------
// 2. TABLE VIEW (Compact Responsive Density Matrix)
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// 3. CARDS VIEW (Multi-Card Gauges & Trends)
// -----------------------------------------------------------------------------

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
