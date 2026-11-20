package xpu

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"
)

// MockCollector provides simulated telemetry for testing or presentation
type MockCollector struct {
	mu       sync.Mutex
	tick     int64
	rnd      *rand.Rand
	devices  []DeviceInfo
	topology *TopologyData
}

// NewMockCollector creates a mock collector initialized with sample Intel Arc GPUs
func NewMockCollector() *MockCollector {
	src := rand.NewSource(time.Now().UnixNano())
	r := rand.New(src)

	devices := []DeviceInfo{
		{
			DeviceID:        0,
			DeviceName:      "Intel(R) Arc(TM) Pro B70 Graphics",
			DeviceState:     "normal",
			DeviceType:      "GPU",
			DrmDevice:       "/dev/dri/card1",
			PciBdf:          "0000:19:00.0",
			PciDeviceID:     "0xe223",
			UUID:            "00000000-0000-0019-0000-0000e2238086",
			VendorName:      "Intel(R) Corporation",
			DriverVersion:   "0440644969978EC8EB1C8B2",
			VbiosVersion:    "BMG_ _31.1062",
			OptionROM:       "OPROM CODE VERSION:2304240000_OPROM DATA VERSION:2304140000",
			EccSupported:    "yes",
			BARBytes:        32 << 30,
			MemoryType:      "GDDR6",
			MemoryBusWidth:  256,
			MemoryChannels:  8,
			MemoryTotalMiB:  28568,
			MemoryFreeMiB:   5520,
			PowerLimitW:     275,
			MaxFrequencyMHz: 2800,
		},
		{
			DeviceID:        1,
			DeviceName:      "Intel(R) Arc(TM) Pro B70 Graphics",
			DeviceState:     "normal",
			DeviceType:      "GPU",
			DrmDevice:       "/dev/dri/card0",
			PciBdf:          "0000:67:00.0",
			PciDeviceID:     "0xe223",
			UUID:            "00000000-0000-0067-0000-0000e2238086",
			VendorName:      "Intel(R) Corporation",
			DriverVersion:   "0440644969978EC8EB1C8B2",
			VbiosVersion:    "BMG_ _31.1062",
			OptionROM:       "OPROM CODE VERSION:2304240000_OPROM DATA VERSION:2304140000",
			EccSupported:    "yes",
			BARBytes:        32 << 30,
			MemoryType:      "GDDR6",
			MemoryBusWidth:  256,
			MemoryChannels:  8,
			MemoryTotalMiB:  28568,
			MemoryFreeMiB:   8285,
			PowerLimitW:     275,
			MaxFrequencyMHz: 2800,
		},
	}

	topo := &TopologyData{
		Headers: []string{"GPU 0/0", "GPU 1/0", "NIC0", "CPU Affinity"},
		Rows: []TopologyRow{
			{
				DeviceName: "GPU 0/0",
				Connections: map[string]string{
					"GPU 0/0": "S",
					"GPU 1/0": "SYS",
					"NIC0":    "SYS",
				},
				CPUAffinity: "0-31",
			},
			{
				DeviceName: "GPU 1/0",
				Connections: map[string]string{
					"GPU 0/0": "SYS",
					"GPU 1/0": "S",
					"NIC0":    "SYS",
				},
				CPUAffinity: "0-31",
			},
			{
				DeviceName: "NIC0",
				Connections: map[string]string{
					"GPU 0/0": "SYS",
					"GPU 1/0": "SYS",
					"NIC0":    "S",
				},
				CPUAffinity: "0-31",
			},
		},
	}

	return &MockCollector{
		devices:  devices,
		topology: topo,
		rnd:      r,
	}
}

func (m *MockCollector) Discover(ctx context.Context) ([]DeviceInfo, error) {
	return m.devices, nil
}

func (m *MockCollector) CollectStats(ctx context.Context, deviceIDs []int) (map[int]*GPUStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tick++
	t := float64(m.tick) * 0.2
	result := make(map[int]*GPUStats)

	for _, devID := range deviceIDs {
		// Each simulated card runs its own load, so a many-card demo is not six
		// copies of one: GPU 0 heavy, GPU 1 medium, then a spread down to idle.
		p := demoProfiles[devID%len(demoProfiles)]
		basePower, baseTemp, baseMemTemp, baseUtil, baseFreq := p.power, p.temp, p.memTemp, p.util, p.freq

		jitter := (m.rnd.Float64() - 0.5) * 6.0
		wave := math.Sin(t+float64(devID)) * 10.0

		power := math.Max(30, math.Min(275, basePower+wave+jitter))
		coreTemp := math.Max(40, baseTemp+math.Sin(t*0.5)*3.0+(m.rnd.Float64()-0.5)*2.0)
		memTemp := math.Max(45, baseMemTemp+math.Sin(t*0.3)*2.0+(m.rnd.Float64()-0.5)*1.5)
		memUtil := math.Max(20, math.Min(98, baseUtil+(m.rnd.Float64()-0.5)*3.0))
		memUsed := 28568.0 * (memUtil / 100.0)
		freq := math.Round(math.Max(300, math.Min(2800, baseFreq+wave*12+jitter*10))/50) * 50

		stats := &GPUStats{
			DeviceIndex:            devID,
			PciBdf:                 m.devices[devID].PciBdf,
			DeviceType:             "discrete",
			Timestamp:              time.Now(),
			PowerW:                 power,
			FrequencyMHz:           freq,
			MediaFrequencyMHz:      400.0,
			CoreTempC:              coreTemp,
			MemoryTempC:            memTemp,
			VrTempC:                coreTemp + 22.0,
			MemoryUsedMiB:          memUsed,
			MemoryUtilPercent:      memUtil,
			MemoryReadKBps:         math.Abs(math.Sin(t)) * 1250000,
			MemoryWriteKBps:        math.Abs(math.Cos(t)) * 850000,
			MemoryBandwidthPercent: math.Abs(math.Sin(t+float64(devID))) * p.util * 0.6,
			PcieReadKBps:           math.Abs(math.Sin(t*1.5)) * 340000,
			PcieWriteKBps:          math.Abs(math.Cos(t*1.5)) * 120000,
			FanSpeedRPM:            p.fan + math.Sin(t+float64(devID))*40.0 + (m.rnd.Float64()-0.5)*15.0,
			Tiles: map[string]TileDetails{
				"tile_0": {
					PowerW:       TileMetric{Current: power, Avg: power * 0.98, Max: 275},
					FrequencyMHz: TileMetric{Current: freq, Avg: freq, Max: 2800},
					CoreTempC:    TileMetric{Current: coreTemp, Avg: coreTemp, Max: 85},
					MemoryTempC:  TileMetric{Current: memTemp, Avg: memTemp, Max: 95},
				},
			},
		}
		stats.FanAvailable = true
		stats.Sysfs = m.mockSysfs(devID, t, power, coreTemp, memTemp, memUtil)
		stats.Sysfs.GTs[0].Freq.ActMHz, stats.Sysfs.GTs[0].Freq.CurMHz = freq, freq
		stats.Fdinfo = m.mockFdinfo(devID, t, memUtil, memUsed)
		stats.ClientsComplete = false
		result[devID] = stats
	}

	return result, nil
}

// mockSysfs synthesizes the per-card sysfs detail so demo mode exercises the
// same views as real hardware -- the channel heat map, throttle badges and
// per-GT busy figures would otherwise be blank under --demo.
type demoProfile struct{ power, temp, memTemp, util, freq, fan float64 }

var demoProfiles = []demoProfile{
	{240, 70, 85, 83, 2800, 1050}, // heavy
	{58, 59, 64, 74, 1200, 1050},  // medium
	{165, 66, 78, 55, 2400, 980},
	{31, 46, 52, 22, 400, 720}, // near idle
	{205, 68, 82, 91, 2600, 1180},
	{96, 62, 70, 41, 1800, 900},
}

func (m *MockCollector) mockSysfs(devID int, t, power, coreTemp, memTemp, util float64) *SysfsStats {
	st := &SysfsStats{
		Available:   true,
		TempByLabel: map[string]TempSensor{},
		PowerLimits: PowerLimits{CapW: 275, CritW: 550, CapInterval: 15},
		Energy: EnergyDomains{
			CardJ: float64(m.tick) * power * 0.2,
			PkgJ:  float64(m.tick) * power * 0.12,
		},
		CardPower: DerivedPower{Watts: power, WindowS: 1, Valid: true},
		PkgPower:  DerivedPower{Watts: power * 0.62, WindowS: 1, Valid: true},
		Pcie: PcieLink{
			SpeedGTs: 8.0, Width: 16, MaxSpeedGTs: 16.0, MaxWidth: 16,
			SourceBDF: "0000:17:00.0", LocalSpeed: 2.5, LocalWidth: 1, Resolved: true,
		},
		PM: RuntimePM{Status: "active", ActiveMs: uint64(m.tick) * 1000, AutosuspendDelayMs: 1000},
	}

	add := func(idx int, label string, value, crit, emerg, max float64) {
		s := TempSensor{Index: idx, Label: label, ValueC: value, CritC: crit, EmergencyC: emerg, MaxC: max}
		st.Temps = append(st.Temps, s)
		st.TempByLabel[label] = s
	}
	if devID == 0 {
		st.Displays = []string{"DP-1"}
	}
	add(2, "pkg", coreTemp, 100, 125, 60)
	add(3, "vram", memTemp, 105, 120, 0)
	add(4, "mctrl", coreTemp-15, 100, 125, 0)
	add(5, "pcie", coreTemp-1, 100, 125, 0)
	// Give the channels an uneven spread, which is the thing the heat map exists
	// to show; the aggregate equals the hottest channel as it does on real cards.
	for ch := 0; ch < 16; ch++ {
		offset := math.Sin(t*0.4+float64(ch)*0.7+float64(devID)) * 4.0
		add(6+ch, fmt.Sprintf("vram_ch_%d", ch), memTemp-4+offset, 105, 120, 0)
	}

	busy := math.Max(0, math.Min(100, util+math.Sin(t*0.6+float64(devID))*12))
	st.GTs = []GTStats{
		{
			Name: "gt0",
			Freq: GTFreq{CurMHz: 2400, ActMHz: 2350, MinMHz: 1200, MaxMHz: 2800,
				RpnMHz: 400, Rp0MHz: 2800, RpeMHz: 400, RpaMHz: 2600, Profile: "base"},
			Throttle: ThrottleFlags{Status: power > 260, PL2: power > 260, Reasons: "pl2"},
			Idle:     GTIdle{Status: "gt-c0", Name: "gt0-rc", BusyPercent: busy, BusyValid: true},
		},
		{
			Name: "gt1",
			Freq: GTFreq{CurMHz: 400, ActMHz: 0, MinMHz: 400, MaxMHz: 1500,
				RpnMHz: 400, Rp0MHz: 1500, RpeMHz: 400, RpaMHz: 1400, Profile: "base"},
			Throttle: ThrottleFlags{Reasons: "none"},
			Idle:     GTIdle{Status: "gt-c6", Name: "gt1-mc", BusyPercent: 2, BusyValid: true},
		},
	}
	if !st.GTs[0].Throttle.Status {
		st.GTs[0].Throttle.Reasons = "none"
	}
	return st
}

func (m *MockCollector) CollectProcesses(ctx context.Context) ([]ProcessInfo, error) {
	return []ProcessInfo{
		{
			PID:            2752,
			ProcessName:    "/usr/bin/python3",
			DeviceID:       0,
			Engines:        18,
			SharedMemBytes: 2357248 * 1024,
			MemBytes:       3028264 * 1024,
		},
		{
			PID:            2263263,
			ProcessName:    "/app/llama-server (DeepSeek-R1-Distill-Qwen-32B)",
			DeviceID:       0,
			Engines:        18,
			SharedMemBytes: 0,
			MemBytes:       24706676 * 1024,
		},
		{
			PID:            2266545,
			ProcessName:    "/app/llama-server (Qwen2.5-Coder-32B-Instruct)",
			DeviceID:       1,
			Engines:        18,
			SharedMemBytes: 0,
			MemBytes:       24710768 * 1024,
		},
		{
			PID:            3891402,
			ProcessName:    "/usr/bin/ffmpeg -hwaccel vaapi -i stream.mp4",
			DeviceID:       1,
			Engines:        4,
			SharedMemBytes: 524288 * 1024,
			MemBytes:       854120 * 1024,
		},
	}, nil
}

func (m *MockCollector) CollectHealth(ctx context.Context) (map[int]DeviceHealth, error) {
	return map[int]DeviceHealth{
		0: {
			DeviceID: 0,
			CoreTemperature: HealthSensor{
				Status:            "OK",
				ThrottleThreshold: 105,
				ShutdownThreshold: 130,
				Description:       "Values for all the temperature sensors are healthy.",
			},
			MemoryTemperature: HealthSensor{
				Status:            "Warning",
				ThrottleThreshold: 85,
				ShutdownThreshold: 100,
				Description:       "Unhealthy temperature sensor value, 86.00 >= 85 (threshold)",
			},
			PowerHealth: HealthSensor{
				Status:      "OK",
				Description: "Values for all the power domains are healthy.",
			},
			MemoryHealth: HealthSensor{
				Status:      "OK",
				Description: "Memory subsystem operating normally.",
			},
			FrequencyHealth: HealthSensor{
				Status:      "OK",
				Description: "Device frequency is not throttled",
			},
		},
		1: {
			DeviceID: 1,
			CoreTemperature: HealthSensor{
				Status:            "OK",
				ThrottleThreshold: 105,
				ShutdownThreshold: 130,
				Description:       "Values for all the temperature sensors are healthy.",
			},
			MemoryTemperature: HealthSensor{
				Status:            "OK",
				ThrottleThreshold: 85,
				ShutdownThreshold: 100,
				Description:       "Values for all the temperature sensors are healthy.",
			},
			PowerHealth: HealthSensor{
				Status:      "OK",
				Description: "Values for all the power domains are healthy.",
			},
			MemoryHealth: HealthSensor{
				Status:      "OK",
				Description: "Memory subsystem operating normally.",
			},
			FrequencyHealth: HealthSensor{
				Status:      "OK",
				Description: "Device frequency is not throttled",
			},
		},
	}, nil
}

func (m *MockCollector) CollectTopology(ctx context.Context) (*TopologyData, error) {
	return m.topology, nil
}

// mockFdinfo synthesizes per-engine utilization and per-process memory so demo
// mode exercises the CCS column rather than showing it as
// unavailable.
func (m *MockCollector) mockFdinfo(devID int, t, util, usedMiB float64) *FdinfoStats {
	ccs := math.Max(0, math.Min(100, util+math.Sin(t*0.5+float64(devID))*15))
	// A constant float with a fraction cannot be converted to int64 directly.
	// The workload accounts for all but a little of the card's used memory; the
	// remainder is what the overview shows as "other". Card 0 drives a display
	// in the demo, so it is left with more unattributed.
	unattributed := 44.0
	if devID == 0 {
		unattributed = 255
	}
	resident := int64((usedMiB - unattributed) * 1024 * 1024)
	return &FdinfoStats{
		Available: true,
		Engines: []EngineUtil{
			{Name: "ccs", BusyPercent: ccs, Valid: true},
			{Name: "rcs", BusyPercent: 0, Valid: true},
			{Name: "bcs", BusyPercent: math.Abs(math.Sin(t)) * 6, Valid: true},
			{Name: "vcs", BusyPercent: 0, Valid: true},
			{Name: "vecs", BusyPercent: 0, Valid: true},
		},
		Processes: []DrmProcess{
			{PID: 2263263 + devID, Command: "llama-server", Bdf: m.devices[devID].PciBdf, Clients: 2,
				Model: []string{"Qwen3.8-27B-Q4_K_M", "Qwen3.8-Flash-Next-IQ3_XXS"}[devID%2], Port: fmt.Sprintf("%d", 6000+devID),
				Memory: ProcessGPUMemory{
					VramBytes:         resident,
					ResidentVramBytes: resident,
					GttBytes:          814 * 1024 * 1024,
					SystemBytes:       int64(186+devID*60) * 1024 * 1024,
				}},
		},
	}
}

// WithDevices sets the simulated machine to n cards (at least 1), cloning the
// first card's specs under new ids and bus addresses. Bus numbers stay below
// 0x100 and in id order, so cards number the same way as on real hardware.
func (m *MockCollector) WithDevices(n int) *MockCollector {
	n = max(n, 1)
	if len(m.devices) > n {
		m.devices = m.devices[:n]
	}
	for len(m.devices) < n {
		d := m.devices[0]
		id := len(m.devices)
		bus := 0x80 + 0x08*(id-2)
		d.DeviceID = id
		d.PciBdf = fmt.Sprintf("0000:%02x:00.0", bus)
		d.DrmDevice = fmt.Sprintf("/dev/dri/card%d", id)
		d.UUID = fmt.Sprintf("00000000-0000-%04x-0000-0000e2238086", bus)
		m.devices = append(m.devices, d)
	}
	return m
}
