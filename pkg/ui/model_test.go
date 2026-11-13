package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/components"
	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	tea "charm.land/bubbletea/v2"
)

func TestModelLifecycle(t *testing.T) {
	cfg := Config{
		RefreshInterval: 1 * time.Second,
		DemoMode:        true,
		DefaultDevice:   0,
	}

	m := NewModel(cfg)
	m.termWidth = 120
	m.termHeight = 35

	// 1. Initial view before data loads
	initialView := m.View().Content
	if !strings.Contains(initialView, "XPU-SMI") {
		t.Errorf("Initial view should contain title banner")
	}

	// 2. Simulate discovery / fetch message
	mock := xpu.NewMockCollector()
	devs, _ := mock.Discover(nil)
	stats, _ := mock.CollectStats(nil, []int{0, 1})
	health, _ := mock.CollectHealth(nil)
	topo, _ := mock.CollectTopology(nil)

	loadedMsg := DiscoveredMsg{
		Devices:  devs,
		Stats:    stats,
		Health:   health,
		Topology: topo,
	}

	newM, _ := m.Update(loadedMsg)
	m = newM.(*Model)

	// Overview is the default and must answer the operator's questions directly.
	vOverview := m.View().Content
	for _, want := range []string{"BUSY", "VRAM"} {
		if !strings.Contains(vOverview, want) {
			t.Errorf("default Overview missing %q", want)
		}
	}

	// 1. Switch to Detail
	newM, _ = m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = newM.(*Model)
	if m.activeTab != components.TabDetail {
		t.Errorf("expected TabDetail, got %v", m.activeTab)
	}
	if v := m.View().Content; !strings.Contains(v, "THERMAL") {
		t.Errorf("Detail view should show the thermal section")
	}

	// 2. Switch to Trends
	newM, _ = m.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	m = newM.(*Model)
	if m.activeTab != components.TabTrends {
		t.Errorf("expected TabTrends, got %v", m.activeTab)
	}

	// Right arrow wraps back round to Overview.
	newM, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = newM.(*Model)
	if m.activeTab != components.TabOverview {
		t.Errorf("expected TabOverview after wrapping, got %v", m.activeTab)
	}

	// Left arrow from the first tab wraps to the last.
	newM, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = newM.(*Model)
	if m.activeTab != components.TabTrends {
		t.Errorf("expected TabTrends after left arrow from Overview, got %v", m.activeTab)
	}

	// 7. Toggle Help Modal
	newM, _ = m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = newM.(*Model)
	if !m.showHelp {
		t.Errorf("expected showHelp to be true")
	}
	vHelp := m.View().Content
	if !strings.Contains(vHelp, "KEYBOARD SHORTCUTS") {
		t.Errorf("Help modal not rendered")
	}

	// Close help
	newM, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = newM.(*Model)
	if m.showHelp {
		t.Errorf("expected showHelp to be false after Esc")
	}

	// 8. Test live CLI hardware collector discovery
	cli := xpu.NewLiveCollector()
	defer cli.Close()
	liveDevs, err := cli.Discover(context.Background())
	if err == nil && len(liveDevs) > 0 {
		liveStats, _ := cli.CollectStats(context.Background(), []int{0})
		liveHealth, _ := cli.CollectHealth(context.Background())
		liveTopo, _ := cli.CollectTopology(context.Background())

		liveLoaded := DiscoveredMsg{
			Devices:  liveDevs,
			Stats:    liveStats,
			Health:   liveHealth,
			Topology: liveTopo,
		}

		newM, _ = m.Update(liveLoaded)
		m = newM.(*Model)
		vLive := m.View().Content
		if len(vLive) == 0 {
			t.Errorf("expected non-empty live view")
		}
	}
}
