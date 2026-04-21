package browse

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/journal"
)

// focus determines where keystrokes (other than global shortcuts) are routed.
type focus int

const (
	focusList focus = iota
	focusFilter
	focusConfirm
	focusHelp
	focusFormula
)

// pendingMutation captures a proposed install/remove that's waiting on y/n.
type pendingMutation struct {
	action Action
	name   string
}

// model is the top-level bubbletea Model for `yum browse`. It is intentionally
// unexported — callers drive the TUI through Run().
type model struct {
	// Dependencies
	reg     *backend.Registry
	jrnl    *journal.Journal
	logW    io.Writer
	cancel  context.CancelFunc
	rootCtx context.Context

	// Layout
	width  int
	height int
	ready  bool

	// Data
	scope         Scope
	pkgs          []backend.Package // unfiltered, as returned for current scope
	filtered      []int             // indices into pkgs
	cache         map[Scope][]backend.Package
	cursor        int // index within filtered slice
	listOffset    int // scroll offset into filtered slice
	info          *backend.Package
	infoRevCount  int
	infoName      string // name the current info is about (could be stale)
	loadingList   bool
	loadingInfo   bool
	loadErr       error

	// Filter input
	filter   textinput.Model
	focus    focus

	// Confirm prompt
	pending pendingMutation
	status  string // the bottom-bar status line ("installing git…", etc.)
	flash   string // short-lived notice (e.g. "yanked git")

	// In-flight mutation
	mutating       bool
	mutationName   string
	mutationAction Action
	muBuf          strings.Builder // captured progress output
	muCh           chan string     // goroutine -> program (progress lines)
	muDoneCh       chan error      // goroutine -> program (final err)

	// Formula overlay
	formulaName string
	formulaBody string
	formulaScrl int
	formulaErr  error
	formulaBusy bool

	// spinner tick
	spin int
}

// --- init ---

func newModel(reg *backend.Registry, jrnl *journal.Journal, logW io.Writer) *model {
	ti := textinput.New()
	ti.Placeholder = "type to filter"
	ti.Prompt = "/ "
	ti.CharLimit = 80
	// Don't focus the filter initially — focus is on the list.

	m := &model{
		reg:    reg,
		jrnl:   jrnl,
		logW:   logW,
		scope:  ScopeInstalled,
		cache:  map[Scope][]backend.Package{},
		filter: ti,
		focus:  focusList,
	}
	return m
}

func (m *model) Init() tea.Cmd {
	// Kick off the initial list fetch and a spinner tick.
	return tea.Batch(
		m.loadScopeCmd(m.scope),
		tickCmd(),
	)
}

// tickCmd produces a periodic tick so the spinner can animate during
// long-running loads/mutations. It's cheap — 200ms cadence.
func tickCmd() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

// loadScopeCmd fetches the package list for the given scope in a goroutine
// and returns the result as a pkgsLoadedMsg. Cached results short-circuit.
func (m *model) loadScopeCmd(s Scope) tea.Cmd {
	if cached, ok := m.cache[s]; ok {
		return func() tea.Msg { return pkgsLoadedMsg{scope: s, pkgs: cached} }
	}
	reg := m.reg
	ctx := m.ctxOr()
	return func() tea.Msg {
		var (
			ps  []backend.Package
			err error
		)
		switch s {
		case ScopeInstalled:
			ps, err = reg.Primary().List(ctx, backend.ListInstalled)
		case ScopeOutdated:
			ps, err = reg.Primary().Outdated(ctx)
		case ScopeLeaves:
			ps, err = reg.Primary().List(ctx, backend.ListLeaves)
		case ScopeAll:
			ps, err = reg.Primary().List(ctx, backend.ListAvailable)
		}
		return pkgsLoadedMsg{scope: s, pkgs: ps, err: err}
	}
}

// loadInfoCmd kicks off Info+ReverseDeps for the given package name.
func (m *model) loadInfoCmd(name string) tea.Cmd {
	reg := m.reg
	ctx := m.ctxOr()
	return func() tea.Msg {
		p, err := reg.Primary().Info(ctx, name)
		if err != nil {
			return infoLoadedMsg{name: name, err: err}
		}
		rd, _ := reg.Primary().ReverseDeps(ctx, name)
		return infoLoadedMsg{name: name, pkg: p, revCount: len(rd)}
	}
}

// ctxOr returns the root context if available, otherwise a fresh background.
// We lazily stash a cancel in model so tea.Quit kills in-flight work.
func (m *model) ctxOr() context.Context {
	if m.rootCtx != nil {
		return m.rootCtx
	}
	return context.Background()
}

// selectedPackage returns the package pointed to by the cursor, or nil.
func (m *model) selectedPackage() *backend.Package {
	if len(m.filtered) == 0 || m.cursor < 0 || m.cursor >= len(m.filtered) {
		return nil
	}
	idx := m.filtered[m.cursor]
	if idx < 0 || idx >= len(m.pkgs) {
		return nil
	}
	return &m.pkgs[idx]
}

// applyFilter rebuilds m.filtered from the current filter string.
func (m *model) applyFilter() {
	q := strings.TrimSpace(strings.ToLower(m.filter.Value()))
	m.filtered = m.filtered[:0]
	for i, p := range m.pkgs {
		if q == "" || strings.Contains(strings.ToLower(p.Name), q) {
			m.filtered = append(m.filtered, i)
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = 0
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.clampOffset()
}

// clampOffset ensures the scroll window covers the cursor.
func (m *model) clampOffset() {
	vis := m.listVisibleRows()
	if vis <= 0 {
		return
	}
	if m.cursor < m.listOffset {
		m.listOffset = m.cursor
	}
	if m.cursor >= m.listOffset+vis {
		m.listOffset = m.cursor - vis + 1
	}
	if m.listOffset < 0 {
		m.listOffset = 0
	}
}

// listVisibleRows returns the number of rows the list pane can display.
// Reserves room for the bottom bar (3 lines) and pane borders (2 lines).
func (m *model) listVisibleRows() int {
	h := m.height - 5 // 2 border, 3 bottom bar
	if h < 1 {
		return 1
	}
	return h
}
