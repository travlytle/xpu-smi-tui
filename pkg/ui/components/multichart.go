package components

import (
	"fmt"
	"image/color"
	"math"
	"math/bits"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"
)

// MultiChartOptions configures an overlay of any number of series, each drawn
// as a line. Filled areas, as the two-series chart uses, would bury all but
// the highest series once there are more than two.
type MultiChartOptions struct {
	Title      string
	Unit       string
	Width      int
	Height     int
	FixedMin   *float64
	FixedMax   *float64
	TimeWindow string
	Window     int // samples spanning the width (default 60)
	Series     []SeriesData
	// Fill draws each series as a filled area, striped through the colours of
	// every series sharing a cell, like the two-series chart; otherwise lines.
	Fill bool
}

func (o MultiChartOptions) window() int { return windowOr(o.Window) }

// multiCanvas is a braille canvas that records which series touched each
// cell, as a bitmask (series 0 = bit 0; up to 32 series).
type multiCanvas struct {
	charW, charH int
	dotsW, dotsH int
	grid         [][]rune
	owners       [][]uint32
}

func newMultiCanvas(charW, charH int) *multiCanvas {
	c := &multiCanvas{charW: charW, charH: charH, dotsW: charW * 2, dotsH: charH * 4}
	c.grid = make([][]rune, charH)
	c.owners = make([][]uint32, charH)
	for y := range c.grid {
		c.grid[y] = make([]rune, charW)
		c.owners[y] = make([]uint32, charW)
		for x := range c.grid[y] {
			c.grid[y][x] = 0x2800
		}
	}
	return c
}

func (c *multiCanvas) set(x, y, series int) {
	if x < 0 || x >= c.dotsW || y < 0 || y >= c.dotsH {
		return
	}
	inv := (c.dotsH - 1) - y
	cx, cy := x/2, inv/4
	c.grid[cy][cx] |= brailleOffsets[inv%4][x%2]
	c.owners[cy][cx] |= 1 << uint(series%32)
}

// fill sets every dot from the baseline up to y in column x.
func (c *multiCanvas) fill(x, y, series int) {
	for h := 0; h <= y; h++ {
		c.set(x, h, series)
	}
}

func (c *multiCanvas) line(x0, y0, x1, y1, series int) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		c.set(x0, y0, series)
		if x0 == x1 && y0 == y1 {
			return
		}
		if e2 := 2 * err; e2 >= dy {
			err += dy
			x0 += sx
		} else {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// render colours each cell by its owner; a cell several series share cycles
// through their colours across the row, the same striping the two-series
// chart uses for its overlap.
func (c *multiCanvas) render(colors []color.Color, guideRows map[int]bool) []string {
	stylesBy := make([]lipgloss.Style, len(colors))
	for i, col := range colors {
		stylesBy[i] = lipgloss.NewStyle().Foreground(col).Bold(true)
	}
	grid := lipgloss.NewStyle().Foreground(styles.ColorGrid)
	lines := make([]string, c.charH)
	for y := 0; y < c.charH; y++ {
		var b strings.Builder
		for x := 0; x < c.charW; x++ {
			mask := c.owners[y][x]
			if mask == 0 {
				if guideRows[y] && x%2 == 0 {
					b.WriteString(grid.Render("·"))
				} else {
					b.WriteByte(' ')
				}
				continue
			}
			pick := x % bits.OnesCount32(mask)
			for s := 0; s < 32; s++ {
				if mask&(1<<uint(s)) == 0 {
					continue
				}
				if pick == 0 {
					b.WriteString(stylesBy[s%len(stylesBy)].Render(string(c.grid[y][x])))
					break
				}
				pick--
			}
		}
		lines[y] = b.String()
	}
	return lines
}

// RenderMultiTimeSeriesChart draws every series as a line on one shared scale,
// with a legend of each series' latest value that wraps to the chart width.
func RenderMultiTimeSeriesChart(opt MultiChartOptions) string {
	if opt.Width < 30 {
		opt.Width = 30
	}
	if opt.Height < 4 {
		opt.Height = 4
	}
	inner := opt.Width - styles.CardStyle.GetHorizontalFrameSize()
	plotWidth := max(inner-axisPrefixWidth, 10)
	canvas := newMultiCanvas(plotWidth, opt.Height)

	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range opt.Series {
		for _, v := range s.Values {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
	}
	if math.IsInf(lo, 0) {
		lo, hi = 0, 0
	}
	if opt.FixedMin != nil {
		lo = *opt.FixedMin
	}
	if opt.FixedMax != nil {
		hi = *opt.FixedMax
	}
	if hi <= lo {
		hi = lo + 10
	}
	span := hi - lo

	colors := make([]color.Color, len(opt.Series))
	for i, s := range opt.Series {
		colors[i] = s.Color
		data := s.Values
		if len(data) > opt.window() {
			data = data[len(data)-opt.window():]
		}
		px, py := -1, -1
		for j, v := range data {
			x := sampleX(j, len(data), opt.window(), canvas.dotsW)
			norm := math.Max(0, math.Min(1, (v-lo)/span))
			y := int(math.Round(norm * float64(canvas.dotsH-1)))
			switch {
			case opt.Fill && px >= 0 && x > px:
				for col := px + 1; col <= x; col++ {
					canvas.fill(col, int(math.Round(float64(py)+float64(y-py)*float64(col-px)/float64(x-px))), i)
				}
			case opt.Fill:
				canvas.fill(x, y, i)
			case px >= 0:
				canvas.line(px, py, x, y, i)
			default:
				canvas.set(x, y, i)
			}
			px, py = x, y
		}
	}

	axisLabels, guideRows := yAxis(lo, hi, opt.Height)

	title := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText).Render(opt.Title)
	var entries []string
	for _, s := range opt.Series {
		latest := 0.0
		if len(s.Values) > 0 {
			latest = s.Values[len(s.Values)-1]
		}
		prec := 1
		if math.Abs(latest) >= 100 {
			prec = 0 // keeps the legend short enough to pack three to a row
		}
		text := fmt.Sprintf("■ %s: %.*f %s", s.Label, prec, latest, opt.Unit)
		if len(s.Values) == 0 {
			text = fmt.Sprintf("■ %s: --", s.Label) // the card has no such reading
		}
		entries = append(entries, lipgloss.NewStyle().Bold(true).Foreground(s.Color).Render(text))
	}
	header := append([]string{title}, wrapEntries(entries, inner)...)

	axis := lipgloss.NewStyle().Foreground(styles.ColorSubtle)
	chart := canvas.render(colors, guideRows)
	rows := make([]string, 0, opt.Height)
	for r := 0; r < opt.Height; r++ {
		rows = append(rows, axis.Render(axisLabels[r])+chart[r])
	}

	left, right := axis.Render(opt.TimeWindow), axis.Render("now")
	if opt.TimeWindow == "" {
		left = axis.Render("-60s")
	}
	gap := max(plotWidth-lipgloss.Width(left)-lipgloss.Width(right), 1)

	content := lipgloss.JoinVertical(lipgloss.Left,
		strings.Join(header, "\n"),
		strings.Join(rows, "\n"),
		axis.Render("      └"+strings.Repeat("─", plotWidth)),
		"       "+left+strings.Repeat(" ", gap)+right,
	)
	return styles.CardStyle.Width(opt.Width).Render(content)
}

// wrapEntries packs legend entries into lines no wider than width.
func wrapEntries(entries []string, width int) []string {
	var lines []string
	cur, curW := "", 0
	for _, e := range entries {
		w := lipgloss.Width(e)
		if curW > 0 && curW+3+w > width {
			lines = append(lines, cur)
			cur, curW = "", 0
		}
		if curW > 0 {
			cur += "   "
			curW += 3
		}
		cur += e
		curW += w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
