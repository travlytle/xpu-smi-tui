package styles

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Theme is a palette. Paint themes also set the terminal's background and
// foreground; the default keeps the terminal's own.
type Theme struct {
	Name                       string
	Paint                      bool
	Primary, Secondary, Accent string
	Success, Warning, Danger   string
	Bg, CardBg, Border, Grid   string
	Text, Muted, Subtle        string
	GPU0, GPU1                 string
	badges                     *badgeColors
}

type badgeColors struct {
	okFg, okBg, warnFg, warnBg, dangerFg, dangerBg, infoFg, infoBg, live, paused, demo string
}

var Themes = []Theme{
	{
		Name: "default", Primary: "#00C7FD", Secondary: "#0071C5", Accent: "#818CF8",
		Success: "#10B981", Warning: "#F59E0B", Danger: "#EF4444",
		Bg: "#0F172A", CardBg: "#1E293B", Border: "#334155", Grid: "#334155",
		Text: "#F8FAFC", Muted: "#94A3B8", Subtle: "#64748B",
		GPU0: "#00F5FF", GPU1: "#E056FD",
		badges: &badgeColors{
			okFg: "#065F46", okBg: "#A7F3D0", warnFg: "#92400E", warnBg: "#FDE68A",
			dangerFg: "#991B1B", dangerBg: "#FECACA", infoFg: "#1E40AF", infoBg: "#BFDBFE",
			live: "#10B981", paused: "#F59E0B", demo: "#A855F7",
		},
	},
	{
		Name: "dracula", Paint: true, Primary: "#8BE9FD", Secondary: "#BD93F9", Accent: "#BD93F9",
		Success: "#50FA7B", Warning: "#FFB86C", Danger: "#FF5555",
		Bg: "#282A36", CardBg: "#44475A", Border: "#6272A4", Grid: "#44475A",
		Text: "#F8F8F2", Muted: "#BFBFBF", Subtle: "#6272A4",
		GPU0: "#8BE9FD", GPU1: "#FF79C6",
	},
	{
		Name: "nord", Paint: true, Primary: "#88C0D0", Secondary: "#5E81AC", Accent: "#B48EAD",
		Success: "#A3BE8C", Warning: "#EBCB8B", Danger: "#BF616A",
		Bg: "#2E3440", CardBg: "#3B4252", Border: "#4C566A", Grid: "#3B4252",
		Text: "#ECEFF4", Muted: "#D8DEE9", Subtle: "#7B88A1",
		GPU0: "#88C0D0", GPU1: "#B48EAD",
	},
	{
		Name: "gruvbox", Paint: true, Primary: "#83A598", Secondary: "#458588", Accent: "#D3869B",
		Success: "#B8BB26", Warning: "#FABD2F", Danger: "#FB4934",
		Bg: "#282828", CardBg: "#3C3836", Border: "#504945", Grid: "#3C3836",
		Text: "#EBDBB2", Muted: "#BDAE93", Subtle: "#928374",
		GPU0: "#8EC07C", GPU1: "#D3869B",
	},
	{
		Name: "catppuccin-mocha", Paint: true, Primary: "#89DCEB", Secondary: "#89B4FA", Accent: "#CBA6F7",
		Success: "#A6E3A1", Warning: "#FAB387", Danger: "#F38BA8",
		Bg: "#1E1E2E", CardBg: "#313244", Border: "#45475A", Grid: "#313244",
		Text: "#CDD6F4", Muted: "#A6ADC8", Subtle: "#7F849C",
		GPU0: "#89DCEB", GPU1: "#F5C2E7",
	},
	{
		Name: "catppuccin-latte", Paint: true, Primary: "#1E66F5", Secondary: "#04A5E5", Accent: "#8839EF",
		Success: "#40A02B", Warning: "#DF8E1D", Danger: "#D20F39",
		Bg: "#EFF1F5", CardBg: "#CCD0DA", Border: "#ACB0BE", Grid: "#CCD0DA",
		Text: "#4C4F69", Muted: "#5C5F77", Subtle: "#8C8FA1",
		GPU0: "#209FB5", GPU1: "#EA76CB",
	},
	{
		Name: "tokyo-night", Paint: true, Primary: "#7DCFFF", Secondary: "#7AA2F7", Accent: "#BB9AF7",
		Success: "#9ECE6A", Warning: "#E0AF68", Danger: "#F7768E",
		Bg: "#1A1B26", CardBg: "#24283B", Border: "#414868", Grid: "#292E42",
		Text: "#C0CAF5", Muted: "#A9B1D6", Subtle: "#565F89",
		GPU0: "#7DCFFF", GPU1: "#BB9AF7",
	},
	{
		Name: "solarized-dark", Paint: true, Primary: "#2AA198", Secondary: "#268BD2", Accent: "#6C71C4",
		Success: "#859900", Warning: "#B58900", Danger: "#DC322F",
		Bg: "#002B36", CardBg: "#073642", Border: "#586E75", Grid: "#073642",
		Text: "#EEE8D5", Muted: "#93A1A1", Subtle: "#657B83",
		GPU0: "#2AA198", GPU1: "#D33682",
	},
	{
		Name: "one-dark", Paint: true, Primary: "#56B6C2", Secondary: "#61AFEF", Accent: "#C678DD",
		Success: "#98C379", Warning: "#E5C07B", Danger: "#E06C75",
		Bg: "#282C34", CardBg: "#3E4451", Border: "#4B5263", Grid: "#3E4451",
		Text: "#ABB2BF", Muted: "#9DA5B4", Subtle: "#5C6370",
		GPU0: "#56B6C2", GPU1: "#C678DD",
	},
	{
		Name: "monokai", Paint: true, Primary: "#66D9EF", Secondary: "#66D9EF", Accent: "#AE81FF",
		Success: "#A6E22E", Warning: "#FD971F", Danger: "#F92672",
		Bg: "#272822", CardBg: "#3E3D32", Border: "#49483E", Grid: "#3E3D32",
		Text: "#F8F8F2", Muted: "#CFCFC2", Subtle: "#75715E",
		GPU0: "#66D9EF", GPU1: "#AE81FF",
	},
	{
		Name: "rose-pine", Paint: true, Primary: "#9CCFD8", Secondary: "#31748F", Accent: "#C4A7E7",
		Success: "#3E8FB0", Warning: "#F6C177", Danger: "#EB6F92",
		Bg: "#191724", CardBg: "#26233A", Border: "#403D52", Grid: "#26233A",
		Text: "#E0DEF4", Muted: "#908CAA", Subtle: "#6E6A86",
		GPU0: "#9CCFD8", GPU1: "#EBBCBA",
	},
}

// ThemeNames lists the names accepted by SetTheme.
func ThemeNames() []string {
	names := make([]string, len(Themes))
	for i, t := range Themes {
		names[i] = t.Name
	}
	return names
}

var current int

// Current is the active theme.
func Current() Theme { return Themes[current] }

// SetTheme applies the named theme.
func SetTheme(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = "default"
	}
	for i, t := range Themes {
		if t.Name == name {
			current = i
			apply(t)
			return nil
		}
	}
	return fmt.Errorf("unknown theme %q (one of: %s)", name, strings.Join(ThemeNames(), ", "))
}

// NextTheme applies the theme after the current one and returns it.
func NextTheme() Theme {
	current = (current + 1) % len(Themes)
	apply(Themes[current])
	return Themes[current]
}

// TerminalColors returns the background and foreground a theme paints the
// terminal with; nil leaves the terminal's own.
func TerminalColors() (bg, fg color.Color) {
	t := Current()
	if !t.Paint {
		return nil, nil
	}
	return lipgloss.Color(t.Bg), lipgloss.Color(t.Text)
}

var (
	ColorPrimary, ColorSecondary, ColorAccent color.Color
	ColorSuccess, ColorWarning, ColorDanger   color.Color
	ColorBg, ColorCardBg, ColorBorder         color.Color
	ColorText, ColorMuted, ColorSubtle        color.Color
	ColorGPU0, ColorGPU1, ColorGrid           color.Color

	TitleStyle, SubtitleStyle, BoldText, RegularText     lipgloss.Style
	MutedText, SubtleText, HighlightText                 lipgloss.Style
	BadgeOk, BadgeWarn, BadgeDanger, BadgeInfo           lipgloss.Style
	BadgeLive, BadgePaused, BadgeDemo                    lipgloss.Style
	CardStyle, ActiveCardStyle, HeaderStyle, FooterStyle lipgloss.Style
	TabActive, TabInactive, KeyHintKey, KeyHintDesc      lipgloss.Style
)

func init() { apply(Themes[0]) }

func apply(t Theme) {
	c := lipgloss.Color
	ColorPrimary, ColorSecondary, ColorAccent = c(t.Primary), c(t.Secondary), c(t.Accent)
	ColorSuccess, ColorWarning, ColorDanger = c(t.Success), c(t.Warning), c(t.Danger)
	ColorBg, ColorCardBg, ColorBorder, ColorGrid = c(t.Bg), c(t.CardBg), c(t.Border), c(t.Grid)
	ColorText, ColorMuted, ColorSubtle = c(t.Text), c(t.Muted), c(t.Subtle)
	ColorGPU0, ColorGPU1 = c(t.GPU0), c(t.GPU1)

	b := t.badges
	if b == nil {
		b = &badgeColors{
			okFg: t.Bg, okBg: t.Success, warnFg: t.Bg, warnBg: t.Warning,
			dangerFg: t.Bg, dangerBg: t.Danger, infoFg: t.Bg, infoBg: t.Secondary,
			live: t.Success, paused: t.Warning, demo: t.Accent,
		}
	}

	TitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
	SubtitleStyle = lipgloss.NewStyle().Foreground(ColorMuted)
	BoldText = lipgloss.NewStyle().Bold(true).Foreground(ColorText)
	RegularText = lipgloss.NewStyle().Foreground(ColorText)
	MutedText = lipgloss.NewStyle().Foreground(ColorMuted)
	SubtleText = lipgloss.NewStyle().Foreground(ColorSubtle)
	HighlightText = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)

	badge := func(fg, bg string) lipgloss.Style {
		return lipgloss.NewStyle().Bold(true).Foreground(c(fg)).Background(c(bg)).Padding(0, 1)
	}
	BadgeOk = badge(b.okFg, b.okBg)
	BadgeWarn = badge(b.warnFg, b.warnBg)
	BadgeDanger = badge(b.dangerFg, b.dangerBg)
	BadgeInfo = badge(b.infoFg, b.infoBg)
	BadgeLive = lipgloss.NewStyle().Bold(true).Foreground(c(b.live)).SetString("● LIVE")
	BadgePaused = lipgloss.NewStyle().Bold(true).Foreground(c(b.paused)).SetString("⏸ PAUSED")
	BadgeDemo = lipgloss.NewStyle().Bold(true).Foreground(c(b.demo)).SetString("✦ DEMO")

	CardStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorBorder).Padding(0, 1)
	ActiveCardStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorPrimary).Padding(0, 1)
	// No margins: every row of chrome is a row less of telemetry.
	HeaderStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, true, false).BorderForeground(ColorBorder).Padding(0, 1)
	FooterStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(ColorBorder).Padding(0, 1)
	TabActive = lipgloss.NewStyle().Bold(true).Foreground(ColorBg).Background(ColorPrimary).Padding(0, 1).MarginRight(1)
	TabInactive = lipgloss.NewStyle().Foreground(ColorMuted).Background(ColorCardBg).Padding(0, 1).MarginRight(1)
	KeyHintKey = lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
	KeyHintDesc = lipgloss.NewStyle().Foreground(ColorMuted)
}
