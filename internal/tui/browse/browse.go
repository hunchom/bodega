// Package browse implements `yum browse` — the interactive package browser.
// It lives under internal/tui so it can depend on backend/journal/ui without
// introducing a cycle back to internal/cmd. Callers construct the concrete
// dependencies (registry, journal, log writer) and hand them to Run.
package browse

import (
	"context"
	"io"
	"os"
	"os/signal"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/journal"
)

// Run starts the TUI. It blocks until the user quits. Errors from the
// underlying program surface directly; ErrProgramKilled etc. are not masked.
//
// The logW writer is reserved for debug traces; it is not currently written
// to, but accepting it keeps the signature stable against future logging
// needs and mirrors other internal entry points.
func Run(registry *backend.Registry, jrnl *journal.Journal, logW io.Writer) error {
	if logW == nil {
		logW = io.Discard
	}
	if registry == nil || registry.Primary() == nil {
		return errNoBackend
	}

	m := newModel(registry, jrnl, logW)

	// Plumb a cancellable context into the model so in-flight backend
	// calls abort when the user quits or SIGINT arrives.
	ctx, cancel := context.WithCancel(context.Background())
	m.rootCtx = ctx
	m.cancel = cancel

	// SIGINT → context cancel → tea will receive a KeyMsg for ctrl+c anyway.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		cancel()
	}()
	defer cancel()

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// errNoBackend is returned when Run is invoked before a backend registry is
// wired up — primarily a defensive guard for test harnesses.
type runError string

func (e runError) Error() string { return string(e) }

const errNoBackend = runError("browse: no backend configured")
