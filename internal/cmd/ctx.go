package cmd

import (
	"context"
	"os"
	"os/signal"

	"github.com/hunchom/yum/internal/backend"
	"github.com/hunchom/yum/internal/backend/brew"
	"github.com/hunchom/yum/internal/config"
	"github.com/hunchom/yum/internal/journal"
	"github.com/hunchom/yum/internal/runner"
	"github.com/hunchom/yum/internal/ui"
	"github.com/hunchom/yum/internal/ui/theme"
)

type AppCtx struct {
	Ctx      context.Context
	Cancel   context.CancelFunc
	Cfg      *config.Config
	W        *ui.Writer
	Registry *backend.Registry
	Journal  *journal.Journal
}

func boot() (*AppCtx, error) {
	cfg, err := config.Load(Flags.Config)
	if err != nil {
		return nil, err
	}
	if Flags.NoColor || os.Getenv("NO_COLOR") != "" {
		theme.NoColor = true
		theme.Load()
	}
	w := ui.NewWriter()
	w.JSON = Flags.JSON

	r := runner.Real{}
	reg := &backend.Registry{Backends: []backend.Backend{brew.New(r)}}

	j, err := journal.Open(journal.DefaultPath())
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		<-sig
		cancel()
	}()
	return &AppCtx{Ctx: ctx, Cancel: cancel, Cfg: cfg, W: w, Registry: reg, Journal: j}, nil
}
