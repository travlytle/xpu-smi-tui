package views

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/components"
	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"
	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	"charm.land/lipgloss/v2"
)

// RenderOverview is the default view: a table of cards, then the workloads
// using them.
//
// It answers the five operator questions without a keystroke -- is the card
// working, how hard, is it limited, how much VRAM is left, what is using it --
// which previously took three dashboard modes and three tabs.
func RenderOverview(gpus []*xpu.GPUCombined, selectedIdx int, filterText string, width int) string {
	if len(gpus) == 0 {
		return styles.CardStyle.Width(width - 4).Render(
			styles.MutedText.Render("No Intel GPUs detected."))
	}

	boxWidth := width - 6
	// Below this the essential columns (GPU, BUSY, POWER) cannot fit and the
	// frame would exceed the terminal.
	if boxWidth < 36 {
		boxWidth = 36
	}
	inner := boxWidth - 4
	rule := styles.SubtleText.Render(strings.Repeat("─", inner))

	sections := []string{renderCardTable(gpus, selectedIdx, inner)}
	if trends := renderTrendRows(gpus, selectedIdx, inner); trends != "" {
		sections = append(sections, "", trends)
	}
	sections = append(sections, "", rule, renderWorkloadTable(gpus, filterText, inner))

	return styles.CardStyle.Width(boxWidth + 2).Render(lipgloss.JoinVertical(lipgloss.Left, sections...))
}

// column is one field of the card table.
//
// The strip used to repeat a label beside every value on every card -- "core 72
// vram 80 vr 89" twice over -- which spent width on saying the same words again
// and made the two cards hard to compare, since matching fields did not line up
// under one another. Naming each field once in a header costs one row and gives
// the columns back.
type column struct {
	group    string // the heading drawn above a run of columns: ACTIVITY, VRAM, HOST, THERMAL
	header   string
	width    int
	priority int // lower survives longer when the terminal is narrow
	numeric  bool
	value    func(*xpu.GPUCombined) string
}

// columnGap is the space between columns.
//
// Cells used to be laid out edge to edge with only whatever padding a value
// happened to leave inside its own width, so a value that filled its column sat
// flush against the next one.
const columnGap = 2

// overviewColumns is the full set, grouped by what each column is about and
// labelled as such in a heading row above the headers: ACTIVITY (is it
// working, how hard, what is holding it back), VRAM (what is loaded and what
// is moving through it), HOST (the PCIe link), THERMAL. "VRAM" is said once in
// the group heading, so the memory columns can be "USED", "rd/wr" and "BW %"
// without each repeating it -- and without "MEM" being read as system RAM,
// which it was.
//
// Display order and drop order are separate: priority encodes the operator's
// questions -- is it working (BUSY), how hard (POWER, with the throttle reason
// inline), how much VRAM is left, how hot -- and the least important columns
// go first as the terminal narrows, whatever group they sit in.
//
// Every header that names a bare number carries its unit -- "CORE °C", not
// "CORE" -- so the column is legible without a trip to the legend. A column
// whose value already carries its own unit inline (USED and FREE render
// "8.1/28.5G"; POWER W's two numbers share one W) is not doubled up.
func overviewColumns() []column {
	return []column{
		{"ACTIVITY", "GPU", 4, 0, false, func(g *xpu.GPUCombined) string {
			return fmt.Sprintf("%d", g.Info.GPUNumber())
		}},
		{"ACTIVITY", "BUSY %", 6, 1, true, busyValue},
		{"ACTIVITY", "CCS %", 5, 5, true, ccsValue},
		{"ACTIVITY", "CLOCK MHz", 9, 8, false, clockValue},
		{"ACTIVITY", "POWER W", 12, 1, false, powerValue},
		{"VRAM", "USED +host", 17, 2, false, vramUsedValue},
		{"VRAM", "FREE", 6, 2, true, vramFreeValue},
		{"VRAM", "rd/wr", 11, 9, false, bwValue},
		{"VRAM", "BW %", 4, 10, true, bwPercentValue},
		{"HOST", "PCIE rx/tx", 11, 7, false, pcieValue},
		{"THERMAL", "CORE °C", 7, 3, true, func(g *xpu.GPUCombined) string { return tempValue(g, g.Stats.CoreTempC, "pkg") }},
		{"THERMAL", "VRAM °C", 7, 3, true, func(g *xpu.GPUCombined) string { return tempValue(g, g.Stats.MemoryTempC, "vram") }},
		{"THERMAL", "VR °C", 5, 10, true, func(g *xpu.GPUCombined) string { return tempValue(g, g.Stats.VrTempC, "") }},
		{"THERMAL", "FAN RPM", 7, 6, true, fanValue},
	}
}

// groupRow draws the heading above a run of same-group columns: the label,
// then a rule out to the end of the run, so the eye can see which headers it
// covers. A group that lost all its columns to a narrow terminal is simply
// absent.
func groupRow(cols []column, gap string) string {
	parts := []string{"  "}
	for i := 0; i < len(cols); {
		j := i
		span := 0
		for j < len(cols) && cols[j].group == cols[i].group {
			if j > i {
				span += columnGap
			}
			span += cols[j].width
			j++
		}
		if i > 0 {
			parts = append(parts, gap)
		}
		label := cols[i].group
		if len(label) > span {
			label = label[:span]
		}
		rule := ""
		if n := span - len(label) - 1; n > 0 {
			rule = " " + strings.Repeat("─", n)
		}
		parts = append(parts, styles.SubtleText.Width(span).Render(label+rule))
		i = j
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, parts...)
}

// fitColumns drops the least important columns until the row fits, counting the
// gaps between them.
func fitColumns(all []column, width int) []column {
	const gutter = 2
	measure := func(cols []column) int {
		total := gutter
		for _, c := range cols {
			total += c.width
		}
		if len(cols) > 1 {
			total += columnGap * (len(cols) - 1)
		}
		return total
	}
	total := measure(all)

	// Drop by descending priority until it fits.
	kept := make([]column, len(all))
	copy(kept, all)
	for total > width {
		worst, at := -1, -1
		for i, c := range kept {
			// >= : on a tie the later-listed column goes, so CORE outlives MEM.
			if c.priority >= worst {
				worst, at = c.priority, i
			}
		}
		if at < 0 || worst <= 1 {
			break // never drop the essentials
		}
		kept = append(kept[:at], kept[at+1:]...)
		total = measure(kept)
	}
	return kept
}

// renderCardTable draws the header row and one row per card.
func renderCardTable(gpus []*xpu.GPUCombined, selectedIdx, width int) string {
	cols := fitColumns(overviewColumns(), width)

	gap := strings.Repeat(" ", columnGap)

	header := []string{styles.SubtleText.Render("  ")}
	for i, c := range cols {
		if i > 0 {
			header = append(header, gap)
		}
		align := lipgloss.Left
		if c.numeric {
			align = lipgloss.Right // values are right-aligned, so the header sits over them
		}
		header = append(header, styles.BoldText.Width(c.width).Align(align).Render(c.header))
	}
	rows := []string{groupRow(cols, gap), lipgloss.JoinHorizontal(lipgloss.Left, header...)}

	for i, g := range gpus {
		accent := accentFor(g.Info.GPUNumber())
		// One cell of gutter marks focus, rather than a whole bordered card.
		gutter := "  "
		if i == selectedIdx {
			gutter = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("▌ ")
		}

		cells := []string{gutter}
		for i, c := range cols {
			if i > 0 {
				cells = append(cells, gap)
			}
			align := lipgloss.Left
			if c.numeric {
				align = lipgloss.Right
			}
			cells = append(cells, lipgloss.NewStyle().Width(c.width).Align(align).Render(c.value(g)))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Left, cells...))
	}
	return strings.Join(rows, "\n")
}

// ---- cell values -----------------------------------------------------------

// busyValue is whether the graphics tile has left its idle power state, from
// gtidle's residency counter -- not whether it is executing anything.
//
// Observed: a fully idle card holding a resident model --
// no request in flight, nothing queued -- sat at gt-c0 (active) continuously
// for 5+ seconds with idle_residency_ms not moving at all, so this read 100%
// throughout. These cards do not appear to power-gate the tile down while a
// model is loaded, resident or not. So BUSY at 100% on its own says only
// "a model is here"; CCS is the column that says whether it is computing.
func busyValue(g *xpu.GPUCombined) string {
	busiest, valid := 0.0, false
	if g.Stats.Sysfs != nil {
		for _, gt := range g.Stats.Sysfs.GTs {
			if gt.Idle.BusyValid && (!valid || gt.Idle.BusyPercent > busiest) {
				busiest, valid = gt.Idle.BusyPercent, true
			}
		}
	}
	if !valid {
		return styles.SubtleText.Render("--")
	}
	return lipgloss.NewStyle().Foreground(busyColor(busiest)).Bold(true).
		Render(fmt.Sprintf("%3.0f%%", busiest))
}

// ccsValue is the compute engine specifically; BUSY only says the tile is awake.
func ccsValue(g *xpu.GPUCombined) string {
	if g.Stats.Fdinfo != nil {
		if ccs, ok := g.Stats.Fdinfo.Engine("ccs"); ok && ccs.Valid {
			return lipgloss.NewStyle().Foreground(busyColor(ccs.BusyPercent)).
				Render(fmt.Sprintf("%3.0f%%", ccs.BusyPercent))
		}
	}
	return styles.SubtleText.Render("--")
}

// powerValue carries the throttle reason, because the reason belongs with the
// limit it is constraining rather than three keystrokes away.
func powerValue(g *xpu.GPUCombined) string {
	limit, known := PowerCap(g)
	color := styles.ColorText
	if known && g.Stats.PowerW >= limit*0.97 {
		color = styles.ColorWarning
	}

	tag := ""
	if g.Stats.Sysfs != nil {
		for _, gt := range g.Stats.Sysfs.GTs {
			if gt.Throttle.AnyVR() {
				tag, color = "VR!", styles.ColorDanger
				break
			}
			if gt.Throttle.Status && tag == "" {
				tag, color = strings.ToUpper(shortReason(gt.Throttle)), styles.ColorWarning
			}
		}
	}
	capText := " --"
	if known {
		capText = fmt.Sprintf("%3.0f", limit)
	}
	if g.Stats.Idle {
		// Left alone to suspend: the last sample, dimmed and marked.
		return styles.SubtleText.Render(fmt.Sprintf("%3.0f/%s %-4s", g.Stats.PowerW, capText, "zz"))
	}
	return lipgloss.NewStyle().Foreground(color).Bold(tag != "").
		Render(fmt.Sprintf("%3.0f/%s %-4s", g.Stats.PowerW, capText, tag))
}

func shortReason(t xpu.ThrottleFlags) string {
	switch {
	case t.PL1:
		return "pl1"
	case t.PL2:
		return "pl2"
	case t.PL4:
		return "pl4"
	case t.Thermal:
		return "thrm"
	case t.Prochot:
		return "proc"
	case t.RATL:
		return "ratl"
	}
	return "thrt"
}

func tempValue(g *xpu.GPUCombined, value float64, label string) string {
	if value <= 0 {
		return styles.SubtleText.Render("--")
	}
	crit := 0.0
	if label != "" {
		var fallback xpu.HealthSensor
		if label == "pkg" {
			fallback = g.Health.CoreTemperature
		} else {
			fallback = g.Health.MemoryTemperature
		}
		crit, _ = TempLimits(g.Stats.Sysfs, label, fallback)
	}
	return lipgloss.NewStyle().Foreground(tempSeverityColor(value, crit)).
		Render(fmt.Sprintf("%3.0f", value))
}

// clockValue shows the clock the card is running at, not the one the driver
// last asked for -- which sits at the maximum on a parked card; see Clock().
func clockValue(g *xpu.GPUCombined) string {
	mhz, parked := g.Stats.Clock()
	cur := fmt.Sprintf("%4.0f", mhz)
	style := styles.RegularText
	if parked {
		cur, style = "idle", styles.SubtleText
	}
	if g.Info.MaxFrequencyMHz <= 0 {
		return style.Render(cur)
	}
	return style.Render(fmt.Sprintf("%s/%4.0f", cur, g.Info.MaxFrequencyMHz))
}

func vramUsedValue(g *xpu.GPUCombined) string {
	used := g.Stats.MemoryUsedMiB
	total, known := MemTotalMiB(g)
	if g.Stats.MemoryUsedUnavailable {
		if known {
			return styles.SubtleText.Render(fmt.Sprintf("--/%.1fG", total/1024))
		}
		return styles.SubtleText.Render("--")
	}
	approx := ""
	if g.Stats.MemoryUsedEstimated {
		approx = "~" // summed from per-process usage, not reported by the driver
	}
	cell := fmt.Sprintf("%s%.1fG", approx, used/1024)
	if known {
		cell = fmt.Sprintf("%s%.1f/%.1fG", approx, used/1024, total/1024)
	}
	// Memory the driver has evicted to host RAM because VRAM is full. Shown
	// in warning colour: it is the state that precedes both a slow model (it
	// pays to bring those buffers back) and the driver's crashes.
	if g.Stats.HostBackedBytes >= hostResidentMin {
		return styles.RegularText.Render(cell) + lipgloss.NewStyle().Foreground(styles.ColorWarning).
			Render(" +"+compactBytes(g.Stats.HostBackedBytes))
	}
	return styles.RegularText.Render(cell)
}

// vramFreeValue is the number that decides whether the next model loads.
func vramFreeValue(g *xpu.GPUCombined) string {
	total, known := MemTotalMiB(g)
	if !known || g.Stats.MemoryUsedUnavailable {
		return styles.SubtleText.Render("--")
	}
	free := total - g.Stats.MemoryUsedMiB
	if free < 0 {
		free = 0
	}
	color := styles.ColorSuccess
	switch ratio := free / total; {
	case ratio < 0.05:
		color = styles.ColorDanger
	case ratio < 0.15:
		color = styles.ColorWarning
	}
	approx := ""
	if g.Stats.MemoryUsedEstimated {
		approx = "~"
	}
	return lipgloss.NewStyle().Foreground(color).Bold(true).Render(fmt.Sprintf("%s%.1fG", approx, free/1024))
}

// bwValue is VRAM read/write throughput, unit scaled like the PCIe column.
// bwPercentValue beside it is the same traffic against the card's peak: the
// rate says what is moving, the percent says how close to the ceiling that is.
func bwValue(g *xpu.GPUCombined) string {
	if g.Stats.TrafficUnavailable {
		return styles.SubtleText.Render("--")
	}
	return styles.RegularText.Render(fmt.Sprintf("%s/%s",
		rateShort(g.Stats.MemoryReadKBps), rateShort(g.Stats.MemoryWriteKBps)))
}

func bwPercentValue(g *xpu.GPUCombined) string {
	if g.Stats.TrafficUnavailable {
		return styles.SubtleText.Render("--")
	}
	return styles.RegularText.Render(fmt.Sprintf("%3.0f%%", g.Stats.MemoryBandwidthPercent))
}

func fanValue(g *xpu.GPUCombined) string {
	if !g.Stats.FanAvailable && g.Stats.FanSpeedRPM <= 0 {
		return styles.SubtleText.Render("--")
	}
	return styles.MutedText.Render(fmt.Sprintf("%4.0f", g.Stats.FanSpeedRPM))
}

func pcieValue(g *xpu.GPUCombined) string {
	if g.Stats.TrafficUnavailable && !g.Stats.PcieMeasured {
		return styles.SubtleText.Render("--")
	}
	return styles.MutedText.Render(fmt.Sprintf("%s/%s",
		rateShort(g.Stats.PcieReadKBps), rateShort(g.Stats.PcieWriteKBps)))
}

// rateShort formats a KB/s figure the way du -h, docker stats and the network
// monitors do: the unit scales with the value, and the number keeps two or
// three significant figures. The column used to be fixed at whole MB/s, which
// rendered the tens-of-KB/s a resident model moves during generation -- the
// normal state of these cards -- as a permanent "0/0".
//
// The suffix is the unit: B, K, M or G per second (binary, like the rest of
// the tool's memory figures).
func rateShort(kbps float64) string {
	bytes := kbps * 1024
	units := []string{"B", "K", "M", "G", "T"}
	v := bytes
	i := 0
	for v >= 1024 && i < len(units)-1 { // like du -h: a unit rolls over at 1024, not 1000
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0fB", v)
	}
	if v < 9.95 {
		return fmt.Sprintf("%.1f%s", v, units[i])
	}
	return fmt.Sprintf("%.0f%s", v, units[i])
}

// renderTrendRows draws the last minute of busy, power and core temperature for
// each card, from history the collector already keeps.
//
// The table answers "what is it doing now"; this answers "has it been doing it
// for a while", without switching to the Trends tab. It also puts to use rows
// that were otherwise blank on any terminal taller than the tables.
func renderTrendRows(gpus []*xpu.GPUCombined, selectedIdx, width int) string {
	const labelW = 8 // "  GPU 0 "
	groups := []struct {
		name  string
		width int
	}{{"busy %", 6}, {"power W", 7}, {"core °C", 7}}

	fixed := labelW
	for _, g := range groups {
		fixed += g.width + 1 + columnGap
	}
	sparkW := (width - fixed) / len(groups)
	if sparkW > 30 {
		sparkW = 30
	}
	if sparkW < 6 {
		return "" // not enough room to draw a trend worth reading
	}

	withHistory := 0
	for _, g := range gpus {
		if g.History != nil {
			withHistory++
		}
	}
	if withHistory == 0 {
		return "" // no orphan heading over nothing
	}
	rows := []string{styles.SubtleText.Render("  LAST 60s")}
	gap := strings.Repeat(" ", columnGap)
	for i, g := range gpus {
		if g.History == nil {
			continue
		}
		// A single braille row uses the full height of its cell, so one card's
		// trace sits flush against the next card's and the two read as one
		// taller graph. A blank row between cards keeps them apart.
		if i > 0 {
			rows = append(rows, "")
		}
		accent := accentFor(g.Info.GPUNumber())
		mark := "  "
		if i == selectedIdx {
			mark = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("▌ ")
		}

		label := lipgloss.NewStyle().Foreground(accent).Width(labelW - 2).
			Render(g.Info.Label())
		coreCrit := CoreTempScale(g)

		// Traces take the card's colour, matching its label. They used to be
		// coloured by metric, which is already named beside each trace, leaving
		// nothing to tell one card's row from the other's.
		cells := []string{mark, label}
		for j, grp := range []struct {
			values []float64
			max    float64
		}{
			{g.History.Busy.Values(), 100},
			{g.History.PowerW.Values(), PowerLimitW(g)},
			{g.History.CoreTemp.Values(), coreCrit},
		} {
			if j > 0 {
				cells = append(cells, gap)
			}
			cells = append(cells,
				styles.SubtleText.Width(groups[j].width+1).Render(groups[j].name),
				components.RenderSparkline(grp.values, sparkW, accent, 0, grp.max))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Left, cells...))
	}
	return strings.Join(rows, "\n")
}

// workload is one process's use of one card, as shown in the table.
type workload struct {
	proc     xpu.DrmProcess
	deviceID int
	capacity float64 // card VRAM in bytes, 0 if unknown
}

// strayClientBytes is the size below which a process's presence on a card is
// treated as incidental when that same process holds real memory elsewhere.
// Model servers open a DRM client on every card they can see but allocate on
// one; the stray client on the other card is a few megabytes and is noise in a
// list meant to say what is using each card.
const strayClientBytes = 64 << 20

// isStrayClient reports whether p's client on card g is the incidental one a
// model server opens on a card it is not using: small, and the same process
// holds more on another card. The Overview drops these from its table; Detail
// applies the same rule so the two never disagree about what is on a card.
func isStrayClient(g *xpu.GPUCombined, p xpu.DrmProcess, all []*xpu.GPUCombined) bool {
	if p.Memory.VramBytes >= strayClientBytes {
		return false
	}
	for _, other := range all {
		if other == nil || other == g || other.Stats.Fdinfo == nil {
			continue
		}
		for _, q := range other.Stats.Fdinfo.Processes {
			if q.PID == p.PID && q.Memory.VramBytes > p.Memory.VramBytes {
				return true
			}
		}
	}
	return false
}

// collectWorkloads gathers per-card workloads from fdinfo.
//
// This replaced `xpu-smi ps`, which was thinner -- it attributed a server to one
// card when fdinfo shows it holding both -- and cost another subprocess on
// every tick.
func collectWorkloads(gpus []*xpu.GPUCombined) []workload {
	var all []workload
	largest := map[int]int64{} // pid -> biggest allocation on any card
	for _, g := range gpus {
		if g.Stats.Fdinfo == nil {
			continue
		}
		capacity := 0.0
		if total, ok := MemTotalMiB(g); ok {
			capacity = total * 1024 * 1024
		}
		for _, p := range g.Stats.Fdinfo.Processes {
			all = append(all, workload{proc: p, deviceID: g.Info.GPUNumber(), capacity: capacity})
			if p.Memory.VramBytes > largest[p.PID] {
				largest[p.PID] = p.Memory.VramBytes
			}
		}
	}

	// Per process: a server that holds real memory somewhere drops its stray
	// clients on other cards; one holding nothing substantial anywhere (idle,
	// or still loading) keeps only its largest entry, so it appears once rather
	// than once per card it can see.
	best := map[int]int{} // pid -> index in all of its largest entry
	for i, w := range all {
		if j, ok := best[w.proc.PID]; !ok || w.proc.Memory.VramBytes > all[j].proc.Memory.VramBytes {
			best[w.proc.PID] = i
		}
	}
	var kept []workload
	for i, w := range all {
		switch {
		case w.proc.Memory.VramBytes < strayClientBytes && best[w.proc.PID] != i:
			// A small client of a process that holds more elsewhere is the stray
			// fd every server opens on the cards it is not using. Judged against
			// the process's own largest entry rather than a fixed 1 GiB, so a
			// server part-way through loading (say 500 MiB) does not sprout a
			// phantom row on the other card.
			continue
		}
		kept = append(kept, w)
	}

	// One server can be several processes on a card: vLLM runs its API server
	// and its engine as separate processes, both holding a client. Rows serving
	// the same model on the same port on the same card are one workload, so
	// they are merged, keeping the PID of the largest.
	//
	// Only a known port identifies a server. Two llama.cpp instances of the
	// same model with no --port are distinct workloads, and merging them by
	// model alone made one of them vanish from the table.
	type serverKey struct {
		device      int
		model, port string
		pid         int // set only when there is no port to identify the server
	}
	merged := make([]workload, 0, len(kept))
	index := map[serverKey]int{}
	for _, w := range kept {
		if w.proc.Model == "" {
			merged = append(merged, w)
			continue
		}
		key := serverKey{device: w.deviceID, model: w.proc.Model, port: w.proc.Port}
		if w.proc.Port == "" {
			key.pid = w.proc.PID
		}
		i, seen := index[key]
		if !seen {
			index[key] = len(merged)
			merged = append(merged, w)
			continue
		}
		into := &merged[i]
		// Take the larger figure, not the sum: both of vLLM's processes report
		// the same shared allocation, and summing them showed 144% of the card.
		if w.proc.Memory.VramBytes > into.proc.Memory.VramBytes {
			into.proc.PID = w.proc.PID
			into.proc.Memory.VramBytes = w.proc.Memory.VramBytes
		}
		if w.proc.Memory.GttBytes > into.proc.Memory.GttBytes {
			into.proc.Memory.GttBytes = w.proc.Memory.GttBytes
		}
		into.proc.Clients += w.proc.Clients
	}
	kept = merged

	sort.SliceStable(kept, func(i, j int) bool {
		return kept[i].proc.Memory.VramBytes > kept[j].proc.Memory.VramBytes
	})
	return kept
}

// unattributedVRAM is the memory a card reports in use that no visible process
// accounts for.
//
// Without root this is mostly the display server: another user's file
// descriptors cannot be read, and the login screen's compositor runs as
// gdm-greeter. Under sudo every client is visible and what remains is driver overhead. The sum is taken over
// every client on the card -- including ones the table leaves out as stray --
// since those hold memory too.
func unattributedVRAM(g *xpu.GPUCombined) int64 {
	if g.Stats.MemoryUsedUnavailable || g.Stats.MemoryUsedEstimated {
		return 0 // no independent card-wide figure to subtract the processes from
	}
	used := int64(g.Stats.MemoryUsedMiB * 1024 * 1024)
	var visible int64
	if g.Stats.Fdinfo != nil {
		for _, p := range g.Stats.Fdinfo.Processes {
			visible += p.Memory.VramBytes
		}
	}
	if gap := used - visible; gap > 0 {
		return gap
	}
	return 0
}

// otherLabel explains an unattributed row, according to what could be seen.
func otherLabel(g *xpu.GPUCombined) string {
	display := ""
	if g.Stats.Sysfs != nil && len(g.Stats.Sysfs.Displays) > 0 {
		display = "display " + strings.Join(g.Stats.Sysfs.Displays, ",") + " + "
	}
	if g.Stats.ClientsComplete {
		return "other: " + display + "driver, not owned by any process"
	}
	return "other: " + display + "driver, and processes hidden without sudo"
}

// hostResidentMin is how much of a workload's memory must sit in host RAM
// (GTT) before the table says so. Every client keeps a little there; a model
// server normally keeps none of its weights there.
const hostResidentMin = 512 << 20

// workloadLabel names a workload by the model it is serving when that is
// known, and flags memory the driver has evicted to host RAM.
//
// A 27B model here showed 8 GiB of VRAM one moment and the card's use fell by
// 14 GiB the next, which read as the model having been swapped out. It had
// not: another job needed the VRAM, and xe evicted 17.7 GiB of the model's
// buffers to system memory, where they stay until the model is next used and
// pays to bring them back. The VRAM column alone cannot show that; the GTT
// figure can.
func workloadLabel(p xpu.DrmProcess) string {
	name := p.Command
	if p.Model != "" {
		name = p.Model
	}
	if p.Port != "" {
		name += "  :" + p.Port
	}
	if p.Memory.GttBytes >= hostResidentMin {
		name += fmt.Sprintf("  +%s in host RAM", compactBytes(p.Memory.GttBytes))
	}
	return name
}

// renderWorkloadTable lists what is using the cards, largest first.
func renderWorkloadTable(gpus []*xpu.GPUCombined, filterText string, width int) string {
	workloads := collectWorkloads(gpus)

	needle := strings.ToLower(strings.TrimSpace(filterText))
	filtered := workloads[:0:0]
	for _, w := range workloads {
		if needle == "" ||
			strings.Contains(strings.ToLower(w.proc.Model), needle) ||
			strings.Contains(strings.ToLower(w.proc.Command), needle) ||
			strings.Contains(fmt.Sprintf("%d", w.proc.PID), needle) {
			filtered = append(filtered, w)
		}
	}

	// Memory no visible process accounts for, one row per card. Always present
	// -- a row that appeared only when the gap crossed a threshold came and went
	// as the two readings moved -- and hidden only while filtering, since it
	// matches no name.
	type otherRow struct {
		g     *xpu.GPUCombined
		bytes int64
	}
	var others []otherRow
	if needle == "" {
		for _, g := range gpus {
			others = append(others, otherRow{g, unattributedVRAM(g)})
		}
	}

	if len(filtered) == 0 && len(others) == 0 {
		msg := "No GPU workloads. Model servers appear here once they open a card."
		if needle != "" {
			msg = fmt.Sprintf("No workloads matching %q.", filterText)
		}
		return styles.MutedText.Render(" " + msg)
	}

	gap := strings.Repeat(" ", columnGap)
	showBar := width >= 90
	header := []string{
		styles.BoldText.Width(8).Render("PID"), gap,
		styles.BoldText.Width(3).Render("GPU"), gap,
		styles.BoldText.Width(6).Render("VRAM"), gap,
		styles.BoldText.Width(5).Render("%CARD"), gap,
	}
	if showBar {
		header = append(header, styles.BoldText.Width(14).Render(""), gap)
	}
	header = append(header, styles.BoldText.Render("WORKLOAD"))
	rows := []string{lipgloss.JoinHorizontal(lipgloss.Left, header...)}

	used := 8 + 3 + 6 + 5 + 4*columnGap
	if showBar {
		used += 14 + columnGap
	}
	labelW := width - used
	if labelW < 10 {
		labelW = 10
	}

	for _, w := range filtered {
		accent := accentFor(w.deviceID)

		share, shareText := 0.0, "  --"
		if w.capacity > 0 {
			share = float64(w.proc.Memory.VramBytes) / w.capacity * 100
			shareText = fmt.Sprintf("%3.0f%%", math.Min(share, 100))
		}

		cells := []string{
			styles.MutedText.Width(8).Render(fmt.Sprintf("%d", w.proc.PID)), gap,
			lipgloss.NewStyle().Foreground(accent).Width(3).Render(fmt.Sprintf("%d", w.deviceID)), gap,
			styles.RegularText.Width(6).Render(compactBytes(w.proc.Memory.VramBytes)), gap,
			styles.MutedText.Width(5).Render(shareText), gap,
		}
		if showBar {
			// An empty bar for a process holding a few megabytes is noise; only
			// draw one when there is something to see.
			bar := ""
			if share >= 1 {
				bar = components.RenderBar(float64(w.proc.Memory.VramBytes), w.capacity, 14, components.GaugeColorLoad)
			}
			cells = append(cells, lipgloss.NewStyle().Width(14).Render(bar), gap)
		}
		label := styles.RegularText.Render(truncate(workloadLabel(w.proc), labelW))
		if w.proc.Model == "" {
			label = styles.MutedText.Render(truncate(workloadLabel(w.proc), labelW))
		}
		cells = append(cells, label)
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Left, cells...))
	}

	for _, o := range others {
		accent := accentFor(o.g.Info.GPUNumber())
		share := "  --"
		if total, ok := MemTotalMiB(o.g); ok && total > 0 {
			share = fmt.Sprintf("%3.0f%%", float64(o.bytes)/(total*1024*1024)*100)
		}
		cells := []string{
			styles.SubtleText.Width(8).Render("-"), gap,
			lipgloss.NewStyle().Foreground(accent).Width(3).Render(fmt.Sprintf("%d", o.g.Info.GPUNumber())), gap,
			styles.SubtleText.Width(6).Render(compactBytes(o.bytes)), gap,
			styles.SubtleText.Width(5).Render(share), gap,
		}
		if showBar {
			cells = append(cells, lipgloss.NewStyle().Width(14).Render(""), gap)
		}
		cells = append(cells, styles.SubtleText.Render(truncate(otherLabel(o.g), labelW)))
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Left, cells...))
	}

	return strings.Join(rows, "\n")
}

// compactBytes formats a size for a table cell: "186M", "1.2G". The long form
// ("186.0 MiB") is right for a detail row and too wide for a column.
func compactBytes(n int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1fG", float64(n)/gib)
	case n >= mib:
		return fmt.Sprintf("%.0fM", float64(n)/mib)
	case n >= kib:
		return fmt.Sprintf("%.0fK", float64(n)/kib)
	}
	return fmt.Sprintf("%dB", n)
}

// OverviewColumnHeaders lists the headers the overview can show, so tests can
// check every one is explained in the legend.
func OverviewColumnHeaders() []string {
	cols := overviewColumns()
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.header)
	}
	return out
}
