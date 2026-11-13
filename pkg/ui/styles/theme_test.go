package styles

import (
	"regexp"
	"testing"
)

func TestThemes(t *testing.T) {
	defer SetTheme("default")
	hex := regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	seen := map[string]bool{}
	for _, th := range Themes {
		if seen[th.Name] {
			t.Errorf("duplicate theme %q", th.Name)
		}
		seen[th.Name] = true
		for _, c := range []string{th.Primary, th.Secondary, th.Accent, th.Success, th.Warning, th.Danger,
			th.Bg, th.CardBg, th.Border, th.Grid, th.Text, th.Muted, th.Subtle, th.GPU0, th.GPU1} {
			if !hex.MatchString(c) {
				t.Errorf("%s: bad colour %q", th.Name, c)
			}
		}
		if err := SetTheme(th.Name); err != nil || Current().Name != th.Name {
			t.Errorf("SetTheme(%q): %v", th.Name, err)
		}
	}
	if err := SetTheme("nope"); err == nil {
		t.Error("unknown theme accepted")
	}
	SetTheme("default")
	if bg, _ := TerminalColors(); bg != nil {
		t.Error("default theme paints the terminal")
	}
	if NextTheme().Name != Themes[1].Name {
		t.Error("NextTheme did not advance")
	}
	if bg, _ := TerminalColors(); bg == nil {
		t.Error("named theme does not paint the terminal")
	}
}
