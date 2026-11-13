package styles

import "charm.land/lipgloss/v2"

var (
	// Colors
	ColorPrimary   = lipgloss.Color("#00C7FD") // Intel Cyan
	ColorSecondary = lipgloss.Color("#0071C5") // Intel Deep Blue
	ColorAccent    = lipgloss.Color("#818CF8") // Indigo/Purple
	ColorSuccess   = lipgloss.Color("#10B981") // Emerald Green
	ColorWarning   = lipgloss.Color("#F59E0B") // Amber
	ColorDanger    = lipgloss.Color("#EF4444") // Coral Red
	ColorBg        = lipgloss.Color("#0F172A") // Slate 900
	ColorCardBg    = lipgloss.Color("#1E293B") // Slate 800
	ColorBorder    = lipgloss.Color("#334155") // Slate 700
	ColorText      = lipgloss.Color("#F8FAFC") // Slate 50
	ColorMuted     = lipgloss.Color("#94A3B8") // Slate 400
	ColorSubtle    = lipgloss.Color("#64748B") // Slate 500

	// Dedicated high-luminance Neon GPU series colors for maximum contrast (btop-style punch)
	ColorGPU0 = lipgloss.Color("#00F5FF") // Neon Electric Cyan (high luminance)
	ColorGPU1 = lipgloss.Color("#E056FD") // Vibrant Electric Magenta / Violet (high luminance)
	ColorGrid = lipgloss.Color("#334155") // Subtle slate for guide lines

	// Text Styles
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	BoldText = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorText)

	RegularText = lipgloss.NewStyle().
			Foreground(ColorText)

	MutedText = lipgloss.NewStyle().
			Foreground(ColorMuted)

	SubtleText = lipgloss.NewStyle().
			Foreground(ColorSubtle)

	HighlightText = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	// Badges & Pills
	BadgeOk = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#065F46")).
		Background(lipgloss.Color("#A7F3D0")).
		Padding(0, 1)

	BadgeWarn = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#92400E")).
			Background(lipgloss.Color("#FDE68A")).
			Padding(0, 1)

	BadgeDanger = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#991B1B")).
			Background(lipgloss.Color("#FECACA")).
			Padding(0, 1)

	BadgeInfo = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E40AF")).
			Background(lipgloss.Color("#BFDBFE")).
			Padding(0, 1)

	BadgeLive = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#10B981")).
			SetString("● LIVE")

	BadgePaused = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F59E0B")).
			SetString("⏸ PAUSED")

	BadgeDemo = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#A855F7")).
			SetString("✦ DEMO")

	// Boxes & Cards
	CardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	ActiveCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary).
			Padding(0, 1)

	// Header and footer carry no margins. Each spent a blank row on spacing,
	// which together with a two-row title block came to seven rows of chrome on
	// every screen before any telemetry was drawn.
	HeaderStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	FooterStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	TabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0F172A")).
			Background(ColorPrimary).
			Padding(0, 1).
			MarginRight(1)

	TabInactive = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Background(ColorCardBg).
			Padding(0, 1).
			MarginRight(1)

	KeyHintKey = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	KeyHintDesc = lipgloss.NewStyle().
			Foreground(ColorMuted)
)
