// Package sysman reads GPU telemetry straight from Level Zero's Sysman API --
// the interface xpu-smi itself is built on -- without running xpu-smi.
//
// Everything it returns is what the driver reports, unprocessed: counters are
// cumulative and it is the caller's job to turn them into rates. The library
// is loaded at run time, so a program built with this package still starts on
// a machine without Level Zero; Open then reports why it is unavailable.
package sysman

import (
	"errors"
	"fmt"
)

// ErrUnavailable is returned by Open when Sysman cannot be used at all.
var ErrUnavailable = errors.New("sysman unavailable")

// Device is one GPU as Sysman enumerates it. Index is the position in that
// enumeration and is what Sample takes; it is unrelated to xpu-smi's numbering.
type Device struct {
	Index int
	BDF   string // "0000:19:00.0"
	Name  string

	VendorID, DeviceID uint32
	UUID               string // formatted like xpu-smi prints it
	DriverVersion      string
	GFXFirmware        string  // "BMG__31.1062"; empty where the card publishes none
	MaxFreqMHz         float64 // GPU domain ceiling
	ECC                string  // "Enabled", "Disabled" or "N/A"
	ECCAvailable       int     // -1 unknown, 0 no, 1 yes
	Integrated         bool

	OptionROM      string // "OPROM CODE VERSION:..."; empty where the card publishes none
	BARBytes       uint64 // largest memory BAR: the VRAM the CPU can map; 0 unknown
	MemoryType     string // "GDDR6"; empty if unknown
	MemoryBusWidth int    // bits; 0 unknown
	MemoryChannels int    // 0 unknown
}

// memTypeNames follows zes_mem_type_t.
var memTypeNames = []string{"HBM", "DDR", "DDR3", "DDR4", "DDR5", "LPDDR", "LPDDR3", "LPDDR4", "LPDDR5",
	"SRAM", "L1", "L3", "GRF", "SLM", "GDDR4", "GDDR5", "GDDR5X", "GDDR6", "GDDR6X", "GDDR7", "LPDDR5X", "HBM2", "DDR2"}

func memTypeName(t int) string {
	if t < 0 || t >= len(memTypeNames) {
		return ""
	}
	return memTypeNames[t]
}

// positive turns the driver's -1 for "not reported" into 0.
func positive(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

// formatUUID prints a Level Zero device UUID the way xpu-smi does: the 16
// bytes in reverse order, grouped 4-2-2-2-6. It is a label on the exporter's
// series, so it must not change spelling.
func formatUUID(id [16]byte) string {
	var b [16]byte
	for i := range b {
		b[i] = id[15-i]
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func eccName(state int) string {
	switch state {
	case 1:
		return "Enabled"
	case 2:
		return "Disabled"
	}
	return "N/A"
}

// Temperature sensor types (zes_temp_sensors_t).
const (
	TempGlobal           = 0
	TempGPU              = 1
	TempMemory           = 2
	TempVoltageRegulator = 8
)

// Frequency domain types (zes_freq_domain_t).
const (
	FreqGPU    = 0
	FreqMemory = 1
	FreqMedia  = 2
)

// Power domains (zes_power_domain_t).
const (
	PowerUnknown = 0
	PowerCard    = 1
	PowerPackage = 2
	PowerStack   = 3
	PowerMemory  = 4
	PowerGPU     = 5
)

type Temp struct {
	Type int
	C    float64
}

type Power struct {
	Domain      int
	EnergyUJ    uint64 // cumulative microjoules
	TimestampUS uint64 // for deltas of this counter only
}

type Freq struct {
	Type       int
	ActualMHz  float64
	RequestMHz float64
}

// Sample is one reading of one device. Have* say whether the driver supports
// that group at all -- some cards report memory and power but no bandwidth or
// PCIe counters -- which is not the same as the value being zero.
type Sample struct {
	HaveMemory       bool
	MemSize, MemFree uint64 // bytes

	HaveBandwidth bool
	BWRead        uint64 // cumulative bytes
	BWWrite       uint64
	BWMax         uint64 // bytes/s
	BWTimestampUS uint64

	Temps  []Temp
	Powers []Power
	Freqs  []Freq

	HavePCI       bool
	PCIRx, PCITx  uint64 // cumulative bytes
	PCITimestampU uint64

	HaveState     bool
	ResetRequired bool     // the driver says the card needs a reset
	ResetReasons  []string // "wedged", "repair"
	Repaired      bool     // an in-field repair has been performed
}

func resetReasons(flags uint32) []string {
	var out []string
	if flags&1 != 0 {
		out = append(out, "wedged")
	}
	if flags&2 != 0 {
		out = append(out, "repair")
	}
	return out
}
