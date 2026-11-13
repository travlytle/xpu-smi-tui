package xpu

import (
	"testing"
	"time"

	"github.com/travlytle/xpu-smi-tui/internal/sysman"
)

func b70Sample(rx, tx, energy, ts uint64) sysman.Sample {
	return sysman.Sample{
		HaveMemory: true, MemSize: 28568 << 20, MemFree: 1280 << 20,
		HaveBandwidth: true, BWRead: rx, BWWrite: tx, BWMax: 637534208000, BWTimestampUS: ts,
		HavePCI: true, PCIRx: rx, PCITx: tx, PCITimestampU: ts,
		Temps: []sysman.Temp{{Type: sysman.TempGlobal, C: 99}, {Type: sysman.TempGPU, C: 37},
			{Type: sysman.TempMemory, C: 40}, {Type: sysman.TempVoltageRegulator, C: 46}},
		Powers: []sysman.Power{{Domain: sysman.PowerCard, EnergyUJ: energy, TimestampUS: ts},
			{Domain: sysman.PowerPackage, EnergyUJ: energy / 2, TimestampUS: ts}},
		Freqs: []sysman.Freq{{Type: sysman.FreqMedia, ActualMHz: 400, RequestMHz: 400},
			{Type: sysman.FreqGPU, ActualMHz: 517, RequestMHz: 400}},
	}
}

func TestStatsFromSysmanMapsInstantaneousValues(t *testing.T) {
	st := statsFromSysman(b70Sample(0, 0, 0, 1e6), sysman.Sample{}, false, "0000:19:00.0", 7, time.Unix(1, 0))
	if st.DeviceIndex != 7 || st.PciBdf != "0000:19:00.0" {
		t.Errorf("identity = %d %s", st.DeviceIndex, st.PciBdf)
	}
	if st.CoreTempC != 37 || st.MemoryTempC != 40 || st.VrTempC != 46 {
		t.Errorf("temps core %v mem %v vr %v, want 37/40/46 (the global sensor is not the core)", st.CoreTempC, st.MemoryTempC, st.VrTempC)
	}
	if st.MemoryUsedMiB != 27288 || int(st.MemoryUtilPercent) != 95 {
		t.Errorf("memory = %v MiB, %v%%", st.MemoryUsedMiB, st.MemoryUtilPercent)
	}
	// The requested clock, not the 517 the GPU domain reports as actual.
	if st.FrequencyMHz != 400 || st.MediaFrequencyMHz != 400 {
		t.Errorf("clocks = %v / %v, want 400 / 400", st.FrequencyMHz, st.MediaFrequencyMHz)
	}
	if st.PowerW != 0 || st.PcieMeasured {
		t.Error("rates reported without a previous sample")
	}
}

func TestStatsFromSysmanDerivesRatesFromCounterDeltas(t *testing.T) {
	prev := b70Sample(100<<20, 50<<20, 10_000_000, 1_000_000)
	// Two seconds later: 200 MiB rx, 100 MiB tx, 30 J on the card domain.
	cur := b70Sample(300<<20, 150<<20, 40_000_000, 3_000_000)
	st := statsFromSysman(cur, prev, true, "0000:19:00.0", 0, time.Unix(3, 0))

	if st.PowerW != 15 {
		t.Errorf("power = %v W, want 15 (30 J over 2 s, card domain)", st.PowerW)
	}
	if st.PcieReadKBps != 100*1024 || st.PcieWriteKBps != 50*1024 || !st.PcieMeasured {
		t.Errorf("pcie = %v / %v KB/s measured=%v, want 102400 / 51200 / true", st.PcieReadKBps, st.PcieWriteKBps, st.PcieMeasured)
	}
	if st.MemoryReadKBps != 100*1024 || st.MemoryWriteKBps != 50*1024 {
		t.Errorf("memory traffic = %v / %v KB/s", st.MemoryReadKBps, st.MemoryWriteKBps)
	}
	if want := float64(150<<20) / 637534208000 * 100; st.MemoryBandwidthPercent < want*0.99 || st.MemoryBandwidthPercent > want*1.01 {
		t.Errorf("bandwidth = %v%%, want ~%v%%", st.MemoryBandwidthPercent, want)
	}
}

// The A40 publishes package power only, and no bandwidth or PCIe counters.
// Those must come out as absent, not as a measured zero.
func TestStatsFromSysmanA40HasPackagePowerAndNoTraffic(t *testing.T) {
	a40 := func(energy, ts uint64) sysman.Sample {
		return sysman.Sample{HaveMemory: true, MemSize: 6088 << 20, MemFree: 6088 << 20,
			Powers: []sysman.Power{{Domain: sysman.PowerPackage, EnergyUJ: energy, TimestampUS: ts}},
			Freqs:  []sysman.Freq{{Type: sysman.FreqGPU, ActualMHz: 600, RequestMHz: 600}}}
	}
	st := statsFromSysman(a40(17_000_000, 2_000_000), a40(0, 1_000_000), true, "0000:b5:00.0", 2, time.Now())
	if st.PowerW != 17 {
		t.Errorf("power = %v, want 17 from the package domain", st.PowerW)
	}
	if st.PcieMeasured || st.MemoryBandwidthPercent != 0 {
		t.Errorf("traffic reported for a card with no counters: %+v", st)
	}
	if st.MemoryUsedMiB != 0 {
		t.Errorf("used = %v, want 0 (unprivileged xpu-smi/Sysman say so; the i915 fallback handles it)", st.MemoryUsedMiB)
	}
}

func TestDeltaPerSecondRejectsResetsAndEmptyWindows(t *testing.T) {
	for name, c := range map[string][4]uint64{
		"counter reset": {5, 100, 2_000_000, 1_000_000},
		"no time":       {200, 100, 1_000_000, 1_000_000},
		"time reversed": {200, 100, 1_000_000, 2_000_000},
	} {
		if v, ok := deltaPerSecond(c[0], c[1], c[2], c[3]); ok {
			t.Errorf("%s: got %v, want rejected", name, v)
		}
	}
	if v, ok := deltaPerSecond(300, 100, 3_000_000, 1_000_000); !ok || v != 100 {
		t.Errorf("valid window = %v, %v; want 100", v, ok)
	}
}

func TestMarkIntegratedBlanksVRAMAndPCIe(t *testing.T) {
	st := &GPUStats{MemoryUsedMiB: 512, MemoryUtilPercent: 10, PcieReadKBps: 100, PcieWriteKBps: 50}
	markIntegrated(st)
	if !st.Integrated || !st.MemoryUsedUnavailable || !st.TrafficUnavailable ||
		st.MemoryUsedMiB != 0 || st.PcieReadKBps != 0 || st.PcieWriteKBps != 0 {
		t.Errorf("integrated stats not blanked: %+v", st)
	}
	st.Fdinfo = &FdinfoStats{Processes: []DrmProcess{{}}}
	applyFdinfoFallbacks(st)
	if st.MemoryUsedEstimated {
		t.Error("VRAM estimated for an integrated GPU")
	}
}
