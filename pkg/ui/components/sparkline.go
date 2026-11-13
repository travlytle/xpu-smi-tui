package components

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"

	"charm.land/lipgloss/v2"
)

// RenderSparkline renders a 1-row continuous Braille curve using 2x4 sub-pixel dots.
// Optional bounds[0]=fixedMin, bounds[1]=fixedMax allow consistent scaling across multi-device views.
func RenderSparkline(values []float64, width int, color color.Color, bounds ...float64) string {
	if width <= 0 {
		width = 10
	}
	if len(values) == 0 {
		return strings.Repeat(" ", width)
	}

	canvas := NewBrailleCanvas(width, 1)
	dotsW := canvas.dotsWidth
	dotsH := canvas.dotsHeight

	data := values
	if len(data) > dotsW {
		data = data[len(data)-dotsW:]
	}

	minVal := data[0]
	maxVal := data[0]
	for _, v := range data {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	displayMin := minVal
	displayMax := maxVal
	if len(bounds) >= 2 {
		displayMin = bounds[0]
		displayMax = bounds[1]
	} else {
		if displayMax-displayMin < 10.0 {
			mid := (displayMin + displayMax) / 2.0
			displayMin = math.Max(0, mid-5.0)
			displayMax = displayMin + 10.0
		}
	}

	span := displayMax - displayMin
	if span <= 0.0001 {
		span = 1.0
	}

	startX := dotsW - len(data)
	if startX < 0 {
		startX = 0
	}

	prevX := -1
	prevY := -1
	for i, v := range data {
		x := startX + i
		// Offset by the display floor, not the data's own minimum. With fixed
		// bounds the span comes from the bounds, so subtracting the data minimum
		// mapped any steady value to zero: 250 W against a 0-275 W scale drew
		// along the bottom, as did every other constant reading.
		norm := (v - displayMin) / span
		if norm < 0 {
			norm = 0
		}
		if norm > 1 {
			norm = 1
		}
		y := int(math.Round(norm * float64(dotsH-1)))

		if prevX >= 0 && x > prevX {
			for col := prevX + 1; col <= x; col++ {
				t := float64(col-prevX) / float64(x-prevX)
				interpY := int(math.Round(float64(prevY) + t*float64(y-prevY)))
				canvas.FillColumn(col, interpY)
			}
		} else {
			canvas.FillColumn(x, y)
		}
		prevX = x
		prevY = y
	}

	emptyGuide := make(map[int]bool)
	lines := canvas.RenderLines(color, emptyGuide)
	if len(lines) > 0 {
		return lines[0]
	}
	return strings.Repeat(" ", width)
}

// RenderSparklineBlock renders a titled mini Braille chart (3 rows tall) with area fill and stats.
// Optional bounds[0]=fixedMin, bounds[1]=fixedMax ensure multi-device cards share the exact same scale.
func RenderSparklineBlock(title string, values []float64, width int, unit string, color color.Color, bounds ...float64) string {
	if len(values) == 0 {
		return ""
	}
	// Window to what is plotted before computing the scale and the stats line,
	// or both describe samples that are not on screen.
	if plotted := width * 2; len(values) > plotted {
		values = values[len(values)-plotted:]
	}

	latest := values[len(values)-1]
	minVal := values[0]
	maxVal := values[0]
	sum := 0.0
	for _, v := range values {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
		sum += v
	}
	avgVal := sum / float64(len(values))

	if width < 15 {
		width = 15
	}

	// 3-row mini Braille canvas
	const chartHeight = 3
	canvas := NewBrailleCanvas(width, chartHeight)
	dotsW := canvas.dotsWidth
	dotsH := canvas.dotsHeight

	data := values
	if len(data) > dotsW {
		data = data[len(data)-dotsW:]
	}

	displayMin := minVal
	displayMax := maxVal
	if len(bounds) >= 2 {
		displayMin = bounds[0]
		displayMax = bounds[1]
	} else {
		// Prevent tiny noise fluctuations from stretching across the entire height
		if displayMax-displayMin < 10.0 {
			mid := (displayMin + displayMax) / 2.0
			displayMin = math.Max(0, mid-5.0)
			displayMax = displayMin + 10.0
		}
	}

	span := displayMax - displayMin
	if span <= 0.0001 {
		span = 1.0
	}

	startX := dotsW - len(data)
	if startX < 0 {
		startX = 0
	}

	prevX := -1
	prevY := -1
	for i, v := range data {
		x := startX + i
		norm := (v - displayMin) / span
		if norm < 0 {
			norm = 0
		}
		if norm > 1 {
			norm = 1
		}
		y := int(math.Round(norm * float64(dotsH-1)))

		if prevX >= 0 && x > prevX {
			for col := prevX + 1; col <= x; col++ {
				t := float64(col-prevX) / float64(x-prevX)
				interpY := int(math.Round(float64(prevY) + t*float64(y-prevY)))
				canvas.FillColumn(col, interpY)
			}
		} else {
			canvas.FillColumn(x, y)
		}
		prevX = x
		prevY = y
	}

	guideRows := map[int]bool{1: true} // middle guide row
	chartLines := canvas.RenderLines(color, guideRows)

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText)
	statsStyle := lipgloss.NewStyle().Foreground(styles.ColorMuted)
	valStyle := lipgloss.NewStyle().Bold(true).Foreground(color)

	line1 := fmt.Sprintf("%s: %s", titleStyle.Render(title), valStyle.Render(fmt.Sprintf("%.1f %s", latest, unit)))
	plotContent := strings.Join(chartLines, "\n")
	line3 := statsStyle.Render(fmt.Sprintf("min: %.0f | avg: %.0f | max: %.0f", minVal, avgVal, maxVal))

	return lipgloss.JoinVertical(lipgloss.Left, line1, plotContent, line3)
}
