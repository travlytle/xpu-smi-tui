package ui

import (
	"context"
	"testing"
	"time"

	"github.com/travlytle/xpu-smi-tui/pkg/xpu"
)

// stuckCollector blocks in CollectStats until released, standing in for a
// sysfs or /proc read that hangs in the kernel.
type stuckCollector struct {
	*xpu.MockCollector
	release chan struct{}
}

func (s *stuckCollector) CollectStats(ctx context.Context, ids []int) (map[int]*xpu.GPUStats, error) {
	<-s.release
	return s.MockCollector.CollectStats(ctx, ids)
}

// A collection that never returns used to leave inFlight set for good: every
// later tick was skipped and the display froze on its last numbers with no
// message. The fetch now gives up at the fetch timeout and reports it.
func TestFetchReportsAStuckCollection(t *testing.T) {
	m := NewModel(Config{RefreshInterval: time.Second, DemoMode: true})
	stuck := &stuckCollector{MockCollector: xpu.NewMockCollector(), release: make(chan struct{})}
	m.currentCol = stuck
	m.devices = []xpu.DeviceInfo{{DeviceID: 0}}

	start := time.Now()
	msg := m.fetchTelemetryCmd()()
	took := time.Since(start)

	errMsg, ok := msg.(ErrorMsg)
	if !ok {
		t.Fatalf("got %T, want ErrorMsg", msg)
	}
	if errMsg.Discovery {
		t.Error("a stuck poll must not force re-discovery")
	}
	if took < m.fetchTimeout() || took > m.fetchTimeout()+time.Second {
		t.Errorf("gave up after %s, want about the fetch timeout %s", took, m.fetchTimeout())
	}

	// The error clears inFlight so the next tick polls again.
	m.inFlight = true
	m.Update(errMsg)
	if m.inFlight {
		t.Error("inFlight still set after the timeout error")
	}
	close(stuck.release)
}
