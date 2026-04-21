package browse

import "github.com/charmbracelet/lipgloss"

// Muted palette — professional, low-saturation, picking up where the
// existing theme package leaves off but tuned for the browse TUI's denser
// layout. The colors are deliberately dim so the cursor row, the amber
// "installed" marker, and the green/red status glyphs stand out without
// shouting.
var (
	colBorder   = lipgloss.Color("#3A3A3A") // dim slate for pane rulings
	colBorderHi = lipgloss.Color("#5A5A5A") // focused pane ruling
	colText     = lipgloss.Color("#CFCFCF") // primary text
	colDim      = lipgloss.Color("#6B7280") // captions, secondary info
	colFaint    = lipgloss.Color("#4B5563") // version-when-not-installed
	colAmber    = lipgloss.Color("#D7A663") // installed version + subtle accents
	colGreen    = lipgloss.Color("#87A987") // ✓ installed
	colRed      = lipgloss.Color("#B4656F") // remove? confirm
	colBlue     = lipgloss.Color("#7A99BA") // source / taps
	colCursorBG = lipgloss.Color("#2E2E2E") // inverse row background
	colIndicatr = lipgloss.Color("#D7A663") // left-edge cursor ▎
)

// styles bundles every lipgloss.Style used by the view. Instantiated once in
// newStyles() and threaded through render helpers. Values are plain
// lipgloss.Style so they compose cleanly.
type styles struct {
	pane        lipgloss.Style
	paneFocused lipgloss.Style
	bottomBar   lipgloss.Style

	title       lipgloss.Style
	text        lipgloss.Style
	dim         lipgloss.Style
	faint       lipgloss.Style
	amber       lipgloss.Style
	green       lipgloss.Style
	red         lipgloss.Style
	blue        lipgloss.Style
	cursorRow   lipgloss.Style
	cursorMark  lipgloss.Style
	scopeActive lipgloss.Style
	scopeIdle   lipgloss.Style
	keycap      lipgloss.Style
	confirm     lipgloss.Style
	helpPanel   lipgloss.Style
	helpKey     lipgloss.Style
	helpDesc    lipgloss.Style
}

func newStyles() styles {
	pane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Padding(0, 1)

	paneFocused := pane.BorderForeground(colBorderHi)

	bottomBar := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colBorder).
		Padding(0, 1)

	return styles{
		pane:        pane,
		paneFocused: paneFocused,
		bottomBar:   bottomBar,
		title:       lipgloss.NewStyle().Foreground(colDim).Bold(true),
		text:        lipgloss.NewStyle().Foreground(colText),
		dim:         lipgloss.NewStyle().Foreground(colDim),
		faint:       lipgloss.NewStyle().Foreground(colFaint),
		amber:       lipgloss.NewStyle().Foreground(colAmber),
		green:       lipgloss.NewStyle().Foreground(colGreen),
		red:         lipgloss.NewStyle().Foreground(colRed).Bold(true),
		blue:        lipgloss.NewStyle().Foreground(colBlue),
		cursorRow:   lipgloss.NewStyle().Background(colCursorBG).Foreground(colText),
		cursorMark:  lipgloss.NewStyle().Foreground(colIndicatr),
		scopeActive: lipgloss.NewStyle().Foreground(colAmber).Bold(true),
		scopeIdle:   lipgloss.NewStyle().Foreground(colDim),
		keycap:      lipgloss.NewStyle().Foreground(colBlue).Bold(true),
		confirm:     lipgloss.NewStyle().Foreground(colRed).Bold(true),
		helpPanel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorderHi).
			Padding(1, 2).
			Background(lipgloss.Color("#1B1B1B")).
			Foreground(colText),
		helpKey:  lipgloss.NewStyle().Foreground(colAmber).Bold(true),
		helpDesc: lipgloss.NewStyle().Foreground(colDim),
	}
}
