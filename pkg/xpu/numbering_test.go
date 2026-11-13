package xpu

import (
	"reflect"
	"testing"
)

func bdfs(devs []DeviceInfo) []string {
	out := make([]string, len(devs))
	for i, d := range devs {
		out[i] = d.PciBdf
	}
	return out
}

// Whatever order they arrive in, numbering follows
// the PCI address.
func TestAssignGPUNumbersDefaultsToPCIOrder(t *testing.T) {
	in := []DeviceInfo{
		{DeviceID: 0, PciBdf: "0000:67:00.0"},
		{DeviceID: 1, PciBdf: "0000:b5:00.0"},
		{DeviceID: 2, PciBdf: "0000:19:00.0"},
	}
	out := AssignGPUNumbers(in, nil)
	if want := []string{"0000:19:00.0", "0000:67:00.0", "0000:b5:00.0"}; !reflect.DeepEqual(bdfs(out), want) {
		t.Fatalf("order = %v, want %v", bdfs(out), want)
	}
	for i, d := range out {
		if d.GPUNumber() != i {
			t.Errorf("%s numbered %d, want %d", d.PciBdf, d.GPUNumber(), i)
		}
	}
	// The xpu-smi index is untouched: it is what gets passed back to xpu-smi.
	if out[0].DeviceID != 2 || out[2].DeviceID != 1 {
		t.Errorf("DeviceID changed: %+v", out)
	}
	if in[0].numbered {
		t.Error("input slice was modified")
	}
}

func TestAssignGPUNumbersHonoursUserOrder(t *testing.T) {
	in := []DeviceInfo{
		{PciBdf: "0000:19:00.0"}, {PciBdf: "0000:67:00.0"}, {PciBdf: "0000:b5:00.0"},
	}
	// The A40 first; short-form and upper-case addresses match too.
	out := AssignGPUNumbers(in, ParseGPUOrder("B5:00.0, 0000:67:00.0"))
	if want := []string{"0000:b5:00.0", "0000:67:00.0", "0000:19:00.0"}; !reflect.DeepEqual(bdfs(out), want) {
		t.Errorf("order = %v, want %v (unnamed cards follow, in PCI order)", bdfs(out), want)
	}
}

// A stale or mistyped address must not eat a number and leave a gap.
func TestAssignGPUNumbersIgnoresUnknownAddresses(t *testing.T) {
	in := []DeviceInfo{{PciBdf: "0000:19:00.0"}, {PciBdf: "0000:67:00.0"}}
	out := AssignGPUNumbers(in, []string{"0000:ff:00.0", "0000:67:00.0"})
	if out[0].PciBdf != "0000:67:00.0" || out[0].GPUNumber() != 0 || out[1].GPUNumber() != 1 {
		t.Errorf("got %+v", out)
	}
}

// Hand-built infos (tests, mock) that never went through Discover keep using
// the xpu-smi index.
func TestGPUNumberFallsBackToDeviceID(t *testing.T) {
	if got := (DeviceInfo{DeviceID: 3}).GPUNumber(); got != 3 {
		t.Errorf("GPUNumber = %d, want 3", got)
	}
}

func TestLabel(t *testing.T) {
	if got := (DeviceInfo{DeviceID: 1}).Label(); got != "GPU 1" {
		t.Errorf("discrete: %q", got)
	}
	if got := (DeviceInfo{DeviceID: 0, Integrated: true}).Label(); got != "iGPU 0" {
		t.Errorf("integrated: %q", got)
	}
}
