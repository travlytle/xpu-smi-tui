package views

import (
	"image/color"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	"charm.land/lipgloss/v2"
)

func widestLine(s string) int {
	w := 0
	for _, l := range strings.Split(s, "\n") {
		if n := lipgloss.Width(l); n > w {
			w = n
		}
	}
	return w
}

// Every live view must fit the width it is given, at every plausible width.
// lipgloss pads each line to the widest, so one over-wide line drags the whole
// frame off-screen -- which is what Trends did at 60 columns.
func TestAllLiveViewsFitAllWidths(t *testing.T) {
	gpus, _, topo := createSampleData()
	for _, w := range []int{60, 80, 100, 120, 150, 200} {
		checks := map[string]string{
			"overview":    RenderOverview(gpus, 0, "", w),
			"detail":      RenderDetail(gpus[0], topo, w, 60),
			"trends":      RenderTrends(gpus, 0, w, 50),
			"trends-dual": RenderTrends(gpus, -1, w, 50),
			"legend":      RenderLegend(w, 40),
			"help":        RenderHelp(w, 40),
		}
		for name, out := range checks {
			if got := widestLine(out); got > w {
				t.Errorf("%s at width %d renders %d columns", name, w, got)
			}
		}
	}
}

// Detail rows must not wrap: a wrapped tail lands in column 1 and breaks the
// label column. With the width-adaptive blocks (the channel map legitimately
// takes more rows when narrow) taken out, the row count must not change with
// the width -- and the inputs here are the ones that used to overflow: a long
// device name in the title and a long model name in the clients block.
func TestDetailDoesNotWrapWhenNarrow(t *testing.T) {
	gpus, _, topo := createSampleData()
	g := gpus[0]
	g.Stats.Sysfs = nil
	g.Info.DeviceName = "Intel(R) Arc(TM) Pro B70 Graphics with an unreasonably long marketing name"
	g.Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{{
		PID: 1, Command: "llama-server", Port: "6001",
		Model:  "Qwen3.8-27B-GPTQ-Int4-sym-G128-MTP-BF16-with-a-very-long-suffix",
		Memory: xpu.ProcessGPUMemory{VramBytes: 25 << 30, GttBytes: 800 << 20, SystemBytes: 186 << 20},
	}}}

	wide := strings.Count(RenderDetail(g, topo, 200, 60), "\n")
	for _, w := range []int{60, 80} {
		if got := strings.Count(RenderDetail(g, topo, w, 60), "\n"); got != wide {
			t.Errorf("width %d: %d lines vs %d at 200 -- rows are wrapping", w, got, wide)
		}
	}
}

// truncate must never split a multi-byte rune or exceed its width.
func TestTruncateIsRuneSafe(t *testing.T) {
	for _, in := range []string{"héllo wörld ünicode", "日本語のモデル名です", "Qwen3.8-27B-Q4_K_M", "a"} {
		for n := 0; n < 14; n++ {
			out := truncate(in, n)
			if !utf8.ValidString(out) {
				t.Errorf("truncate(%q,%d) = invalid UTF-8 %q", in, n, out)
			}
			if lipgloss.Width(out) > n {
				t.Errorf("truncate(%q,%d) is %d wide", in, n, lipgloss.Width(out))
			}
		}
	}
}

// Two servers with the same model and no port are two workloads.
func TestWorkloadsKeepSameModelWithoutPort(t *testing.T) {
	gpus, _, _ := createSampleData()
	gpus[0].Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{
		{PID: 100, Model: "Qwen", Memory: xpu.ProcessGPUMemory{VramBytes: 19 << 30}},
		{PID: 200, Model: "Qwen", Memory: xpu.ProcessGPUMemory{VramBytes: 9 << 30}},
	}}
	gpus[1].Stats.Fdinfo = &xpu.FdinfoStats{Available: true}
	if got := len(collectWorkloads(gpus)); got != 2 {
		t.Errorf("got %d workloads, want 2 -- a server vanished", got)
	}
}

// vLLM's two processes report the same shared allocation; merged, the figure
// is the larger one, not the sum, and %CARD cannot exceed 100.
func TestWorkloadMergeDoesNotDoubleCount(t *testing.T) {
	gpus, _, _ := createSampleData()
	gpus[0].Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{
		{PID: 1, Model: "M", Port: "8000", Memory: xpu.ProcessGPUMemory{VramBytes: 23 << 30}},
		{PID: 2, Model: "M", Port: "8000", Memory: xpu.ProcessGPUMemory{VramBytes: 23 << 30}},
	}}
	gpus[1].Stats.Fdinfo = &xpu.FdinfoStats{Available: true}
	got := collectWorkloads(gpus)
	if len(got) != 1 || got[0].proc.Memory.VramBytes != 23<<30 {
		t.Errorf("merged = %+v, want one row of 23 GiB", got)
	}
	out := stripANSIViews(RenderOverview(gpus, 0, "", 160))
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, ":8000") && (strings.Contains(l, "144%") || strings.Contains(l, "161%")) {
			t.Errorf("share over 100%%: %s", strings.TrimSpace(l))
		}
	}
}

// A server part-way through loading (between 64 MiB and 1 GiB) must not
// sprout a phantom row on the card it is not using.
func TestStrayClientHoleIsClosed(t *testing.T) {
	gpus, _, _ := createSampleData()
	gpus[0].Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{
		{PID: 400, Model: "small", Port: "9000", Memory: xpu.ProcessGPUMemory{VramBytes: 500 << 20}}}}
	gpus[1].Stats.Fdinfo = &xpu.FdinfoStats{Available: true, Processes: []xpu.DrmProcess{
		{PID: 400, Model: "small", Port: "9000", Memory: xpu.ProcessGPUMemory{VramBytes: 5 << 20}}}}
	got := collectWorkloads(gpus)
	if len(got) != 1 || got[0].deviceID != 0 {
		t.Errorf("got %+v, want only the 500 MiB entry on card 0", got)
	}
}

// With no published limit there is no verdict: the VR runs hot by design and
// was painted red at its normal temperature.
func TestNoLimitMeansNoColourVerdict(t *testing.T) {
	if c := tempSeverityColor(92, 0); c == lipgloss.Color("#EF4444") || c == lipgloss.Color("#F59E0B") {
		t.Errorf("92 °C with no limit coloured %v", c)
	}
	if c := tempSeverityColor(92, 100); c != lipgloss.Color("#EF4444") {
		t.Errorf("with a limit the verdict must still apply, got %v", c)
	}
}

// Memory-temperature charts must use the sensor's crit, not 95.
func TestTrendsMemoryChartUsesSensorLimit(t *testing.T) {
	gpus, _, _ := createSampleData()
	if MemTempScale(gpus[0]) != 105 {
		t.Skip("fixture does not publish a 105 crit")
	}
	out := stripANSIViews(RenderTrends(gpus, 0, 160, 60))
	if strings.Contains(out, " 95.0 °C") || strings.Contains(out, "95 °C┤") {
		t.Errorf("memory chart still scaled to 95:\n%s", out)
	}
}

// An unknown power cap is shown as unknown, not as a 275 W nobody measured.
func TestUnknownPowerCapIsNotInvented(t *testing.T) {
	gpus, _, topo := createSampleData()
	g := gpus[0]
	g.Stats.Sysfs.PowerLimits.CapW = 0
	g.Info.PowerLimitW = 0
	if _, ok := PowerCap(g); ok {
		t.Fatal("PowerCap reported a cap with none available")
	}
	out := stripANSIViews(RenderDetail(g, topo, 150, 60))
	if strings.Contains(out, "cap 275") {
		t.Errorf("Detail invented a 275 W cap:\n%s", out)
	}
}

// Three cards must not share a colour.
func TestAccentDistinctForThreeCards(t *testing.T) {
	seen := map[color.Color]bool{}
	for i := 0; i < 3; i++ {
		c := accentFor(i)
		if seen[c] {
			t.Errorf("card %d reuses a colour", i)
		}
		seen[c] = true
	}
}

// threeCards builds combined data for a simulated three-card machine.
func threeCards(t *testing.T) []*xpu.GPUCombined {
	t.Helper()
	mock := xpu.NewMockCollector().WithDevices(3)
	devs, _ := mock.Discover(nil)
	ids := []int{0, 1, 2}
	stats, _ := mock.CollectStats(nil, ids)
	health, _ := mock.CollectHealth(nil)
	var gpus []*xpu.GPUCombined
	for _, d := range devs {
		g := &xpu.GPUCombined{Info: d, Stats: *stats[d.DeviceID], Health: health[d.DeviceID], History: xpu.NewMetricHistory(30)}
		for i := 0; i < 5; i++ {
			g.History.Record(g.Stats)
		}
		gpus = append(gpus, g)
	}
	return gpus
}

// A third card must appear everywhere the first two do, at every width, and
// nothing may overflow. Trends previously overlaid the first two cards and
// never drew a third.
func TestThreeCardsRenderEverywhere(t *testing.T) {
	gpus := threeCards(t)
	if len(gpus) != 3 {
		t.Fatalf("mock produced %d cards", len(gpus))
	}
	for _, w := range []int{80, 120, 160} {
		over := RenderOverview(gpus, -1, "", w)
		if c := strings.Count(stripANSIViews(over), "  :60"); c < 3 {
			t.Errorf("width %d: overview lists %d workloads for 3 cards", w, c)
		}
		trends := RenderTrends(gpus, -1, w, 50)
		for _, id := range []string{"GPU 0", "GPU 1", "GPU 2"} {
			if !strings.Contains(stripANSIViews(trends), id) {
				t.Errorf("width %d: Trends omits %s with all cards selected", w, id)
			}
		}
		for name, out := range map[string]string{"overview": over, "trends": trends, "detail": RenderDetail(gpus[2], nil, w, 60)} {
			if got := widestLine(out); got > w {
				t.Errorf("%s at width %d renders %d columns with 3 cards", name, w, got)
			}
		}
	}
}
