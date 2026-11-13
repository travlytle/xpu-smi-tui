package components

import (
	"fmt"
	"strings"
	"time"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"

	"charm.land/lipgloss/v2"
)

type TabType int

// Three tabs, down from five plus three dashboard modes.
//
// The old surfaces mostly restated the same six metrics in different idioms:
// power appeared in five places, VRAM in six, device identity in six -- while
// the throttle reason, the only thing that explains why a card is slow,
// appeared in one, three keystrokes away. Overview now answers the operator's
// questions directly, Detail holds everything about one card, and Trends is the
// opt-in history. Topology is gone: it was static, read once ever, and its
// two-column branch rendered unreadably between 125 and 190 columns.
const (
	TabOverview TabType = iota
	TabDetail
	TabTrends
)

const TabCount = 3

var TabNames = []string{
	"1: Overview",
	"2: Detail",
	"3: Trends",
}

var ShortTabNames = []string{
	"1: Over",
	"2: Detail",
	"3: Trends",
}

var TinyTabNames = []string{
	"1",
	"2",
	"3",
}

// RenderHeader renders the top navigation bar with tabs and status
// ram is a preformatted host-RAM figure ("RAM 7/123G") shown in the richest
// header variant, or "" to omit it.
func RenderHeader(activeTab TabType, staleFor time.Duration, paused bool, isDemo bool, refreshInterval time.Duration, gpuCount int, width int, ram string) string {
	if width < 12 {
		width = 12
	}
	avail := width - 4

	// A stale reading outranks every other status: it means the numbers below
	// are old, whatever the badge would otherwise say.
	var badge string
	switch {
	case staleFor > 0:
		badge = styles.BadgeWarn.Render(fmt.Sprintf("⚠ STALE %s", staleFor))
	case isDemo:
		badge = styles.BadgeDemo.Render()
	case paused:
		badge = styles.BadgePaused.Render()
	default:
		badge = styles.BadgeLive.Render()
	}

	interval := fmt.Sprintf("%ds", int(refreshInterval.Seconds()))
	clock := time.Now().Format("15:04:05")

	tabs := func(names []string, pad int) string {
		var views []string
		for i, name := range names {
			style := styles.TabInactive
			if TabType(i) == activeTab {
				style = styles.TabActive
			}
			views = append(views, style.Padding(0, pad).Render(name))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, views...)
	}

	// Title, tabs and status share one row. Each variant is measured and the
	// richest that fits is used, rather than gating each part on its own width
	// threshold: separate thresholds do not compose, which is how the previous
	// header wrapped into nine rows at around 100 columns.
	brand := styles.TitleStyle.Render("⚡ XPU-SMI")
	variants := []struct {
		tabs, right string
	}{
		{tabs(TabNames, 2), strings.Join(nonEmpty(ram, fmt.Sprintf("%s | %d GPUs | %s", interval, gpuCount, clock)), " | ")},
		{tabs(ShortTabNames, 1), fmt.Sprintf("%s | %s", interval, clock)},
		{tabs(TinyTabNames, 1), interval},
	}
	for _, v := range variants {
		right := lipgloss.JoinHorizontal(lipgloss.Center, badge, " ", styles.SubtleText.Render(v.right))
		used := lipgloss.Width(brand) + 2 + lipgloss.Width(v.tabs) + 1 + lipgloss.Width(right)
		if used > avail {
			continue
		}
		row := lipgloss.JoinHorizontal(lipgloss.Center,
			brand, "  ", v.tabs, strings.Repeat(" ", avail-used+1), right)
		return styles.HeaderStyle.Width(width - 2).Render(row)
	}

	// Too narrow for one row: tabs go underneath.
	top := lipgloss.JoinHorizontal(lipgloss.Center, brand, "  ", badge)
	return styles.HeaderStyle.Width(width - 2).Render(
		lipgloss.JoinVertical(lipgloss.Left, top, tabs(TinyTabNames, 1)))
}

// nonEmpty drops empty strings, so an absent figure leaves no stray separator.
func nonEmpty(parts ...string) []string {
	out := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
