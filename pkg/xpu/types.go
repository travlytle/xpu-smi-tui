package xpu

import (
	"fmt"
	"time"
)

// DeviceInfo represents static hardware identity and specifications
type DeviceInfo struct {
	DeviceID        int     `json:"device_id"`
	DeviceName      string  `json:"device_name"`
	DeviceState     string  `json:"device_state"`
	DeviceType      string  `json:"device_type"`
	DrmDevice       string  `json:"drm_device"`
	PciBdf          string  `json:"pci_bdf_address"`
	PciDeviceID     string  `json:"pci_device_id"`
	UUID            string  `json:"uuid"`
	VendorName      string  `json:"vendor_name"`
	DriverVersion   string  `json:"driver_version"`
	VbiosVersion    string  `json:"vbios_version"`
	MemoryTotalMiB  float64 `json:"memory_total_mib"`
	MemoryFreeMiB   float64 `json:"memory_free_mib"`
	PowerLimitW     float64 `json:"power_limit_w"`
	MaxFrequencyMHz float64 `json:"max_frequency_mhz"`
	Integrated      bool    `json:"integrated"`

	// ECC state as the hardware reports it. xpu-smi's *health* check returns
	// "Unknown" for the memory subsystem on these cards, which is not the same
	// as ECC being off -- discovery and --query-gpu both report it plainly.
	EccMode        string `json:"memory_ecc_state"`
	EccCorrected   string
	EccUncorrected string

	// CPUAffinity is the CPU list local to the card ("0-31"), from sysfs. Empty
	// when unknown.
	CPUAffinity string

	// number is the stable number this card is shown as; see numbering.go.
	number   int
	numbered bool
}

// TileMetric holds min/avg/max/current values for a tile
type TileMetric struct {
	Avg     float64 `json:"avg"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Current float64 `json:"current"`
}

// TileDetails aggregates tile-specific readings
type TileDetails struct {
	PowerW          TileMetric
	FrequencyMHz    TileMetric
	MediaFreqMHz    TileMetric
	CoreTempC       TileMetric
	MemoryTempC     TileMetric
	VrTempC         TileMetric
	MemoryUsedMiB   TileMetric
	MemoryUtilPct   TileMetric
	MemoryReadKBps  TileMetric
	MemoryWriteKBps TileMetric
}

// GPUStats represents real-time telemetry snapshot for a GPU
type GPUStats struct {
	DeviceIndex int
	PciBdf      string
	DeviceType  string
	Integrated  bool
	Timestamp   time.Time
	// PowerW is the card's draw averaged over the sample window: one second
	// from the dump stream, ~0.2 s from a direct stats call. The window's own
	// min/avg/max are not kept -- over 0.2 s they were three copies of the
	// same number -- and the Detail view takes them from History instead.
	PowerW float64
	// FrequencyMHz is the *requested* graphics clock, which latches at the
	// maximum after a job while the GT is parked; ActualFrequencyMHz is what it
	// is running at, 0 while parked. Use Clock() for what to show.
	FrequencyMHz       float64
	ActualFrequencyMHz float64
	MediaFrequencyMHz  float64
	CoreTempC          float64
	MemoryTempC        float64
	VrTempC            float64
	MemoryUsedMiB      float64
	MemoryUtilPercent  float64
	// MemoryUsedUnavailable reports that the driver does not expose card-wide
	// VRAM usage, so MemoryUsedMiB and MemoryUtilPercent are not "zero used"
	// but "not measured". The i915 driver (Arc Pro A40) is the case: xpu-smi
	// returns 0.0 for it while a model is resident. Callers must render this
	// as unknown rather than as an empty card.
	MemoryUsedUnavailable bool
	// MemoryUsedEstimated reports that MemoryUsedMiB is the sum of the
	// per-process footprints in fdinfo rather than a figure from the driver,
	// used when xpu-smi returns nothing for an i915 card (it only does as
	// root). It came within about 3% (once low, once high) of xpu-smi's own root-only reading on
	// the A40, but it misses processes this user cannot inspect and memory the
	// driver holds for itself, and can double-count a shared buffer.
	MemoryUsedEstimated    bool
	MemoryReadKBps         float64
	MemoryWriteKBps        float64
	MemoryBandwidthPercent float64

	// HostBackedBytes is GPU memory the driver holds in host RAM for this
	// card -- the GTT total across its clients. A card whose VRAM is full
	// evicts buffers there, and the eviction path is where xe has crashed,
	// so this is the early warning for VRAM oversubscription. Not
	// counted: the ~184 MiB of plain host memory every DRM client carries.
	HostBackedBytes int64

	// Idle reports that the card is being left alone to suspend, so every
	// value here is carried over from the last time it was sampled --
	// Timestamp says when -- and nothing was read from the device this tick.
	// See idlegate.go.
	Idle bool

	// TrafficUnavailable reports that throughput has been missing for longer
	// than it is worth holding, and should be rendered as unknown rather than
	// as a stale number or a misleading zero.
	TrafficUnavailable bool

	// PcieMeasured reports that PcieReadKBps and PcieWriteKBps are a real
	// counter reading -- the CPU's IIO counters (iio.go) or Sysman's -- so a
	// zero is a measured idle rather than a gap, and is never held over from
	// an earlier burst. It is independent of TrafficUnavailable, which an
	// i915 card still has for memory bandwidth.
	PcieMeasured   bool
	PcieReadKBps   float64
	PcieWriteKBps  float64
	FanSpeedRPM    float64
	FanSpeedRPMRaw float64
	FanAvailable   bool
	Tiles          map[string]TileDetails

	// Fdinfo carries per-engine utilization and per-process memory read from
	// /proc/<pid>/fdinfo. It is the only source for these: xpu-smi returns N/A
	// for every per-engine utilization field on these cards. Nil when no DRM
	// client holds the device, in which case there is nothing to attribute.
	Fdinfo *FdinfoStats

	// ClientsComplete reports whether every process on the machine could be
	// inspected when Fdinfo was collected. Without root it is false, and the
	// display server's memory is in use but not attributed to anything.
	ClientsComplete bool

	// Sysfs carries the per-card detail xpu-smi does not report -- all 20
	// temperature sensors, per-GT frequency and throttle state, idle residency,
	// power limits and the real PCIe link. Nil when the card's sysfs nodes could
	// not be read, so callers must check before dereferencing.
	Sysfs *SysfsStats
}

// ProcessInfo represents a running process utilizing an Intel GPU
type ProcessInfo struct {
	PID            int    `json:"process_id"`
	ProcessName    string `json:"process_name"`
	DeviceID       int    `json:"device_id"`
	Engines        int    `json:"engines"`
	SharedMemBytes int64  `json:"shared_mem_size"`
	MemBytes       int64  `json:"mem_size"`
}

// HealthSensor holds status and threshold info
type HealthSensor struct {
	Status            string  `json:"status"`
	ThrottleThreshold float64 `json:"throttle_threshold"`
	ShutdownThreshold float64 `json:"shutdown_threshold"`
	Description       string  `json:"description"`
}

// DeviceHealth represents the health check status for a device
type DeviceHealth struct {
	DeviceID          int          `json:"device_id"`
	CoreTemperature   HealthSensor `json:"core_temperature"`
	MemoryTemperature HealthSensor `json:"memory_temperature"`
	PowerHealth       HealthSensor `json:"power_health"`
	MemoryHealth      HealthSensor `json:"memory_health"`
	FrequencyHealth   HealthSensor `json:"frequency_health"`
}

// TopologyRow holds matrix connectivity for a device
type TopologyRow struct {
	DeviceName  string
	Connections map[string]string
	CPUAffinity string
}

// TopologyData holds system topology matrix information
type TopologyData struct {
	Headers []string
	Rows    []TopologyRow
	Raw     string
}

// GPUCombined brings together specs, live telemetry, health, and process information
type GPUCombined struct {
	Info      DeviceInfo
	Stats     GPUStats
	Health    DeviceHealth
	Processes []ProcessInfo
	History   *MetricHistory
}

// FormatMemoryBytes formats bytes into human-readable GiB/MiB
func FormatMemoryBytes(bytes int64) string {
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
	)
	if bytes >= GiB {
		return fmt.Sprintf("%.2f GiB", float64(bytes)/float64(GiB))
	}
	if bytes >= MiB {
		return fmt.Sprintf("%.1f MiB", float64(bytes)/float64(MiB))
	}
	if bytes >= KiB {
		return fmt.Sprintf("%d KiB", bytes/KiB)
	}
	return fmt.Sprintf("%d B", bytes)
}

// FormatMiB formats MiB values into GiB or MiB string
func FormatMiB(mib float64) string {
	if mib >= 1024 {
		return fmt.Sprintf("%.2f GiB", mib/1024.0)
	}
	return fmt.Sprintf("%.0f MiB", mib)
}
