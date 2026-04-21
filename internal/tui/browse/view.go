package browse

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/hunchom/bodega/internal/backend"
)

// View is the top-level render method. It composes the list pane, the preview
// pane (when the terminal is wide enough), and the bottom bar — then overlays
// the help or confirm panel if active.
func (m *model) View() string {
	if !m.ready || m.width == 0 || m.height == 0 {
		return "loading…"
	}
	s := newStyles()

	// Overlays take over the full view — they get their own rendering path.
	if m.focus == focusFormula {
		return m.viewFormula(s)
	}

	wideEnough := m.width >= 100
	listW := m.width
	rightW := 0
	if wideEnough {
		// Left pane takes ~40% of width, right pane takes the rest.
		listW = max(m.width*40/100, 32)
		rightW = m.width - listW
	}
	// Leave 5 rows for the bottom bar + pane borders overhead.
	bodyH := max(m.height-3, 5)

	list := m.renderList(s, listW, bodyH)
	var body string
	if wideEnough {
		preview := m.renderPreview(s, rightW, bodyH)
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, preview)
	} else {
		body = list
	}

	bottom := m.renderBottomBar(s, m.width)

	if m.focus == focusConfirm {
		// Swap the bottom bar for an inline confirm prompt — feels more
		// attached to the list than a floating overlay.
		bottom = m.renderConfirm(s)
	}
	out := lipgloss.JoinVertical(lipgloss.Left, body, bottom)

	if m.focus == focusHelp {
		return overlay(m.renderHelp(s), m.width, m.height)
	}
	return out
}

// renderList renders the left pane.
func (m *model) renderList(s styles, w, h int) string {
	contentW := max(w-4, 10) // pane border (2) + horizontal padding (2)
	innerH := max(h-2, 1)    // pane border top/bottom

	// Header line inside the list pane.
	header := s.title.Render("packages")
	scopeLine := m.renderScopeBar(s)
	headerBlock := lipgloss.JoinVertical(lipgloss.Left, header, scopeLine, s.dim.Render(strings.Repeat("─", contentW)))
	usedRows := 3
	rowsAvail := max(innerH-usedRows, 1)

	// Re-clamp offset using actual rows.
	vis := rowsAvail
	if m.cursor < m.listOffset {
		m.listOffset = m.cursor
	}
	if m.cursor >= m.listOffset+vis {
		m.listOffset = m.cursor - vis + 1
	}
	m.listOffset = max(m.listOffset, 0)

	var rows []string
	if m.loadingList {
		rows = append(rows, s.dim.Render(spinnerChar(m.spin)+" loading packages…"))
	} else if m.loadErr != nil {
		rows = append(rows, s.red.Render("error: "+m.loadErr.Error()))
	} else if len(m.filtered) == 0 {
		rows = append(rows, s.dim.Render("no packages match"))
	} else {
		end := min(m.listOffset+vis, len(m.filtered))
		for i := m.listOffset; i < end; i++ {
			rows = append(rows, m.renderRow(s, m.filtered[i], i == m.cursor, contentW))
		}
	}

	// Scroll indicators.
	top := "  "
	bottom := "  "
	if m.listOffset > 0 {
		top = s.dim.Render(" ↑")
	}
	if m.listOffset+vis < len(m.filtered) {
		bottom = s.dim.Render(" ↓")
	}
	if len(rows) > 0 {
		rows[0] = top + " " + stripLeading(rows[0], 3)
		rows[len(rows)-1] = bottom + " " + stripLeading(rows[len(rows)-1], 3)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, headerBlock, lipgloss.JoinVertical(lipgloss.Left, rows...))
	pane := s.pane
	if m.focus == focusList || m.focus == focusFilter {
		pane = s.paneFocused
	}
	return pane.Width(w - 2).Height(h - 2).Render(content)
}

// renderRow renders a single list row (name + version, cursor-aware).
func (m *model) renderRow(s styles, idx int, selected bool, width int) string {
	p := m.pkgs[idx]
	name := p.Name
	ver := p.Version
	installed := ver != ""
	if !installed {
		ver = p.Latest
	}
	if ver == "" {
		ver = "-"
	}
	// Reserve right-hand room for the version column.
	verW := 10
	nameW := max(width-verW-3, 6)

	nameCell := padRight(truncStr(name, nameW), nameW)
	verCell := truncStr(ver, verW)

	if selected {
		// Left-edge indicator + inverse background. We style the version
		// inside the highlighted row with amber/faint to retain the
		// installed-vs-available cue.
		var verStyled string
		if installed {
			verStyled = s.amber.Render(verCell)
		} else {
			verStyled = s.faint.Render(verCell)
		}
		row := s.cursorRow.Render(" " + nameCell + " " + verStyled + " ")
		return s.cursorMark.Render("▎") + row
	}
	var nameR, verR string
	if installed {
		nameR = s.text.Render(nameCell)
		verR = s.amber.Render(verCell)
	} else {
		nameR = s.dim.Render(nameCell)
		verR = s.faint.Render(verCell)
	}
	return "  " + nameR + " " + verR
}

// renderScopeBar renders the [installed] [outdated] [leaves] [all] selector.
func (m *model) renderScopeBar(s styles) string {
	labels := []Scope{ScopeInstalled, ScopeOutdated, ScopeLeaves, ScopeAll}
	var parts []string
	for _, sc := range labels {
		if sc == m.scope {
			parts = append(parts, s.scopeActive.Render(sc.String()))
		} else {
			parts = append(parts, s.scopeIdle.Render(sc.String()))
		}
	}
	return s.dim.Render("tab ") + strings.Join(parts, s.dim.Render(" · "))
}

// renderPreview renders the right pane with detail about the selected package.
func (m *model) renderPreview(s styles, w, h int) string {
	contentW := max(w-4, 10)

	sel := m.selectedPackage()
	var body string
	switch {
	case m.loadingInfo:
		body = s.dim.Render(spinnerChar(m.spin) + " loading info…")
	case sel == nil:
		body = s.dim.Render("no selection")
	case m.info == nil:
		body = s.dim.Render("no info available")
	default:
		body = m.renderInfoBody(s, m.info, contentW)
	}

	pane := s.pane.Width(w - 2).Height(h - 2)
	return pane.Render(body)
}

func (m *model) renderInfoBody(s styles, p *backend.Package, w int) string {
	var b strings.Builder

	// Title + source.
	title := s.title.Render(p.Name)
	src := s.blue.Render("(" + string(p.Source) + ")")
	b.WriteString(title + " " + src + "\n")

	if p.Homepage != "" {
		b.WriteString(s.dim.Render(truncStr(p.Homepage, w)) + "\n")
	}
	b.WriteString("\n")

	if p.Desc != "" {
		b.WriteString(s.text.Render(wrapText(p.Desc, w)) + "\n\n")
	}

	// Version line.
	var installed, latest string
	if p.Version == "" {
		installed = s.dim.Render("-")
	} else {
		installed = s.amber.Render(p.Version)
	}
	if p.Latest == "" {
		latest = s.dim.Render("-")
	} else {
		latest = s.text.Render(p.Latest)
	}
	b.WriteString(s.dim.Render("Installed: ") + installed + s.dim.Render("   Latest: ") + latest + "\n")

	if p.Tap != "" {
		b.WriteString(s.dim.Render("Tap: ") + s.blue.Render(p.Tap) + "\n")
	}
	if p.License != "" {
		b.WriteString(s.dim.Render("License: ") + s.text.Render(p.License) + "\n")
	}
	if p.Size > 0 {
		b.WriteString(s.dim.Render("Size: ") + s.text.Render(formatSize(p.Size)) + "\n")
	}
	if p.Pinned {
		b.WriteString(s.amber.Render("★ pinned") + "\n")
	}

	b.WriteString("\n")

	if len(p.Deps) > 0 {
		deps := strings.Join(p.Deps, ", ")
		b.WriteString(s.dim.Render("Deps: ") + s.text.Render(wrapText(deps, w-6)) + "\n")
	}
	b.WriteString(s.dim.Render(fmt.Sprintf("Used by: %d packages", m.infoRevCount)) + "\n")

	b.WriteString("\n")

	// Installed marker.
	if p.Version != "" {
		b.WriteString(s.green.Render("✓ installed"))
	} else {
		b.WriteString(s.dim.Render("not installed"))
	}

	return b.String()
}

// renderBottomBar renders the filter input + help hint + status.
func (m *model) renderBottomBar(s styles, w int) string {
	leftWidth := max(w*2/5, 20)
	rightWidth := max(w-leftWidth-4, 10)

	var left string
	if m.focus == focusFilter {
		// Active filter input.
		left = m.filter.View()
	} else if m.filter.Value() != "" {
		left = s.dim.Render("/ ") + s.text.Render(m.filter.Value())
	} else {
		left = s.dim.Render("/ filter")
	}

	// Right side: status + primary keybinds.
	status := m.status
	if status == "" && m.flash != "" {
		status = m.flash
	}
	binds := s.dim.Render(
		keycap(s, "↑↓") + " nav  " +
			keycap(s, "/") + " filter  " +
			keycap(s, "tab") + " scope  " +
			keycap(s, "enter") + " act  " +
			keycap(s, "?") + " help  " +
			keycap(s, "q") + " quit",
	)
	var right string
	if status != "" {
		right = s.amber.Render(spinnerChar(m.spin)+" ") + s.text.Render(truncStr(status, rightWidth-2))
	} else {
		right = binds
	}

	content := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftWidth).Render(left),
		lipgloss.NewStyle().Width(rightWidth).Align(lipgloss.Right).Render(right),
	)
	return s.bottomBar.Width(w - 2).Render(content)
}

// renderHelp returns the centered overlay panel listing keybinds.
func (m *model) renderHelp(s styles) string {
	keys := [][2]string{
		{"↑/↓ or j/k", "navigate"},
		{"PgUp/PgDn or ctrl+u/d", "page"},
		{"g / G", "top / bottom"},
		{"/", "focus filter (esc to clear)"},
		{"tab", "cycle scope"},
		{"enter", "install / remove"},
		{"i", "install (even if installed → reinstall)"},
		{"u", "upgrade (if outdated)"},
		{"r", "remove"},
		{"v", "view raw formula"},
		{"y", "yank name to clipboard"},
		{"?", "toggle this help"},
		{"q / ctrl+c", "quit"},
	}
	var lines []string
	lines = append(lines, s.title.Render("keybindings"), "")
	for _, kv := range keys {
		lines = append(lines, s.helpKey.Render(padRight(kv[0], 22))+"  "+s.helpDesc.Render(kv[1]))
	}
	lines = append(lines, "", s.dim.Render("press esc or ? to close"))
	return s.helpPanel.Render(strings.Join(lines, "\n"))
}

// renderConfirm is the in-place y/n prompt for destructive actions. It's
// shaped to replace the bottom bar so the layout doesn't jitter.
func (m *model) renderConfirm(s styles) string {
	verb := m.pending.action.verb()
	name := m.pending.name
	var head string
	if m.pending.action == ActRemove {
		head = s.red.Render("remove " + name + "?")
	} else {
		head = s.amber.Render(verb + " " + name + "?")
	}
	line := head + "  " + s.dim.Render("press ") + s.keycap.Render("y") + s.dim.Render(" to confirm, ") + s.keycap.Render("n") + s.dim.Render("/esc to cancel")
	return s.bottomBar.Width(m.width - 2).Render(line)
}

// renderFormula overlays the raw formula dump.
func (m *model) viewFormula(s styles) string {
	inner := max(m.height-4, 5)
	_ = max(m.width-4, 20) // content width reserved; header uses it implicitly
	var body string
	switch {
	case m.formulaBusy:
		body = s.dim.Render(spinnerChar(m.spin) + " loading formula…")
	case m.formulaErr != nil:
		body = s.red.Render("error: " + m.formulaErr.Error())
	default:
		lines := strings.Split(m.formulaBody, "\n")
		start := max(m.formulaScrl, 0)
		start = min(start, len(lines))
		end := min(start+inner-2, len(lines))
		body = strings.Join(lines[start:end], "\n")
	}

	head := s.title.Render("formula: "+m.formulaName) + "  " + s.dim.Render("(esc / q to close — j/k to scroll)")
	panel := s.paneFocused.Width(m.width - 2).Height(m.height - 2).Render(head + "\n\n" + body)
	return panel
}

// overlay renders `top` centered in a viewport of w x h. Bubbletea can't
// truly composite, so we just dim away the main view and paint the overlay
// panel centered on an otherwise-blank screen. Help is the only caller that
// needs this; confirm renders inline in the bottom bar.
func overlay(top string, w, h int) string {
	// Center horizontally: measure and pad.
	topW := min(lipgloss.Width(top), w)
	topH := min(lipgloss.Height(top), h)
	padL := max((w-topW)/2, 0)
	padT := max((h-topH)/2, 0)
	var b strings.Builder
	for range padT {
		b.WriteString("\n")
	}
	for line := range strings.SplitSeq(top, "\n") {
		b.WriteString(strings.Repeat(" ", padL))
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// keycap wraps a key label in the keycap style for bottom-bar rendering.
func keycap(s styles, k string) string { return s.keycap.Render(k) }

// spinnerChar returns a cycling braille dot for the loading indicator.
func spinnerChar(step int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[step%len(frames)]
}

// stripLeading removes up to n leading characters (runes) from s.
func stripLeading(s string, n int) string {
	r := []rune(s)
	if n >= len(r) {
		return ""
	}
	return string(r[n:])
}

// wrapText does a naive word wrap at column w.
func wrapText(s string, w int) string {
	if w <= 0 {
		return s
	}
	words := strings.Fields(s)
	var (
		out  strings.Builder
		line strings.Builder
	)
	for _, word := range words {
		if line.Len() == 0 {
			line.WriteString(word)
			continue
		}
		if line.Len()+1+len(word) > w {
			out.WriteString(line.String())
			out.WriteString("\n")
			line.Reset()
			line.WriteString(word)
			continue
		}
		line.WriteString(" ")
		line.WriteString(word)
	}
	if line.Len() > 0 {
		out.WriteString(line.String())
	}
	return out.String()
}
