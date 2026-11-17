package views

import (
	"strings"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"

	"charm.land/lipgloss/v2"
)

// legendSection groups related terms so the page reads as an explanation rather
// than a flat list of abbreviations.
type legendSection struct {
	title   string
	entries []legendEntry
}

type legendEntry struct{ term, meaning string }

var legendSections = []legendSection{
	{"UTILIZATION — is the card working, and at what", []legendEntry{
		{"BUSY", "The graphics tile's idle-residency counter: it left its parked power state at some point in the last interval. It does NOT mean the card is computing: a resident model keeps the tile from parking. Read BUSY as \"a model is loaded here\", and CCS as \"it is actually working right now\"."},
		{"CCS", "Compute engine busy, from per-process cycle counters. This is the number that answers \"is it generating tokens\": it sits near 0% between requests and rises only while one is being processed, even while BUSY stays pinned at 100%."},
		{"rcs / bcs", "Render engine (3D) and blitter (copies between host memory and VRAM). High BCS with low CCS means the card is moving data rather than working."},
		{"vcs / vecs", "Video decode, and video encode/enhance. Idle on an inference box."},
	}},
	{"POWER — how hard, and what is holding it back", []legendEntry{
		{"POWER W", "Draw against the limit the driver reports, not a hardcoded TDP: the card's own limit, or on cards that publish none (the A40 on xe) the package's. A tag beside it names whatever is currently limiting the card."},
		{"spike limit", "In Detail: the ceiling on millisecond-long power spikes. Often about twice the cap; the cap is what the card can draw for longer than that."},
		{"PL1", "Sustained power limit: held at its long-run average cap."},
		{"PL2", "Burst power limit: pinned at the cap and would go faster with more budget. This is the expected state under a full load, not a fault."},
		{"PL4", "Peak current limit: clocks pulled back pre-emptively to stop a current spike."},
		{"THRM", "Die temperature is limiting the card. Unlike the PL tags this is a cooling problem, not a budget one."},
		{"VR!", "The voltage regulator is limiting. It is the hottest component on these cards and has no hwmon sensor, so this flag is the only warning the host ever gets about it."},
		{"PROC / RATL", "An external prochot signal, or the reliability lifetime limit."},
	}},
	{"THERMAL", []legendEntry{
		{"CORE", "Die temperature, the hwmon 'pkg' sensor. Coloured against that sensor's own critical limit rather than a fixed threshold."},
		{"VRAM °C", "Memory temperature. The aggregate equals the hottest of the 16 per-channel sensors; Detail shows the full channel map."},
		{"VR", "Voltage regulator temperature, from Level Zero. Typically runs 15 °C above everything else and has no published limit, so no threshold is shown."},
		{"FAN", "Fan speed in RPM, median-filtered: the raw tachometer quantises badly and swings hundreds of RPM between samples."},
	}},
	{"MEMORY", []legendEntry{
		{"VRAM USED", "Used against total on the card. The Overview says VRAM once, in the group heading over these columns, and labels this one USED. A \"+2.1G\" beside it, in warning colour, is memory the driver has evicted to host RAM because VRAM is full: the model pays to bring it back on its next use, and the eviction path is where the xe driver has crashed. Fix it by not loading more onto the card than fits."},
		{"FREE", "What is left. This is the number that decides whether the next model loads, which is why it gets its own column instead of a percentage."},
		{"VRAM rd/wr", "VRAM read/write throughput per second, unit in the suffix (K, M, G). Token generation is bandwidth-bound, so this is closer to a throughput ceiling than the clock is."},
		{"VRAM BW %", "The same VRAM traffic as a percent of the card's peak bandwidth: how close to the memory ceiling the work is. Reads 0% on an idle card; the rate column beside it still shows the few MB/s that are moving."},
		{"PCIE rx/tx", "Host PCIe traffic per second: rx is the card reading from host memory, tx the card writing back. The suffix is the unit -- B, K, M or G per second -- so a resident model's few KB/s during generation and a load's GB/s are both legible. Steady-state generation moves almost nothing over PCIe (the weights are already on the card), so tens of KB/s next to a busy card is normal."},
	}},
	{"WORKLOADS", []legendEntry{
		{"WORKLOAD", "What each process is serving. For a model server this is the model file it was started with, plus its port; otherwise the process name. Read from the process's command line, and from its parent's when the engine runs in a child that rewrites its own (vLLM does)."},
		{"%CARD", "That process's VRAM as a share of the card's total."},
		{"LAST 60s", "Busy, power and core temperature over the last minute, each drawn against its own ceiling: 100%, the card's power cap, and the die's critical temperature."},
		{"not shown", "The few megabytes a model server holds on a card it is not using: it opens a DRM client there and would otherwise appear as a workload."},
		{"other", "VRAM the card reports in use that no listed process accounts for. Without sudo this is mostly the display server: other users' processes cannot be inspected, and the login screen's compositor runs as its own user. Run with sudo to list those processes by name; what remains is driver overhead."},
	}},
	{"CLOCKS & STATE", []legendEntry{
		{"CLOCK MHz", "The clock the render tile is actually running at, against the hardware maximum. \"idle\" means the tile is parked (c6) and clocked to nothing. The driver's *requested* clock is deliberately not shown: it latches at the maximum after a job and stays there while the card sleeps at 7 W."},
		{"act idle", "In Detail: the actual clock reads 0 because the tile is parked in a c6 window. Expected at idle, not a failed reading."},
		{"gt0 / gt1", "The two graphics tiles: gt0 does render and compute, gt1 does media."},
		{"gt-c0 / gt-c6", "Tile power state. c0 is active, c6 is idle."},
	}},
	{"DETAIL — the card itself", []legendEntry{
		{"type", "Memory type, bus width and channel count, as the driver reports them; parts it does not report are left out."},
		{"rebar", "Resizable BAR: how much VRAM the CPU can map at once. \"on\" means all of it. Without it the window is 256 MiB, which costs Arc cards a lot of performance; turn it on in the firmware setup."},
		{"oprom", "Option ROM (card firmware) code and data versions."},
		{"health", "Shown only when it matters: \"reset required\" (in red) when the driver says the card is wedged or needs a reset to finish a repair."},
		{"ecc", "Error-correcting memory mode, or \"not supported\" on cards without it."},
	}},
	{"TRENDS", []legendEntry{
		{"stripes", "Every card on one chart per metric as a filled area, alternating colours where cards overlap. The default for two cards."},
		{"lines", "Every card on one chart per metric as a line. The default for three cards, where stripes already turn into a weave."},
		{"per card", "A small graph per card, shaded green to amber to red by value against the scale in the box's corner. The default from four cards; s switches braille (finer) and block (bolder)."},
		{"v", "Cycles stripes, lines and per card. The choice is saved and then used at any card count."},
	}},
	{"WHEN A READING IS NOT LIVE", []legendEntry{
		{"--", "No value. Either the hardware does not report it, or it has been missing long enough that showing the last one would be misleading."},
		{"zz", "Shown in the POWER column: the card is suspended (or being left alone to suspend) and is not being read, because reading it would wake it and roughly double its draw. Every value in the row is the last sample, refreshed every --idle-poll seconds; Detail says how old it is."},
		{"~", "An estimate, not a driver reading: VRAM summed from per-process usage, on a card whose driver reports none without CAP_PERFMON. Typically within about 3%."},
		{"stale", "Shown in the header when no telemetry has arrived for several refresh intervals. The numbers on screen are the last ones received, not current."},
	}},
}

// RenderLegend explains the abbreviations used across the views.
//
// It is a full-width page rather than a centred modal box, and it renders
// through the viewport like any other view, so it scrolls. As a fixed box it
// had to truncate every description to fit -- "Compute engine (Command
// Streamer). What LLM i..." -- which defeats the point of a glossary, and it
// had to drop entries entirely when the terminal was short.
func RenderLegend(width, height int) string {
	boxWidth := width - 6
	if boxWidth < 30 {
		boxWidth = 30
	}
	inner := boxWidth - 4

	// Term column, then the description wrapped in what is left. Nothing is
	// truncated: a description too long for one line continues on the next.
	termW := 20
	if inner < 60 {
		termW = 14
	}
	descW := inner - termW - 2
	if descW < 20 {
		descW = 20
	}

	rows := []string{
		styles.HighlightText.Render("📖 WHAT THE READINGS MEAN"),
		styles.SubtleText.Render(strings.Repeat("─", inner)),
	}

	wrapStyle := lipgloss.NewStyle().Width(descW)
	for _, section := range legendSections {
		rows = append(rows, "", styles.BoldText.Render(section.title))
		for _, e := range section.entries {
			wrapped := strings.Split(wrapStyle.Render(e.meaning), "\n")
			for i, line := range wrapped {
				term := ""
				if i == 0 {
					term = e.term
				}
				rows = append(rows, "  "+
					lipgloss.NewStyle().Foreground(styles.ColorPrimary).Width(termW).Render(term)+
					styles.MutedText.Render(line))
			}
		}
	}

	rows = append(rows, "",
		styles.SubtleText.Render("↑/↓ to scroll    [i] or [Esc] to go back"))

	return styles.CardStyle.Width(boxWidth + 2).Render(
		lipgloss.JoinVertical(lipgloss.Left, rows...))
}
