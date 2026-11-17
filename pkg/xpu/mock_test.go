package xpu

import "testing"

func TestWithDevicesCountAndOrder(t *testing.T) {
	for _, n := range []int{1, 3, 16} {
		devs, _ := NewMockCollector().WithDevices(n).Discover(nil)
		if len(devs) != n {
			t.Fatalf("WithDevices(%d) gave %d cards", n, len(devs))
		}
		for i := 1; i < len(devs); i++ {
			if devs[i-1].PciBdf >= devs[i].PciBdf {
				t.Errorf("n=%d: card %d at %s does not sort after %s", n, i, devs[i].PciBdf, devs[i-1].PciBdf)
			}
		}
	}
}
