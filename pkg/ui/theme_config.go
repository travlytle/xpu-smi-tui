package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/travlytle/xpu-smi-tui/pkg/ui/components"
	"github.com/travlytle/xpu-smi-tui/pkg/ui/views"
)

// Preferences live one per file under ~/.config/xpu-smi-tui: theme, graph
// (braille|block), trends (stripes|lines|per card).
func prefFile(name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "xpu-smi-tui", name)
}

// Pref is a saved preference, or "".
func Pref(name string) string {
	f := prefFile(name)
	if f == "" {
		return ""
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func savePref(name, value string) error {
	f := prefFile(name)
	if f == "" {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	return os.WriteFile(f, []byte(value+"\n"), 0o644)
}

// SavedTheme is the theme last chosen with the t key, or "".
func SavedTheme() string { return Pref("theme") }

func saveTheme(name string) error { return savePref("theme", name) }

// LoadGraphPrefs applies the saved graph symbols and Trends layout.
func LoadGraphPrefs() {
	components.Symbols = components.ParseGraphSymbols(Pref("graph"))
	views.Style = views.ParseTrendsStyle(Pref("trends"))
}
