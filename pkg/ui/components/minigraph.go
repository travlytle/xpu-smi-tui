package components

import (
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// GraphSymbols is how filled mini graphs are drawn, as in btop: braille for
// detail (2 columns x 4 levels a cell), block for solid bars (8 levels a cell).
type GraphSymbols int

const (
	Braille GraphSymbols = iota
	Block
)

// Symbols is the active setting; the UI toggles it.
var Symbols = Braille

func (g GraphSymbols) String() string {
	if g == Block {
		return "block"
	}
	return "braille"
}

// ParseGraphSymbols reads a saved setting; anything unknown is braille.
func ParseGraphSymbols(s string) GraphSymbols {
	if strings.TrimSpace(s) == "block" {
		return Block
	}
	return Braille
}

var blockRunes = []rune(" ▁▂▃▄▅▆▇█")

// braille dot bits, bottom row first, for the left and right column.
var brailleLeft = [4]rune{0x40, 0x04, 0x02, 0x01}
var brailleRight = [4]rune{0x80, 0x20, 0x10, 0x08}

// RenderMiniGraph draws values as a filled graph width cells wide and rows
// high on the scale lo..hi. Each cell takes heat's colour at the height its
// fill reaches, so the shade reads the level as btop's graphs do. Columns
// before the first sample stay empty.
func RenderMiniGraph(values []float64, lo, hi float64, width, rows, window int, heat func(float64) color.Color) []string {
	if width < 1 || rows < 1 {
		return nil
	}
	if window < 2 {
		window = defaultWindow
	}
	sub := 4 // levels per cell
	cols := width * 2
	if Symbols == Block {
		sub, cols = 8, width
	}
	total := rows * sub
	span := hi - lo
	if span <= 0 {
		span = 1
	}

	// Fill level per column; -1 means no data yet.
	level := make([]int, cols)
	for i := range level {
		level[i] = -1
	}
	data := values
	if len(data) > window {
		data = data[len(data)-window:]
	}
	toLevel := func(v float64) float64 { return math.Max(0, math.Min(1, (v-lo)/span)) * float64(total) }
	px, pl := -1, 0.0
	for i, v := range data {
		x := sampleX(i, len(data), window, cols)
		l := toLevel(v)
		if px >= 0 && x > px {
			for c := px + 1; c <= x; c++ {
				level[c] = int(math.Round(pl + (l-pl)*float64(c-px)/float64(x-px)))
			}
		} else {
			level[x] = int(math.Round(l))
		}
		px, pl = x, l
	}
	for i, l := range level {
		if l == 0 {
			level[i] = 1 // a visible baseline: data present, value at the floor
		}
	}

	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		bottom := (rows - 1 - r) * sub
		var b strings.Builder
		for x := 0; x < width; x++ {
			var ch rune
			top := 0
			if Symbols == Block {
				k := clampInt(level[x]-bottom, 0, sub)
				ch, top = blockRunes[k], k
			} else {
				l := clampInt(level[2*x]-bottom, 0, sub)
				rr := clampInt(level[2*x+1]-bottom, 0, sub)
				ch = 0x2800
				for d := 0; d < l; d++ {
					ch |= brailleLeft[d]
				}
				for d := 0; d < rr; d++ {
					ch |= brailleRight[d]
				}
				top = max(l, rr)
			}
			if top == 0 {
				b.WriteByte(' ')
				continue
			}
			c := heat(float64(bottom+top) / float64(total))
			b.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(ch)))
		}
		lines[r] = b.String()
	}
	return lines
}

func clampInt(v, lo, hi int) int {
	return max(lo, min(hi, v))
}
