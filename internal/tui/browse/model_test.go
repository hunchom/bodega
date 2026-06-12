package browse

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/hunchom/bodega/internal/backend"
)

// newTestModel builds a model ready for direct Update() calls. No backend is
// wired because every test here exercises pure state transitions.
func newTestModel() *model {
	m := newModel(nil, nil, nil)
	m.ready = true
	m.width = 120
	m.height = 30
	return m
}

func seed(m *model, names ...string) {
	pkgs := make([]backend.Package, 0, len(names))
	for _, n := range names {
		pkgs = append(pkgs, backend.Package{Name: n, Version: "1.0", Source: backend.SrcFormula})
	}
	m.pkgs = pkgs
	m.applyFilter()
}

func TestScopeCycle(t *testing.T) {
	tests := []struct {
		in   Scope
		want Scope
	}{
		{ScopeInstalled, ScopeOutdated},
		{ScopeOutdated, ScopeLeaves},
		{ScopeLeaves, ScopeAll},
		{ScopeAll, ScopeInstalled},
	}
	for _, tc := range tests {
		if got := tc.in.next(); got != tc.want {
			t.Errorf("%v.next() = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestCursorWrapDown(t *testing.T) {
	m := newTestModel()
	seed(m, "a", "b", "c")
	m.cursor = 2
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.cursor != 0 {
		t.Errorf("down wrap: cursor = %d; want 0", m.cursor)
	}
}

func TestCursorWrapUp(t *testing.T) {
	m := newTestModel()
	seed(m, "a", "b", "c")
	m.cursor = 0
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m.cursor != 2 {
		t.Errorf("up wrap: cursor = %d; want 2", m.cursor)
	}
}

func TestGotoTopBottom(t *testing.T) {
	m := newTestModel()
	seed(m, "a", "b", "c", "d", "e")
	m.cursor = 2
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if m.cursor != 4 {
		t.Errorf("G: cursor = %d; want 4", m.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if m.cursor != 0 {
		t.Errorf("g: cursor = %d; want 0", m.cursor)
	}
}

func TestFilterNarrowsList(t *testing.T) {
	m := newTestModel()
	seed(m, "git", "gh", "ripgrep", "hyperfine")
	m.filter.SetValue("rip")
	m.applyFilter()
	if got := len(m.filtered); got != 1 {
		t.Errorf("filtered len = %d; want 1", got)
	}
	if m.filtered[0] != 2 {
		t.Errorf("filtered[0] = %d; want 2", m.filtered[0])
	}
}

func TestFilterClearResetsList(t *testing.T) {
	m := newTestModel()
	seed(m, "git", "gh", "ripgrep")
	m.filter.SetValue("x")
	m.applyFilter()
	if len(m.filtered) != 0 {
		t.Fatalf("expected 0 after nonmatching filter, got %d", len(m.filtered))
	}
	m.filter.SetValue("")
	m.applyFilter()
	if len(m.filtered) != 3 {
		t.Errorf("after clearing filter: len = %d; want 3", len(m.filtered))
	}
}

func TestTabCyclesScope(t *testing.T) {
	m := newTestModel()
	m.scope = ScopeInstalled
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.scope != ScopeOutdated {
		t.Errorf("after tab: scope = %v; want %v", m.scope, ScopeOutdated)
	}
}

func TestSlashFocusesFilter(t *testing.T) {
	m := newTestModel()
	if m.focus != focusList {
		t.Fatalf("initial focus = %v; want focusList", m.focus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.focus != focusFilter {
		t.Errorf("after /: focus = %v; want focusFilter", m.focus)
	}
}

func TestQuestionTogglesHelp(t *testing.T) {
	m := newTestModel()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m.focus != focusHelp {
		t.Errorf("after ?: focus = %v; want focusHelp", m.focus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != focusList {
		t.Errorf("after esc: focus = %v; want focusList", m.focus)
	}
}

func TestDecidePrimaryAction(t *testing.T) {
	installed := &backend.Package{Name: "git", Version: "2.50"}
	notInstalled := &backend.Package{Name: "fzf"}
	if got := decidePrimaryAction(installed); got != ActRemove {
		t.Errorf("installed: action = %v; want ActRemove", got)
	}
	if got := decidePrimaryAction(notInstalled); got != ActInstall {
		t.Errorf("not installed: action = %v; want ActInstall", got)
	}
	if got := decidePrimaryAction(nil); got != ActInstall {
		t.Errorf("nil: action = %v; want ActInstall", got)
	}
}

func TestEscClearsFilter(t *testing.T) {
	m := newTestModel()
	seed(m, "git", "gh", "ripgrep")
	m.focus = focusFilter
	m.filter.Focus()
	m.filter.SetValue("rip")
	m.applyFilter()
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.filter.Value() != "" {
		t.Errorf("after esc: filter = %q; want empty", m.filter.Value())
	}
	if m.focus != focusList {
		t.Errorf("after esc: focus = %v; want focusList", m.focus)
	}
}

func TestConfirmAcceptRejectRoundTrip(t *testing.T) {
	m := newTestModel()
	seed(m, "git")
	// Enter prompts — git is installed, so primary action is remove.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != focusConfirm {
		t.Fatalf("after enter: focus = %v; want focusConfirm", m.focus)
	}
	if m.pending.action != ActRemove || m.pending.name != "git" {
		t.Errorf("pending = %+v; want remove/git", m.pending)
	}
	// Reject.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.focus != focusList {
		t.Errorf("after n: focus = %v; want focusList", m.focus)
	}
	if m.pending != (pendingMutation{}) {
		t.Errorf("after n: pending = %+v; want zero", m.pending)
	}
}

func TestWindowResize(t *testing.T) {
	m := newTestModel()
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if m.width != 60 || m.height != 20 {
		t.Errorf("resize: %dx%d; want 60x20", m.width, m.height)
	}
	// Re-render shouldn't panic on a narrow window.
	_ = m.View()
}

// TestRenderRowFitsWidth: a selected row rendering even one cell wider than
// the pane makes lipgloss wrap it, splitting an SGR escape across lines and
// leaking "8;2;...m" fragments into the terminal.
func TestRenderRowFitsWidth(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.pkgs = []backend.Package{
		{Name: "libmagic", Version: "5.48", Source: backend.SrcFormula},
		{Name: "a-very-long-package-name-that-truncates", Version: "10.20.30_4", Source: backend.SrcFormula},
	}
	m.filtered = []int{0, 1}
	s := newStyles()

	for _, width := range []int{20, 32, 47, 80} {
		for idx := range m.pkgs {
			for _, selected := range []bool{true, false} {
				row := m.renderRow(s, idx, selected, width)
				if got := lipgloss.Width(row); got > width {
					t.Errorf("width=%d idx=%d selected=%v: row is %d cells wide:\n%q",
						width, idx, selected, got, row)
				}
			}
		}
	}
}

// TestScrollIndicatorPreservesEscapes: the ↓/↑ indicators replace the first
// cells of the first/last visible rows. Rune-slicing there beheaded the
// selected row's first SGR sequence, printing "8;2;215;166;99m" as literal
// text (user-visible corruption). Strip must be ANSI-aware.
func TestScrollIndicatorPreservesEscapes(t *testing.T) {
	s := newStyles()
	styled := s.cursorMark.Render("▎") + s.cursorRow.Render(" libmagic   5.48 ")

	got := stripLeading(styled, 3)
	if strings.Contains(ansi.Strip(got), ";2;") {
		t.Fatalf("escape beheaded — SGR params visible as text: %q", ansi.Strip(got))
	}
	if w := lipgloss.Width(got); w != lipgloss.Width(styled)-3 {
		t.Errorf("visible width: got %d want %d", w, lipgloss.Width(styled)-3)
	}

	// Full-list integration: selected row last visible with more below —
	// the exact screenshot layout.
	m := newModel(nil, nil, nil)
	for i := 0; i < 30; i++ {
		m.pkgs = append(m.pkgs, backend.Package{Name: fmt.Sprintf("pkg-%02d", i), Version: "5.48", Source: backend.SrcFormula})
		m.filtered = append(m.filtered, i)
	}
	m.ready = true
	m.width, m.height = 64, 14
	m.cursor = 9 // bottom of the visible window, more entries below
	out := m.renderList(s, 64, 12)
	if vis := ansi.Strip(out); strings.Contains(vis, ";2;") || strings.Contains(vis, "[3") {
		t.Fatalf("visible escape fragments in list render:\n%s", vis)
	}
}
