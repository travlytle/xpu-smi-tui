package ui

import (
	"context"
	"testing"
	"time"

	"github.com/travlytle/xpu-smi-tui/pkg/xpu"
)

// blockingCollector's Discover blocks until release is closed, standing in
// for a discovery call slower than one refresh interval.
type blockingCollector struct {
	*xpu.MockCollector
	release chan struct{}
}

func (c *blockingCollector) Discover(ctx context.Context) ([]xpu.DeviceInfo, error) {
	<-c.release
	return c.MockCollector.Discover(ctx)
}

// Init used to leave inFlight false until the TickMsg handler set it, so the
// first tick -- which fires before a slow discovery has returned -- found an
// empty device list and an idle flag and dispatched a second, overlapping
// discoverAndFetchCmd. The two raced CollectStats for the same lock, and the
// loser surfaced as "Error: previous collection still running", on nearly
// every launch given how slow xpu-smi's own discovery call is here.
func TestInitPreventsTheFirstTickFromDuplicatingDiscovery(t *testing.T) {
	m := NewModel(Config{RefreshInterval: time.Second, DemoMode: true})
	col := &blockingCollector{MockCollector: xpu.NewMockCollector(), release: make(chan struct{})}
	defer close(col.release)
	m.currentCol = col

	m.Init()
	if !m.inFlight {
		t.Fatal("inFlight must already be true when Init returns, not left for a later message to set")
	}

	// The tick that fires mid-discovery. tea.Batch runs a single command
	// exactly as given but wraps two or more in a BatchMsg without running
	// either -- so if the guard failed and queued a second discoverAndFetchCmd
	// alongside the re-armed tick, invoking the result resolves immediately;
	// a lone tickCmd blocks for the full refresh interval. That difference is
	// enough to tell them apart without touching xpu-smi or waiting on a
	// real-time timer to actually finish.
	_, cmd := m.Update(TickMsg{At: time.Now(), Gen: m.tickGen})
	if cmd == nil {
		t.Fatal("no command returned; the tick should at least re-arm itself")
	}
	done := make(chan struct{})
	go func() { cmd(); close(done) }()
	select {
	case <-done:
		t.Error("the tick during startup resolved immediately: it queued more than just the next tick, meaning discovery was dispatched a second time")
	case <-time.After(150 * time.Millisecond):
		// Still running well under the 1s refresh interval: this is the lone
		// re-armed tick waiting out its timer, as it should be.
	}
}
