package xpu

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Collector retrieves hardware specifications and real-time stats.
type Collector interface {
	Discover(ctx context.Context) ([]DeviceInfo, error)
	CollectStats(ctx context.Context, deviceIDs []int) (map[int]*GPUStats, error)
	CollectHealth(ctx context.Context) (map[int]DeviceHealth, error)
	CollectTopology(ctx context.Context) (*TopologyData, error)
}

// fanSampleWindow is how many recent genuine tachometer readings are kept per card
// to median-filter the fan speed. Five samples reject the spurious highs described
// on readFanRPM while still following a real fan ramp within a few refreshes.
const fanSampleWindow = 5

// LiveCollector implements Collector against the real hardware: Level Zero
// Sysman for the base telemetry, and sysfs, /proc fdinfo and the CPU's IIO
// counters for everything Sysman does not report. It runs no external program.
type LiveCollector struct {
	// collectMu serialises CollectStats. Every collector that turns a counter
	// into a rate -- energy to watts, idle residency to busy%, engine cycles to
	// busy%, the fan median -- keeps exactly one previous sample and assumes
	// calls do not overlap; two in flight would overwrite each other's baselines
	// and compute every derived rate across the wrong window. The UI also skips
	// a tick while one is outstanding; this guards the callers that do not.
	collectMu sync.Mutex

	fanMu      sync.Mutex
	fanSamples map[string][]float64
	sysfs      *SysfsCollector
	fdinfo     *FdinfoCollector
	iio        *IioCollector

	// The Sysman session is opened once, on first use; srcErr says why not.
	srcOnce sync.Once
	src     telemetrySource
	srcErr  error

	// Idle power: see idlegate.go. idleMu guards the maps below, which
	// Discover and CollectStats both reach.
	now       func() time.Time // time.Now; a field so tests can drive the idle gate
	idle      IdlePolicy
	idleMu    sync.Mutex
	gates     map[string]*idleGate  // by PCI address
	last      map[string]*GPUStats  // each card's last real sample
	infoCache map[string]DeviceInfo // each card's last full discovery

	bdfMu   sync.Mutex
	bdfByID map[int]string // device id -> PCI address, from Discover

	// gpuOrder is the user's chosen PCI-address order, applied to discovery
	// results; see numbering.go.
	gpuOrder []string
}

// NewLiveCollector creates a collector for the machine's real GPUs.
func NewLiveCollector() *LiveCollector {
	return &LiveCollector{
		fanSamples: make(map[string][]float64),
		sysfs:      NewSysfsCollector(""),
		fdinfo:     NewFdinfoCollector(),
		iio:        NewIioCollector(),
		now:        time.Now,
		idle:       DefaultIdlePolicy(),
		gates:      make(map[string]*idleGate),
		last:       make(map[string]*GPUStats),
		infoCache:  make(map[string]DeviceInfo),
	}
}

// SetIdlePolicy chooses how suspended cards are treated. Call it before the
// first collection.
func (c *LiveCollector) SetIdlePolicy(p IdlePolicy) {
	c.idle = p
}

// Close releases the IIO counters. Safe to call more than once.
func (c *LiveCollector) Close() {
	c.iio.Close()
}

// SetGPUOrder sets the PCI-address order GPUs are numbered in; see
// AssignGPUNumbers. Call it before Discover.
func (c *LiveCollector) SetGPUOrder(order []string) {
	c.gpuOrder = order
}

// source opens Sysman on first use.
func (c *LiveCollector) source() (telemetrySource, error) {
	c.srcOnce.Do(func() {
		src, err := newSysmanSource()
		if err != nil {
			c.srcErr = err
			return
		}
		c.src = src
	})
	if c.srcErr != nil {
		return nil, fmt.Errorf("%w (needs libze_loader.so.1 from the Level Zero runtime, and a build with cgo)", c.srcErr)
	}
	return c.src, nil
}

// Discover lists the GPUs, from Sysman and sysfs.
func (c *LiveCollector) Discover(ctx context.Context) ([]DeviceInfo, error) {
	defer phase("Discover")()
	src, err := c.source()
	if err != nil {
		return nil, err
	}
	// A card being left alone to suspend is not touched by discovery either:
	// the exporter rediscovers every minute, which would otherwise wake it.
	hold := func(bdf string) bool {
		if !c.idle.Enabled {
			return false
		}
		pm := c.sysfs.RuntimePMFor(bdf)
		c.idleMu.Lock()
		defer c.idleMu.Unlock()
		g := c.gates[bdf]
		return pm.Suspended() || (g != nil && g.quiet())
	}
	c.idleMu.Lock()
	cache := make(map[string]DeviceInfo, len(c.infoCache))
	for k, v := range c.infoCache {
		cache[k] = v
	}
	c.idleMu.Unlock()
	devices := src.discover(c.sysfs, hold, cache)
	c.idleMu.Lock()
	for _, d := range devices {
		c.infoCache[d.PciBdf] = d
	}
	c.idleMu.Unlock()

	c.bdfMu.Lock()
	c.bdfByID = make(map[int]string, len(devices))
	for _, d := range devices {
		c.bdfByID[d.DeviceID] = d.PciBdf
	}
	c.bdfMu.Unlock()
	return AssignGPUNumbers(devices, c.gpuOrder), nil
}

// CollectHealth returns nothing: temperature limits come from each card's own
// hwmon critical and emergency thresholds, which TempLimits prefers anyway.
func (c *LiveCollector) CollectHealth(ctx context.Context) (map[int]DeviceHealth, error) {
	return map[int]DeviceHealth{}, nil
}

// CollectTopology returns nothing: the one thing read from it, CPU affinity,
// is DeviceInfo.CPUAffinity.
func (c *LiveCollector) CollectTopology(ctx context.Context) (*TopologyData, error) {
	return nil, nil
}

// CollectStats reads every requested card once.
func (c *LiveCollector) CollectStats(ctx context.Context, deviceIDs []int) (map[int]*GPUStats, error) {
	// Refuse rather than queue: a collection still running from an earlier
	// tick means something below is blocked, and lining up behind it would
	// turn one stuck read into a pile of goroutines that all wake at once.
	if !c.collectMu.TryLock() {
		return nil, errors.New("previous collection still running")
	}
	defer c.collectMu.Unlock()
	defer phase("CollectStats total")()

	src, err := c.source()
	if err != nil {
		return nil, err
	}

	now := c.now()
	result := make(map[int]*GPUStats, len(deviceIDs))
	skipNodes := map[string]bool{} // device nodes of cards being left alone
	var errs []error
	for _, id := range deviceIDs {
		done := phase(fmt.Sprintf("device %d sysman", id))
		c.bdfMu.Lock()
		bdf, known := c.bdfByID[id]
		c.bdfMu.Unlock()
		if !known {
			errs = append(errs, fmt.Errorf("device %d: not discovered", id))
			done()
			continue
		}

		// The idle gate: may this tick touch the card at all? Its PM state is
		// the one thing that is free to read.
		if c.idle.Enabled {
			pm := c.sysfs.RuntimePMFor(bdf)
			c.idleMu.Lock()
			g := c.gates[bdf]
			if g == nil {
				g = &idleGate{}
				c.gates[bdf] = g
			}
			last := c.last[bdf]
			touch := g.decide(now, pm, looksIdle(last), c.idle)
			c.idleMu.Unlock()
			if !touch {
				result[id] = heldStats(id, bdf, last, pm, now)
				for _, n := range c.sysfs.DrmNodes(bdf) {
					skipNodes[n] = true
				}
				done()
				continue
			}
		}

		stats, err := src.stats(bdf, id, now)
		done()
		if err != nil {
			// Surfaced, not swallowed: a card that drops out of the result
			// with no error would keep its last numbers stamped as fresh.
			errs = append(errs, fmt.Errorf("device %d: %w", id, err))
			continue
		}
		result[id] = stats
	}
	if len(result) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	var partialErr error
	if len(errs) > 0 {
		partialErr = errors.Join(errs...)
	}

	// Sequentially resolve fan speeds using direct raw syscalls (bypasses Go netpoller sysfs hang)
	for _, stats := range result {
		if stats != nil && !stats.Idle && stats.PciBdf != "" {
			done := phase("fan " + stats.PciBdf)
			rpm, rawRPM, ok := c.readFanRPM(stats.PciBdf)
			done()
			if ok {
				stats.FanAvailable = true
				stats.FanSpeedRPM = rpm
				stats.FanSpeedRPMRaw = rawRPM
			}
		}
	}

	// Read the per-card sysfs detail Sysman does not expose. The whole sweep
	// costs ~5 ms for two cards, so it runs at the full refresh rate -- except
	// when a card is resuming from runtime suspend underneath the read, which
	// is the one thing here with no upper bound; see debug.go.
	for _, stats := range result {
		if stats != nil && !stats.Idle && stats.PciBdf != "" {
			done := phase("sysfs " + stats.PciBdf)
			stats.Sysfs = c.sysfs.Collect(stats.PciBdf)
			done()
			applySysfsFallbacks(stats)
			c.applyPcieFromCounters(stats)
		}
	}

	// One /proc walk covers every card, so it is done once and distributed.
	doneFdinfo := phase("fdinfo /proc walk")
	byBdf, complete := c.fdinfo.CollectExcluding(skipNodes)
	doneFdinfo()
	for _, stats := range result {
		if stats == nil || stats.Idle || stats.PciBdf == "" {
			continue
		}
		stats.ClientsComplete = complete
		if fd, ok := byBdf[stats.PciBdf]; ok {
			stats.Fdinfo = fd
		}
		applyFdinfoFallbacks(stats)
		if stats.Fdinfo != nil {
			stats.HostBackedBytes = 0
			for _, p := range stats.Fdinfo.Processes {
				stats.HostBackedBytes += p.Memory.GttBytes
			}
		}

		// Remember it: the next tick may leave this card alone and show this.
		cp := *stats
		c.idleMu.Lock()
		c.last[stats.PciBdf] = &cp
		c.idleMu.Unlock()
	}

	return result, partialErr
}

// heldStats is what a tick reports for a card it did not touch: the last real
// sample, marked Idle, with only the free PM state refreshed. With no earlier
// sample at all it is just that PM state.
func heldStats(id int, bdf string, last *GPUStats, pm RuntimePM, now time.Time) *GPUStats {
	st := GPUStats{DeviceIndex: id, PciBdf: bdf, Timestamp: now, Tiles: make(map[string]TileDetails)}
	if last != nil {
		st = *last
	}
	st.Idle = true
	sys := SysfsStats{TempByLabel: map[string]TempSensor{}}
	if st.Sysfs != nil {
		sys = *st.Sysfs // a copy: the earlier sample's own must not change
	}
	sys.Available = true
	sys.PM = pm
	st.Sysfs = &sys
	return &st
}

// applyPcieFromCounters replaces a card's PCIe throughput with the CPU's IIO
// counters where they can be read.
//
// Sysman's own PCIe counters are raw link bytes: measured against a copy of a
// known size they read 5% high host->GPU and 16% high GPU->host, plus about
// 1 MB/s while idle, where the IIO counters were within 1%. And the i915 A40
// has no Sysman PCIe counters at all. The IIO counters need CAP_PERFMON or
// root; without them this does nothing, and a card keeps whatever it had --
// Sysman's raw counters, or "--".
func (c *LiveCollector) applyPcieFromCounters(stats *GPUStats) {
	t, ok := c.iio.Collect(stats.PciBdf)
	if !ok {
		return
	}
	stats.PcieReadKBps = t.HostToGPUBytesPerSec / 1024
	stats.PcieWriteKBps = t.GPUToHostBytesPerSec / 1024
	stats.PcieMeasured = true
}

// applySysfsFallbacks fills in what Sysman cannot report for a card, from
// sysfs, and flags what nothing can.
//
// On the i915 driver (Arc Pro A40) there is no temperature sensor Sysman can
// see, and without CAP_PERFMON memory used reads 0.0 and there are no traffic
// counters. Temperature is available from hwmon; card-wide
// VRAM usage is not exposed by i915 at all (the per-process figures are in
// fdinfo), so it is marked unavailable instead of being left as a zero that
// reads as an empty card.
func applySysfsFallbacks(stats *GPUStats) {
	sys := stats.Sysfs
	if sys == nil || !sys.Available {
		return
	}
	if stats.CoreTempC <= 0 {
		if t, ok := sys.Temp("pkg"); ok && t.ValueC > 0 {
			stats.CoreTempC = t.ValueC
		}
	}
	if sys.Driver == "i915" && stats.MemoryUsedMiB <= 0 {
		stats.MemoryUsedUnavailable = true
	}
	// Likewise memory and PCIe throughput: xpu-smi has no such counters for
	// i915, so a card that has never produced one is "not measured", not idle.
	// (The hold logic that runs later only covers a card that *had* data.)
	if sys.Driver == "i915" && stats.MemoryBandwidthPercent == 0 &&
		stats.MemoryReadKBps == 0 && stats.MemoryWriteKBps == 0 &&
		stats.PcieReadKBps == 0 && stats.PcieWriteKBps == 0 {
		stats.TrafficUnavailable = true
	}
}

// applyFdinfoFallbacks estimates card-wide VRAM used from the per-process
// footprints when the driver gave none. It only steps in for a card already
// flagged MemoryUsedUnavailable, so a real figure from xpu-smi (which reports
// one for the A40 when run as root) is always preferred. With no DRM client
// visible there is nothing to sum, and the value stays unknown.
func applyFdinfoFallbacks(stats *GPUStats) {
	if !stats.MemoryUsedUnavailable || stats.Integrated || stats.Fdinfo == nil || len(stats.Fdinfo.Processes) == 0 {
		return
	}
	var resident int64
	for _, p := range stats.Fdinfo.Processes {
		resident += p.Memory.ResidentVramBytes
	}
	stats.MemoryUsedMiB = float64(resident) / (1024 * 1024)
	stats.MemoryUsedUnavailable = false
	stats.MemoryUsedEstimated = true
}

// readFanRPM reads fan speed from Linux sysfs hwmon using direct syscalls and returns
// both a median-filtered RPM and the raw sample it was derived from.
//
// The GPU tachometer is far noisier than the chassis fan sensors on the same box: its
// period counter quantises badly, so a fan physically holding a steady ~3300 RPM reads
// back as a mix of the true speed, occasional zeros, and spurious highs landing on a
// discrete set (3750 / 4000 / 4286 / 5000). Consecutive raw samples reverse direction
// roughly half the time, which a fan with rotational inertia cannot do — the outliers
// are measurement artifacts, not fan movement.
//
// The retry loop discards the zeros. The median over the last fanSampleWindow genuine
// readings discards the highs, which are common enough (~1 sample in 8) to reach the
// display unfiltered but far too sparse to survive a median.
func (c *LiveCollector) readFanRPM(pciBdf string) (filtered float64, raw float64, ok bool) {
	cleanBdf := strings.TrimSpace(pciBdf)
	if cleanBdf == "" {
		return 0, 0, false
	}
	patterns := []string{
		"/sys/bus/pci/devices/" + cleanBdf + "/hwmon/hwmon*/fan1_input",
		"/sys/bus/pci/devices/0000:" + strings.TrimPrefix(cleanBdf, "0000:") + "/hwmon/hwmon*/fan1_input",
	}

	var match string
	for _, pat := range patterns {
		matches, err := filepath.Glob(pat)
		if err == nil && len(matches) > 0 {
			match = matches[0]
			break
		}
	}

	if match == "" {
		return 0, 0, false
	}

	// Try reading via raw syscall (up to 3 attempts; transient zero reads are a
	// known EAGAIN sampling-window artifact on this hwmon, so retry on zero too)
	for attempt := 0; attempt < 3; attempt++ {
		fd, err := syscall.Open(match, syscall.O_RDONLY, 0)
		if err == nil {
			buf := make([]byte, 32)
			n, rerr := syscall.Read(fd, buf)
			syscall.Close(fd)
			if rerr == nil && n > 0 {
				val := strings.TrimSpace(string(buf[:n]))
				rpm, parseErr := strconv.ParseFloat(val, 64)
				if parseErr == nil && rpm > 0 {
					return c.recordFanSample(cleanBdf, rpm), rpm, true
				}
			}
		}
		if attempt < 2 {
			time.Sleep(60 * time.Millisecond)
		}
	}

	// Every attempt landed on a zero or a failed read. Hold the last filtered value
	// rather than rendering a dropout the fan did not actually have.
	c.fanMu.Lock()
	defer c.fanMu.Unlock()
	if samples := c.fanSamples[cleanBdf]; len(samples) > 0 {
		return medianOf(samples), 0, true
	}

	// Nothing read and nothing held: report the fan as unavailable rather than
	// as a stopped fan. Started during a tachometer dropout, this showed 0 RPM
	// with FanAvailable set, which on a hot card reads as a failed fan.
	return 0, 0, false
}

// recordFanSample appends a genuine reading to the card's rolling window and returns
// the median across it.
func (c *LiveCollector) recordFanSample(bdf string, rpm float64) float64 {
	c.fanMu.Lock()
	defer c.fanMu.Unlock()

	samples := append(c.fanSamples[bdf], rpm)
	if len(samples) > fanSampleWindow {
		samples = samples[len(samples)-fanSampleWindow:]
	}
	c.fanSamples[bdf] = samples

	return medianOf(samples)
}

// medianOf returns the median of vals, leaving the caller's slice unmodified.
func medianOf(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)

	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
