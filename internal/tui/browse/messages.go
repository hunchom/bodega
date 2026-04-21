package browse

import (
	"github.com/hunchom/bodega/internal/backend"
)

// Scope describes which view of the package universe is currently rendered
// in the list pane. It cycles via Tab: installed -> outdated -> leaves -> all.
type Scope int

const (
	ScopeInstalled Scope = iota
	ScopeOutdated
	ScopeLeaves
	ScopeAll
)

func (s Scope) String() string {
	switch s {
	case ScopeInstalled:
		return "installed"
	case ScopeOutdated:
		return "outdated"
	case ScopeLeaves:
		return "leaves"
	case ScopeAll:
		return "all"
	}
	return "?"
}

func (s Scope) next() Scope { return Scope((int(s) + 1) % 4) }

// Action encodes an install/remove/reinstall/upgrade verb for the confirm
// prompt and the background goroutine that runs it.
type Action int

const (
	ActInstall Action = iota
	ActRemove
	ActReinstall
	ActUpgrade
)

func (a Action) verb() string {
	switch a {
	case ActInstall:
		return "install"
	case ActRemove:
		return "remove"
	case ActReinstall:
		return "reinstall"
	case ActUpgrade:
		return "upgrade"
	}
	return "?"
}

// ---- tea.Msg types ----

// pkgsLoadedMsg is emitted when the list backing the current Scope has been
// fetched. Err is non-nil if the fetch failed.
type pkgsLoadedMsg struct {
	scope Scope
	pkgs  []backend.Package
	err   error
}

// infoLoadedMsg is emitted when the preview pane's Info+ReverseDeps fetch
// for the selected package completes.
type infoLoadedMsg struct {
	name     string
	pkg      *backend.Package
	revCount int
	err      error
}

// formulaLoadedMsg is emitted when `brew cat <name>` finishes populating the
// raw-view overlay.
type formulaLoadedMsg struct {
	name string
	body string
	err  error
}

// mutationStartedMsg fires as the goroutine begins; we use it to switch UI
// into the "busy" indicator.
type mutationStartedMsg struct {
	action Action
	name   string
}

// mutationProgressMsg carries a single chunk of progress text from the
// backend's ProgressWriter.
type mutationProgressMsg struct {
	line string
}

// mutationStepMsg is a structured step annotation (e.g. "downloading").
type mutationStepMsg struct {
	msg string
}

// mutationDoneMsg fires when the goroutine exits; err is non-nil on failure.
type mutationDoneMsg struct {
	action Action
	name   string
	err    error
}

// tickMsg is used to drive the spinner during loading/mutation states.
type tickMsg struct{}

// clipboardMsg signals a yank succeeded so the status bar can flash a hint.
type clipboardMsg struct {
	name string
	err  error
}
