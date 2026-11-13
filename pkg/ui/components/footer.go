package components

import (
	"fmt"
	"strings"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/styles"

	"charm.land/lipgloss/v2"
)

// KeyHint represents a keyboard shortcut helper
type KeyHint struct {
	Key  string
	Desc string
}

func formatHint(key, desc string) string {
	// The bracket keys are shown as the two characters themselves. Wrapping
	// them in brackets like every other hint produced "[[/]]", and the earlier
	// workaround of passing "[/]" verbatim made them indistinguishable from the
	// "/" filter hint -- two different actions advertised with the same key.
	label := "[" + key + "]"
	if key == "[]" {
		label = key
	}
	k := styles.KeyHintKey.Render(label)
	d := styles.KeyHintDesc.Render(desc)
	return fmt.Sprintf("%s %s", k, d)
}

// RenderFooter renders the bottom status bar and shortcut hints adaptively
// RenderFooter draws the hint bar. scrollInfo, when non-empty, reports how far
// through a scrollable view the reader is; it is placed on the hint row so a
// tall view always advertises that there is more below.
func RenderFooter(width int, activeGpuIdx int, totalGpus int, statusMsg string, scrollInfo string) string {
	if width < 12 {
		width = 12
	}

	// Hints name actions, in priority order, and as many are shown as fit.
	//
	// The bar was three fixed lists chosen by width. Filtering -- the main thing
	// you do on Overview -- was missing below 115 columns; the narrowest list
	// still labelled the card keys "[/]", the same as the filter key; and each
	// list showed the focused card ("Card Both") as if it were an action. Which
	// card is focused is visible in the views themselves.
	all := []string{
		formatHint("1-3", "Tabs"),
		formatHint("[]", "Card"),
		formatHint("/", "Filter"),
		formatHint("i", "Legend"),
		formatHint("?", "Keys"),
		formatHint("q", "Quit"),
		formatHint("↑/↓", "Scroll"),
		formatHint("Space", "Pause"),
		formatHint("r", "Refresh"),
		formatHint("+/-", "Rate"),
	}
	avail := width - 4
	if scrollInfo != "" {
		avail -= lipgloss.Width(scrollInfo) + 2
	}
	var hints []string
	used := 0
	for _, h := range all {
		w := lipgloss.Width(h)
		if len(hints) > 0 {
			w++
		}
		if used+w > avail {
			break
		}
		hints = append(hints, h)
		used += w
	}
	_ = activeGpuIdx
	_ = totalGpus

	hintBar := strings.Join(hints, " ")
	if scrollInfo != "" {
		marker := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorPrimary).Render(scrollInfo)
		if lipgloss.Width(hintBar)+lipgloss.Width(marker)+2 <= width-2 {
			hintBar = lipgloss.JoinHorizontal(lipgloss.Left, hintBar, "  ", marker)
		} else {
			// No room beside the hints; the scroll state matters more than the
			// last few reminders, so it replaces them.
			hintBar = marker
		}
	}

	var statusLine string
	if statusMsg != "" {
		statusLine = lipgloss.NewStyle().Foreground(styles.ColorWarning).Render(statusMsg)
	}

	content := hintBar
	if statusLine != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, statusLine, hintBar)
	}

	return styles.FooterStyle.Width(width - 2).Render(content)
}
