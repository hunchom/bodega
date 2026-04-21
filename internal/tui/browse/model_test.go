package browse

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
