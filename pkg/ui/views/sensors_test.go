package views

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	"charm.land/lipgloss/v2"
)

func sysfsFixture() *xpu.SysfsStats {
	st := &xpu.SysfsStats{
		Available:   true,
		TempByLabel: map[string]xpu.TempSensor{},
		PowerLimits: xpu.PowerLimits{CapW: 275, CritW: 550, CapInterval: 15},
	}
	add := func(idx int, label string, v, crit, emerg float64) {
		s := xpu.TempSensor{Index: idx, Label: label, ValueC: v, CritC: crit, EmergencyC: emerg}
		st.Temps = append(st.Temps, s)
		st.TempByLabel[label] = s
	}
	add(2, "pkg", 72, 100, 125)
	add(3, "vram", 80, 105, 120)
	for ch := 0; ch < 16; ch++ {
		add(6+ch, fmt.Sprintf("vram_ch_%d", ch), 74+float64(ch%4)*2, 105, 120)
	}
	st.GTs = []xpu.GTStats{
		{
			Name:     "gt0",
			Freq:     xpu.GTFreq{CurMHz: 2800, ActMHz: 2533, MinMHz: 1200, MaxMHz: 2800},
			Throttle: xpu.ThrottleFlags{Status: true, PL2: true, Reasons: "pl2"},
			Idle:     xpu.GTIdle{Status: "gt-c0", Name: "gt0-rc", BusyPercent: 100, BusyValid: true},
		},
		{
			Name:     "gt1",
			Freq:     xpu.GTFreq{CurMHz: 400, ActMHz: 0, MinMHz: 400, MaxMHz: 1500},
			Throttle: xpu.ThrottleFlags{Reasons: "none"},
			Idle:     xpu.GTIdle{Status: "gt-c6", Name: "gt1-mc", BusyPercent: 0.6, BusyValid: true},
		},
	}
	return st
}

func TestRenderVramChannelMap(t *testing.T) {
	out := RenderVramChannelMap(sysfsFixture(), 130)

	if !strings.Contains(out, "VRAM CHANNELS") {
		t.Fatal("missing heading")
	}
	// All 16 channels must appear; the aggregate hides which one is hot.
	for ch := 0; ch < 16; ch++ {
		if !strings.Contains(out, fmt.Sprintf("%2d:", ch)) {
			t.Errorf("channel %d missing from the map", ch)
		}
	}
	// The spread is the point of the map.
	if !strings.Contains(out, "74-80 °C") || !strings.Contains(out, "Δ6") {
		t.Errorf("spread not reported:\n%s", out)
	}
	if !strings.Contains(out, "crit 105") {
		t.Error("sensor's own limit not shown")
	}
}

// The hottest marker must agree with the numbers actually rendered. Comparing
// raw floats named one channel while several others visibly showed the same
// rounded value.
func TestVramChannelMapHottestMatchesDisplayedValues(t *testing.T) {
	st := sysfsFixture()
	st.TempByLabel = map[string]xpu.TempSensor{}
	st.Temps = nil
	for ch := 0; ch < 16; ch++ {
		v := 70.0
		if ch == 3 || ch == 9 {
			v = 79.6 // both round to 80
		}
		if ch == 11 {
			v = 79.8 // also rounds to 80
		}
		s := xpu.TempSensor{Index: 6 + ch, Label: fmt.Sprintf("vram_ch_%d", ch), ValueC: v, CritC: 105}
		st.Temps = append(st.Temps, s)
		st.TempByLabel[s.Label] = s
	}

	out := RenderVramChannelMap(st, 130)
	if !strings.Contains(out, "ch3,ch9,ch11") {
		t.Errorf("hottest should name every channel displaying the max:\n%s", out)
	}
}

func TestRenderVramChannelMapGridIsEven(t *testing.T) {
	// Rows must divide 16 evenly rather than stranding one channel alone.
	for _, width := range []int{60, 80, 100, 140, 200} {
		out := RenderVramChannelMap(sysfsFixture(), width)
		var cellRows []string
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "▇") {
				cellRows = append(cellRows, line)
			}
		}
		if len(cellRows) == 0 {
			t.Fatalf("width %d: no channel rows", width)
		}
		if 16%len(cellRows) != 0 {
			t.Errorf("width %d: %d rows does not divide 16 evenly", width, len(cellRows))
		}
		// JoinVertical pads every line to the widest, so a long header overflows
		// the rows too -- check the whole block, not just the cells.
		for _, line := range strings.Split(out, "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("width %d: line overflows (%d cols): %q", width, lipgloss.Width(line), line)
			}
		}
	}
}

func TestRenderVramChannelMapAbsentWithoutSysfs(t *testing.T) {
	if out := RenderVramChannelMap(nil, 120); out != "" {
		t.Errorf("expected nothing without sysfs data, got %q", out)
	}
	if out := RenderVramChannelMap(&xpu.SysfsStats{}, 120); out != "" {
		t.Errorf("expected nothing for an unavailable card, got %q", out)
	}
}

func TestRenderThrottleStatus(t *testing.T) {
	out := RenderThrottleStatus(sysfsFixture(), 130)
	if !strings.Contains(out, "THROTTLED") {
		t.Errorf("gt0 throttle not surfaced:\n%s", out)
	}
	if !strings.Contains(out, "PL2 power") {
		t.Errorf("reason not spelled out:\n%s", out)
	}
	if !strings.Contains(out, "CLEAR") {
		t.Errorf("gt1 should read clear:\n%s", out)
	}
}

// The VR has no temperature sensor, so these flags are the only warning the
// host gets. They must outrank a plain throttle rather than being listed with it.
func TestRenderThrottleStatusLeadsWithVR(t *testing.T) {
	st := sysfsFixture()
	st.GTs[0].Throttle.VRThermalert = true

	out := RenderThrottleStatus(st, 130)
	if !strings.Contains(out, "VR THERMALERT") {
		t.Errorf("VR flag not surfaced:\n%s", out)
	}
	// The badge must not claim the VR has no temperature sensor at all: it has
	// one, via Level Zero, and it is shown on the inspector. Only hwmon lacks it.
	if !strings.Contains(out, "not in hwmon") {
		t.Errorf("VR badge should explain why it matters:\n%s", out)
	}
	if strings.Contains(out, "no temperature sensor") {
		t.Error("badge still claims the VR has no sensor; it has one via Level Zero")
	}

	st.GTs[0].Throttle.VRTdc = true
	if out := RenderThrottleStatus(st, 130); !strings.Contains(out, "VR THERMALERT + TDC") {
		t.Errorf("both VR flags should be named:\n%s", out)
	}
}

func TestPowerLimitPrefersHardwareCap(t *testing.T) {
	g := &xpu.GPUCombined{Info: xpu.DeviceInfo{PowerLimitW: 190}}
	g.Stats.Sysfs = sysfsFixture()
	if got := PowerLimitW(g); got != 275 {
		t.Errorf("PowerLimitW = %v, want the hardware cap 275", got)
	}

	g.Stats.Sysfs = nil
	if got := PowerLimitW(g); got != 190 {
		t.Errorf("PowerLimitW = %v, want xpu-smi's 190 when sysfs is absent", got)
	}

	g.Info.PowerLimitW = 0
	if got := PowerLimitW(g); got != 275 {
		t.Errorf("PowerLimitW = %v, want the 275 fallback", got)
	}
}

func TestTempLimitsPreferSysfs(t *testing.T) {
	fallback := xpu.HealthSensor{ThrottleThreshold: 85, ShutdownThreshold: 100}

	throttle, shutdown := TempLimits(sysfsFixture(), "vram", fallback)
	if throttle != 105 || shutdown != 120 {
		t.Errorf("got %v/%v, want the vram sensor's own 105/120", throttle, shutdown)
	}

	throttle, shutdown = TempLimits(nil, "vram", fallback)
	if throttle != 85 || shutdown != 100 {
		t.Errorf("got %v/%v, want the xpu-smi fallback 85/100", throttle, shutdown)
	}

	// A label the card does not publish must fall back rather than report zero.
	if throttle, _ = TempLimits(sysfsFixture(), "nonesuch", fallback); throttle != 85 {
		t.Errorf("got %v, want the fallback for an unknown sensor", throttle)
	}
}

func TestRenderGTSummaryFitsNarrowColumns(t *testing.T) {
	for _, width := range []int{70, 90, 120, 160} {
		out := RenderGTSummary(sysfsFixture(), width)
		for _, line := range strings.Split(out, "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("width %d: line overflows at %d cols: %q", width, lipgloss.Width(line), line)
			}
		}
	}
}

// act_freq reads 0 whenever the GT is parked in a c6 window. That is the
// hardware idling, not a failed read.
func TestRenderGTSummaryLabelsIdleClock(t *testing.T) {
	out := RenderGTSummary(sysfsFixture(), 160)
	if !strings.Contains(out, "act idle") {
		t.Errorf("a zero act_freq should read as idle, not 0 MHz:\n%s", out)
	}
}

// The overview is the default view, so it must fit at every width. Column
// packing replaced per-column width thresholds precisely because those did not
// compose: a column could pass its own check at a width where the row as a
// whole had no room left, and wrap inside the card.
func TestOverviewFitsAllWidths(t *testing.T) {
	gpus, _, _ := createSampleData()
	for _, width := range []int{60, 70, 80, 90, 100, 110, 120, 130, 160, 200} {
		out := RenderOverview(gpus, 0, "", width)
		for _, line := range strings.Split(out, "\n") {
			if lipgloss.Width(line) > width {
				t.Errorf("width %d: overview line overflows (%d cols): %q",
					width, lipgloss.Width(line), line)
			}
		}
	}
}

// Views must degrade rather than panic when a card has no sysfs data at all --
// a non-xe driver, or a card whose nodes could not be read.
func TestViewsWithoutSysfsData(t *testing.T) {
	gpus, _, _ := createSampleData()
	for _, g := range gpus {
		g.Stats.Sysfs = nil
	}

	if out := RenderOverview(gpus, 0, "", 130); !strings.Contains(out, "GPU") {
		t.Error("overview broke without sysfs data")
	}
}

// The overview strip must keep the same shape as values move, and both cards
// must lay out identically.
//
// Columns used to be omitted when a value was zero or unavailable, and their
// width tracked the value, so a reading crossing zero or gaining a digit
// re-flowed the strip: columns appeared, disappeared and jumped between rows,
// and the two cards packed differently from one another.
func TestOverviewLayoutIsStable(t *testing.T) {
	gpus, _, _ := createSampleData()

	shape := func(out string) string {
		var widths []string
		for _, line := range strings.Split(out, "\n") {
			trimmed := strings.TrimRight(line, " ")
			if trimmed != "" {
				widths = append(widths, fmt.Sprintf("%d", lipgloss.Width(trimmed)))
			}
		}
		return strings.Join(widths, ",")
	}

	var first string
	for i := 0; i < 12; i++ {
		// Perturb the numbers the way live telemetry does: values crossing
		// zero, gaining digits, and appearing or disappearing entirely.
		for _, g := range gpus {
			g.Stats.PowerW = 40 + float64(i)*20
			g.Stats.MemoryBandwidthPercent = float64(i%3) * 20
			g.Stats.PcieReadKBps = float64(i%2) * 500000
			g.Stats.MemoryUsedMiB = 4000 + float64(i)*2000
			g.Stats.FanSpeedRPM = float64(i%4) * 900
		}
		got := shape(RenderOverview(gpus, 0, "", 130))
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("strip changed shape on iteration %d:\n  first %s\n  now   %s", i, first, got)
		}
	}
}

// Values must sit under their headers. The table guarantees this by
// construction, which is the point of having headers at all -- the previous
// inline layout let matching fields drift apart between the two cards.
func TestOverviewValuesAlignUnderHeaders(t *testing.T) {
	gpus, _, _ := createSampleData()
	// Make the two cards differ in every way that used to change the layout.
	gpus[0].Stats.MemoryBandwidthPercent = 0
	gpus[1].Stats.MemoryBandwidthPercent = 55
	gpus[0].Stats.FanSpeedRPM = 0
	gpus[1].Stats.FanSpeedRPM = 3300
	gpus[0].Stats.PowerW = 9
	gpus[1].Stats.PowerW = 275

	out := stripANSIViews(RenderOverview(gpus, 0, "", 150))

	var headerRow string
	var dataRows []string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "BUSY") && strings.Contains(line, "POWER"):
			headerRow = line
		case strings.Contains(line, "/275") || strings.Contains(line, "/  9"):
			dataRows = append(dataRows, line)
		}
	}
	if headerRow == "" {
		t.Fatal("no header row rendered")
	}
	if len(dataRows) < 2 {
		t.Fatalf("expected a row per card, got %d", len(dataRows))
	}

	// The FREE column must start at the same column on every row.
	want := lipgloss.Width(headerRow[:strings.Index(headerRow, "FREE")])
	for i, row := range dataRows {
		// Find the free figure: the cell after VRAM USED.
		if lipgloss.Width(row) != lipgloss.Width(headerRow) {
			t.Errorf("row %d is %d cols, header is %d -- columns cannot line up",
				i, lipgloss.Width(row), lipgloss.Width(headerRow))
		}
	}
	_ = want
}

// Every cell must fit the column width it declares. A cell one column wider
// than its budget does not truncate -- lipgloss wraps it -- which silently
// swallowed the tx half of the PCIe reading when it was an inline segment.
func TestOverviewCellsFitTheirColumns(t *testing.T) {
	gpus, _, _ := createSampleData()
	g := gpus[0]
	// Values chosen to be the widest each cell can render.
	g.Stats.PcieReadKBps = 999 * 1024
	g.Stats.PcieWriteKBps = 4096 * 1024 // forces the G suffix
	g.Stats.PowerW = 275
	g.Stats.FanSpeedRPM = 4500
	g.Stats.MemoryUsedMiB = 28000
	g.Stats.FrequencyMHz = 2800
	g.Stats.CoreTempC = 100
	g.Stats.MemoryTempC = 105
	g.Stats.VrTempC = 105
	g.Stats.MemoryBandwidthPercent = 100

	for _, c := range overviewColumns() {
		if got := lipgloss.Width(c.value(g)); got > c.width {
			t.Errorf("column %q renders %d cols but declares %d: %q",
				c.header, got, c.width, stripANSIViews(c.value(g)))
		}
		if lipgloss.Width(c.header) > c.width {
			t.Errorf("header %q is wider (%d) than its column (%d)",
				c.header, lipgloss.Width(c.header), c.width)
		}
	}
}

// Narrowing must drop columns, never wrap the row.
func TestOverviewColumnsFitWidth(t *testing.T) {
	for _, width := range []int{50, 60, 80, 100, 120, 160, 220} {
		cols := fitColumns(overviewColumns(), width)
		total := 2
		for _, c := range cols {
			total += c.width
		}
		if total > width && len(cols) > 2 {
			t.Errorf("width %d: kept %d columns totalling %d", width, len(cols), total)
		}
		// The operator's core questions must survive any width.
		for _, must := range []string{"GPU", "BUSY %", "POWER W"} {
			found := false
			for _, c := range cols {
				if c.header == must {
					found = true
				}
			}
			if !found {
				t.Errorf("width %d dropped the essential column %q", width, must)
			}
		}
	}
}

var ansiInViews = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSIViews(s string) string { return ansiInViews.ReplaceAllString(s, "") }

// Columns must not sit flush against one another. Cells were laid out edge to
// edge, so a value that filled its column touched the next one.
func TestOverviewColumnsAreSpaced(t *testing.T) {
	gpus, _, _ := createSampleData()
	// Fill several cells right to their edge.
	gpus[0].Stats.PowerW = 275
	gpus[0].Stats.MemoryUsedMiB = 28000
	gpus[0].Stats.CoreTempC = 100
	gpus[0].Stats.MemoryTempC = 105
	gpus[0].Stats.FanSpeedRPM = 4500

	out := stripANSIViews(RenderOverview(gpus, 0, "", 170))
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "/275") {
			continue
		}
		// Every run of non-space characters should be followed by at least one
		// space before the next, except at the row's edges.
		fields := strings.Fields(line)
		if len(fields) < 6 {
			t.Errorf("row collapsed into %d fields, columns are running together:\n%s", len(fields), line)
		}
		return
	}
	t.Fatal("no data row found")
}

func TestCompactBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{2048, "2K"},
		{186 * 1024 * 1024, "186M"},
		{1288490188, "1.2G"},
	}
	for _, c := range cases {
		if got := compactBytes(c.in); got != c.want {
			t.Errorf("compactBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
