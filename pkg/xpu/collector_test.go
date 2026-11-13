package xpu

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func readFanRawSyscall(pciBdf string) float64 {
	matches, err := filepath.Glob("/sys/bus/pci/devices/" + pciBdf + "/hwmon/hwmon*/fan1_input")
	if err != nil || len(matches) == 0 {
		return 0
	}

	// Retry on transient zero reads (EAGAIN sampling window)
	for attempt := 0; attempt < 3; attempt++ {
		fd, err := syscall.Open(matches[0], syscall.O_RDONLY, 0)
		if err == nil {
			buf := make([]byte, 32)
			n, rerr := syscall.Read(fd, buf)
			syscall.Close(fd)
			if rerr == nil && n > 0 {
				val := strings.TrimSpace(string(buf[:n]))
				rpm, perr := strconv.ParseFloat(val, 64)
				if perr == nil && rpm > 0 {
					return rpm
				}
			}
		}
		time.Sleep(60 * time.Millisecond)
	}
	return 0
}

func TestRawSyscallFan(t *testing.T) {
	fans, _ := filepath.Glob("/sys/bus/pci/devices/*/hwmon/hwmon*/fan1_input")
	var bdfs []string
	for _, f := range fans {
		bdf := strings.Split(f, "/")[5]
		if v, err := os.ReadFile("/sys/bus/pci/devices/" + bdf + "/vendor"); err == nil && strings.TrimSpace(string(v)) == "0x8086" {
			bdfs = append(bdfs, bdf)
		}
	}
	if len(bdfs) == 0 {
		t.Skip("no Intel GPU fan sensors")
	}
	for _, d := range bdfs {
		t0 := time.Now()
		rpm := readFanRawSyscall(d)
		dt := time.Since(t0)
		t.Logf("%s: %.1f RPM (%v)", d, rpm, dt)
		if dt > time.Second {
			t.Errorf("fan read for %s took %v", d, dt)
		}
	}
}

func TestCollectorStatsFanAvailable(t *testing.T) {
	cli := NewLiveCollector()
	t.Cleanup(cli.Close)
	devices, err := cli.Discover(context.Background())
	if err != nil {
		t.Skipf("no Level Zero devices: %v", err)
	}
	ids := make([]int, len(devices))
	for i, d := range devices {
		ids[i] = d.DeviceID
	}
	stats, err := cli.CollectStats(context.Background(), ids)
	if err != nil {
		t.Fatalf("CollectStats failed: %v", err)
	}

	for id, s := range stats {
		t.Logf("GPU %d: FanAvailable=%v, FanRPM=%.1f", id, s.FanAvailable, s.FanSpeedRPM)
		if s.FanSpeedRPM < 0 {
			t.Errorf("Expected FanSpeedRPM >= 0 for GPU %d, got %.1f", id, s.FanSpeedRPM)
		}
	}
}

func TestMedianOf(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"empty", nil, 0},
		{"single", []float64{3300}, 3300},
		{"odd", []float64{5000, 3300, 3284}, 3300},
		{"even averages the middle pair", []float64{3284, 3300, 3316, 5000}, 3308},
		{"already sorted input is not disturbed", []float64{1, 2, 3, 4, 5}, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := make([]float64, len(tc.in))
			copy(in, tc.in)

			got := medianOf(in)
			if got != tc.want {
				t.Errorf("medianOf(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range in {
				if in[i] != tc.in[i] {
					t.Fatalf("medianOf mutated the caller's slice: %v became %v", tc.in, in)
				}
			}
		})
	}
}

// TestFanMedianRejectsTachometerOutliers replays a capture of the real B70 tachometer
// noise: a fan physically holding ~3300 RPM, with the quantisation spikes the sensor
// intersperses (3750 / 4000 / 4286 / 5000). None of those are fan movement, so none of
// them should reach the display.
func TestFanMedianRejectsTachometerOutliers(t *testing.T) {
	const bdf = "0000:19:00.0"
	samples := []float64{
		3300, 3284, 3300, 3300, 3284, 5000, 3300, 3300, 4000, 3300,
		3284, 4286, 3300, 3750, 3300, 3300, 3317, 5000, 3300, 3284,
	}

	c := NewLiveCollector()
	for i, raw := range samples {
		filtered := c.recordFanSample(bdf, raw)
		if i+1 < fanSampleWindow {
			continue // still filling the window
		}
		if filtered < 3200 || filtered > 3400 {
			t.Errorf("sample %d (raw %.0f): filtered speed %.0f escaped the true ~3300 RPM band", i, raw, filtered)
		}
	}

	if got := len(c.fanSamples[bdf]); got != fanSampleWindow {
		t.Errorf("window grew unbounded: holding %d samples, want %d", got, fanSampleWindow)
	}
}

// TestFanMedianTracksRealRamp guards against over-filtering: a median that rejects the
// spikes is only useful if it still follows the fan when it genuinely spins up.
func TestFanMedianTracksRealRamp(t *testing.T) {
	const bdf = "0000:19:00.0"
	c := NewLiveCollector()

	for i := 0; i < 10; i++ {
		c.recordFanSample(bdf, 1200)
	}
	if got := c.recordFanSample(bdf, 1200); got != 1200 {
		t.Fatalf("steady state should report the steady speed, got %.0f", got)
	}

	var filtered float64
	for i := 0; i < fanSampleWindow; i++ {
		filtered = c.recordFanSample(bdf, 3300)
	}
	if filtered != 3300 {
		t.Errorf("after %d samples at the new speed the filter reported %.0f, want 3300", fanSampleWindow, filtered)
	}
}

// TestFanSamplesAreIsolatedPerCard guards against one card's readings leaking into the
// other's filter window.
func TestFanSamplesAreIsolatedPerCard(t *testing.T) {
	c := NewLiveCollector()
	for i := 0; i < fanSampleWindow; i++ {
		c.recordFanSample("0000:19:00.0", 3300)
		c.recordFanSample("0000:67:00.0", 1200)
	}

	if got := c.recordFanSample("0000:19:00.0", 3300); got != 3300 {
		t.Errorf("card 19:00.0 reported %.0f, want 3300", got)
	}
	if got := c.recordFanSample("0000:67:00.0", 1200); got != 1200 {
		t.Errorf("card 67:00.0 reported %.0f, want 1200", got)
	}
}

// On i915 (Arc Pro A40) xpu-smi reports N/A temperatures and 0.0 for memory
// used and traffic. Those must come out as "fall back to hwmon" and "not
// measured", not as an idle, empty, cold card.
func TestApplySysfsFallbacksI915(t *testing.T) {
	stats := &GPUStats{
		Sysfs: &SysfsStats{
			Available:   true,
			Driver:      "i915",
			TempByLabel: map[string]TempSensor{"pkg": {Label: "pkg", ValueC: 62}},
		},
	}
	applySysfsFallbacks(stats)
	if stats.CoreTempC != 62 {
		t.Errorf("core temp = %v, want 62 from hwmon", stats.CoreTempC)
	}
	if !stats.MemoryUsedUnavailable {
		t.Error("memory used not flagged unavailable")
	}
	if !stats.TrafficUnavailable {
		t.Error("traffic not flagged unavailable")
	}
}

// The same zeros from an xe card are a real reading, and a real xpu-smi
// temperature is never overwritten.
func TestApplySysfsFallbacksLeavesXeAlone(t *testing.T) {
	stats := &GPUStats{
		CoreTempC: 41,
		Sysfs: &SysfsStats{
			Available:   true,
			Driver:      "xe",
			TempByLabel: map[string]TempSensor{"pkg": {Label: "pkg", ValueC: 99}},
		},
	}
	applySysfsFallbacks(stats)
	if stats.CoreTempC != 41 {
		t.Errorf("core temp = %v, want xpu-smi's 41 kept", stats.CoreTempC)
	}
	if stats.MemoryUsedUnavailable || stats.TrafficUnavailable {
		t.Errorf("xe card flagged unavailable: %+v", stats)
	}
}

// If i915 ever does report memory, believe it.
func TestApplySysfsFallbacksTrustsRealI915Memory(t *testing.T) {
	stats := &GPUStats{
		MemoryUsedMiB: 1024, PcieReadKBps: 5,
		Sysfs: &SysfsStats{Available: true, Driver: "i915", TempByLabel: map[string]TempSensor{}},
	}
	applySysfsFallbacks(stats)
	if stats.MemoryUsedUnavailable || stats.TrafficUnavailable {
		t.Errorf("real i915 values flagged unavailable: %+v", stats)
	}
}

func i915Procs(resident ...int64) *FdinfoStats {
	fd := &FdinfoStats{Available: true}
	for i, r := range resident {
		fd.Processes = append(fd.Processes, DrmProcess{PID: i + 1, Memory: ProcessGPUMemory{
			VramBytes: r * 2, ResidentVramBytes: r, // total is deliberately not what is summed
		}})
	}
	return fd
}

// With xpu-smi giving nothing for an i915 card, used VRAM is the sum of the
// processes' resident local memory, and is marked as an estimate.
func TestFdinfoFallbackEstimatesMemoryUsed(t *testing.T) {
	stats := &GPUStats{MemoryUsedUnavailable: true, Fdinfo: i915Procs(512<<20, 183<<20)}
	applyFdinfoFallbacks(stats)
	if stats.MemoryUsedMiB != 695 {
		t.Errorf("used = %v MiB, want 695", stats.MemoryUsedMiB)
	}
	if !stats.MemoryUsedEstimated || stats.MemoryUsedUnavailable {
		t.Errorf("flags = estimated %v unavailable %v, want true/false",
			stats.MemoryUsedEstimated, stats.MemoryUsedUnavailable)
	}
}

// A real reading (xpu-smi as root reports one for the A40) is never replaced.
func TestFdinfoFallbackLeavesARealReadingAlone(t *testing.T) {
	stats := &GPUStats{MemoryUsedMiB: 695.6, Fdinfo: i915Procs(999 << 20)}
	applyFdinfoFallbacks(stats)
	if stats.MemoryUsedMiB != 695.6 || stats.MemoryUsedEstimated {
		t.Errorf("real reading overwritten: %+v", stats)
	}
}

// Nothing visible to sum is not "zero used".
func TestFdinfoFallbackNeedsSomethingToSum(t *testing.T) {
	for name, fd := range map[string]*FdinfoStats{"nil": nil, "no processes": {Available: true}} {
		stats := &GPUStats{MemoryUsedUnavailable: true, Fdinfo: fd}
		applyFdinfoFallbacks(stats)
		if !stats.MemoryUsedUnavailable || stats.MemoryUsedEstimated {
			t.Errorf("%s: became %+v, want still unavailable", name, stats)
		}
	}
}
