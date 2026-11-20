package components

import (
	"fmt"
	"math"
	"strconv"
)

// axisPrefixWidth is the fixed width of the y-axis label and its tick, "  275┤".
const axisPrefixWidth = 7

// yAxis labels a chart's rows on round values only (steps of 1, 2, 2.5 or 5
// x 10^n), as Grafana and most plotting libraries do. It returns each row's
// axis prefix and the labelled rows inside the plot, which carry a guide line.
func yAxis(lo, hi float64, height int) ([]string, map[int]bool) {
	prefixes := make([]string, height)
	for r := range prefixes {
		prefixes[r] = "      │"
	}
	guides := map[int]bool{}
	span := hi - lo
	if height < 2 || span <= 0 {
		return prefixes, guides
	}
	step := niceStep(span, height)
	decimals := 0
	if step < 1 {
		decimals = len(strconv.FormatFloat(step, 'f', -1, 64)) - 2
	}
	for k := math.Ceil(lo/step - 1e-9); k*step <= hi+1e-9*span; k++ {
		v := k * step
		r := int(math.Round((1 - (v-lo)/span) * float64(height-1)))
		if r < 0 || r >= height {
			continue
		}
		if v == 0 {
			v = 0 // no "-0"
		}
		prefixes[r] = fmt.Sprintf("%6.6s┤", fmt.Sprintf("%.*f ", decimals, v))
		if r > 0 && r < height-1 {
			guides[r] = true
		}
	}
	return prefixes, guides
}

// niceStep is the smallest round step giving at most six labels that sit at
// least two rows apart, so rounding to rows keeps them evenly spaced.
func niceStep(span float64, height int) float64 {
	mag := math.Pow(10, math.Floor(math.Log10(span/6)))
	for {
		for _, m := range []float64{1, 2, 2.5, 5} {
			step := m * mag
			labels := math.Floor(span/step+1e-9) + 1
			if labels <= 6 && float64(height-1)*step/span >= 2 {
				return step
			}
		}
		mag *= 10
	}
}
