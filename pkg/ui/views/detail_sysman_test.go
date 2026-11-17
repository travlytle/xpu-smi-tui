package views

import (
	"strings"
	"testing"

	"github.com/travlytle/xpu-smi-tui/pkg/xpu"
)

func TestOptionROM(t *testing.T) {
	got := optionROM("OPROM CODE VERSION:2304240000_OPROM DATA VERSION:2304140000")
	if got != "code 2304240000   data 2304140000" {
		t.Errorf("got %q", got)
	}
	if optionROM("something else") != "something else" {
		t.Error("unparsed string should pass through")
	}
}

func TestMemoryKind(t *testing.T) {
	if got := memoryKind(xpu.DeviceInfo{MemoryType: "GDDR6", MemoryBusWidth: 256, MemoryChannels: 8}); got != "GDDR6   256-bit   8 channels" {
		t.Errorf("got %q", got)
	}
	if got := memoryKind(xpu.DeviceInfo{MemoryType: "GDDR6"}); got != "GDDR6" {
		t.Errorf("unknown width should be left out, got %q", got)
	}
}

func TestRebarRow(t *testing.T) {
	g := &xpu.GPUCombined{Info: xpu.DeviceInfo{MemoryTotalMiB: 32 * 1024, BARBytes: 32 << 30}, Stats: xpu.GPUStats{}}
	if r := rebarRow(g); !strings.Contains(r, "on") {
		t.Errorf("full BAR: %q", r)
	}
	g.Info.BARBytes = 256 << 20
	if r := rebarRow(g); !strings.Contains(r, "off") {
		t.Errorf("256 MiB BAR: %q", r)
	}
	g.Info.BARBytes = 0
	if rebarRow(g) != "" {
		t.Error("unknown BAR should hide the row")
	}
}

func TestFormatMillis(t *testing.T) {
	for in, want := range map[float64]string{15: "15 ms", 28000: "28 s", 1500: "1.5 s"} {
		if got := formatMillis(in); got != want {
			t.Errorf("formatMillis(%v) = %q, want %q", in, got, want)
		}
	}
}
