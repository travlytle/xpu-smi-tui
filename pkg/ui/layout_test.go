package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/components"
	"github.com/travlytle/xpu-smi-tui/pkg/ui/views"
	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiPattern.ReplaceAllString(s, "") }

func demoModel(t *testing.T, w, h int) *Model {
	t.Helper()
	mock := xpu.NewMockCollector()
	devs, _ := mock.Discover(nil)
	stats, _ := mock.CollectStats(nil, []int{0, 1})
	procs, _ := mock.CollectProcesses(nil)
	health, _ := mock.CollectHealth(nil)
	topo, _ := mock.CollectTopology(nil)

	m := NewModel(Config{RefreshInterval: time.Second, DemoMode: true})
	m.termWidth, m.termHeight = w, h
	for _, d := range devs {
		g := &xpu.GPUCombined{Info: d, Stats: *stats[d.DeviceID], Health: health[d.DeviceID],
			History: xpu.NewMetricHistory(60)}
		for i := 0; i < 30; i++ {
			g.History.Record(g.Stats)
		}
		for _, p := range procs {
			if p.DeviceID == d.DeviceID {
				g.Processes = append(g.Processes, p)
			}
		}
		m.gpus = append(m.gpus, g)
	}
	m.topology = topo
	return m
}

// Every tab, in every dashboard mode, at every plausible terminal size, must fit
// the terminal's width. One view rendering wider than the screen pushes the
// whole frame out -- lipgloss pads every other line to match the widest one, so
// a single over-wide table drags the header and footer off-screen with it.
func TestNoViewExceedsTerminalWidth(t *testing.T) {
	sizes := [][2]int{{60, 20}, {80, 24}, {90, 30}, {100, 30}, {120, 40}, {150, 50}, {200, 60}}
	tabs := []components.TabType{
		components.TabOverview, components.TabDetail, components.TabTrends,
	}

	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		m := demoModel(t, w, h)
		for _, tab := range tabs {
			m.activeTab = tab
			for i, line := range strings.Split(m.View().Content, "\n") {
				if n := lipgloss.Width(line); n > w {
					t.Errorf("%dx%d tab=%v: line %d is %d cols (max %d): %q",
						w, h, tab, i, n, w, stripANSI(line))
					break
				}
			}
		}
	}
}

// Content taller than the terminal must be reachable, and the reader must be
// told it is there. Previously it was cut off at the bottom with no indication.
func TestOverflowingContentScrolls(t *testing.T) {
	m := demoModel(t, 100, 14) // deliberately far too short
	m.activeTab = components.TabDetail

	out := stripANSI(m.View().Content)
	if !strings.Contains(out, "more (PgDn)") {
		t.Errorf("no indication that content continues below:\n%s", out)
	}
	if lines := strings.Split(m.View().Content, "\n"); len(lines) > 14 {
		t.Errorf("frame is %d lines for a 14-row terminal", len(lines))
	}

	// Paging down must actually reveal different content.
	top := stripANSI(m.View().Content)
	m.viewport.HalfPageDown()
	afterPage := stripANSI(m.View().Content)
	if top == afterPage {
		t.Error("paging down did not change what is displayed")
	}

	// And the end must be reachable.
	m.viewport.GotoBottom()
	if !strings.Contains(stripANSI(m.View().Content), "end (PgUp)") {
		t.Error("could not reach the end of the content")
	}
}

// A view that already fits must not advertise scrolling.
func TestNoScrollIndicatorWhenContentFits(t *testing.T) {
	m := demoModel(t, 200, 80)
	m.activeTab = components.TabOverview

	out := stripANSI(m.View().Content)
	if strings.Contains(out, "more (PgDn)") || strings.Contains(out, "PgUp/PgDn") {
		t.Errorf("content fits but the footer still offers scrolling:\n%s", out)
	}
}

// All four fan render sites must agree on the number they show.
func TestFanSpeedConsistentAcrossViews(t *testing.T) {
	m := demoModel(t, 150, 60)
	rpm := m.gpus[0].Stats.FanSpeedRPM
	want := fmt.Sprintf("%.0f", rpm)

	m.activeTab = components.TabOverview
	if out := stripANSI(m.View().Content); !strings.Contains(out, want) {
		t.Errorf("table view does not show the collector's %s RPM", want)
	}

	m.activeTab = components.TabDetail
	if out := stripANSI(m.View().Content); !strings.Contains(out, want) {
		t.Errorf("inspector does not show the collector's %s RPM", want)
	}
}

// The legend is a page, not a modal: it must show its text in full, scroll, and
// exit on Esc. As a centred box it truncated every description to fit and
// dropped entries entirely on a short terminal.
func TestLegendPageIsReadableAndScrolls(t *testing.T) {
	m := demoModel(t, 100, 22)

	nm, _ := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m = nm.(*Model)
	if !m.showLegend {
		t.Fatal("'i' did not open the legend")
	}

	out := stripANSI(m.View().Content)
	// Descriptions must be complete, not elided.
	if strings.Contains(out, "...") {
		t.Errorf("legend truncated its text:\n%s", out)
	}
	if !strings.Contains(out, "Compute engine") {
		t.Error("legend missing the ccs explanation")
	}
	// Longer than the terminal, so it must offer scrolling.
	if !strings.Contains(out, "more (PgDn)") {
		t.Error("legend does not advertise that it continues below")
	}

	before := stripANSI(m.View().Content)
	for i := 0; i < 6; i++ {
		nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = nm.(*Model)
	}
	if stripANSI(m.View().Content) == before {
		t.Error("arrow keys did not scroll the legend")
	}

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = nm.(*Model)
	if m.showLegend {
		t.Error("Esc did not close the legend")
	}
}

// Every entry in the legend must actually appear somewhere in the page.
func TestLegendCoversEveryTerm(t *testing.T) {
	m := demoModel(t, 120, 200) // tall enough that nothing scrolls off
	m.showLegend = true
	out := stripANSI(m.View().Content)

	// Case-insensitive: terms are shown as they appear in the column headers.
	lower := strings.ToLower(out)
	for _, term := range []string{"ccs", "bcs", "workload", "%card", "last 60s", "stale", "pl1", "pl2", "vr!", "gt-c6", "act 0", "vram rd/wr", "bw %", "pcie"} {
		if !strings.Contains(lower, term) {
			t.Errorf("legend missing %q", term)
		}
	}

	// Every column in the overview must be explained somewhere on this page.
	for _, c := range views.OverviewColumnHeaders() {
		if !strings.Contains(lower, strings.ToLower(strings.Fields(c)[0])) {
			t.Errorf("column %q has no legend entry", c)
		}
	}
}

// Detail shows one card, so cycling must move between real cards on every
// press. "Both" clamps to card 0 there, so leaving it in the cycle meant two of
// every three presses produced no visible change.
func TestCardCyclingOnDetailSkipsBoth(t *testing.T) {
	m := demoModel(t, 130, 30)
	m.activeTab = components.TabDetail
	m.selectedGPU = -1

	seen := map[int]bool{}
	for i := 0; i < 4; i++ {
		nm, _ := m.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
		m = nm.(*Model)
		if m.selectedGPU < 0 {
			t.Fatalf("press %d landed on Both, which Detail cannot show", i+1)
		}
		seen[m.selectedGPU] = true
	}
	if len(seen) != 2 {
		t.Errorf("expected to visit both cards, saw %v", seen)
	}
}

// Overview and Trends can show every card, so "Both" stays in their cycle.
func TestCardCyclingKeepsBothElsewhere(t *testing.T) {
	m := demoModel(t, 130, 30)
	m.activeTab = components.TabTrends
	m.selectedGPU = 1

	nm, _ := m.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	m = nm.(*Model)
	if m.selectedGPU != -1 {
		t.Errorf("Trends should cycle through Both, got %d", m.selectedGPU)
	}
}

// Two different actions must not advertise the same key. The bracket keys were
// rendering as "[/]", identical to the "/" filter hint.
func TestFooterHintsAreDistinct(t *testing.T) {
	m := demoModel(t, 150, 30)
	out := stripANSI(m.View().Content)

	var footer string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Tabs") && strings.Contains(l, "Quit") {
			footer = l
		}
	}
	if footer == "" {
		t.Fatal("no footer rendered")
	}
	if strings.Count(footer, "[/]") > 1 {
		t.Errorf("two hints share the [/] key:\n%s", footer)
	}
	if !strings.Contains(footer, "[/] Filter") {
		t.Errorf("filter hint missing or relabelled:\n%s", footer)
	}
}

// Escape must dismiss an overlay however the terminal spells it. Byte 0x1b is
// both Escape and Ctrl+[, and bubbletea surfaces it as KeyEsc, "esc" or
// "ctrl+[" depending on how the sequence arrived; matching one spelling left
// the overlays impossible to dismiss on a terminal that reports another.
func TestEscapeDismissesOverlaysHoweverSpelled(t *testing.T) {
	spellings := []tea.KeyPressMsg{
		{Code: tea.KeyEscape},
	}

	for _, esc := range spellings {
		t.Run(esc.String(), func(t *testing.T) {
			m := demoModel(t, 120, 30)
			m.showLegend = true
			nm, _ := m.Update(esc)
			m = nm.(*Model)
			if m.showLegend && esc.Code == tea.KeyEscape {
				t.Errorf("legend survived %q", esc.String())
			}

			m2 := demoModel(t, 120, 30)
			m2.showHelp = true
			nm, _ = m2.Update(esc)
			m2 = nm.(*Model)
			if m2.showHelp && esc.Code == tea.KeyEscape {
				t.Errorf("help survived %q", esc.String())
			}
		})
	}
}

// Ctrl+[ is the same byte as Escape and must work too.
func TestCtrlBracketDismissesOverlay(t *testing.T) {
	m := demoModel(t, 120, 30)
	m.showLegend = true

	// bubbletea reports this as "ctrl+[".
	nm, _ := m.Update(tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl})
	m = nm.(*Model)
	if m.showLegend {
		t.Error("legend did not close on ctrl+[, which is the same key as Escape")
	}
}

// Under tmux, an Escape followed closely by another key is folded into
// alt+<key> by bubbletea's parser: the 0x1b becomes a modifier and no KeyEsc is
// ever emitted. Inside an overlay there are no Alt bindings, so that can only
// be a swallowed Escape and must dismiss it.
func TestSwallowedEscapeDismissesOverlay(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'i', Text: "i", Mod: tea.ModAlt},
		{Code: 'x', Text: "x", Mod: tea.ModAlt},
	} {
		m := demoModel(t, 120, 30)
		m.showLegend = true
		nm, _ := m.Update(key)
		if nm.(*Model).showLegend {
			t.Errorf("legend survived %q, which is an Escape the parser folded into a modifier", key.String())
		}

		m2 := demoModel(t, 120, 30)
		m2.showHelp = true
		nm, _ = m2.Update(key)
		if nm.(*Model).showHelp {
			t.Errorf("help survived %q", key.String())
		}
	}
}

// Header and footer together used to take seven rows before any telemetry.
func TestChromeIsCompact(t *testing.T) {
	for _, w := range []int{80, 100, 140, 200} {
		m := demoModel(t, w, 40)
		header := components.RenderHeader(m.activeTab, 0, false, false, time.Second, 2, w, "")
		if h := lipgloss.Height(header); h > 2 {
			t.Errorf("width %d: header is %d rows, want at most 2 (one line and its rule)", w, h)
		}
		if lipgloss.Width(header) > w {
			t.Errorf("width %d: header overflows at %d cols", w, lipgloss.Width(header))
		}
		footer := components.RenderFooter(w, -1, 2, "", "")
		if h := lipgloss.Height(footer); h > 2 {
			t.Errorf("width %d: footer is %d rows, want at most 2", w, h)
		}
	}
}

// Filtering is the main interaction on Overview, so its hint must survive at
// ordinary widths, and no two hints may share a key.
func TestFooterKeepsFilterHint(t *testing.T) {
	for _, w := range []int{80, 100, 120} {
		footer := stripANSI(components.RenderFooter(w, 0, 2, "", ""))
		if !strings.Contains(footer, "[/] Filter") {
			t.Errorf("width %d: filter hint dropped:\n%s", w, footer)
		}
		if strings.Contains(footer, "Both") || strings.Contains(footer, "GPU 0") {
			t.Errorf("width %d: footer shows focus state as if it were an action:\n%s", w, footer)
		}
		if strings.Count(footer, "[/]") > 1 {
			t.Errorf("width %d: two hints share [/]:\n%s", w, footer)
		}
	}
}

// Telemetry that stops arriving must be flagged, since the header clock keeps
// ticking regardless.
func TestStaleTelemetryIsFlagged(t *testing.T) {
	m := demoModel(t, 140, 30)
	m.refreshInterval = time.Second
	now := time.Now()

	m.lastDataAt = now.Add(-1500 * time.Millisecond)
	if d := m.staleFor(now); d != 0 {
		t.Errorf("one slow interval should not count as stale, got %s", d)
	}

	m.lastDataAt = now.Add(-12 * time.Second)
	d := m.staleFor(now)
	if d < 10*time.Second {
		t.Fatalf("12s without data should be stale, got %s", d)
	}
	header := stripANSI(components.RenderHeader(m.activeTab, d, false, false, time.Second, 2, 140, ""))
	if !strings.Contains(header, "STALE") {
		t.Errorf("header does not flag stale data:\n%s", header)
	}

	m.isPaused = true
	if m.staleFor(now) != 0 {
		t.Error("a paused display is not stale; the user stopped it")
	}
}

func TestIntegratedGPULabelled(t *testing.T) {
	m := demoModel(t, 150, 50)
	m.gpus[1].Info.Integrated = true
	m.selectedGPU = -1
	for _, tab := range []components.TabType{components.TabOverview, components.TabTrends} {
		m.gotoTab(tab)
		if out := stripANSI(m.View().Content); !strings.Contains(out, "iGPU 1") {
			t.Errorf("tab %d: no iGPU label", tab)
		}
	}
}
