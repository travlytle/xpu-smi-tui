package views

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"
	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	"charm.land/lipgloss/v2"
)

// RenderDetail is everything about one card.
//
// This is the old Inspector and Health tabs merged. They overlapped almost
// completely -- both showed power, both showed temperatures with thresholds,
// both restated device identity -- and the one thing that only Health had, the
// throttle reasons, was the single most useful field in either. Here the
// limiting reason comes first, because it is the answer to "why is this card
// slow", and identity is last, because it never changes.
func RenderDetail(g *xpu.GPUCombined, topo *xpu.TopologyData, width, height int) string {
	return RenderDetailAmong(g, nil, topo, width, height)
}

// RenderDetailAmong is RenderDetail given every card on the machine, which the
// clients list needs: a model server opens a small DRM client on each card it
// can see and allocates on only one, and whether a client is that stray or a
// real workload can only be told by looking at what the process holds elsewhere.
func RenderDetailAmong(g *xpu.GPUCombined, all []*xpu.GPUCombined, topo *xpu.TopologyData, width, height int) string {
	if g == nil {
		return styles.CardStyle.Width(width - 4).Render(
			styles.MutedText.Render("No GPU selected."))
	}

	boxWidth := width - 6
	if boxWidth < 30 {
		boxWidth = 30
	}
	inner := boxWidth - 4

	accent := accentFor(g.Info.GPUNumber())
	title := lipgloss.JoinHorizontal(lipgloss.Center,
		lipgloss.NewStyle().Foreground(accent).Bold(true).Render(g.Info.Label()),
		"  ", styles.BoldText.Render(truncate(g.Info.DeviceName, inner/2)),
		"  ", styles.SubtleText.Render(g.Info.PciBdf),
		"  ", styles.SubtleText.Render(g.Info.DrmDevice))

	sections := []string{title, styles.SubtleText.Render(strings.Repeat("─", inner))}

	add := func(block string) {
		if block != "" {
			sections = append(sections, "", block)
		}
	}

	// A block that needs sysfs is replaced by a note when sysfs is unavailable,
	// rather than vanishing: a section that is simply absent looks the same as
	// one that had nothing to report.
	orNote := func(block, title, why string) string {
		if block != "" {
			return block
		}
		return detailSection(title, []string{styles.SubtleText.Render(why)}, inner)
	}

	// The reason first.
	add(orNote(RenderThrottleStatus(g.Stats.Sysfs, inner), "THROTTLE STATUS",
		"no sysfs data for this card"))
	add(detailPower(g, inner))
	add(detailThermal(g, inner))
	add(detailMemory(g, inner))
	add(orNote(RenderGTSummary(g.Stats.Sysfs, inner), "GRAPHICS TILES",
		"no sysfs data for this card"))
	add(orNote(RenderVramChannelMap(g.Stats.Sysfs, inner), "VRAM CHANNELS",
		"per-channel sensors not available"))
	add(detailEngines(g, all, inner))
	add(detailSystem(g, topo, inner))

	// Every line is cut to the inner width, including the title row and the
	// shared sensor blocks that do not go through detailSection: lipgloss wraps
	// rather than truncates, and one wrapped tail breaks the label column.
	body := strings.Split(lipgloss.JoinVertical(lipgloss.Left, sections...), "\n")
	for i, line := range body {
		body[i] = truncate(line, inner)
	}
	return styles.CardStyle.Width(boxWidth + 2).Render(strings.Join(body, "\n"))
}

// detailSection renders a titled block, cutting each row to the width it was
// given. lipgloss wraps rather than truncates, so an over-long row used to
// continue in column 1 of the next line and break the label column; every
// helper took a width and none used it.
func detailSection(title string, rows []string, width int) string {
	if len(rows) == 0 {
		return ""
	}
	out := []string{styles.BoldText.Render(title)}
	for _, r := range rows {
		out = append(out, truncate("  "+r, width))
	}
	return lipgloss.JoinVertical(lipgloss.Left, out...)
}

// Every row is always drawn. Rows used to be omitted when a value was missing,
// so the view changed shape as readings came and went and the reader could not
// tell "this card does not report it" from "it happens to be zero right now".
// A missing value shows as a dash instead.
func detailPower(g *xpu.GPUCombined, width int) string {
	st := g.Stats.Sysfs
	capText := "--"
	if cap, ok := PowerCap(g); ok {
		capText = fmt.Sprintf("%.0f", cap)
	}
	// avg and peak span the history window rather than xpu-smi's own sample
	// window, which is too short to differ from the draw itself.
	avgText, peakText := "--", "--"
	if g.History != nil && g.History.PowerW != nil && len(g.History.PowerW.Values()) > 0 {
		_, peak, avg := g.History.PowerW.MinMaxAvg()
		avgText = fmt.Sprintf("%.1f", avg)
		peakText = fmt.Sprintf("%.1f", peak)
	}
	rows := []string{
		fmt.Sprintf("%-9s %.1f W   cap %s   avg %s   peak %s  (last %ds)",
			"draw", g.Stats.PowerW, capText, avgText, peakText, xpu.DefaultHistoryCapacity),
	}

	capLine := unknownRow("cap")
	derived := unknownRow("derived")
	energy := unknownRow("energy")
	if st != nil && st.Available {
		if pl := st.PowerLimits; pl.CapW > 0 {
			capLine = fmt.Sprintf("%-9s %.0f W", "cap", pl.CapW)
			if pl.CapInterval > 0 {
				capLine += " averaged over " + formatMillis(pl.CapInterval)
			}
			if pl.CritW > 0 {
				capLine += fmt.Sprintf("   spike limit %.0f W", pl.CritW)
			}
		}
		if st.CardPower.Valid {
			derived = fmt.Sprintf("%-9s %.1f W over %.1fs", "derived", st.CardPower.Watts, st.CardPower.WindowS)
			if st.PkgPower.Valid {
				derived += fmt.Sprintf("   die %.1f / rest %.1f W",
					st.PkgPower.Watts, st.CardPower.Watts-st.PkgPower.Watts)
			}
		}
		if st.Energy.CardJ > 0 {
			energy = fmt.Sprintf("%-9s board %.1f kJ   package %.1f kJ",
				"energy", st.Energy.CardJ/1000, st.Energy.PkgJ/1000)
		}
	}
	return detailSection("POWER", append(rows, capLine, derived, energy), width)
}

// unknownRow renders a labelled row whose value is not available.
func unknownRow(label string) string {
	return fmt.Sprintf("%-9s %s", label, styles.SubtleText.Render("--"))
}

func detailThermal(g *xpu.GPUCombined, width int) string {
	line := func(label string, value, throttle, shutdown float64) string {
		if value <= 0 {
			return unknownRow(label)
		}
		s := lipgloss.NewStyle().Foreground(tempSeverityColor(value, throttle))
		lim := func(v float64) string {
			if v <= 0 {
				return "--"
			}
			return fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprintf("%-9s %s   throttle %s   shutdown %s",
			label, s.Render(fmt.Sprintf("%3.0f °C", value)), lim(throttle), lim(shutdown))
	}
	coreT, coreS := TempLimits(g.Stats.Sysfs, "pkg", g.Health.CoreTemperature)
	memT, memS := TempLimits(g.Stats.Sysfs, "vram", g.Health.MemoryTemperature)

	rows := []string{
		line("core", g.Stats.CoreTempC, coreT, coreS),
		line("vram", g.Stats.MemoryTempC, memT, memS),
	}
	if g.Stats.VrTempC > 0 {
		// No hwmon sensor and no published limit, so no thresholds.
		rows = append(rows, fmt.Sprintf("%-9s %3.0f °C   (no hwmon sensor, no published limit)",
			"vr", g.Stats.VrTempC))
	} else {
		rows = append(rows, unknownRow("vr"))
	}

	for _, label := range []string{"mctrl", "pcie"} {
		row := unknownRow(label)
		if st := g.Stats.Sysfs; st != nil {
			if sensor, ok := st.Temp(label); ok {
				row = fmt.Sprintf("%-9s %3.0f °C   crit %.0f", label, sensor.ValueC, sensor.CritC)
			}
		}
		rows = append(rows, row)
	}

	if g.Stats.FanAvailable || g.Stats.FanSpeedRPM > 0 {
		rows = append(rows, fmt.Sprintf("%-9s %.0f rpm", "fan", g.Stats.FanSpeedRPM))
	} else {
		rows = append(rows, unknownRow("fan"))
	}
	return detailSection("THERMAL", rows, width)
}

func detailMemory(g *xpu.GPUCombined, width int) string {
	used := g.Stats.MemoryUsedMiB
	total, known := MemTotalMiB(g)

	var rows []string
	if g.Stats.MemoryUsedUnavailable {
		// Not "0 used": the driver exposes no card-wide figure. The per-process
		// footprints in the workload table are the real numbers.
		if known {
			rows = append(rows, fmt.Sprintf("%-9s used n/a (driver does not report it)   of %.1f GiB", "vram", total/1024))
		} else {
			rows = append(rows, unknownRow("vram"))
		}
	} else if known {
		free := total - used
		if free < 0 {
			free = 0
		}
		approx := ""
		if g.Stats.MemoryUsedEstimated {
			approx = "~"
		}
		rows = append(rows, fmt.Sprintf("%-9s %s%.1f free   of %s%.1f / %.1f GiB used",
			"vram", approx, free/1024, approx, used/1024, total/1024))
		if g.Stats.MemoryUsedEstimated {
			rows = append(rows, styles.SubtleText.Render("          estimated from per-process usage (driver reports none; run as root for the exact figure)"))
		}
	} else {
		rows = append(rows, fmt.Sprintf("%-9s %.1f GiB used, capacity unknown", "vram", used/1024))
	}
	if t := memoryKind(g.Info); t != "" {
		rows = append(rows, fmt.Sprintf("%-9s %s", "type", t))
	}
	if g.Stats.TrafficUnavailable {
		rows = append(rows, unknownRow("traffic"))
	} else {
		traffic := fmt.Sprintf("%-9s read %.1f  write %.1f MB/s",
			"traffic", g.Stats.MemoryReadKBps/1024, g.Stats.MemoryWriteKBps/1024)
		rows = append(rows, traffic)
	}
	if bw := g.Stats.MemoryBandwidthPercent; bw > 0 && !g.Stats.TrafficUnavailable {
		rows = append(rows, fmt.Sprintf("%-9s %.0f%% of peak", "bandwidth", bw))
	} else {
		rows = append(rows, unknownRow("bandwidth"))
	}
	if g.Stats.HostBackedBytes > 0 {
		spilled := fmt.Sprintf("%-9s %s in host RAM (GTT)", "spilled", xpu.FormatMemoryBytes(g.Stats.HostBackedBytes))
		if g.Stats.HostBackedBytes >= hostResidentMin {
			spilled = lipgloss.NewStyle().Foreground(styles.ColorWarning).Render(spilled + "   VRAM is full; the driver evicted this")
		}
		rows = append(rows, spilled)
	}
	pcieLine := unknownRow("pcie")
	if !g.Stats.TrafficUnavailable || g.Stats.PcieMeasured {
		pcieLine = fmt.Sprintf("%-9s rx %.1f  tx %.1f MB/s",
			"pcie", g.Stats.PcieReadKBps/1024, g.Stats.PcieWriteKBps/1024)
	}
	rows = append(rows, pcieLine)
	return detailSection("MEMORY", rows, width)
}

// detailEngines shows the per-engine split and per-process memory, both
// of which only fdinfo can see.
func detailEngines(g *xpu.GPUCombined, all []*xpu.GPUCombined, width int) string {
	fd := g.Stats.Fdinfo
	if fd == nil || !fd.Available {
		// Keep the section, so its absence is visible rather than silent.
		return detailSection("ENGINES & CLIENTS", []string{
			unknownRow("busy"),
			styles.SubtleText.Render("  no DRM client holds this card, so there is nothing to attribute"),
		}, width)
	}

	var parts []string
	for _, e := range fd.Engines {
		if !e.Valid {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %3.0f%%", e.Name, e.BusyPercent))
	}
	rows := []string{unknownRow("busy")}
	if len(parts) > 0 {
		rows[0] = fmt.Sprintf("%-9s %s", "busy", strings.Join(parts, "   "))
	}
	for _, p := range fd.Processes {
		if p.Memory.VramBytes < 1024*1024 || isStrayClient(g, p, all) {
			continue
		}
		line := fmt.Sprintf("%-9s %-28s vram %-10s gtt %-10s",
			fmt.Sprintf("%d", p.PID), truncate(workloadLabel(p), 28),
			xpu.FormatMemoryBytes(p.Memory.VramBytes),
			xpu.FormatMemoryBytes(p.Memory.GttBytes))
		// drm-total-system is not overflow: every DRM client carries ~184 MiB of
		// it regardless of what it does. Shown for completeness, not as a warning.
		line += styles.SubtleText.Render("host " + xpu.FormatMemoryBytes(p.Memory.SystemBytes))
		rows = append(rows, line)
	}
	rows = append(rows, styles.SubtleText.Render(fmt.Sprintf("%-9s %-28s vram %-10s",
		"-", truncate(otherLabel(g), 28), xpu.FormatMemoryBytes(unattributedVRAM(g)))))
	return detailSection("ENGINES & CLIENTS", rows, width)
}

// detailSystem is what survives of the Topology tab: the handful of facts that
// were worth keeping, as four lines rather than a tab of static diagrams.
func detailSystem(g *xpu.GPUCombined, topo *xpu.TopologyData, width int) string {
	rows := []string{
		fmt.Sprintf("%-9s %s   vbios %s", "driver",
			truncate(g.Info.DriverVersion, 24), truncate(g.Info.VbiosVersion, 20)),
	}
	if g.Stats.ResetRequired {
		why := ""
		if len(g.Stats.ResetReasons) > 0 {
			why = " (" + strings.Join(g.Stats.ResetReasons, ", ") + ")"
		}
		rows = append(rows, lipgloss.NewStyle().Foreground(styles.ColorDanger).Bold(true).
			Render(fmt.Sprintf("%-9s reset required%s", "health", why)))
	} else if g.Stats.Repaired {
		rows = append(rows, fmt.Sprintf("%-9s ok   repaired in the field", "health"))
	}
	if g.Info.OptionROM != "" {
		rows = append(rows, fmt.Sprintf("%-9s %s", "oprom", optionROM(g.Info.OptionROM)))
	}

	link, runtime := unknownRow("link"), unknownRow("runtime")
	if st := g.Stats.Sysfs; st != nil && st.Available {
		if st.Pcie.Resolved {
			link = fmt.Sprintf("%-9s %.1f GT/s x%d @ %s", "link",
				st.Pcie.SpeedGTs, st.Pcie.Width, st.Pcie.SourceBDF)
			if st.Pcie.LocalWidth > 0 && st.Pcie.LocalWidth < st.Pcie.Width {
				link += fmt.Sprintf("   (GPU fn reads %.1f x%d, on-card switch)",
					st.Pcie.LocalSpeed, st.Pcie.LocalWidth)
			}
		}
		if st.PM.Status != "" {
			total := st.PM.ActiveMs + st.PM.SuspendedMs
			pm := st.PM.Status
			if total > 0 {
				pm = fmt.Sprintf("%s   %.2f%% suspended since boot",
					st.PM.Status, 100*float64(st.PM.SuspendedMs)/float64(total))
			}
			runtime = fmt.Sprintf("%-9s %s", "runtime", pm)
			if g.Stats.Idle {
				runtime += styles.SubtleText.Render(fmt.Sprintf("   idle: values sampled %s ago",
					time.Since(g.Stats.Timestamp).Round(time.Second)))
			}
		}
	}
	rows = append(rows, link)
	if r := rebarRow(g); r != "" {
		rows = append(rows, r)
	}
	rows = append(rows, runtime)

	display := fmt.Sprintf("%-9s %s", "display", styles.SubtleText.Render("none connected"))
	if st := g.Stats.Sysfs; st != nil && len(st.Displays) > 0 {
		display = fmt.Sprintf("%-9s %s connected", "display", strings.Join(st.Displays, ", "))
	}
	rows = append(rows, display)

	// CPU affinity and interconnect are the only topology facts worth keeping.
	affinity := unknownRow("affinity")
	if g.Info.CPUAffinity != "" {
		affinity = fmt.Sprintf("%-9s cores %s", "affinity", g.Info.CPUAffinity)
	} else if topo != nil {
		for _, r := range topo.Rows {
			if strings.Contains(r.DeviceName, fmt.Sprintf("%d", g.Info.DeviceID)) && r.CPUAffinity != "" {
				affinity = fmt.Sprintf("%-9s cores %s", "affinity", r.CPUAffinity)
				break
			}
		}
	}
	rows = append(rows, affinity)

	ecc := unknownRow("ecc")
	if g.Info.EccSupported == "no" {
		ecc = fmt.Sprintf("%-9s not supported", "ecc")
	} else if g.Info.EccMode != "" {
		ecc = fmt.Sprintf("%-9s %s   corrected %s   uncorrected %s",
			"ecc", g.Info.EccMode, orUnknown(g.Info.EccCorrected), orUnknown(g.Info.EccUncorrected))
	}
	rows = append(rows, ecc, fmt.Sprintf("%-9s %s", "uuid", g.Info.UUID))
	return detailSection("SYSTEM", rows, width)
}

// memoryKind is "GDDR6   256-bit   8 channels", leaving out what is unknown.
func memoryKind(d xpu.DeviceInfo) string {
	var parts []string
	if d.MemoryType != "" {
		parts = append(parts, d.MemoryType)
	}
	if d.MemoryBusWidth > 0 {
		parts = append(parts, fmt.Sprintf("%d-bit", d.MemoryBusWidth))
	}
	if d.MemoryChannels > 0 {
		parts = append(parts, fmt.Sprintf("%d channels", d.MemoryChannels))
	}
	return strings.Join(parts, "   ")
}

var opromRe = regexp.MustCompile(`CODE VERSION:\s*(\S+?)_OPROM DATA VERSION:\s*(\S+)`)

// optionROM shortens "OPROM CODE VERSION:2304240000_OPROM DATA VERSION:2304140000".
func optionROM(s string) string {
	if m := opromRe.FindStringSubmatch(s); m != nil {
		return fmt.Sprintf("code %s   data %s", m[1], m[2])
	}
	return truncate(s, 60)
}

// rebarRow reports the CPU's window onto VRAM. Without Resizable BAR it is a
// 256 MiB aperture, which costs Arc cards a lot of performance.
func rebarRow(g *xpu.GPUCombined) string {
	bar := g.Info.BARBytes
	if bar == 0 {
		return ""
	}
	window := xpu.FormatMemoryBytes(int64(bar))
	total, known := MemTotalMiB(g)
	if !known || float64(bar) >= total*1024*1024*0.99 {
		return fmt.Sprintf("%-9s on   %s window", "rebar", window)
	}
	return lipgloss.NewStyle().Foreground(styles.ColorWarning).Render(
		fmt.Sprintf("%-9s off  %s window of %.0f GiB VRAM; enable Resizable BAR in firmware setup", "rebar", window, total/1024))
}

// formatMillis prints a hwmon interval: "15 ms", "28 s".
func formatMillis(ms float64) string {
	if ms >= 1000 {
		return trimFloat(ms/1000) + " s"
	}
	return trimFloat(ms) + " ms"
}
