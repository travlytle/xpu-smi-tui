package views

import (
	"fmt"
	"image/color"

	"math"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"
	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	"charm.land/lipgloss/v2"
)

// tempSeverityColor grades a reading against the limit the sensor itself
// carries, rather than against a hardcoded threshold. Sensors on these cards
// disagree about what is hot: vram crits at 105 °C while pkg crits at 100 °C.
func tempSeverityColor(valueC, critC float64) color.Color {
	if critC <= 0 {
		// No limit published, so no verdict. An absolute scale here painted the
		// VR red at its normal operating point -- it runs 15-20 °C above the die
		// by design -- which trains the reader to ignore the one VR warning that
		// does exist, the throttle flag.
		return styles.ColorText
	}
	switch ratio := valueC / critC; {
	case ratio >= 0.90:
		return styles.ColorDanger
	case ratio >= 0.75:
		return styles.ColorWarning
	default:
		return styles.ColorSuccess
	}
}

// RenderVramChannelMap draws the 16 per-channel VRAM sensors as a heat map.
//
// The aggregate vram sensor reads equal to the hottest channel, so it hides
// which channel is hot and how uneven the spread is. That spread is the point:
// a wide delta means one part of the memory is working much harder, or is
// getting much less airflow, than the rest.
func RenderVramChannelMap(st *xpu.SysfsStats, width int) string {
	if st == nil || !st.Available {
		return ""
	}
	channels := st.VramChannels()
	if len(channels) == 0 {
		return ""
	}

	min, max := channels[0].ValueC, channels[0].ValueC
	for _, ch := range channels {
		if ch.ValueC < min {
			min = ch.ValueC
		}
		if ch.ValueC > max {
			max = ch.ValueC
		}
	}
	// Compare on the rounded values the cells actually display. Matching on the
	// raw floats names a single channel while several others visibly show the
	// same number, which just reads as a bug.
	min, max = math.Round(min), math.Round(max)
	hottest := make([]string, 0, 2)
	for _, ch := range channels {
		if math.Round(ch.ValueC) == max {
			hottest = append(hottest, strings.TrimPrefix(ch.Label, "vram_ch_"))
		}
	}

	crit := channels[0].CritC

	// A long tie list ("hottest ch3,ch7,ch11,ch15") makes the header the widest
	// line in the block, and JoinVertical then pads every channel row out to
	// match it, overflowing the card. Cap the list and drop trailing segments
	// that do not fit.
	const maxNamed = 3
	names := hottest
	suffix := ""
	if len(names) > maxNamed {
		suffix = fmt.Sprintf(" +%d", len(names)-maxNamed)
		names = names[:maxNamed]
	}

	header := styles.BoldText.Render("VRAM CHANNELS")
	for _, segment := range []string{
		styles.MutedText.Render(fmt.Sprintf("%.0f-%.0f °C (Δ%.0f)", min, max, max-min)),
		lipgloss.NewStyle().Foreground(tempSeverityColor(max, crit)).
			Render(fmt.Sprintf("hottest ch%s%s @ %.0f °C", strings.Join(names, ",ch"), suffix, max)),
		styles.SubtleText.Render(fmt.Sprintf("crit %.0f °C", crit)),
	} {
		candidate := lipgloss.JoinHorizontal(lipgloss.Left, header, "   ", segment)
		if lipgloss.Width(candidate) > width-2 {
			break
		}
		header = candidate
	}

	// Each cell renders as "12:▇80 " -- eight columns. Snap to a divisor of 16 so
	// the grid comes out even rather than leaving one channel stranded on its own
	// row.
	const cellWidth = 8
	fits := (width - 4) / cellWidth
	perRow := 2
	for _, candidate := range []int{4, 8, 16} {
		if candidate <= fits {
			perRow = candidate
		}
	}
	if perRow > len(channels) {
		perRow = len(channels)
	}

	var rows []string
	for i := 0; i < len(channels); i += perRow {
		end := i + perRow
		if end > len(channels) {
			end = len(channels)
		}
		var cells []string
		for _, ch := range channels[i:end] {
			idx := strings.TrimPrefix(ch.Label, "vram_ch_")
			style := lipgloss.NewStyle().Foreground(tempSeverityColor(ch.ValueC, ch.CritC))
			cells = append(cells, fmt.Sprintf("%s%s",
				styles.SubtleText.Render(fmt.Sprintf("%2s:", idx)),
				style.Render(fmt.Sprintf("▇%-4.0f", ch.ValueC))))
		}
		rows = append(rows, "  "+strings.Join(cells, ""))
	}

	return lipgloss.JoinVertical(lipgloss.Left, append([]string{header}, rows...)...)
}

// RenderThrottleStatus draws per-GT throttle state.
//
// The voltage-regulator flags get their own badge rather than being listed with
// the rest: the VR is the hottest component on these cards and has no
// host-readable temperature sensor, so these flags are the only warning the host
// ever gets about it.
func RenderThrottleStatus(st *xpu.SysfsStats, width int) string {
	if st == nil || !st.Available || len(st.GTs) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, styles.BoldText.Render("THROTTLE STATUS"))

	for _, gt := range st.GTs {
		name := styles.SubtleText.Render(fmt.Sprintf("  %-4s", gt.Name))

		var badge, detail string
		switch {
		case gt.Throttle.AnyVR():
			// Lead with this. It is the only signal the VR is in trouble.
			which := "VR THERMALERT"
			if gt.Throttle.VRTdc && !gt.Throttle.VRThermalert {
				which = "VR TDC"
			} else if gt.Throttle.VRTdc {
				which = "VR THERMALERT + TDC"
			}
			badge = styles.BadgeDanger.Render("⚠ " + which)
			detail = styles.MutedText.Render("voltage regulator limiting; its temperature is on the inspector, not in hwmon")
		case gt.Throttle.Status:
			badge = styles.BadgeWarn.Render("⚠ THROTTLED")
			detail = styles.MutedText.Render(throttleReasonText(gt.Throttle))
		default:
			badge = styles.BadgeOk.Render("✓ CLEAR")
		}

		line := lipgloss.JoinHorizontal(lipgloss.Center, name, " ", badge)
		if detail != "" {
			line = lipgloss.JoinHorizontal(lipgloss.Center, line, " ", detail)
		}
		lines = append(lines, line)
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// throttleReasonText spells out which limits are asserted, preferring the
// individual flags over the driver's summary string.
func throttleReasonText(t xpu.ThrottleFlags) string {
	var reasons []string
	for _, r := range []struct {
		set  bool
		name string
	}{
		{t.PL1, "PL1 power"},
		{t.PL2, "PL2 power"},
		{t.PL4, "PL4 power"},
		{t.Thermal, "die thermal"},
		{t.Prochot, "prochot"},
		{t.RATL, "reliability limit"},
	} {
		if r.set {
			reasons = append(reasons, r.name)
		}
	}
	if len(reasons) == 0 {
		if t.Reasons != "" && t.Reasons != "none" {
			return t.Reasons
		}
		return "limit not identified"
	}
	return strings.Join(reasons, ", ")
}

// RenderGTSummary draws a one-line-per-GT summary of clock and busy state.
func RenderGTSummary(st *xpu.SysfsStats, width int) string {
	if st == nil || !st.Available || len(st.GTs) == 0 {
		return ""
	}

	// The inspector column is roughly half the width of the health card, so drop
	// the wider columns rather than letting the line wrap.
	showRole := width >= 100
	showLimits := width >= 115

	lines := []string{styles.BoldText.Render("GRAPHICS TILES")}
	for _, gt := range st.GTs {
		role := "render/compute"
		if strings.HasSuffix(gt.Idle.Name, "-mc") {
			role = "media"
		}

		busy := styles.SubtleText.Render("  busy    n/a")
		if gt.Idle.BusyValid {
			busy = lipgloss.NewStyle().Foreground(busyColor(gt.Idle.BusyPercent)).
				Render(fmt.Sprintf("  busy %5.1f%%", gt.Idle.BusyPercent))
		}

		// act_freq reads 0 whenever the GT is parked in a c6 window: the clock
		// is stopped. Dimmed so it reads as asleep rather than a failed read.
		actual := styles.RegularText.Render(fmt.Sprintf("act %4.0f", gt.Freq.ActMHz))
		if gt.Freq.ActMHz == 0 {
			actual = styles.SubtleText.Render("act    0")
		}

		parts := []string{styles.SubtleText.Render(fmt.Sprintf("  %-4s", gt.Name))}
		if showRole {
			parts = append(parts, styles.MutedText.Render(fmt.Sprintf("%-15s", role)))
		}
		parts = append(parts, busy,
			styles.RegularText.Render(fmt.Sprintf("   cur %4.0f  ", gt.Freq.CurMHz))+actual+styles.RegularText.Render("  MHz"))
		if showLimits {
			parts = append(parts, styles.SubtleText.Render(fmt.Sprintf("   floor %.0f / ceil %.0f",
				gt.Freq.MinMHz, gt.Freq.MaxMHz)))
		}
		if gt.Idle.Status != "" { // i915 has no idle_status equivalent
			parts = append(parts, styles.SubtleText.Render(fmt.Sprintf("  [%s]", gt.Idle.Status)))
		}

		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Center, parts...))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func busyColor(pct float64) color.Color {
	switch {
	case pct >= 80:
		return styles.ColorSuccess
	case pct >= 20:
		return styles.ColorPrimary
	default:
		return styles.ColorMuted
	}
}

// PowerLimitW returns the enforced cap, preferring what the hardware reports
// over whatever xpu-smi guessed, and falling back only if neither is available.
func PowerLimitW(g *xpu.GPUCombined) float64 {
	if cap, ok := PowerCap(g); ok {
		return cap
	}
	return 275
}

// PowerCap is the enforced cap and whether it is actually known. Callers that
// display the cap use this, so an unknown one reads "--" rather than a 275 W
// nobody measured; PowerLimitW keeps the fallback for chart axes, which need a
// number to draw against.
func PowerCap(g *xpu.GPUCombined) (float64, bool) {
	if g.Stats.Sysfs != nil && g.Stats.Sysfs.PowerLimits.CapW > 0 {
		return g.Stats.Sysfs.PowerLimits.CapW, true
	}
	if g.Info.PowerLimitW > 0 {
		return g.Info.PowerLimitW, true
	}
	return 0, false
}

// TempLimits returns the throttle and shutdown limits for a sensor, preferring
// the _crit / _emergency values the hardware publishes over the thresholds
// xpu-smi reports. The two disagree -- xpu-smi puts the memory throttle at
// 85 °C where the vram sensor's own crit is 105 °C -- and showing different
// numbers on different tabs for the same limit is worse than either.
func TempLimits(st *xpu.SysfsStats, label string, fallback xpu.HealthSensor) (throttle, shutdown float64) {
	throttle, shutdown = fallback.ThrottleThreshold, fallback.ShutdownThreshold
	if st == nil || !st.Available {
		return throttle, shutdown
	}
	sensor, ok := st.Temp(label)
	if !ok {
		return throttle, shutdown
	}
	if sensor.CritC > 0 {
		throttle = sensor.CritC
	}
	if sensor.EmergencyC > 0 {
		shutdown = sensor.EmergencyC
	}
	return throttle, shutdown
}

// CoreTempScale and MemTempScale return the value a temperature gauge or chart
// should be drawn against: the limit that sensor itself publishes.
//
// These were hardcoded, and inconsistently -- the memory gauge used 95 °C while
// everything else used 105 °C, so the same sensor pegged at 100% ten degrees
// early in one view and not in another.
func CoreTempScale(g *xpu.GPUCombined) float64 {
	throttle, _ := TempLimits(g.Stats.Sysfs, "pkg", g.Health.CoreTemperature)
	return orDefaultScale(throttle, 105)
}

func MemTempScale(g *xpu.GPUCombined) float64 {
	throttle, _ := TempLimits(g.Stats.Sysfs, "vram", g.Health.MemoryTemperature)
	return orDefaultScale(throttle, 105)
}

// MemTotalMiB reports the card's VRAM capacity, and whether it is known.
//
// The previous fallback hardcoded 32656 MiB; these cards actually report 28568,
// so an unknown capacity silently overstated the card by 4 GiB and understated
// every utilization figure derived from it.
func MemTotalMiB(g *xpu.GPUCombined) (float64, bool) {
	if g.Info.MemoryTotalMiB > 0 {
		return g.Info.MemoryTotalMiB, true
	}
	return 0, false
}

func orDefaultScale(v, fallback float64) float64 {
	if v > 0 {
		return v
	}
	return fallback
}

// accentFor is a card's identifying colour. It cycles through a palette rather
// than alternating two colours by parity, so a third card is not drawn in the
// first card's colour.
func accentFor(deviceID int) color.Color {
	return styles.GPUColor(deviceID)
}

// truncate shortens s to at most maxWidth display columns, ending in "..." when
// it had to cut. It works in runes and display width: the previous version
// sliced bytes, which produced invalid UTF-8 on any non-ASCII model path and
// mis-measured wide characters.
func truncate(s string, maxWidth int) string {
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	if maxWidth <= 3 {
		return ansi.Truncate(s, maxWidth, "")
	}
	return ansi.Truncate(s, maxWidth, "...")
}

// orUnknown renders an empty string as a dash.
func orUnknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "--"
	}
	return v
}
