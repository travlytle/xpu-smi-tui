package ui

import (
	"os"
	"path/filepath"
	"strings"
)

func themeFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "xpu-smi-tui", "theme")
}

// SavedTheme is the theme last chosen with the t key, or "".
func SavedTheme() string {
	f := themeFile()
	if f == "" {
		return ""
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func saveTheme(name string) error {
	f := themeFile()
	if f == "" {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	return os.WriteFile(f, []byte(name+"\n"), 0o644)
}
