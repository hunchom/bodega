package theme

import "github.com/charmbracelet/lipgloss"

var NoColor bool

// Palette — muted, one-accent. Gruvbox material ancestry.
var (
	colAccent = lipgloss.Color("#D79921")
	colOK     = lipgloss.Color("#689D6A")
	colErr    = lipgloss.Color("#CC241D")
	colWarn   = lipgloss.Color("#D65D0E")
	colMuted  = lipgloss.Color("#928374")
	colBorder = lipgloss.Color("#504945")
	colText   = lipgloss.Color("#EBDBB2")
)

var (
	Accent   lipgloss.Style
	OK       lipgloss.Style
	Err      lipgloss.Style
	Warn     lipgloss.Style
	Muted    lipgloss.Style
	Text     lipgloss.Style
	Bold     lipgloss.Style
	Header   lipgloss.Style
	Border   lipgloss.Border
	BorderFG lipgloss.Color
)

func init() { Load() }

func Load() {
	mk := func(c lipgloss.Color) lipgloss.Style {
		s := lipgloss.NewStyle()
		if !NoColor {
			s = s.Foreground(c)
		}
		return s
	}
	Accent = mk(colAccent)
	OK = mk(colOK)
	Err = mk(colErr).Bold(true)
	Warn = mk(colWarn)
	Muted = mk(colMuted)
	Text = mk(colText)
	Bold = lipgloss.NewStyle().Bold(true)
	Header = mk(colMuted).Bold(true)
	Border = lipgloss.Border{
		Top: "─", Bottom: "─", Left: "│", Right: "│",
		TopLeft: "┌", TopRight: "┐", BottomLeft: "└", BottomRight: "┘",
		MiddleLeft: "├", MiddleRight: "┤", Middle: "┼",
		MiddleTop: "┬", MiddleBottom: "┴",
	}
	BorderFG = colBorder
}
