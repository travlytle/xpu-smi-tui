package xpu

import (
	"context"
	"testing"
)

// TestLiveCollectStatsDeviceIdentity guards against cross-device data mixing in the
// concurrent CollectStats pipeline (e.g. GPU 0 receiving GPU 1's frequency).
// Requires Level Zero and two physical GPUs; skipped otherwise.
func TestLiveCollectStatsDeviceIdentity(t *testing.T) {
	c := NewLiveCollector()
	t.Cleanup(c.Close)
	ctx := context.Background()

	devices, err := c.Discover(ctx)
	if err != nil {
		t.Skipf("discovery failed: %v", err)
	}
	if len(devices) < 2 {
		t.Skip("need at least 2 GPUs for identity cross-check")
	}

	expectedBDF := make(map[int]string)
	for _, d := range devices {
		expectedBDF[d.DeviceID] = d.PciBdf
	}
	ids := make([]int, 0, len(devices))
	for id := range expectedBDF {
		ids = append(ids, id)
	}

	for round := 0; round < 25; round++ {
		stats, err := c.CollectStats(ctx, ids)
		if err != nil {
			t.Fatalf("round %d: CollectStats failed: %v", round, err)
		}
		for id, st := range stats {
			if st == nil {
				t.Fatalf("round %d: nil stats for device %d", round, id)
			}
			if want, ok := expectedBDF[id]; ok && st.PciBdf != want {
				t.Fatalf("round %d: DEVICE MIXING: requested %d but got stats for %s (expected %s)", round, id, st.PciBdf, want)
			}
			if st.DeviceIndex != id {
				t.Fatalf("round %d: device index mismatch: requested %d, stats.DeviceIndex=%d", round, id, st.DeviceIndex)
			}
			if st.FrequencyMHz <= 0 {
				t.Errorf("round %d: device %d reported no frequency", round, id)
			}
		}
	}
}
