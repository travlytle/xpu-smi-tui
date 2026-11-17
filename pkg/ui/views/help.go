package views

import (
	"fmt"
	"strings"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"

	"charm.land/lipgloss/v2"
)

// RenderHelp renders keyboard shortcuts and metric explanations modal overlay
func RenderHelp(width int, height int) string {
	modalWidth := 72
	if modalWidth > width-6 {
		modalWidth = width - 6
	}
	if modalWidth < 30 {
		modalWidth = 30 // below this the divider's Repeat count went negative
	}

	title := styles.HighlightText.Render("❓ XPU-SMI TUI - KEYBOARD SHORTCUTS & MANUAL")
	divider := styles.SubtleText.Render(strings.Repeat("─", modalWidth-4))

	shortcuts := []struct {
		key  string
		desc string
	}{
		{"1 / 2 / 3", "Overview, Detail, Trends"},
		{"Tab / Shift+Tab", "Cycle tabs forward and backward"},
		{"← / → or h / l", "Cycle tabs backward and forward"},
		{"↑ / ↓ or k / j", "Scroll the current view"},
		{"PgUp / PgDn", "Scroll a full page"},
		{"Ctrl+U / Ctrl+D", "Scroll half a page"},
		{"g / G or Home/End", "Jump to top or bottom"},
		{"[ / ]", "Focus previous / next card"},
		{"b", "Show all cards"},
		{"/", "Filter the workload list (Overview)"},
		{"Esc", "Leave the filter, then clear it"},
		{"Space", "Pause or resume polling"},
		{"r", "Refresh now"},
		{"t", "Cycle theme (saved)"},
		{"v", "Trends style: stripes / lines / per card (saved)"},
		{"s", "Per-card graphs: braille / block (saved)"},
		{"+ / -", "Poll faster / slower"},
		{"D", "Toggle demo / live mode"},
		{"i", "What the abbreviations mean"},
		{"? or F1", "Toggle this help"},
		{"q or Ctrl+C", "Quit"},
	}

	var scRows []string
	for _, sc := range shortcuts {
		k := styles.BoldText.Width(18).Render(sc.key)
		d := styles.RegularText.Render(sc.desc)
		scRows = append(scRows, fmt.Sprintf("  %s %s", k, d))
	}

	metrics := []string{
		styles.BoldText.Render("Metric Descriptions:"),
		styles.MutedText.Render("  • BUSY: GT idle residency; ccs is the compute engine inference runs on"),
		styles.MutedText.Render("  • Core / Mem Temp: die and VRAM sensors, against their own crit limits"),
		styles.MutedText.Render("  • VR Temp: voltage regulator; usually the hottest point, no hwmon sensor"),
		styles.MutedText.Render("  • Power: draw against the limit the driver reports, not a hardcoded TDP"),
	}

	closeHint := styles.HighlightText.Render("Press [?] or [Esc] to dismiss")
	hintRow := lipgloss.NewStyle().Align(lipgloss.Center).Width(modalWidth - 4).Render(closeHint)

	// The modal must fit the terminal. It used to be a fixed 36 rows regardless
	// of height, and View returns it directly rather than through the viewport,
	// so on any terminal shorter than that the frame was taller than the screen:
	// the display scrolled, and dismissing it left no visible change -- which
	// reads exactly like Esc not working.
	//
	// Chrome is 2 border rows + 2 padding rows. Shed the metric guide first,
	// then trim the shortcut list, keeping the dismiss hint whatever happens.
	const chrome = 4
	budget := height - chrome
	if budget < 6 {
		budget = 6
	}

	body := []string{title, divider, styles.BoldText.Render("Keyboard Navigation:")}
	fixed := len(body) + 2 // + blank + hint row

	showMetrics := fixed+len(scRows)+1+len(metrics) <= budget
	room := budget - fixed
	if showMetrics {
		room -= len(metrics) + 1
	}
	if room < 1 {
		room = 1
	}
	if len(scRows) > room {
		// Say so rather than silently dropping the tail.
		scRows = append(scRows[:room-1:room-1],
			styles.SubtleText.Render(fmt.Sprintf("  ... %d more (resize for the full list)", len(scRows)-room+1)))
	}

	body = append(body, strings.Join(scRows, "\n"))
	if showMetrics {
		body = append(body, "")
		body = append(body, metrics...)
	}
	body = append(body, "", hintRow)

	content := lipgloss.JoinVertical(lipgloss.Left, body...)

	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(styles.ColorPrimary).
		Padding(1, 2).
		Width(modalWidth + 2).
		Render(content)
}
