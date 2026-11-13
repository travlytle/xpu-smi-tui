package views

import (
	"fmt"
	"strings"
	"testing"

	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	"charm.land/lipgloss/v2"
)

func createSampleData() ([]*xpu.GPUCombined, []xpu.ProcessInfo, *xpu.TopologyData) {
	mock := xpu.NewMockCollector()
	devs, _ := mock.Discover(nil)
	stats, _ := mock.CollectStats(nil, []int{0, 1})
	procs, _ := mock.CollectProcesses(nil)
	health, _ := mock.CollectHealth(nil)
	topo, _ := mock.CollectTopology(nil)

	var gpus []*xpu.GPUCombined
	for _, d := range devs {
		comb := &xpu.GPUCombined{
			Info:    d,
			Stats:   *stats[d.DeviceID],
			Health:  health[d.DeviceID],
			History: xpu.NewMetricHistory(30),
		}
		comb.History.Record(comb.Stats)
		for _, p := range procs {
			if p.DeviceID == d.DeviceID {
				comb.Processes = append(comb.Processes, p)
			}
		}
		gpus = append(gpus, comb)
	}

	return gpus, procs, topo
}

func TestRenderTrends(t *testing.T) {
	gpus, _, _ := createSampleData()

	single := RenderTrends(gpus, 0, 120, 35)
	if !strings.Contains(single, "Power Draw") {
		t.Errorf("Trends (single GPU) missing power chart")
	}

	dual := RenderTrends(gpus, -1, 120, 35)
	if !strings.Contains(dual, "GPU 0") || !strings.Contains(dual, "GPU 1") {
		t.Errorf("Trends (overlaid) should legend both GPUs")
	}
}

func TestRenderOverview(t *testing.T) {
	gpus, _, _ := createSampleData()
	out := RenderOverview(gpus, 0, "", 150)

	// Every operator question should be answerable from this one view.
	for _, want := range []string{"BUSY", "POWER W", "VRAM", "USED", "FREE", "WORKLOAD", "LAST 60s",
		"Qwen3.8-27B-Q4_K_M", ":6000"} {
		if !strings.Contains(out, want) {
			t.Errorf("overview missing %q", want)
		}
	}
}

func TestRenderOverviewFilters(t *testing.T) {
	gpus, _, _ := createSampleData()

	// Filtering matches the model name, not just the binary.
	out := RenderOverview(gpus, 0, "flash", 150)
	if strings.Contains(out, "Qwen3.8-27B-Q4_K_M") {
		t.Error("filter did not exclude the non-matching workload")
	}
	if !strings.Contains(out, "Qwen3.8-Flash-Next") {
		t.Error("filter dropped the workload whose model matches")
	}

	if out := RenderOverview(gpus, 0, "nothing-like-this", 150); !strings.Contains(out, "No workloads matching") {
		t.Error("an empty filter result should say so")
	}
}

func TestRenderDetail(t *testing.T) {
	gpus, _, topo := createSampleData()
	out := RenderDetail(gpus[0], topo, 150, 60)

	// Detail merges the old Inspector and Health tabs.
	for _, want := range []string{"POWER", "THERMAL", "MEMORY", "SYSTEM", "GPU 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing section %q", want)
		}
	}
}

func TestRenderDetailWithoutGPU(t *testing.T) {
	if out := RenderDetail(nil, nil, 100, 30); !strings.Contains(out, "No GPU selected") {
		t.Errorf("expected a placeholder, got %q", out)
	}
}

func TestRenderHelp(t *testing.T) {
	out := RenderHelp(100, 30)
	if !strings.Contains(out, "KEYBOARD SHORTCUTS") {
		t.Errorf("Help view missing KEYBOARD SHORTCUTS")
	}
}

// Detail must always draw every row. Rows used to be dropped when a value was
// missing, so the view changed shape as readings came and went, and a card that
// does not report something looked the same as one that reports zero.
func TestDetailKeepsAllRowsWithoutData(t *testing.T) {
	gpus, _, topo := createSampleData()
	g := gpus[0]

	full := RenderDetail(g, topo, 150, 200)

	// Strip everything the optional sources provide.
	g.Stats.Sysfs = nil
	g.Stats.Fdinfo = nil
	g.Stats.VrTempC = 0
	g.Stats.MemoryBandwidthPercent = 0
	g.Stats.FanAvailable = false
	g.Stats.FanSpeedRPM = 0
	g.Info.EccMode = ""
	bare := RenderDetail(g, nil, 150, 200)

	for _, label := range []string{"vr", "mctrl", "pcie", "fan", "bandwidth", "cap", "derived", "energy", "link", "runtime", "affinity", "ecc"} {
		if !strings.Contains(bare, label) {
			t.Errorf("row %q disappeared when its value was unavailable", label)
		}
	}
	for _, section := range []string{"POWER", "THERMAL", "MEMORY", "SYSTEM", "ENGINES"} {
		if !strings.Contains(bare, section) {
			t.Errorf("section %q disappeared without data", section)
		}
	}

	fullRows := strings.Count(full, "\n")
	bareRows := strings.Count(bare, "\n")
	if bareRows < fullRows-6 {
		t.Errorf("view collapsed from %d rows to %d without data", fullRows, bareRows)
	}
}

// SPILL is gone because drm-total-system is not overflow: every DRM client
// carries ~184 MiB of it, including the monitor's own xpu-smi calls. The
// overview must not present it as spill anywhere.
func TestOverviewDoesNotClaimSpill(t *testing.T) {
	gpus, _, _ := createSampleData()
	out := strings.ToLower(RenderOverview(gpus, 0, "", 200))
	if strings.Contains(out, "spill") {
		t.Error("overview still reports spill, which fdinfo cannot measure")
	}
}

// A model server opens a client on every card it can see but allocates on one.
// The few megabytes on the other card are noise in a list of what uses each
// card, and are left out.
func TestWorkloadsHideStrayClients(t *testing.T) {
	gpus, _, _ := createSampleData()
	big := xpu.DrmProcess{PID: 42, Command: "llama-server", Model: "Big",
		Memory: xpu.ProcessGPUMemory{VramBytes: 24 << 30}}
	stray := big
	stray.Memory.VramBytes = 3 << 20
	loading := xpu.DrmProcess{PID: 77, Command: "llama-server", Model: "Loading",
		Memory: xpu.ProcessGPUMemory{VramBytes: 500 << 10}}

	gpus[0].Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{big}}
	gpus[1].Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{stray, loading}}

	got := collectWorkloads(gpus)
	var labels []string
	for _, w := range got {
		labels = append(labels, fmt.Sprintf("%d@%d", w.proc.PID, w.deviceID))
	}
	joined := strings.Join(labels, " ")
	if strings.Contains(joined, "42@1") {
		t.Errorf("stray client of a server loaded elsewhere was listed: %s", joined)
	}
	if !strings.Contains(joined, "42@0") {
		t.Errorf("the server's real allocation is missing: %s", joined)
	}
	// A small allocation from a process with nothing bigger elsewhere is a
	// server still loading, not a stray, and must stay.
	if !strings.Contains(joined, "77@1") {
		t.Errorf("a server that is still loading was hidden: %s", joined)
	}
	if len(got) > 0 && got[0].proc.PID != 42 {
		t.Error("workloads should be ordered by VRAM, largest first")
	}
}

func TestWorkloadLabelPrefersModel(t *testing.T) {
	if got := workloadLabel(xpu.DrmProcess{Command: "llama-server", Model: "Qwen3", Port: "6000"}); got != "Qwen3  :6000" {
		t.Errorf("got %q", got)
	}
	if got := workloadLabel(xpu.DrmProcess{Command: "whisper"}); got != "whisper" {
		t.Errorf("without a model the command name should be used, got %q", got)
	}
}

// Memory the driver evicted to host RAM is called out; the few hundred MiB
// every client keeps there is not.
func TestWorkloadLabelFlagsHostResidentMemory(t *testing.T) {
	evicted := xpu.DrmProcess{Command: "llama-server", Model: "Qwen3.8-27B-Q4_K_M", Port: "6000",
		Memory: xpu.ProcessGPUMemory{VramBytes: 8 << 30, GttBytes: 17740 << 20}}
	if got := workloadLabel(evicted); got != "Qwen3.8-27B-Q4_K_M  :6000  +17.3G in host RAM" {
		t.Errorf("got %q", got)
	}
	normal := xpu.DrmProcess{Command: "llama-server", Model: "Qwen3", Port: "6100",
		Memory: xpu.ProcessGPUMemory{VramBytes: 22 << 30, GttBytes: 400 << 20}}
	if got := workloadLabel(normal); got != "Qwen3  :6100" {
		t.Errorf("a normal GTT footprint should not be flagged, got %q", got)
	}
}

// vLLM runs its API server and engine as separate processes on the same card.
// They are one workload and must be one row, with the memory summed.
func TestWorkloadsMergeProcessesOfOneServer(t *testing.T) {
	gpus, _, _ := createSampleData()
	engine := xpu.DrmProcess{PID: 687358, Command: "VLLM::EngineCor", Model: "Qwen-GPTQ", Port: "6001",
		Memory: xpu.ProcessGPUMemory{VramBytes: 25 << 30}}
	api := xpu.DrmProcess{PID: 686095, Command: "vllm", Model: "Qwen-GPTQ", Port: "6001",
		Memory: xpu.ProcessGPUMemory{VramBytes: 175 << 20}}
	gpus[0].Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{engine, api}}
	gpus[1].Stats.Fdinfo = &xpu.FdinfoStats{Available: true}

	got := collectWorkloads(gpus)
	if len(got) != 1 {
		t.Fatalf("expected one merged workload, got %d", len(got))
	}
	if got[0].proc.PID != 687358 {
		t.Errorf("merged row should carry the larger process's PID, got %d", got[0].proc.PID)
	}
	// The larger figure, not the sum: both processes report the same shared
	// allocation, and summing them once showed 144% of the card.
	if want := int64(25 << 30); got[0].proc.Memory.VramBytes != want {
		t.Errorf("merged VRAM = %d, want %d", got[0].proc.Memory.VramBytes, want)
	}
}

// A server with no model loaded holds a few megabytes on every card it can see.
// It should appear once, not once per card.
func TestIdleServerAppearsOnce(t *testing.T) {
	gpus, _, _ := createSampleData()
	idle := xpu.DrmProcess{PID: 380942, Command: "llama-server", Model: "Qwen3.8-27B-Q4_K_M", Port: "6000"}
	onCard0, onCard1 := idle, idle
	onCard0.Memory.VramBytes = 464 << 10
	onCard1.Memory.VramBytes = 3 << 20
	gpus[0].Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{onCard0}}
	gpus[1].Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{onCard1}}

	got := collectWorkloads(gpus)
	if len(got) != 1 {
		t.Fatalf("idle server listed %d times, want once", len(got))
	}
	if got[0].deviceID != 1 {
		t.Errorf("should be shown against the card it holds most on (1), got %d", got[0].deviceID)
	}
}

// Each card's trend row must be visually separate from the next. A single
// braille row fills its whole cell, so adjacent rows ran together into what
// looked like one taller graph.
func TestTrendRowsAreSeparated(t *testing.T) {
	gpus, _, _ := createSampleData()
	for _, g := range gpus {
		g.History = xpu.NewMetricHistory(60)
		for i := 0; i < 30; i++ {
			g.History.Record(g.Stats)
		}
	}
	out := stripANSIViews(renderTrendRows(gpus, -1, 140))
	lines := strings.Split(out, "\n")

	var cardRows []int
	for i, l := range lines {
		if strings.Contains(l, "GPU ") {
			cardRows = append(cardRows, i)
		}
	}
	if len(cardRows) != 2 {
		t.Fatalf("expected a trend row per card, got %d:\n%s", len(cardRows), out)
	}
	if cardRows[1]-cardRows[0] < 2 {
		t.Errorf("trend rows for the two cards are adjacent and run together:\n%s", out)
	}
}

// Memory no visible process accounts for gets its own row per card, worded by
// whether the view could see every process.
func TestOverviewShowsUnattributedMemory(t *testing.T) {
	gpus, _, _ := createSampleData()
	g := gpus[1]
	g.Stats.MemoryUsedMiB = 23029
	g.Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{
		{PID: 1, Command: "llama-server", Model: "Flash", Memory: xpu.ProcessGPUMemory{VramBytes: 22774 << 20}},
	}}
	g.Stats.Sysfs.Displays = []string{"DP-7"}

	if got := unattributedVRAM(g); got != 255<<20 {
		t.Fatalf("unattributed = %d MiB, want 255", got>>20)
	}

	g.Stats.ClientsComplete = false
	out := RenderOverview(gpus, 0, "", 180)
	if !strings.Contains(out, "255M") || !strings.Contains(out, "hidden without sudo") {
		t.Errorf("without sudo the row should show the gap and say processes are hidden:\n%s", out)
	}
	if !strings.Contains(out, "display DP-7") {
		t.Errorf("the row should name the attached display:\n%s", out)
	}

	// Completeness is a property of the whole walk, so it is the same on every card.
	for _, card := range gpus {
		card.Stats.ClientsComplete = true
	}
	if out := RenderOverview(gpus, 0, "", 180); strings.Contains(out, "hidden without sudo") {
		t.Errorf("with every process visible the row must not claim any are hidden:\n%s", out)
	}

	// The row matches no name, so a filter hides it.
	if out := RenderOverview(gpus, 0, "flash", 180); strings.Contains(out, "other:") {
		t.Errorf("filtered output should not include the unattributed row:\n%s", out)
	}
}

// A card whose clients account for more than it reports used must not show a
// negative or spurious remainder.
func TestUnattributedNeverNegative(t *testing.T) {
	gpus, _, _ := createSampleData()
	g := gpus[0]
	g.Stats.MemoryUsedMiB = 100
	g.Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{
		{PID: 1, Memory: xpu.ProcessGPUMemory{VramBytes: 200 << 20}},
	}}
	if got := unattributedVRAM(g); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// Every card keeps its row even when nothing is unaccounted for, so the table
// does not change shape as the readings move.
func TestOtherRowAlwaysPresent(t *testing.T) {
	gpus, _, _ := createSampleData()
	for _, g := range gpus {
		g.Stats.MemoryUsedMiB = 0 // nothing unattributed
	}
	out := RenderOverview(gpus, 0, "", 180)
	if got := strings.Count(out, "other:"); got != len(gpus) {
		t.Errorf("expected an other row for each of %d cards, got %d:\n%s", len(gpus), got, out)
	}
}

// A model server opens a small DRM client on every card it can see and
// allocates on one. Overview already drops the stray from its table; Detail
// listed it, so the A40 appeared to be "loading" the models that live on the
// B70s. The same rule now applies in both.
func TestDetailOmitsAServersStrayClientOnAnotherCard(t *testing.T) {
	gpus, _, topo := createSampleData()
	proc := func(pid int, name string, vram int64) xpu.DrmProcess {
		return xpu.DrmProcess{PID: pid, Command: name, Memory: xpu.ProcessGPUMemory{VramBytes: vram}}
	}
	// pid 4242 holds 20 GiB on card 0 and a 10 MiB stray on card 1; pid 7 is a
	// genuine small workload that lives only on card 1.
	gpus[0].Stats.Fdinfo = &xpu.FdinfoStats{Available: true,
		Processes: []xpu.DrmProcess{proc(4242, "stray-server", 20<<30)}}
	gpus[1].Stats.Fdinfo = &xpu.FdinfoStats{Available: true,
		Processes: []xpu.DrmProcess{proc(4242, "stray-server", 10<<20), proc(7, "real-client", 30<<20)}}

	out := stripANSIViews(RenderDetailAmong(gpus[1], gpus, topo, 150, 80))
	if strings.Contains(out, "stray-server") {
		t.Error("card 1's detail lists the stray 10 MiB client of a server that lives on card 0")
	}
	if !strings.Contains(out, "real-client") {
		t.Error("a process whose only presence is on this card was dropped")
	}

	// On the card where it actually lives, it is listed.
	if on0 := stripANSIViews(RenderDetailAmong(gpus[0], gpus, topo, 150, 80)); !strings.Contains(on0, "stray-server") {
		t.Error("the server is missing from the card it is running on")
	}
}

// An idle card shows its last power dimmed with a "zz" marker, so a stale-looking
// number reads as deliberate; an awake card does not.
func TestOverviewMarksAnIdleCard(t *testing.T) {
	gpus, _, _ := createSampleData()
	gpus[1].Stats.Idle = true
	out := stripANSIViews(RenderOverview(gpus, -1, "", 170))
	lines := strings.Split(out, "\n")
	var zz int
	for _, l := range lines {
		if strings.Contains(l, " zz") {
			zz++
		}
	}
	if zz != 1 {
		t.Errorf("%d rows carry the idle marker, want exactly 1 (only the idle card)", zz)
	}
}

// The PCIe column scales its unit with the value; fixed whole MB/s rendered the
// tens of KB/s a resident model moves as "0" forever.
func TestRateShortScalesUnit(t *testing.T) {
	for kbps, want := range map[float64]string{
		0:                "0B",
		0.4:              "410B", // 409.6 bytes
		43.86:            "44K",  // the B70s during generation, measured
		86.6:             "87K",  // the A40
		9.2:              "9.2K", // one decimal below 10
		512:              "512K",
		1000:             "1000K", // 0.98 M: not yet a megabyte
		1024:             "1.0M",
		9282:             "9.1M", // a model load, measured
		13 * 1024 * 1024: "13G",  // the copy bench's peak
	} {
		if got := rateShort(kbps); got != want {
			t.Errorf("rateShort(%v KB/s) = %q, want %q", kbps, got, want)
		}
	}
}

// A parked card says so instead of showing the driver's latched request.
func TestOverviewShowsIdleForAParkedClock(t *testing.T) {
	gpus, _, _ := createSampleData()
	gpus[0].Stats.FrequencyMHz = 2800
	gpus[0].Stats.Sysfs.GTs[0].Freq.ActMHz = 0
	gpus[0].Stats.Sysfs.GTs[0].Idle.BusyPercent = 0
	out := stripANSIViews(RenderOverview(gpus, -1, "", 170))
	if !strings.Contains(out, "idle/2800") {
		t.Error("parked card's clock is not shown as idle")
	}
	if strings.Contains(out, "2800/2800") {
		t.Error("the latched requested clock is shown as if the card were running at it")
	}
}

// Memory throughput is a rate with a scaled unit, like PCIe, not a percent of
// peak -- which read "0%" for anything short of sustained generation.
func TestOverviewMemoryTrafficIsARate(t *testing.T) {
	gpus, _, _ := createSampleData()
	gpus[0].Stats.TrafficUnavailable = false
	gpus[0].Stats.MemoryReadKBps, gpus[0].Stats.MemoryWriteKBps = 7.5*1024, 1.4*1024
	out := stripANSIViews(RenderOverview(gpus, -1, "", 170))
	if !strings.Contains(out, "7.5M/1.4M") {
		t.Errorf("memory traffic cell missing; want 7.5M/1.4M in:\n%s", out)
	}
	// The percent of peak sits beside it, as its own column.
	gpus[0].Stats.MemoryBandwidthPercent = 31
	out = stripANSIViews(RenderOverview(gpus, -1, "", 170))
	if !strings.Contains(out, "BW %") || !strings.Contains(out, " 31%") {
		t.Errorf("percent-of-peak column missing or wrong; want header BW %% and a 31%% cell in:\n%s", out)
	}
}

// The group headings sit above the column headers and cover exactly the run of
// columns they label, so "VRAM" is said once over USED/FREE/rd/wr/BW %.
func TestOverviewGroupHeadings(t *testing.T) {
	gpus, _, _ := createSampleData()
	out := stripANSIViews(RenderOverview(gpus, -1, "", 170))
	lines := strings.Split(out, "\n")
	var groups, headers string
	for i, l := range lines {
		if strings.Contains(l, "BUSY %") && i > 0 {
			groups, headers = lines[i-1], l
			break
		}
	}
	if headers == "" {
		t.Fatal("no header row")
	}
	for _, g := range []string{"ACTIVITY", "VRAM", "HOST", "THERMAL"} {
		if !strings.Contains(groups, g) {
			t.Errorf("group heading %q missing from %q", g, groups)
		}
	}
	// VRAM starts over USED and the THERMAL heading starts over CORE. Columns
	// are display cells, not bytes: the rule is drawn in multibyte box characters.
	col := func(line, s string) int { return lipgloss.Width(line[:strings.Index(line, s)]) }
	if col(groups, "VRAM") != col(headers, "USED") {
		t.Errorf("VRAM heading at col %d, USED header at col %d", col(groups, "VRAM"), col(headers, "USED"))
	}
	if col(groups, "THERMAL") != col(headers, "CORE") {
		t.Errorf("THERMAL heading at col %d, CORE header at col %d", col(groups, "THERMAL"), col(headers, "CORE"))
	}
	// A narrow terminal that drops every memory column drops the heading too.
	narrow := stripANSIViews(RenderOverview(gpus, -1, "", 60))
	if strings.Contains(narrow, "HOST") {
		t.Error("HOST heading shown with its only column dropped")
	}
}

// Memory evicted to host RAM is shown beside USED and in Detail; a card with
// only the usual per-client baseline shows nothing extra.
func TestOverviewAndDetailShowSpilledMemory(t *testing.T) {
	gpus, _, topo := createSampleData()
	gpus[0].Stats.HostBackedBytes = 1900 << 20
	out := stripANSIViews(RenderOverview(gpus, -1, "", 170))
	if !strings.Contains(out, "+1.9G") {
		t.Error("spilled memory not shown beside USED")
	}
	det := stripANSIViews(RenderDetailAmong(gpus[0], gpus, topo, 150, 80))
	if !strings.Contains(det, "spilled") || !strings.Contains(det, "VRAM is full") {
		t.Error("Detail has no spilled line")
	}
	gpus[1].Stats.HostBackedBytes = 100 << 20 // below the threshold: just the baseline
	out = stripANSIViews(RenderOverview(gpus, -1, "", 170))
	if strings.Count(out, "+1.9G") != 1 || strings.Contains(out, "+100M") {
		t.Error("a sub-threshold figure was flagged")
	}
}

func TestFanScale(t *testing.T) {
	if got := fanScale(nil); got != 1000 {
		t.Errorf("empty history: %v, want 1000", got)
	}
	if got := fanScale([]float64{900}, []float64{3579}); got <= 3579 || got != 4000 {
		t.Errorf("peak 3579 across two cards: %v, want 4000", got)
	}
}
