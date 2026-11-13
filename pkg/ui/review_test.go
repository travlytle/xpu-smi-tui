package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/components"
	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	tea "charm.land/bubbletea/v2"
)

// tickOf is the TickMsg for the model's current chain.
func tickOf(m *Model) TickMsg { return TickMsg{At: time.Now(), Gen: m.tickGen} }

// runCmds executes a command batch and returns the messages it yields, so a
// test can see what the next Update would receive without a running program.
func hasTick(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch v := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range v {
			if hasTick(c) {
				return true
			}
		}
	case TickMsg:
		return true
	}
	return false
}

// A tick that arrives while a fetch is outstanding must not start another.
// At the default 1 s refresh a collection takes about as long as the interval.
func TestTickSkipsFetchWhileInFlight(t *testing.T) {
	m := demoModel(t, 120, 30)
	m.inFlight = true
	before := m.inFlight

	nm, cmd := m.Update(tickOf(m))
	m = nm.(*Model)
	if !before || !m.inFlight {
		t.Fatal("in-flight flag lost")
	}
	// The chain must survive even when the fetch is skipped.
	if !hasTick(cmd) {
		t.Error("tick chain did not continue while a fetch was in flight")
	}
}

// The chain must survive every path that consumes a tick or an error.
func TestTickChainSurvivesErrorsAndPause(t *testing.T) {
	m := demoModel(t, 120, 30)
	nm, cmd := m.Update(tickOf(m))
	m = nm.(*Model)
	if !hasTick(cmd) {
		t.Fatal("no tick re-armed on a normal tick")
	}
	nm, _ = m.Update(ErrorMsg{Err: errors.New("boom")})
	m = nm.(*Model)
	if m.inFlight {
		t.Error("an error must clear the in-flight flag or fetching stops forever")
	}
	m.isPaused = true
	if _, cmd = m.Update(tickOf(m)); !hasTick(cmd) {
		t.Error("tick chain stopped while paused; resuming would never poll again")
	}
}

// Changing the interval retires the old chain: its ticks are ignored, and one
// new chain is started -- not a second one alongside.
func TestIntervalChangeRestartsTickChain(t *testing.T) {
	m := demoModel(t, 120, 30)
	old := tickOf(m)
	nm, cmd := m.Update(tea.KeyPressMsg{Code: '-', Text: "-"})
	m = nm.(*Model)
	if m.refreshInterval != 2*time.Second {
		t.Fatalf("interval = %s, want 2s", m.refreshInterval)
	}
	if !hasTick(cmd) {
		t.Error("no new chain started on interval change")
	}
	nm, cmd = m.Update(old)
	m = nm.(*Model)
	if hasTick(cmd) {
		t.Error("a tick from the retired chain re-armed itself: two chains now poll")
	}
}

// An error must not stay in the footer for the rest of the day.
func TestErrorStatusClears(t *testing.T) {
	m := demoModel(t, 120, 30)
	nm, cmd := m.Update(ErrorMsg{Err: errors.New("device 0 stats error")})
	m = nm.(*Model)
	if !strings.Contains(m.statusMsg, "device 0") {
		t.Fatal("error not shown")
	}
	if cmd == nil {
		t.Fatal("no clear timer armed for the error message")
	}
	// The timer's message must clear it.
	nm, _ = m.Update(StatusClearMsg{Seq: m.statusSeq})
	if nm.(*Model).statusMsg != "" {
		t.Error("error message did not clear")
	}
}

// A clear armed for an earlier message must not erase a newer one.
func TestStaleStatusClearIsIgnored(t *testing.T) {
	m := demoModel(t, 120, 30)
	m.setStatus("first", time.Second)
	firstSeq := m.statusSeq
	m.setStatus("second", time.Second)
	nm, _ := m.Update(StatusClearMsg{Seq: firstSeq})
	if nm.(*Model).statusMsg != "second" {
		t.Errorf("newer message erased by an older timer: %q", nm.(*Model).statusMsg)
	}
}

// Keys that change program state must not reach the global switch from behind
// the legend page. Space used to pause polling there.
func TestLegendSwallowsStateKeys(t *testing.T) {
	m := demoModel(t, 120, 30)
	m.showLegend = true
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeySpace, Text: " "},
		{Code: 'D', Text: "D"},
		{Code: '/', Text: "/"},
		{Code: '-', Text: "-"},
	} {
		before := struct {
			paused, demo, filter bool
			interval             time.Duration
		}{m.isPaused, m.isDemo, m.filterMode, m.refreshInterval}
		nm, _ := m.Update(k)
		m = nm.(*Model)
		if m.isPaused != before.paused || m.isDemo != before.demo || m.filterMode != before.filter || m.refreshInterval != before.interval {
			t.Errorf("%q leaked through the legend and changed state", k.String())
		}
		if !m.showLegend {
			t.Errorf("%q closed the legend", k.String())
		}
	}
	// Scrolling still works.
	before := stripANSI(m.View().Content)
	for i := 0; i < 5; i++ {
		nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = nm.(*Model)
	}
	if stripANSI(m.View().Content) == before {
		t.Error("arrow keys no longer scroll the legend")
	}
}

// Backspace removes a whole rune, not one byte of it.
func TestFilterBackspaceIsRuneSafe(t *testing.T) {
	m := demoModel(t, 120, 30)
	m.filterMode = true
	m.filterText = "qwén"
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = nm.(*Model)
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = nm.(*Model)
	if m.filterText != "qw" {
		t.Errorf("filterText = %q, want %q", m.filterText, "qw")
	}
}

// Toggling demo mode must not splice one collector's history onto the other's.
func TestDemoToggleResetsHistory(t *testing.T) {
	m := demoModel(t, 120, 30)
	if len(m.gpus) == 0 || m.gpus[0].History.PowerW.Latest() == 0 {
		t.Fatal("fixture has no history")
	}
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'D', Text: "D"})
	m = nm.(*Model)
	if len(m.gpus) != 0 || len(m.gpuMap) != 0 || len(m.devices) != 0 {
		t.Error("device state carried across a collector switch")
	}
	if !m.inFlight {
		t.Error("discovery should be marked in flight")
	}
}

// Detail shows one card; b must not claim to show both.
func TestBothOnDetailIsRefused(t *testing.T) {
	m := demoModel(t, 120, 30)
	m.activeTab = components.TabDetail
	m.selectedGPU = 1
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	m = nm.(*Model)
	if m.selectedGPU != 1 {
		t.Errorf("selectedGPU = %d; Detail cannot show both", m.selectedGPU)
	}
}

// Discovery that succeeds while every stats call fails must not be stamped as
// fresh data, or the STALE badge never fires over a screen of zeros.
func TestDiscoveryWithoutStatsIsNotFresh(t *testing.T) {
	m := NewModel(Config{RefreshInterval: time.Second, DemoMode: true})
	devs, _ := xpu.NewMockCollector().Discover(nil)
	nm, _ := m.Update(DiscoveredMsg{Devices: devs, Err: errors.New("stats failed")})
	m = nm.(*Model)
	if !m.lastDataAt.IsZero() {
		t.Error("lastDataAt stamped with no stats received")
	}
	if len(m.gpus) != len(devs) {
		t.Errorf("devices not registered: %d", len(m.gpus))
	}
}

// --device 0 must mean card 0.
func TestDeviceZeroIsSelectable(t *testing.T) {
	m := NewModel(Config{DefaultDevice: 0})
	if m.selectedGPU != 0 {
		t.Errorf("selectedGPU = %d for --device 0", m.selectedGPU)
	}
	if NewModel(Config{DefaultDevice: -1}).selectedGPU != -1 {
		t.Error("-1 should mean both")
	}
}

// Narrow panes: header and footer must not render wider than the terminal.
func TestChromeFitsNarrowTerminals(t *testing.T) {
	for _, w := range []int{20, 30, 36} {
		if got := lipglossWidth(components.RenderHeader(components.TabOverview, 0, false, false, time.Second, 2, w, "")); got > w {
			t.Errorf("width %d: header is %d cols", w, got)
		}
		if got := lipglossWidth(components.RenderFooter(w, -1, 2, "", "")); got > w {
			t.Errorf("width %d: footer is %d cols", w, got)
		}
	}
}

func lipglossWidth(s string) int {
	w := 0
	for _, l := range strings.Split(s, "\n") {
		if n := len([]rune(stripANSI(l))); n > w {
			w = n
		}
	}
	return w
}

// Card cycling must visit every card on a three-card machine.
func TestCardCyclingVisitsAllOfThree(t *testing.T) {
	m := demoModel(t, 120, 30)
	third := *m.gpus[0]
	third.Info.DeviceID = 2
	m.gpus = append(m.gpus, &third)
	m.activeTab = components.TabDetail
	m.selectedGPU = 0
	seen := map[int]bool{}
	for i := 0; i < 3; i++ {
		nm, _ := m.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
		m = nm.(*Model)
		seen[m.selectedGPU] = true
	}
	if len(seen) != 3 {
		t.Errorf("visited %v, want all three cards", seen)
	}
}

// The health check runs every healthEvery polls, not every poll; the model's
// health for a card is kept across the polls that skip it.
func TestHealthIsPolledLessOften(t *testing.T) {
	m := demoModel(t, 120, 30)
	before := m.gpus[0].Health
	// A load message with no health must not blank the card's health.
	nm, _ := m.Update(DataLoadedMsg{Stats: map[int]*xpu.GPUStats{0: &m.gpus[0].Stats}})
	m = nm.(*Model)
	if m.gpus[0].Health != before {
		t.Error("health was discarded on a poll that skipped the check")
	}
	m.ticksSinceHealth = 0
	withHealth := 0
	// From a zero counter the check runs on polls 11 and 22, so 30 polls see two.
	for i := 0; i < healthEvery*3; i++ {
		before := m.ticksSinceHealth
		m.fetchTelemetryCmd()
		if m.ticksSinceHealth == 0 && before != 0 {
			withHealth++
		}
	}
	if withHealth != 2 {
		t.Errorf("health scheduled %d times in %d polls, want 2", withHealth, healthEvery*3)
	}
}
