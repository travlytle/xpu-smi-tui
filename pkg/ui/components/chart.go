package components

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Braille dot offsets:
// row 0: 0x01 (col 0), 0x08 (col 1)
// row 1: 0x02 (col 0), 0x10 (col 1)
// row 2: 0x04 (col 0), 0x20 (col 1)
// row 3: 0x40 (col 0), 0x80 (col 1)
var brailleOffsets = [4][2]rune{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// BrailleCanvas provides 2x4 sub-cell resolution per character
type BrailleCanvas struct {
	charWidth  int
	charHeight int
	dotsWidth  int
	dotsHeight int
	grid       [][]rune
}

// NewBrailleCanvas creates a new canvas with given character dimensions
func NewBrailleCanvas(charWidth, charHeight int) *BrailleCanvas {
	if charWidth <= 0 {
		charWidth = 20
	}
	if charHeight <= 0 {
		charHeight = 5
	}
	grid := make([][]rune, charHeight)
	for y := range grid {
		grid[y] = make([]rune, charWidth)
		for x := range grid[y] {
			grid[y][x] = 0x2800 // blank braille
		}
	}
	return &BrailleCanvas{
		charWidth:  charWidth,
		charHeight: charHeight,
		dotsWidth:  charWidth * 2,
		dotsHeight: charHeight * 4,
		grid:       grid,
	}
}

// Set sets a dot at sub-pixel coordinate (x, y) where y=0 is the bottom!
func (c *BrailleCanvas) Set(x, y int) {
	if x < 0 || x >= c.dotsWidth || y < 0 || y >= c.dotsHeight {
		return
	}
	invertedY := (c.dotsHeight - 1) - y

	charX := x / 2
	charY := invertedY / 4
	dotX := x % 2
	dotY := invertedY % 4

	c.grid[charY][charX] |= brailleOffsets[dotY][dotX]
}

// FillColumn fills all sub-pixel dots in column x from baseline up to y (btop-style area fill)
func (c *BrailleCanvas) FillColumn(x, y int) {
	if x < 0 || x >= c.dotsWidth {
		return
	}
	if y < 0 {
		y = 0
	}
	if y >= c.dotsHeight {
		y = c.dotsHeight - 1
	}
	for h := 0; h <= y; h++ {
		c.Set(x, h)
	}
}

// Line draws a continuous line between two sub-pixel points
func (c *BrailleCanvas) Line(x0, y0, x1, y1 int) {
	dx := math.Abs(float64(x1 - x0))
	dy := math.Abs(float64(y1 - y0))
	sx := 1
	if x0 > x1 {
		sx = -1
	}
	sy := 1
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy

	for {
		c.Set(x0, y0)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

// RenderLines returns the styled text rows with area-fill and subtle guide lines
func (c *BrailleCanvas) RenderLines(fg color.Color, guideRows map[int]bool) []string {
	lines := make([]string, c.charHeight)
	style := lipgloss.NewStyle().Foreground(fg).Bold(true)
	gridStyle := lipgloss.NewStyle().Foreground(styles.ColorGrid)

	for y := 0; y < c.charHeight; y++ {
		isGuide := guideRows[y]
		var rowParts []string
		for x := 0; x < c.charWidth; x++ {
			r := c.grid[y][x]
			if r != 0x2800 {
				rowParts = append(rowParts, style.Render(string(r)))
			} else if isGuide {
				if x%2 == 0 {
					rowParts = append(rowParts, gridStyle.Render("·"))
				} else {
					rowParts = append(rowParts, " ")
				}
			} else {
				rowParts = append(rowParts, " ")
			}
		}
		lines[y] = strings.Join(rowParts, "")
	}
	return lines
}

// axisPrefixWidth is the fixed width of the y-axis label and its tick, "275.0 ┤".
const axisPrefixWidth = 7

// fitHeader lays a chart's title and its right-hand legend out in width
// columns: on one row when both fit, otherwise the legend goes on a row of its
// own, and if it cannot fit even there it is cut to width.
func fitHeader(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap >= 2 {
		return lipgloss.JoinHorizontal(lipgloss.Center, left, strings.Repeat(" ", gap), right)
	}
	if lipgloss.Width(right) > width {
		right = ansi.Truncate(right, width, "…")
	}
	return lipgloss.JoinVertical(lipgloss.Left, ansi.Truncate(left, width, "…"), right)
}

// FitHeader is fitHeader for callers outside this package.
func FitHeader(left, right string, width int) string { return fitHeader(left, right, width) }

// ChartOptions configures a single-series time-series graph
type ChartOptions struct {
	Title      string
	Unit       string
	Width      int
	Height     int
	Color      color.Color
	FixedMin   *float64
	FixedMax   *float64
	TimeWindow string
	Window     int // samples spanning the width (default 60)
}

// RenderTimeSeriesChart renders a single-series btop-style graph with area fill and guide lines
func RenderTimeSeriesChart(values []float64, opt ChartOptions) string {
	if opt.Width < 25 {
		opt.Width = 25
	}
	if opt.Height < 4 {
		opt.Height = 4
	}

	// Everything is laid out against the width inside the frame. Sizing the
	// plot against opt.Width left each row one column too wide once the frame
	// was subtracted below, and lipgloss wrapped every braille row onto a line
	// of its own.
	inner := opt.Width - styles.CardStyle.GetHorizontalFrameSize()
	plotWidth := inner - axisPrefixWidth
	if plotWidth < 10 {
		plotWidth = 10
	}

	canvas := NewBrailleCanvas(plotWidth, opt.Height)

	minVal, maxVal, avgVal, latest := 0.0, 100.0, 0.0, 0.0
	if len(values) > 0 {
		latest = values[len(values)-1]
		minVal = values[0]
		maxVal = values[0]
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
		avgVal = sum / float64(len(values))
	}

	displayMin := minVal
	displayMax := maxVal
	if opt.FixedMin != nil {
		displayMin = *opt.FixedMin
	}
	if opt.FixedMax != nil {
		displayMax = *opt.FixedMax
	}
	if displayMax <= displayMin {
		displayMax = displayMin + 10.0
	}

	dotsW := canvas.dotsWidth
	dotsH := canvas.dotsHeight

	if len(values) > 0 {
		data := values
		if len(data) > opt.window() {
			data = data[len(data)-opt.window():]
		}

		span := displayMax - displayMin
		if span <= 0.0001 {
			span = 1.0
		}

		prevX := -1
		prevY := -1

		for i, v := range data {
			x := sampleX(i, len(data), opt.window(), dotsW)
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
	}

	// Guide rows for horizontal gridlines (at 25%, 50%, 75% height)
	guideRows := make(map[int]bool)
	if opt.Height >= 5 {
		guideRows[opt.Height/2] = true
		guideRows[opt.Height/4] = true
		guideRows[(3*opt.Height)/4] = true
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText)
	valStyle := lipgloss.NewStyle().Bold(true).Foreground(opt.Color)
	statsStyle := lipgloss.NewStyle().Foreground(styles.ColorMuted)

	headerLeft := fmt.Sprintf("%s %s", titleStyle.Render(opt.Title), valStyle.Render(fmt.Sprintf("%.1f %s", latest, opt.Unit)))
	headerRight := statsStyle.Render(fmt.Sprintf("min: %.1f | avg: %.1f | max: %.1f", minVal, avgVal, maxVal))

	headerRow := fitHeader(headerLeft, headerRight, inner)

	chartLines := canvas.RenderLines(opt.Color, guideRows)
	var plotRows []string

	axisStyle := lipgloss.NewStyle().Foreground(styles.ColorSubtle)
	span := displayMax - displayMin

	for row := 0; row < opt.Height; row++ {
		ratio := 1.0 - (float64(row) / float64(opt.Height-1))
		yVal := displayMin + (ratio * span)

		var axisLabel string
		if math.Abs(yVal) >= 1000 {
			axisLabel = fmt.Sprintf("%5.0f %s", yVal, opt.Unit)
		} else if math.Abs(yVal) >= 10 {
			axisLabel = fmt.Sprintf("%5.1f", yVal)
		} else {
			axisLabel = fmt.Sprintf("%5.2f", yVal)
		}

		tick := "┤"
		if guideRows[row] {
			tick = "┼"
		}
		axisPrefix := axisStyle.Render(fmt.Sprintf("%-6.6s%s", axisLabel, tick)) // fixed 7 cols; a 4-digit label overran by 3 and widened its row
		plotRows = append(plotRows, fmt.Sprintf("%s%s", axisPrefix, chartLines[row]))
	}

	bottomAxis := axisStyle.Render("      └" + strings.Repeat("─", plotWidth))

	timeMarker := "-60s"
	if opt.TimeWindow != "" {
		timeMarker = opt.TimeWindow
	}
	timeLabelLeft := axisStyle.Render(timeMarker)
	timeLabelRight := axisStyle.Render("now")
	timeGap := plotWidth - lipgloss.Width(timeLabelLeft) - lipgloss.Width(timeLabelRight)
	if timeGap < 1 {
		timeGap = 1
	}
	timeRow := fmt.Sprintf("       %s%s%s", timeLabelLeft, strings.Repeat(" ", timeGap), timeLabelRight)

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		headerRow,
		strings.Join(plotRows, "\n"),
		bottomAxis,
		timeRow,
	)

	// lipgloss's Width covers the padding but not the border, so a block that
	// is opt.Width wide overall is Width(opt.Width - border); the text inside
	// it then has opt.Width - frame columns, which is what inner was sized to.
	return styles.CardStyle.Width(opt.Width).Render(content)
}

// -----------------------------------------------------------------------------
// DUAL-SERIES OVERLAY CHART (GPU 0 & GPU 1 on the Same Chart)
// -----------------------------------------------------------------------------

// DualBrailleCanvas supports plotting two distinct series with cell ownership colors
type DualBrailleCanvas struct {
	charWidth  int
	charHeight int
	dotsWidth  int
	dotsHeight int
	grid       [][]rune
	owners     [][]uint8 // 0: unowned, 1: series 0, 2: series 1, 3: intersection / both
}

// NewDualBrailleCanvas creates a new dual-series canvas
func NewDualBrailleCanvas(charWidth, charHeight int) *DualBrailleCanvas {
	if charWidth <= 0 {
		charWidth = 20
	}
	if charHeight <= 0 {
		charHeight = 5
	}
	grid := make([][]rune, charHeight)
	owners := make([][]uint8, charHeight)
	for y := range grid {
		grid[y] = make([]rune, charWidth)
		owners[y] = make([]uint8, charWidth)
		for x := range grid[y] {
			grid[y][x] = 0x2800 // blank braille
		}
	}
	return &DualBrailleCanvas{
		charWidth:  charWidth,
		charHeight: charHeight,
		dotsWidth:  charWidth * 2,
		dotsHeight: charHeight * 4,
		grid:       grid,
		owners:     owners,
	}
}

func (c *DualBrailleCanvas) Set(x, y int, seriesID uint8) {
	if x < 0 || x >= c.dotsWidth || y < 0 || y >= c.dotsHeight {
		return
	}
	invertedY := (c.dotsHeight - 1) - y

	charX := x / 2
	charY := invertedY / 4
	dotX := x % 2
	dotY := invertedY % 4

	c.grid[charY][charX] |= brailleOffsets[dotY][dotX]
	currentOwner := c.owners[charY][charX]
	if currentOwner == 0 {
		c.owners[charY][charX] = seriesID
	} else if currentOwner != seriesID {
		c.owners[charY][charX] = 3 // intersection / both!
	}
}

// FillColumn fills all dots from baseline up to y with seriesID
func (c *DualBrailleCanvas) FillColumn(x, y int, seriesID uint8) {
	if x < 0 || x >= c.dotsWidth {
		return
	}
	if y < 0 {
		y = 0
	}
	if y >= c.dotsHeight {
		y = c.dotsHeight - 1
	}
	for h := 0; h <= y; h++ {
		c.Set(x, h, seriesID)
	}
}

func (c *DualBrailleCanvas) Line(x0, y0, x1, y1 int, seriesID uint8) {
	dx := math.Abs(float64(x1 - x0))
	dy := math.Abs(float64(y1 - y0))
	sx := 1
	if x0 > x1 {
		sx = -1
	}
	sy := 1
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy

	for {
		c.Set(x0, y0, seriesID)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

// RenderLines returns the styled text rows of the dual braille chart with area fill and guide lines
func (c *DualBrailleCanvas) RenderLines(color0, color1 color.Color, guideRows map[int]bool) []string {
	lines := make([]string, c.charHeight)
	style0 := lipgloss.NewStyle().Foreground(color0).Bold(true)
	style1 := lipgloss.NewStyle().Foreground(color1).Bold(true)
	gridStyle := lipgloss.NewStyle().Foreground(styles.ColorGrid)

	for y := 0; y < c.charHeight; y++ {
		isGuide := guideRows[y]
		var rowParts []string
		for x := 0; x < c.charWidth; x++ {
			charRune := c.grid[y][x]
			charStr := string(charRune)
			switch c.owners[y][x] {
			case 1:
				rowParts = append(rowParts, style0.Render(charStr))
			case 2:
				rowParts = append(rowParts, style1.Render(charStr))
			case 3:
				// Alternating striped pattern using the two GPU colors
				if x%2 == 0 {
					rowParts = append(rowParts, style0.Render(charStr))
				} else {
					rowParts = append(rowParts, style1.Render(charStr))
				}
			default:
				if isGuide {
					if x%2 == 0 {
						rowParts = append(rowParts, gridStyle.Render("·"))
					} else {
						rowParts = append(rowParts, " ")
					}
				} else {
					rowParts = append(rowParts, " ")
				}
			}
		}
		lines[y] = strings.Join(rowParts, "")
	}
	return lines
}

// SeriesData represents one series in an overlay chart
type SeriesData struct {
	Label  string
	Values []float64
	Color  color.Color
}

// DualChartOptions configures a 2-series overlaid time-series graph
type DualChartOptions struct {
	Title      string
	Unit       string
	Width      int
	Height     int
	FixedMin   *float64
	FixedMax   *float64
	TimeWindow string
	Window     int // samples spanning the width (default 60)
	Series0    SeriesData
	Series1    SeriesData
}

// RenderDualTimeSeriesChart renders two series overlaid onto the same Braille graph
func RenderDualTimeSeriesChart(opt DualChartOptions) string {
	if opt.Width < 30 {
		opt.Width = 30
	}
	if opt.Height < 4 {
		opt.Height = 4
	}

	inner := opt.Width - styles.CardStyle.GetHorizontalFrameSize()
	plotWidth := inner - axisPrefixWidth
	if plotWidth < 10 {
		plotWidth = 10
	}

	canvas := NewDualBrailleCanvas(plotWidth, opt.Height)

	// Calculate stats for Series 0
	latest0, min0, max0 := 0.0, 0.0, 0.0
	if len(opt.Series0.Values) > 0 {
		latest0 = opt.Series0.Values[len(opt.Series0.Values)-1]
		min0 = opt.Series0.Values[0]
		max0 = opt.Series0.Values[0]
		for _, v := range opt.Series0.Values {
			if v < min0 {
				min0 = v
			}
			if v > max0 {
				max0 = v
			}
		}
	}

	// Calculate stats for Series 1
	latest1, min1, max1 := 0.0, 0.0, 0.0
	if len(opt.Series1.Values) > 0 {
		latest1 = opt.Series1.Values[len(opt.Series1.Values)-1]
		min1 = opt.Series1.Values[0]
		max1 = opt.Series1.Values[0]
		for _, v := range opt.Series1.Values {
			if v < min1 {
				min1 = v
			}
			if v > max1 {
				max1 = v
			}
		}
	}

	// Shared Scale
	displayMin := math.Min(min0, min1)
	displayMax := math.Max(max0, max1)
	if opt.FixedMin != nil {
		displayMin = *opt.FixedMin
	}
	if opt.FixedMax != nil {
		displayMax = *opt.FixedMax
	}
	if displayMax <= displayMin {
		displayMax = displayMin + 10.0
	}

	dotsW := canvas.dotsWidth
	dotsH := canvas.dotsHeight
	span := displayMax - displayMin
	if span <= 0.0001 {
		span = 1.0
	}

	// Plot Series 0 (Area fill + Ridge line)
	if len(opt.Series0.Values) > 0 {
		data0 := opt.Series0.Values
		if len(data0) > opt.window() {
			data0 = data0[len(data0)-opt.window():]
		}
		prevX, prevY := -1, -1
		for i, v := range data0 {
			x := sampleX(i, len(data0), opt.window(), dotsW)
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
					canvas.FillColumn(col, interpY, 1)
				}
			} else {
				canvas.FillColumn(x, y, 1)
			}
			prevX = x
			prevY = y
		}
	}

	// Plot Series 1 (Area fill + Ridge line)
	if len(opt.Series1.Values) > 0 {
		data1 := opt.Series1.Values
		if len(data1) > opt.window() {
			data1 = data1[len(data1)-opt.window():]
		}
		prevX, prevY := -1, -1
		for i, v := range data1 {
			x := sampleX(i, len(data1), opt.window(), dotsW)
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
					canvas.FillColumn(col, interpY, 2)
				}
			} else {
				canvas.FillColumn(x, y, 2)
			}
			prevX = x
			prevY = y
		}
	}

	// Check if any overlap exists on the canvas
	hasOverlap := false
	for y := 0; y < canvas.charHeight; y++ {
		for x := 0; x < canvas.charWidth; x++ {
			if canvas.owners[y][x] == 3 {
				hasOverlap = true
				break
			}
		}
		if hasOverlap {
			break
		}
	}

	// Guide rows for horizontal gridlines (at 25%, 50%, 75% height)
	guideRows := make(map[int]bool)
	if opt.Height >= 5 {
		guideRows[opt.Height/2] = true
		guideRows[opt.Height/4] = true
		guideRows[(3*opt.Height)/4] = true
	}

	// Header: Title on Left, Dual Legends on Right
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText)
	titleStr := titleStyle.Render(opt.Title)

	legend0 := lipgloss.NewStyle().Bold(true).Foreground(opt.Series0.Color).Render(
		fmt.Sprintf("■ %s: %.1f %s", opt.Series0.Label, latest0, opt.Unit),
	)
	legend1 := lipgloss.NewStyle().Bold(true).Foreground(opt.Series1.Color).Render(
		fmt.Sprintf("■ %s: %.1f %s", opt.Series1.Label, latest1, opt.Unit),
	)

	var legendRow string
	if hasOverlap {
		sq0 := lipgloss.NewStyle().Foreground(opt.Series0.Color).Render("■")
		sq1 := lipgloss.NewStyle().Foreground(opt.Series1.Color).Render("■")
		legendBoth := lipgloss.NewStyle().Foreground(styles.ColorMuted).Render(fmt.Sprintf("(%s%s Both)", sq0, sq1))
		legendRow = lipgloss.JoinHorizontal(lipgloss.Center, legend0, "  ", legend1, "  ", legendBoth)
	} else {
		legendRow = lipgloss.JoinHorizontal(lipgloss.Center, legend0, "   ", legend1)
	}

	headerRow := fitHeader(titleStr, legendRow, inner)

	chartLines := canvas.RenderLines(opt.Series0.Color, opt.Series1.Color, guideRows)
	var plotRows []string

	axisStyle := lipgloss.NewStyle().Foreground(styles.ColorSubtle)

	for row := 0; row < opt.Height; row++ {
		ratio := 1.0 - (float64(row) / float64(opt.Height-1))
		yVal := displayMin + (ratio * span)

		var axisLabel string
		if math.Abs(yVal) >= 1000 {
			axisLabel = fmt.Sprintf("%5.0f %s", yVal, opt.Unit)
		} else if math.Abs(yVal) >= 10 {
			axisLabel = fmt.Sprintf("%5.1f", yVal)
		} else {
			axisLabel = fmt.Sprintf("%5.2f", yVal)
		}

		tick := "┤"
		if guideRows[row] {
			tick = "┼"
		}
		axisPrefix := axisStyle.Render(fmt.Sprintf("%-6.6s%s", axisLabel, tick)) // fixed 7 cols; a 4-digit label overran by 3 and widened its row
		plotRows = append(plotRows, fmt.Sprintf("%s%s", axisPrefix, chartLines[row]))
	}

	bottomAxis := axisStyle.Render("      └" + strings.Repeat("─", plotWidth))

	timeMarker := "-60s"
	if opt.TimeWindow != "" {
		timeMarker = opt.TimeWindow
	}
	timeLabelLeft := axisStyle.Render(timeMarker)
	timeLabelRight := axisStyle.Render("now")
	timeGap := plotWidth - lipgloss.Width(timeLabelLeft) - lipgloss.Width(timeLabelRight)
	if timeGap < 1 {
		timeGap = 1
	}
	timeRow := fmt.Sprintf("       %s%s%s", timeLabelLeft, strings.Repeat(" ", timeGap), timeLabelRight)

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		headerRow,
		strings.Join(plotRows, "\n"),
		bottomAxis,
		timeRow,
	)

	// lipgloss's Width covers the padding but not the border, so a block that
	// is opt.Width wide overall is Width(opt.Width - border); the text inside
	// it then has opt.Width - frame columns, which is what inner was sized to.
	return styles.CardStyle.Width(opt.Width).Render(content)
}

const defaultWindow = 60

func (o ChartOptions) window() int     { return windowOr(o.Window) }
func (o DualChartOptions) window() int { return windowOr(o.Window) }

func windowOr(w int) int {
	if w > 1 {
		return w
	}
	return defaultWindow
}

// sampleX places sample i of n, newest at the right edge, so a full window
// spans the whole plot whatever its width.
func sampleX(i, n, window, dotsW int) int {
	slot := window - n + i
	return int(math.Round(float64(slot) * float64(dotsW-1) / float64(window-1)))
}
