// Package exporter serves the same telemetry pkg/ui renders, in Prometheus's
// text exposition format, for a standalone always-on binary (cmd/xpu-exporter)
// rather than the interactive TUI.
//
// It deliberately covers only what the Overview table shows -- one row per
// card. Per-workload metrics (PID, model, port) were left out: Prometheus
// series are meant to be long-lived, and a label built from a PID or a model
// name churns constantly as processes come and go, which is exactly the
// "unbounded label cardinality" mistake Prometheus's own docs warn against.
// That data stays where it already works well -- interactively, in the TUI.
package exporter

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/travlytle/xpu-smi-tui/pkg/xpu"
)

// Exporter polls a Collector on its own schedule and keeps a set of
// Prometheus gauges current. Scrapes never touch the collector directly --
// they read whatever the last poll set, which keeps a slow or stuck
// xpu-smi call (see pkg/xpu's own collector timing notes) from ever making
// an HTTP scrape hang.
type Exporter struct {
	col      xpu.Collector
	interval time.Duration

	mu      sync.Mutex
	devices []xpu.DeviceInfo

	info        *prometheus.GaugeVec
	up          *prometheus.GaugeVec
	idle        *prometheus.GaugeVec
	busy        *prometheus.GaugeVec
	computeBusy *prometheus.GaugeVec
	power       *prometheus.GaugeVec
	powerCap    *prometheus.GaugeVec
	memUsed     *prometheus.GaugeVec
	memHost     *prometheus.GaugeVec
	memEstimate *prometheus.GaugeVec
	memTotal    *prometheus.GaugeVec
	temp        *prometheus.GaugeVec
	tempCrit    *prometheus.GaugeVec
	fanRPM      *prometheus.GaugeVec
	pcie        *prometheus.GaugeVec
	clock       *prometheus.GaugeVec
	clockReq    *prometheus.GaugeVec
	clockMax    *prometheus.GaugeVec
	bandwidth   *prometheus.GaugeVec
	pcieWidth   *prometheus.GaugeVec
	pcieSpeed   *prometheus.GaugeVec
	throttled   *prometheus.GaugeVec
}

// New builds an Exporter and registers its metrics with reg.
func New(col xpu.Collector, interval time.Duration, reg prometheus.Registerer) *Exporter {
	const ns = "xpu"
	labels := []string{"device"}

	e := &Exporter{
		col:      col,
		interval: interval,

		info: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_info",
			Help: "Always 1; identity as labels (name, model, pci_bdf, uuid, driver_version).",
		}, []string{"device", "name", "model", "pci_bdf", "uuid", "driver_version"}),

		up: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_up",
			Help: "1 if the last poll returned fresh data for this card, 0 otherwise.",
		}, labels),

		idle: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_idle",
			Help: "1 while the card is being left alone so it can runtime-suspend and save power: its other series then hold the last sample rather than updating, and are refreshed every --idle-poll seconds.",
		}, labels),

		busy: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_busy_ratio",
			Help: "Fraction of the last interval the graphics tile was awake (0-1). This is power-state residency, not compute activity -- see gpu_compute_busy_ratio.",
		}, labels),

		computeBusy: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_compute_busy_ratio",
			Help: "Fraction of the last interval the compute engine (ccs) was actually executing (0-1), from per-process fdinfo cycle counters.",
		}, labels),

		power: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_power_watts",
			Help: "Current power draw.",
		}, labels),

		powerCap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_power_limit_watts",
			Help: "Enforced power limit as the hardware reports it.",
		}, labels),

		memUsed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_memory_used_bytes",
			Help: "VRAM in use.",
		}, labels),

		memHost: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_memory_host_backed_bytes",
			Help: "GPU memory the driver holds in host RAM for this card (GTT total across its clients): what it evicted because VRAM is full. The early warning for oversubscription -- it was 1.9 GiB for an hour before the xe driver crashed on the eviction path.",
		}, labels),

		memEstimate: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_memory_used_estimated",
			Help: "1 when gpu_memory_used_bytes is summed from per-process usage (the driver reports none) rather than read from the driver.",
		}, labels),

		memTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_memory_total_bytes",
			Help: "VRAM installed on the card.",
		}, labels),

		temp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_temperature_celsius",
			Help: "Temperature by sensor: core (die), memory, or vr (voltage regulator; no published limit), plus whatever else the xe driver publishes for this card -- mctrl, pcie, and vram_ch_0..15 (one per VRAM channel).",
		}, []string{"device", "sensor"}),

		tempCrit: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_temperature_critical_celsius",
			Help: "That sensor's own critical threshold, when known, so a dashboard can alert against the real limit instead of a guessed constant.",
		}, []string{"device", "sensor"}),

		fanRPM: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_fan_rpm",
			Help: "Fan speed, median-filtered.",
		}, labels),

		pcie: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_pcie_bytes_per_second",
			Help: "Host PCIe throughput by direction (rx: card reading host memory, tx: card writing it).",
		}, []string{"device", "direction"}),

		clock: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_clock_mhz",
			Help: "The clock the render tile is actually running at; 0 while it is parked in c6.",
		}, labels),

		clockReq: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_clock_requested_mhz",
			Help: "The clock the driver has requested. Latches at the maximum after a job while the tile is parked, so it is not what the card is doing; see gpu_clock_mhz.",
		}, labels),

		clockMax: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_clock_max_mhz",
			Help: "Hardware maximum core clock.",
		}, labels),

		bandwidth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_memory_bandwidth_ratio",
			Help: "Memory bandwidth as a fraction of peak (0-1).",
		}, labels),

		pcieWidth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_pcie_link_width",
			Help: "Negotiated host PCIe link width in lanes, current and max (kind label). Resolved to the first ancestor bridge reporting more than one lane -- the B70's on-card switch makes the GPU function itself report an internal x1 link, which is not the host link.",
		}, []string{"device", "kind"}),

		pcieSpeed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_pcie_link_speed_gtps",
			Help: "Negotiated host PCIe link speed in GT/s, current and max (kind label). See gpu_pcie_link_width for why this is resolved up from the GPU's own (misleading) local value.",
		}, []string{"device", "kind"}),

		throttled: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "gpu_throttled",
			Help: "1 if this GT is currently throttled for the given reason (pl1, pl2, pl4, thermal, vr_thermalert, vr_tdc, prochot, ratl), 0 otherwise. vr_thermalert/vr_tdc are the only host-visible signal that the voltage regulator -- the hottest component on these cards -- is in trouble, since it has no temperature sensor of its own.",
		}, []string{"device", "gt", "reason"}),
	}

	reg.MustRegister(
		e.info, e.up, e.idle, e.busy, e.computeBusy, e.power, e.powerCap,
		e.memUsed, e.memHost, e.memEstimate, e.memTotal, e.temp, e.tempCrit, e.fanRPM, e.pcie,
		e.clock, e.clockReq, e.clockMax, e.bandwidth, e.pcieWidth, e.pcieSpeed, e.throttled,
	)
	return e
}

// Run polls until ctx is cancelled. Discovery is retried on its own schedule
// so a card that is not present (or not yet powered up) at startup is picked
// up later, and re-run periodically so a card added afterward is too --
// the same reasoning the TUI's own re-discovery follows.
func (e *Exporter) Run(ctx context.Context) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	e.discover(ctx)
	lastDiscover := time.Now()

	for {
		e.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if now.Sub(lastDiscover) > time.Minute {
				e.discover(ctx)
				lastDiscover = now
			}
		}
	}
}

func (e *Exporter) discover(ctx context.Context) {
	devices, err := e.col.Discover(ctx)
	if err != nil {
		log.Printf("discover: %v", err)
		return
	}
	e.mu.Lock()
	e.devices = devices
	e.mu.Unlock()
	for _, d := range devices {
		e.info.WithLabelValues(deviceLabel(d.GPUNumber()), d.DeviceName, shortModel(d.DeviceName), d.PciBdf, d.UUID, d.DriverVersion).Set(1)
	}
}

// shortModel pulls the marketing model number out of a full device name --
// "Intel(R) Arc(TM) Pro B70 Graphics" -> "B70" -- for a dashboard legend
// that needs to tell cards apart at a glance without the full string. Falls
// back to the full name for anything that doesn't match the expected
// "... Pro <model> Graphics" shape, so an unrecognized card still gets a
// label rather than an empty one.
func shortModel(name string) string {
	const prefix, suffix = "Pro ", " Graphics"
	i := strings.Index(name, prefix)
	j := strings.LastIndex(name, suffix)
	if i < 0 || j < 0 || j <= i+len(prefix) {
		return name
	}
	return name[i+len(prefix) : j]
}

func (e *Exporter) poll(ctx context.Context) {
	e.mu.Lock()
	devices := e.devices
	e.mu.Unlock()
	if len(devices) == 0 {
		return
	}
	ids := make([]int, len(devices))
	byID := make(map[int]xpu.DeviceInfo, len(devices))
	for i, d := range devices {
		ids[i] = d.DeviceID
		byID[d.DeviceID] = d
	}

	// CollectStats reads sysfs and /proc directly in places, and a call that
	// blocks there ignores its context entirely -- ctx.Done() alone would not
	// return control here (see pkg/ui/model.go's fetchTelemetryCmd, which hit
	// exactly this and is why that path no longer waits on the call directly).
	// Run's loop calls poll synchronously, so without this a single stuck
	// collection would freeze every future poll behind it -- the exporter
	// would keep serving whatever the gauges last held, forever, with nothing
	// to say it had stopped updating.
	pollCtx, cancel := context.WithTimeout(ctx, e.interval*4)
	defer cancel()
	type result struct {
		stats map[int]*xpu.GPUStats
		err   error
	}
	done := make(chan result, 1)
	go func() {
		stats, err := e.col.CollectStats(pollCtx, ids)
		done <- result{stats, err}
	}()

	var stats map[int]*xpu.GPUStats
	select {
	case r := <-done:
		stats, _ = r.stats, r.err
		if r.err != nil && len(r.stats) == 0 {
			log.Printf("poll: %v", r.err)
		}
	case <-pollCtx.Done():
		log.Printf("poll: collection still running after %s", e.interval*4)
		for _, id := range ids {
			e.up.WithLabelValues(deviceLabel(byID[id].GPUNumber())).Set(0)
		}
		return
	}

	for _, id := range ids {
		dev := deviceLabel(byID[id].GPUNumber())
		s, ok := stats[id]
		if !ok || s == nil {
			e.up.WithLabelValues(dev).Set(0)
			continue
		}
		e.up.WithLabelValues(dev).Set(1)
		// A card being left alone to suspend is not updated: reading it would
		// wake it. Its gauges keep the last sample (Prometheus sees a step, not a
		// gap), and this one says why.
		if s.Idle {
			e.idle.WithLabelValues(dev).Set(1)
			continue
		}
		e.idle.WithLabelValues(dev).Set(0)
		// A series is only written when there is a measurement behind it. An
		// unknown value used to be written as 0, which Grafana draws as a
		// healthy idle line -- indistinguishable from the real thing.
		if busy, ok := busyRatio(s); ok {
			e.busy.WithLabelValues(dev).Set(busy)
		}
		if s.Fdinfo != nil {
			if ccs, ok := s.Fdinfo.Engine("ccs"); ok && ccs.Valid {
				e.computeBusy.WithLabelValues(dev).Set(ccs.BusyPercent / 100)
			}
		}
		e.power.WithLabelValues(dev).Set(s.PowerW)
		if cap, ok := powerCap(s, byID[id]); ok {
			e.powerCap.WithLabelValues(dev).Set(cap)
		}
		if !s.MemoryUsedUnavailable {
			e.memUsed.WithLabelValues(dev).Set(s.MemoryUsedMiB * 1024 * 1024)
			estimated := 0.0
			if s.MemoryUsedEstimated {
				estimated = 1
			}
			e.memEstimate.WithLabelValues(dev).Set(estimated)
		}
		if s.Fdinfo != nil {
			e.memHost.WithLabelValues(dev).Set(float64(s.HostBackedBytes))
		}
		if total, ok := memTotal(byID[id]); ok {
			e.memTotal.WithLabelValues(dev).Set(total * 1024 * 1024)
		}
		if s.CoreTempC > 0 {
			e.temp.WithLabelValues(dev, "core").Set(s.CoreTempC)
		}
		if s.MemoryTempC > 0 {
			e.temp.WithLabelValues(dev, "memory").Set(s.MemoryTempC)
		}
		if s.VrTempC > 0 {
			e.temp.WithLabelValues(dev, "vr").Set(s.VrTempC)
		}
		// sysfs's own labels are "pkg"/"vram" (the raw hwmon sensor names);
		// the exported metric uses "core"/"memory" to match xpu_gpu_temperature_celsius.
		if crit, ok := tempCrit(s, "pkg"); ok {
			e.tempCrit.WithLabelValues(dev, "core").Set(crit)
		}
		if crit, ok := tempCrit(s, "vram"); ok {
			e.tempCrit.WithLabelValues(dev, "memory").Set(crit)
		}
		// Every other sysfs sensor beyond pkg/vram (already reported above,
		// under the friendlier core/memory names): mctrl, pcie, and the 16
		// vram_ch_N per-channel VRAM sensors the xe driver publishes. Same
		// metric and label, so an existing dashboard using {{sensor}} in its
		// legend picks these up with no query change.
		if s.Sysfs != nil && s.Sysfs.Available {
			for _, t := range s.Sysfs.Temps {
				if t.Label == "pkg" || t.Label == "vram" {
					continue
				}
				if t.ValueC > 0 {
					e.temp.WithLabelValues(dev, t.Label).Set(t.ValueC)
				}
				if t.CritC > 0 {
					e.tempCrit.WithLabelValues(dev, t.Label).Set(t.CritC)
				}
			}
		}
		if s.Sysfs != nil && s.Sysfs.Pcie.Resolved {
			e.pcieWidth.WithLabelValues(dev, "current").Set(float64(s.Sysfs.Pcie.Width))
			e.pcieWidth.WithLabelValues(dev, "max").Set(float64(s.Sysfs.Pcie.MaxWidth))
			e.pcieSpeed.WithLabelValues(dev, "current").Set(s.Sysfs.Pcie.SpeedGTs)
			e.pcieSpeed.WithLabelValues(dev, "max").Set(s.Sysfs.Pcie.MaxSpeedGTs)
		}
		if s.Sysfs != nil {
			for _, gt := range s.Sysfs.GTs {
				e.setThrottleReasons(dev, gt.Name, gt.Throttle)
			}
		}
		if s.FanAvailable {
			e.fanRPM.WithLabelValues(dev).Set(s.FanSpeedRPM)
		}
		if !s.TrafficUnavailable || s.PcieMeasured {
			e.pcie.WithLabelValues(dev, "rx").Set(s.PcieReadKBps * 1024)
			e.pcie.WithLabelValues(dev, "tx").Set(s.PcieWriteKBps * 1024)
		}
		if !s.TrafficUnavailable {
			e.bandwidth.WithLabelValues(dev).Set(s.MemoryBandwidthPercent / 100)
		}
		mhz, _ := s.Clock()
		e.clock.WithLabelValues(dev).Set(mhz)
		e.clockReq.WithLabelValues(dev).Set(s.FrequencyMHz)
		if byID[id].MaxFrequencyMHz > 0 {
			e.clockMax.WithLabelValues(dev).Set(byID[id].MaxFrequencyMHz)
		}
	}
}

// busyRatio mirrors pkg/ui/views.busyValue: the busiest graphics tile's
// idle-residency reading, as a 0-1 fraction rather than a formatted percent.
// ok is false when no tile has a measurement yet (the first poll, or a card
// whose sysfs could not be read), so the caller can leave the series out.
func busyRatio(s *xpu.GPUStats) (ratio float64, ok bool) {
	if s.Sysfs == nil {
		return 0, false
	}
	busiest := 0.0
	for _, gt := range s.Sysfs.GTs {
		if gt.Idle.BusyValid && (!ok || gt.Idle.BusyPercent > busiest) {
			busiest, ok = gt.Idle.BusyPercent, true
		}
	}
	return busiest / 100, ok
}

func deviceLabel(id int) string { return strconv.Itoa(id) }

// setThrottleReasons sets one xpu_gpu_throttled series per known reason for
// one GT, all eight every poll (not just the asserted ones) so a reason
// that clears reads back to a real 0 instead of leaving a stale 1 forever.
func (e *Exporter) setThrottleReasons(dev, gt string, t xpu.ThrottleFlags) {
	set := func(reason string, v bool) {
		f := 0.0
		if v {
			f = 1
		}
		e.throttled.WithLabelValues(dev, gt, reason).Set(f)
	}
	set("pl1", t.PL1)
	set("pl2", t.PL2)
	set("pl4", t.PL4)
	set("thermal", t.Thermal)
	set("vr_thermalert", t.VRThermalert)
	set("vr_tdc", t.VRTdc)
	set("prochot", t.Prochot)
	set("ratl", t.RATL)
}

// powerCap mirrors pkg/ui/views.PowerCap: the sysfs-enforced limit when known,
// falling back to the static spec read at discovery.
func powerCap(s *xpu.GPUStats, info xpu.DeviceInfo) (float64, bool) {
	if s.Sysfs != nil && s.Sysfs.PowerLimits.CapW > 0 {
		return s.Sysfs.PowerLimits.CapW, true
	}
	if info.PowerLimitW > 0 {
		return info.PowerLimitW, true
	}
	return 0, false
}

// memTotal mirrors pkg/ui/views.MemTotalMiB.
func memTotal(info xpu.DeviceInfo) (float64, bool) {
	if info.MemoryTotalMiB > 0 {
		return info.MemoryTotalMiB, true
	}
	return 0, false
}

// tempCrit mirrors the sysfs half of pkg/ui/views.TempLimits: the hardware's
// own critical threshold for the sensor, preferred there over xpu-smi's own
// (looser, inconsistent) thresholds. The exporter has no health-check poll of
// its own to fall back to, so unlike TempLimits this reports unknown rather
// than substituting one. label is the raw sysfs sensor name ("pkg", "vram"),
// not the friendlier name the metric itself is labelled with.
func tempCrit(s *xpu.GPUStats, label string) (float64, bool) {
	if s.Sysfs == nil || !s.Sysfs.Available {
		return 0, false
	}
	sensor, ok := s.Sysfs.Temp(label)
	if !ok || sensor.CritC <= 0 {
		return 0, false
	}
	return sensor.CritC, true
}
