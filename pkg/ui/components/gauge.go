package components

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"

	"charm.land/lipgloss/v2"
)

type GaugeColorMode int

const (
	GaugeColorPrimary GaugeColorMode = iota
	GaugeColorThermal
	GaugeColorLoad
)

// Braille sub-dot levels from 0 to 8 dots
var brailleLevels = []rune{
	' ', // 0 dots
	'⡀', // 1 dot (bottom left)
	'⣀', // 2 dots (bottom row)
	'⣄', // 3 dots
	'⣤', // 4 dots (bottom 2 rows)
	'⣴', // 5 dots (U+28F4; U+28F5 has six and a gap)
	'⣶', // 6 dots (bottom 3 rows)
	'⣷', // 7 dots
	'⣿', // 8 dots (full 4 rows)
}

// RenderBar renders a high-resolution Braille dot progress bar
func RenderBar(current, max float64, width int, mode GaugeColorMode) string {
	if width <= 0 {
		width = 20
	}
	if max <= 0 {
		max = 100
	}

	ratio := current / max
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}

	totalDots := int(math.Round(ratio * float64(width*8)))
	fullCells := totalDots / 8
	remDots := totalDots % 8

	var filledSb strings.Builder
	cellsUsed := 0

	for i := 0; i < fullCells && cellsUsed < width; i++ {
		filledSb.WriteRune(brailleLevels[8])
		cellsUsed++
	}
	if cellsUsed < width && remDots > 0 {
		filledSb.WriteRune(brailleLevels[remDots])
		cellsUsed++
	}

	emptyCount := width - cellsUsed
	if emptyCount < 0 {
		emptyCount = 0
	}
	emptyStr := strings.Repeat("⠤", emptyCount)

	var color color.Color
	switch mode {
	case GaugeColorThermal:
		if current < 65 {
			color = styles.ColorSuccess
		} else if current < 80 {
			color = styles.ColorWarning
		} else {
			color = styles.ColorDanger
		}
	case GaugeColorLoad:
		pct := ratio * 100
		if pct < 70 {
			color = styles.ColorSuccess
		} else if pct < 85 {
			color = styles.ColorWarning
		} else {
			color = styles.ColorDanger
		}
	default:
		color = styles.ColorPrimary
	}

	filledStyled := lipgloss.NewStyle().Foreground(color).Bold(true).Render(filledSb.String())
	emptyStyled := lipgloss.NewStyle().Foreground(styles.ColorGrid).Render(emptyStr)

	return filledStyled + emptyStyled
}

// RenderMetricGauge renders a labelled metric with Braille gauge bar and formatted values
func RenderMetricGauge(label string, current, max float64, unit string, barWidth int, mode GaugeColorMode) string {
	pct := 0.0
	if max > 0 {
		pct = (current / max) * 100
		if pct > 100 {
			pct = 100
		}
	}

	labelStyled := lipgloss.NewStyle().Width(14).Bold(true).Foreground(styles.ColorMuted).Render(label)
	bar := RenderBar(current, max, barWidth, mode)

	var valueStr string
	if unit == "%" {
		valueStr = fmt.Sprintf("%5.1f%%", current)
	} else if max > 0 {
		valueStr = fmt.Sprintf("%5.1f / %-5.1f %s (%4.1f%%)", current, max, unit, pct)
	} else {
		valueStr = fmt.Sprintf("%5.1f %s", current, unit)
	}

	valStyled := lipgloss.NewStyle().Foreground(styles.ColorText).Render(valueStr)

	return fmt.Sprintf("%s %s %s", labelStyled, bar, valStyled)
}
