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
		if len(th.MoreGPUs) != 6 {
			t.Errorf("%s: %d extra GPU colours, want 6", th.Name, len(th.MoreGPUs))
		}
		for _, c := range append([]string{th.Primary, th.Secondary, th.Accent, th.Success, th.Warning, th.Danger,
			th.Bg, th.CardBg, th.Border, th.Grid, th.Text, th.Muted, th.Subtle, th.GPU0, th.GPU1}, th.MoreGPUs...) {
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

func TestHeat(t *testing.T) {
	defer SetTheme("default")
	SetTheme("default")
	r, g, b, _ := Heat(0).RGBA()
	if r>>8 != 0x10 || g>>8 != 0xB9 || b>>8 != 0x81 {
		t.Errorf("Heat(0) = %x %x %x, want Success", r>>8, g>>8, b>>8)
	}
	r, g, b, _ = Heat(1).RGBA()
	if r>>8 != 0xEF || g>>8 != 0x44 || b>>8 != 0x44 {
		t.Errorf("Heat(1) = %x %x %x, want Danger", r>>8, g>>8, b>>8)
	}
}
