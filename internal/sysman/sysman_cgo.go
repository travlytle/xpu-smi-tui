//go:build cgo

package sysman

/*
#cgo CFLAGS: -I${SRCDIR}/include -O2
#cgo LDFLAGS: -ldl
#include "shim.h"
*/
import "C"

import (
	"fmt"
	"sync"
)

// Sysman is an initialised Level Zero Sysman session.
type Sysman struct {
	mu      sync.Mutex // the shim keeps static state; serialise every call
	devices []Device
}

// Open initialises Sysman and enumerates devices.
func Open() (*Sysman, error) {
	n := int(C.sm_init())
	switch {
	case n == -1000:
		return nil, fmt.Errorf("%w: libze_loader.so.1 not found", ErrUnavailable)
	case n == -1001:
		return nil, fmt.Errorf("%w: libze_loader.so.1 lacks the Sysman entry points", ErrUnavailable)
	case n < 0:
		return nil, fmt.Errorf("%w: zesInit failed (0x%x)", ErrUnavailable, -n)
	case n == 0:
		return nil, fmt.Errorf("%w: no devices", ErrUnavailable)
	}
	s := &Sysman{}
	for i := 0; i < n; i++ {
		var info C.sm_info
		if C.sm_device_info(C.int(i), &info) != 0 {
			continue
		}
		var uuid [16]byte
		for k := range uuid {
			uuid[k] = byte(info.uuid[k])
		}
		s.devices = append(s.devices, Device{
			Index: i, BDF: C.GoString(&info.bdf[0]), Name: C.GoString(&info.name[0]),
			VendorID: uint32(info.vendor_id), DeviceID: uint32(info.device_id),
			UUID: formatUUID(uuid), DriverVersion: C.GoString(&info.driver_version[0]),
			GFXFirmware: C.GoString(&info.gfx_firmware[0]), MaxFreqMHz: float64(info.freq_max_mhz),
			ECC: eccName(int(info.ecc_state)), Integrated: info.integrated != 0,
		})
	}
	return s, nil
}

// Devices lists what Open found.
func (s *Sysman) Devices() []Device { return append([]Device(nil), s.devices...) }

// Sample reads every supported group on one device.
func (s *Sysman) Sample(index int) (Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var raw C.sm_sample
	if rc := C.sm_device_sample(C.int(index), &raw); rc != 0 {
		return Sample{}, fmt.Errorf("sysman: no reading from device %d", index)
	}
	out := Sample{
		HaveMemory: raw.have_mem != 0, MemSize: uint64(raw.mem_size), MemFree: uint64(raw.mem_free),
		HaveBandwidth: raw.have_bw != 0, BWRead: uint64(raw.bw_read), BWWrite: uint64(raw.bw_write),
		BWMax: uint64(raw.bw_max), BWTimestampUS: uint64(raw.bw_ts),
		HavePCI: raw.have_pci != 0, PCIRx: uint64(raw.pci_rx), PCITx: uint64(raw.pci_tx),
		PCITimestampU: uint64(raw.pci_ts),
	}
	for i := 0; i < int(raw.ntemp); i++ {
		out.Temps = append(out.Temps, Temp{Type: int(raw.temp[i]._type), C: float64(raw.temp[i].c)})
	}
	for i := 0; i < int(raw.npower); i++ {
		out.Powers = append(out.Powers, Power{Domain: int(raw.power[i].domain),
			EnergyUJ: uint64(raw.power[i].energy_uj), TimestampUS: uint64(raw.power[i].ts_us)})
	}
	for i := 0; i < int(raw.nfreq); i++ {
		out.Freqs = append(out.Freqs, Freq{Type: int(raw.freq[i]._type),
			ActualMHz: float64(raw.freq[i].actual), RequestMHz: float64(raw.freq[i].request)})
	}
	return out, nil
}
