package exporter

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/travlytle/xpu-smi-tui/pkg/xpu"
)

// gather runs the registry's Gather and indexes samples by metric name, then
// by the value of the given label, for easy assertions.
func gather(t *testing.T, reg *prometheus.Registry, labelName string) map[string]map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]map[string]float64)
	for _, f := range families {
		byLabel := make(map[string]float64)
		for _, m := range f.GetMetric() {
			key := ""
			for _, l := range m.GetLabel() {
				if l.GetName() == labelName {
					key = l.GetValue()
				}
			}
			byLabel[key] = m.GetGauge().GetValue()
		}
		out[f.GetName()] = byLabel
	}
	return out
}

func TestExporterPollPopulatesCoreMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	col := xpu.NewMockCollector()
	e := New(col, time.Second, reg)

	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)

	byDevice := gather(t, reg, "device")

	for _, name := range []string{
		"xpu_gpu_up", "xpu_gpu_busy_ratio", "xpu_gpu_power_watts",
		"xpu_gpu_power_limit_watts", "xpu_gpu_memory_used_bytes",
		"xpu_gpu_memory_total_bytes", "xpu_gpu_fan_rpm",
		"xpu_gpu_clock_mhz", "xpu_gpu_clock_max_mhz",
	} {
		vals, ok := byDevice[name]
		if !ok {
			t.Errorf("%s: not exported at all", name)
			continue
		}
		if _, ok := vals["0"]; !ok {
			t.Errorf("%s: no sample for device 0", name)
		}
		if _, ok := vals["1"]; !ok {
			t.Errorf("%s: no sample for device 1", name)
		}
	}

	if v := byDevice["xpu_gpu_up"]["0"]; v != 1 {
		t.Errorf("xpu_gpu_up{device=0} = %v, want 1 (mock always returns data)", v)
	}
	if v := byDevice["xpu_gpu_memory_total_bytes"]["0"]; v <= 0 {
		t.Errorf("xpu_gpu_memory_total_bytes{device=0} = %v, want > 0", v)
	}
}

// The percent-vs-fraction distinction is easy to get backwards, and the
// homelab dashboard convention (Grafana's percentunit panel unit) depends on
// it: these must be 0-1, not 0-100.
func TestExporterRatiosAreFractionsNotPercent(t *testing.T) {
	reg := prometheus.NewRegistry()
	col := xpu.NewMockCollector()
	e := New(col, time.Second, reg)

	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)

	byDevice := gather(t, reg, "device")
	for _, name := range []string{"xpu_gpu_busy_ratio", "xpu_gpu_compute_busy_ratio", "xpu_gpu_memory_bandwidth_ratio"} {
		for dev, v := range byDevice[name] {
			if v > 1.0001 {
				t.Errorf("%s{device=%s} = %v, looks like a 0-100 percent, not a 0-1 fraction", name, dev, v)
			}
		}
	}
}

func TestExporterSensorLabelsMatchBetweenTempAndCrit(t *testing.T) {
	reg := prometheus.NewRegistry()
	col := xpu.NewMockCollector()
	e := New(col, time.Second, reg)

	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	sensorsOf := func(family string) map[string]bool {
		out := map[string]bool{}
		for _, f := range families {
			if f.GetName() != family {
				continue
			}
			for _, m := range f.GetMetric() {
				for _, l := range m.GetLabel() {
					if l.GetName() == "sensor" {
						out[l.GetValue()] = true
					}
				}
			}
		}
		return out
	}
	temp := sensorsOf("xpu_gpu_temperature_celsius")
	crit := sensorsOf("xpu_gpu_temperature_critical_celsius")
	for _, want := range []string{"core", "memory"} {
		if !temp[want] {
			t.Errorf("xpu_gpu_temperature_celsius missing sensor=%q", want)
		}
		if !crit[want] {
			t.Errorf("xpu_gpu_temperature_critical_celsius missing sensor=%q -- the sysfs label passed to tempCrit doesn't match what Temp() actually keys on", want)
		}
	}
}

func TestExporterMarksACardDownWhenItsStatsAreMissing(t *testing.T) {
	reg := prometheus.NewRegistry()
	col := &droppingCollector{MockCollector: xpu.NewMockCollector(), drop: 1}
	e := New(col, time.Second, reg)

	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)

	byDevice := gather(t, reg, "device")
	if v := byDevice["xpu_gpu_up"]["0"]; v != 1 {
		t.Errorf("device 0 xpu_gpu_up = %v, want 1", v)
	}
	if v := byDevice["xpu_gpu_up"]["1"]; v != 0 {
		t.Errorf("device 1 xpu_gpu_up = %v, want 0 (its stats were dropped)", v)
	}
}

// droppingCollector removes one device's stats from CollectStats's result,
// standing in for a card that failed to report this tick.
type droppingCollector struct {
	*xpu.MockCollector
	drop int
}

func (d *droppingCollector) CollectStats(ctx context.Context, ids []int) (map[int]*xpu.GPUStats, error) {
	stats, err := d.MockCollector.CollectStats(ctx, ids)
	delete(stats, d.drop)
	return stats, err
}

// A poll must never block on a stuck collector: it is on its own timer, not
// triggered by an HTTP scrape, but an unbounded CollectStats call would still
// stall every future poll behind it.
func TestExporterPollDoesNotHangOnAStuckCollector(t *testing.T) {
	reg := prometheus.NewRegistry()
	col := &stuckCollector{MockCollector: xpu.NewMockCollector(), release: make(chan struct{})}
	defer close(col.release)
	e := New(col, 50*time.Millisecond, reg)

	ctx := context.Background()
	e.discover(ctx)

	done := make(chan struct{})
	go func() { e.poll(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poll did not return: it must time out against a stuck CollectStats, not hang the poller forever")
	}
}

// stuckCollector ignores its context entirely, the way a blocked sysfs or
// /proc read really does -- a context-respecting mock would pass even
// without the fix in poll(), since a plain context timeout only helps if the
// callee itself checks it.
type stuckCollector struct {
	*xpu.MockCollector
	release chan struct{}
}

func (s *stuckCollector) CollectStats(_ context.Context, ids []int) (map[int]*xpu.GPUStats, error) {
	<-s.release
	return s.MockCollector.CollectStats(context.Background(), ids)
}

// The registered metric text must actually be valid Prometheus exposition
// format -- promhttp's own handler would refuse to serve it otherwise.
func TestExporterOutputParsesAsPrometheusFormat(t *testing.T) {
	reg := prometheus.NewRegistry()
	col := xpu.NewMockCollector()
	e := New(col, time.Second, reg)
	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) == 0 {
		t.Fatal("no metric families registered")
	}
	for _, f := range families {
		if !strings.HasPrefix(f.GetName(), "xpu_") {
			t.Errorf("metric %q missing the xpu_ namespace prefix", f.GetName())
		}
		if f.GetHelp() == "" {
			t.Errorf("metric %q has no HELP text", f.GetName())
		}
		if f.GetType() != dto.MetricType_GAUGE {
			t.Errorf("metric %q is not a gauge", f.GetName())
		}
	}
}

// An unknown value must be absent from the scrape, not exported as 0: Grafana
// draws a 0 as a healthy idle line, which is exactly what an i915 card with no
// memory counter, or a card on its first poll, is not.
func TestExporterOmitsUnmeasuredSeries(t *testing.T) {
	reg := prometheus.NewRegistry()
	col := &unmeasuredCollector{MockCollector: xpu.NewMockCollector()}
	e := New(col, time.Second, reg)

	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)

	byDevice := gather(t, reg, "device")
	for _, name := range []string{"xpu_gpu_busy_ratio", "xpu_gpu_memory_used_bytes", "xpu_gpu_memory_bandwidth_ratio"} {
		if _, ok := byDevice[name]["0"]; ok {
			t.Errorf("%s{device=0} exported although it was not measured", name)
		}
		if _, ok := byDevice[name]["1"]; !ok {
			t.Errorf("%s{device=1} missing; only device 0 was unmeasured", name)
		}
	}
	if v := byDevice["xpu_gpu_up"]["0"]; v != 1 {
		t.Errorf("xpu_gpu_up{device=0} = %v, want 1: unmeasured is not down", v)
	}
	if v, ok := byDevice["xpu_gpu_power_watts"]["0"]; !ok || v <= 0 {
		t.Errorf("power_watts{device=0} = %v, %v; the values that were measured must still export", v, ok)
	}
}

type unmeasuredCollector struct{ *xpu.MockCollector }

func (u *unmeasuredCollector) CollectStats(ctx context.Context, ids []int) (map[int]*xpu.GPUStats, error) {
	stats, err := u.MockCollector.CollectStats(ctx, ids)
	if s := stats[0]; s != nil {
		s.MemoryUsedUnavailable = true
		s.TrafficUnavailable = true
		s.Sysfs = nil // no GT, so no busy measurement
	}
	return stats, err
}

// The device label is the assigned GPU number, not xpu-smi's index -- the
// index is what moves between boots, and a dashboard's history has to keep
// following the same physical card.
func TestExporterLabelsByAssignedGPUNumber(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := New(&renumberedCollector{MockCollector: xpu.NewMockCollector()}, time.Second, reg)

	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	deviceByBdf := map[string]string{}
	for _, f := range families {
		if f.GetName() != "xpu_gpu_info" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			deviceByBdf[labels["pci_bdf"]] = labels["device"]
		}
	}
	// xpu-smi id 0 sits at the higher PCI address, so it is GPU 1.
	want := map[string]string{"0000:00:00.1": "0", "0000:00:00.2": "1"}
	if !reflect.DeepEqual(deviceByBdf, want) {
		t.Errorf("device by pci_bdf = %v, want %v", deviceByBdf, want)
	}
	// The measurements follow the same labels, and both cards are still up.
	up := gather(t, reg, "device")["xpu_gpu_up"]
	if up["0"] != 1 || up["1"] != 1 {
		t.Errorf("xpu_gpu_up = %v, want both cards up under their assigned numbers", up)
	}
}

type renumberedCollector struct{ *xpu.MockCollector }

func (r *renumberedCollector) Discover(ctx context.Context) ([]xpu.DeviceInfo, error) {
	devs, err := r.MockCollector.Discover(ctx)
	if len(devs) >= 2 {
		devs[0].PciBdf, devs[1].PciBdf = "0000:00:00.2", "0000:00:00.1"
	}
	return xpu.AssignGPUNumbers(devs, nil), err
}

// An estimated memory figure is exported, and flagged as an estimate so a
// dashboard can tell it from the driver's own reading.
func TestExporterFlagsEstimatedMemory(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := New(&estimatedMemCollector{MockCollector: xpu.NewMockCollector()}, time.Second, reg)

	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)

	byDevice := gather(t, reg, "device")
	if _, ok := byDevice["xpu_gpu_memory_used_bytes"]["0"]; !ok {
		t.Error("estimated memory used was not exported")
	}
	if v := byDevice["xpu_gpu_memory_used_estimated"]["0"]; v != 1 {
		t.Errorf("used_estimated{device=0} = %v, want 1", v)
	}
	if v := byDevice["xpu_gpu_memory_used_estimated"]["1"]; v != 0 {
		t.Errorf("used_estimated{device=1} = %v, want 0 for a driver-reported figure", v)
	}
}

type estimatedMemCollector struct{ *xpu.MockCollector }

func (c *estimatedMemCollector) CollectStats(ctx context.Context, ids []int) (map[int]*xpu.GPUStats, error) {
	stats, err := c.MockCollector.CollectStats(ctx, ids)
	if s := stats[0]; s != nil {
		s.MemoryUsedEstimated = true
	}
	return stats, err
}

// A card with measured PCIe but no memory-bandwidth counter (the A40) exports
// the PCIe series and only those.
func TestExporterExportsMeasuredPcieWithoutBandwidth(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := New(&pcieOnlyCollector{MockCollector: xpu.NewMockCollector()}, time.Second, reg)

	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)

	families, _ := reg.Gather()
	seen := map[string]bool{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "device" && l.GetValue() == "0" {
					seen[f.GetName()] = true
				}
			}
		}
	}
	if !seen["xpu_gpu_pcie_bytes_per_second"] {
		t.Error("measured PCIe was not exported")
	}
	if seen["xpu_gpu_memory_bandwidth_ratio"] {
		t.Error("bandwidth exported although the card has no such counter")
	}
}

type pcieOnlyCollector struct{ *xpu.MockCollector }

func (c *pcieOnlyCollector) CollectStats(ctx context.Context, ids []int) (map[int]*xpu.GPUStats, error) {
	stats, err := c.MockCollector.CollectStats(ctx, ids)
	if s := stats[0]; s != nil {
		s.TrafficUnavailable, s.PcieMeasured = true, true
	}
	return stats, err
}

// A card left alone to suspend is not overwritten: its series keep the last
// sample (a step, not a gap), and xpu_gpu_idle says why.
func TestExporterHoldsLastValuesWhileACardIsIdle(t *testing.T) {
	reg := prometheus.NewRegistry()
	col := &idlingCollector{MockCollector: xpu.NewMockCollector()}
	e := New(col, time.Second, reg)

	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)
	before := gather(t, reg, "device")
	if before["xpu_gpu_idle"]["0"] != 0 {
		t.Fatalf("idle before = %v, want 0", before["xpu_gpu_idle"]["0"])
	}
	realPower := before["xpu_gpu_power_watts"]["0"]

	col.idle = true
	e.poll(ctx)
	after := gather(t, reg, "device")
	if after["xpu_gpu_idle"]["0"] != 1 {
		t.Errorf("idle = %v, want 1", after["xpu_gpu_idle"]["0"])
	}
	if after["xpu_gpu_up"]["0"] != 1 {
		t.Errorf("up = %v: an idle card is not down", after["xpu_gpu_up"]["0"])
	}
	if got := after["xpu_gpu_power_watts"]["0"]; got != realPower {
		t.Errorf("power = %v, want the held %v; an idle tick's carried-over zeros must not be exported", got, realPower)
	}
	if after["xpu_gpu_idle"]["1"] != 0 {
		t.Error("the other card was marked idle")
	}
}

type idlingCollector struct {
	*xpu.MockCollector
	idle bool
}

func (c *idlingCollector) CollectStats(ctx context.Context, ids []int) (map[int]*xpu.GPUStats, error) {
	stats, err := c.MockCollector.CollectStats(ctx, ids)
	if s := stats[0]; s != nil && c.idle {
		s.Idle = true
		s.PowerW = 0 // what a careless "held" value would look like
	}
	return stats, err
}

// The per-card sysfs detail beyond the headline sensors: every hwmon
// temperature sensor under its own label, the resolved host PCIe link, and one
// series per throttle reason per GT -- all eight every poll, so a reason that
// clears reads back to 0 instead of leaving a stale 1.
func TestExporterExportsSysfsDetail(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := New(xpu.NewMockCollector(), time.Second, reg)
	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	series := map[string]map[string]float64{} // family -> "k=v,k=v" -> value
	for _, f := range families {
		byLabels := map[string]float64{}
		for _, m := range f.GetMetric() {
			key := ""
			for _, l := range m.GetLabel() {
				key += l.GetName() + "=" + l.GetValue() + ","
			}
			byLabels[key] = m.GetGauge().GetValue()
		}
		series[f.GetName()] = byLabels
	}

	temps := series["xpu_gpu_temperature_celsius"]
	for _, sensor := range []string{"mctrl", "pcie", "vram_ch_0", "vram_ch_15"} {
		if _, ok := temps["device=0,sensor="+sensor+","]; !ok {
			t.Errorf("temperature sensor %q not exported", sensor)
		}
	}
	if _, ok := temps["device=0,sensor=pkg,"]; ok {
		t.Error("pkg exported under its raw name as well as core: a dashboard would double-count it")
	}

	if got := series["xpu_gpu_pcie_link_width"]["device=0,kind=current,"]; got != 16 {
		t.Errorf("link width current = %v, want 16 (the resolved host link, not the on-card x1)", got)
	}
	if got := series["xpu_gpu_pcie_link_speed_gtps"]["device=0,kind=max,"]; got != 16 {
		t.Errorf("link speed max = %v, want 16", got)
	}

	thr := series["xpu_gpu_throttled"]
	for _, reason := range []string{"pl1", "pl2", "pl4", "thermal", "vr_thermalert", "vr_tdc", "prochot", "ratl"} {
		for _, gt := range []string{"gt0", "gt1"} {
			if _, ok := thr["device=0,gt="+gt+",reason="+reason+","]; !ok {
				t.Errorf("throttle reason %s on %s not exported (every reason must be present so a cleared one reads 0)", reason, gt)
			}
		}
	}
}

// A throttle reason that was asserted must read 0 once it clears, not vanish
// or keep its old 1.
func TestExporterClearsAThrottleReason(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := New(xpu.NewMockCollector(), time.Second, reg)
	e.setThrottleReasons("0", "gt0", xpu.ThrottleFlags{Status: true, PL2: true})
	e.setThrottleReasons("0", "gt0", xpu.ThrottleFlags{})
	byDevice := gather(t, reg, "reason")
	if v, ok := byDevice["xpu_gpu_throttled"]["pl2"]; !ok || v != 0 {
		t.Errorf("pl2 after clearing = %v, %v; want 0", v, ok)
	}
}

// gpu_clock_mhz is the actual clock (0 while parked), and the driver's
// requested figure is its own series, so a parked card at 7 W no longer
// graphs as running flat out.
func TestExporterClockIsActualNotRequested(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := New(&parkedCollector{MockCollector: xpu.NewMockCollector()}, time.Second, reg)
	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)
	byDevice := gather(t, reg, "device")
	if v := byDevice["xpu_gpu_clock_mhz"]["0"]; v != 0 {
		t.Errorf("clock_mhz = %v for a parked card, want 0", v)
	}
	if v := byDevice["xpu_gpu_clock_requested_mhz"]["0"]; v != 2800 {
		t.Errorf("clock_requested_mhz = %v, want 2800", v)
	}
}

type parkedCollector struct{ *xpu.MockCollector }

func (c *parkedCollector) CollectStats(ctx context.Context, ids []int) (map[int]*xpu.GPUStats, error) {
	stats, err := c.MockCollector.CollectStats(ctx, ids)
	if s := stats[0]; s != nil && s.Sysfs != nil && len(s.Sysfs.GTs) > 0 {
		s.FrequencyMHz = 2800
		s.Sysfs.GTs[0].Freq.ActMHz = 0
		s.Sysfs.GTs[0].Idle.BusyPercent = 0
	}
	return stats, err
}

// The model label is what a dashboard legend uses to tell cards apart at a
// glance; the full name is kept alongside it.
func TestShortModel(t *testing.T) {
	for name, want := range map[string]string{
		"Intel(R) Arc(TM) Pro B70 Graphics":     "B70",
		"Intel(R) Arc(TM) Pro A40/A50 Graphics": "A40/A50",
		"Intel(R) Arc(TM) A770 Graphics":        "Intel(R) Arc(TM) A770 Graphics", // no "Pro": keep the full name
		"":                                      "",
	} {
		if got := shortModel(name); got != want {
			t.Errorf("shortModel(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestExporterExportsHostBackedMemory(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := New(&spillingCollector{MockCollector: xpu.NewMockCollector()}, time.Second, reg)
	ctx := context.Background()
	e.discover(ctx)
	e.poll(ctx)
	if v := gather(t, reg, "device")["xpu_gpu_memory_host_backed_bytes"]["0"]; v != float64(1900<<20) {
		t.Errorf("host_backed = %v, want %v", v, 1900<<20)
	}
}

type spillingCollector struct{ *xpu.MockCollector }

func (c *spillingCollector) CollectStats(ctx context.Context, ids []int) (map[int]*xpu.GPUStats, error) {
	stats, err := c.MockCollector.CollectStats(ctx, ids)
	if s := stats[0]; s != nil {
		if s.Fdinfo == nil {
			s.Fdinfo = &xpu.FdinfoStats{Available: true}
		}
		s.HostBackedBytes = 1900 << 20
	}
	return stats, err
}
