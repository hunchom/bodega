package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/backend/brew"
	"github.com/hunchom/bodega/internal/config"
	"github.com/hunchom/bodega/internal/journal"
	"github.com/hunchom/bodega/internal/runner"
	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
)

type AppCtx struct {
	Ctx      context.Context
	Cancel   context.CancelFunc
	Cfg      *config.Config
	W        *ui.Writer
	Registry *backend.Registry
	Journal  *journal.Journal // nil until ensureJournal() is called
}

func boot() (*AppCtx, error) {
	if Flags.NoColor || os.Getenv("NO_COLOR") != "" {
		theme.NoColor = true
		theme.Load()
	}
	w := ui.NewWriter()
	w.JSON = Flags.JSON

	r := runner.Real{}
	reg := &backend.Registry{Backends: []backend.Backend{brew.New(r)}}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		<-sig
		cancel()
	}()
	// Journal and Cfg are both opened lazily — read-only commands never
	// touch sqlite and don't care about the user's config.toml.
	return &AppCtx{Ctx: ctx, Cancel: cancel, W: w, Registry: reg}, nil
}

// ensureCfg loads ~/.config/yum/config.toml on first use. Until then,
// AppCtx.Cfg is nil. Only mutation paths (install/remove/upgrade) currently
// read the config; everything else happily works without it.
func (a *AppCtx) ensureCfg() error {
	if a.Cfg != nil {
		return nil
	}
	cfg, err := config.Load(Flags.Config)
	if err != nil {
		return err
	}
	a.Cfg = cfg
	return nil
}

// ensureJournal opens the sqlite history DB on first use. Safe to call
// multiple times; only the first call does any work. Commands that record
// transactions (install/remove/upgrade/...) or read history must call this
// before touching app.Journal.
func (a *AppCtx) ensureJournal() error {
	if a.Journal != nil {
		return nil
	}
	j, err := journal.Open(journal.DefaultPath())
	if err != nil {
		return err
	}
	a.Journal = j
	return nil
}

// CloseJournal is a defer-safe close; it's a no-op if the journal was never
// opened.
func (a *AppCtx) CloseJournal() {
	if a.Journal != nil {
		_ = a.Journal.Close()
	}
}

// maybeRefreshTaps runs `brew update` when the caller actually needs fresh
// tap state (install/upgrade/outdated/sync). Honors the --refresh and
// --no-refresh global flags; otherwise gated by the 24h staleness threshold
// in brew.DefaultStaleAge. Prints a short status line so the user sees why
// they're waiting when a refresh happens — suppressed under --json so
// scripted callers never get a surprise line of human chatter.
func maybeRefreshTaps(app *AppCtx) {
	if Flags.NoRefresh {
		return
	}
	if !Flags.Refresh && !brew.Stale(brew.DefaultStaleAge) {
		return
	}
	// Resolve the *brew.Brew from the registry so we can call RefreshTaps.
	bb, ok := app.Registry.Primary().(*brew.Brew)
	if !ok {
		return
	}
	if !app.W.JSON {
		app.W.Printf("%s %s\n", theme.Muted.Render("→"), "taps stale — refreshing")
	}
	start := time.Now()
	if err := bb.RefreshTaps(app.Ctx, nil); err != nil {
		if !app.W.JSON {
			app.W.Errorf("%s refresh failed: %v\n", theme.Warn.Render("•"), err)
		}
		return
	}
	if !app.W.JSON {
		app.W.Printf("%s %s\n", theme.OK.Render("✓"), fmt.Sprintf("refreshed (%s)", time.Since(start).Round(100*time.Millisecond)))
	}
}
