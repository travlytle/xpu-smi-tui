package xpu

import (
	"fmt"
	"sync"
	"time"

	"github.com/travlytle/xpu-smi-tui/internal/sysman"
)

type sysmanPrev struct {
	s  sysman.Sample
	at time.Time
}

// sysmanSource turns Sysman's cumulative counters into the rates GPUStats
// carries. It holds each card's previous sample, so it must be reused.
type sysmanSource struct {
	sm         *sysman.Sysman
	index      map[string]int // BDF -> Sysman index
	integrated map[string]bool

	mu   sync.Mutex
	prev map[string]sysmanPrev
}

func newSysmanSource() (*sysmanSource, error) {
	sm, err := sysman.Open()
	if err != nil {
		return nil, err
	}
	s := &sysmanSource{sm: sm, index: map[string]int{}, integrated: map[string]bool{}, prev: map[string]sysmanPrev{}}
	now := time.Now()
	for _, d := range sm.Devices() {
		s.index[d.BDF] = d.Index
		s.integrated[d.BDF] = d.Integrated
		// A baseline now, so the first real poll already has a window to take
		// rates over instead of showing a blank tick.
		if smp, err := sm.Sample(d.Index); err == nil {
			s.prev[d.BDF] = sysmanPrev{s: smp, at: now}
		}
	}
	return s, nil
}

// stats reads one card. deviceIndex is the caller's own numbering (xpu-smi's),
// carried through unchanged.
func (s *sysmanSource) stats(bdf string, deviceIndex int, now time.Time) (*GPUStats, error) {
	idx, ok := s.index[bdf]
	if !ok {
		return nil, fmt.Errorf("sysman: no device at %s", bdf)
	}
	cur, err := s.sm.Sample(idx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	prev, had := s.prev[bdf]
	s.prev[bdf] = sysmanPrev{s: cur, at: now}
	s.mu.Unlock()

	st := statsFromSysman(cur, prev.s, had, bdf, deviceIndex, now)
	if s.integrated[bdf] {
		markIntegrated(st)
	}
	return st, nil
}

// markIntegrated blanks the readings an integrated GPU has no equivalent of:
// dedicated VRAM and PCIe traffic.
func markIntegrated(st *GPUStats) {
	st.Integrated = true
	st.MemoryUsedMiB, st.MemoryUtilPercent = 0, 0
	st.MemoryUsedUnavailable = true
	st.PcieReadKBps, st.PcieWriteKBps = 0, 0
	st.TrafficUnavailable = true
}

const (
	mib = 1024 * 1024
	kib = 1024
)

// deltaPerSecond is (cur-prev)/dt for a monotonic counter with its own
// microsecond timestamp. ok is false when there is no usable window or the
// counter went backwards (a reset).
func deltaPerSecond(cur, prev, curTS, prevTS uint64) (float64, bool) {
	if curTS <= prevTS || cur < prev {
		return 0, false
	}
	return float64(cur-prev) / (float64(curTS-prevTS) / 1e6), true
}

func statsFromSysman(cur, prev sysman.Sample, havePrev bool, bdf string, deviceIndex int, now time.Time) *GPUStats {
	st := &GPUStats{
		DeviceIndex: deviceIndex,
		PciBdf:      bdf,
		Timestamp:   now,
		Tiles:       make(map[string]TileDetails),
	}
	if cur.HaveState {
		st.ResetRequired, st.ResetReasons, st.Repaired = cur.ResetRequired, cur.ResetReasons, cur.Repaired
	}

	if cur.HaveMemory && cur.MemSize > 0 {
		used := float64(cur.MemSize - min(cur.MemFree, cur.MemSize))
		st.MemoryUsedMiB = used / mib
		st.MemoryUtilPercent = used / float64(cur.MemSize) * 100
	}

	for _, t := range cur.Temps {
		switch t.Type {
		case sysman.TempGPU:
			st.CoreTempC = t.C
		case sysman.TempMemory:
			st.MemoryTempC = t.C
		case sysman.TempVoltageRegulator:
			st.VrTempC = t.C
		}
	}

	// Graphics clock is the *requested* frequency, matching xpu-smi's
	// clocks.current.graphics and the sysfs cur_freq the rest of the tool
	// shows; the actual value reads 0 whenever the GT sits in a c6 window.
	for _, f := range cur.Freqs {
		switch f.Type {
		case sysman.FreqGPU:
			st.FrequencyMHz = f.RequestMHz
			st.ActualFrequencyMHz = f.ActualMHz
		case sysman.FreqMedia:
			st.MediaFrequencyMHz = f.ActualMHz
		}
	}

	if !havePrev {
		return st // counters need two samples; only the instantaneous values above
	}

	// Card power: the card domain, else the package (all the A40 publishes).
	if w, ok := powerWatts(cur, prev, sysman.PowerCard); ok {
		st.PowerW = w
	} else if w, ok := powerWatts(cur, prev, sysman.PowerPackage); ok {
		st.PowerW = w
	}

	if cur.HaveBandwidth && prev.HaveBandwidth {
		r, rok := deltaPerSecond(cur.BWRead, prev.BWRead, cur.BWTimestampUS, prev.BWTimestampUS)
		w, wok := deltaPerSecond(cur.BWWrite, prev.BWWrite, cur.BWTimestampUS, prev.BWTimestampUS)
		if rok && wok {
			st.MemoryReadKBps, st.MemoryWriteKBps = r/kib, w/kib
			if cur.BWMax > 0 {
				st.MemoryBandwidthPercent = clampPercent((r + w) / float64(cur.BWMax) * 100)
			}
		}
	}

	if cur.HavePCI && prev.HavePCI {
		rx, rxOK := deltaPerSecond(cur.PCIRx, prev.PCIRx, cur.PCITimestampU, prev.PCITimestampU)
		tx, txOK := deltaPerSecond(cur.PCITx, prev.PCITx, cur.PCITimestampU, prev.PCITimestampU)
		if rxOK && txOK {
			st.PcieReadKBps, st.PcieWriteKBps = rx/kib, tx/kib
			st.PcieMeasured = true // a real counter: an idle zero is a reading, not a gap
		}
	}
	return st
}

func powerWatts(cur, prev sysman.Sample, domain int) (float64, bool) {
	for i, p := range cur.Powers {
		if p.Domain != domain || i >= len(prev.Powers) || prev.Powers[i].Domain != domain {
			continue
		}
		uj, ok := deltaPerSecond(p.EnergyUJ, prev.Powers[i].EnergyUJ, p.TimestampUS, prev.Powers[i].TimestampUS)
		if ok {
			return uj / 1e6, true
		}
	}
	return 0, false
}

// telemetrySource is what LiveCollector reads cards through: Sysman in
// production, a fake in tests.
type telemetrySource interface {
	stats(bdf string, deviceIndex int, now time.Time) (*GPUStats, error)
	discover(sysfs *SysfsCollector, hold func(bdf string) bool, cache map[string]DeviceInfo) []DeviceInfo
}

// discover builds the device list from Sysman and sysfs alone, so nothing
// needs `xpu-smi discovery`. DeviceID is Sysman's enumeration index; it is only
// the handle callers pass back, and the number a person sees comes from
// AssignGPUNumbers.
//
// hold reports a card that is being left alone to suspend. Its power cap and
// memory would have to be read from the device -- which resumes it -- so its
// entry comes from cache (the previous discovery) or, failing that, from what
// Sysman already published at start-up plus a few host-side sysfs facts.
func (s *sysmanSource) discover(sysfs *SysfsCollector, hold func(bdf string) bool, cache map[string]DeviceInfo) []DeviceInfo {
	var out []DeviceInfo
	for _, d := range s.sm.Devices() {
		info := DeviceInfo{
			DeviceID:        d.Index,
			DeviceName:      d.Name,
			DeviceState:     "normal",
			DeviceType:      "GPU",
			DrmDevice:       sysfs.DrmDevice(d.BDF),
			PciBdf:          d.BDF,
			PciDeviceID:     fmt.Sprintf("0x%x", d.DeviceID),
			UUID:            d.UUID,
			DriverVersion:   d.DriverVersion,
			VbiosVersion:    d.GFXFirmware,
			MaxFrequencyMHz: d.MaxFreqMHz,
			Integrated:      d.Integrated,
			EccMode:         d.ECC,
			// Sysman publishes no RAS counters on these cards, and a zero the
			// driver never reported would read as "no errors".
			EccCorrected:   "N/A",
			EccUncorrected: "N/A",
			CPUAffinity:    sysfs.CPUAffinity(d.BDF),
			EccSupported:   map[int]string{0: "no", 1: "yes"}[d.ECCAvailable],
			OptionROM:      d.OptionROM,
			BARBytes:       d.BARBytes,
			MemoryType:     d.MemoryType,
			MemoryBusWidth: d.MemoryBusWidth,
			MemoryChannels: d.MemoryChannels,
		}
		if d.VendorID == 0x8086 {
			info.VendorName = "Intel(R) Corporation"
		}
		if hold != nil && hold(d.BDF) {
			if cached, ok := cache[d.BDF]; ok {
				cached.DeviceID = d.Index
				out = append(out, cached)
			} else {
				out = append(out, info)
			}
			continue
		}
		info.PowerLimitW = sysfs.PowerCapW(d.BDF)
		if smp, err := s.sm.Sample(d.Index); err == nil && smp.HaveMemory && !d.Integrated {
			info.MemoryTotalMiB = float64(smp.MemSize) / mib
			info.MemoryFreeMiB = float64(smp.MemFree) / mib
		}
		out = append(out, info)
	}
	return out
}
