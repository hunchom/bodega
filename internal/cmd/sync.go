package cmd

import (
	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/journal"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "update + upgrade + autoremove + cleanup",
		RunE: func(c *cobra.Command, _ []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()

			if Flags.DryRun {
				app.W.Printf("%s would refresh taps, upgrade, autoremove, and cleanup\n",
					theme.Muted.Render("dry-run"))
				return nil
			}

			// sync always refreshes: it's an explicit "update everything".
			if !Flags.NoRefresh {
				Flags.Refresh = true
			}
			maybeRefreshTaps(app)

			if err := app.ensureJournal(); err != nil {
				return err
			}
			txID, err := app.Journal.Begin(app.Ctx, "sync",
				journal.Cmdline([]string{"yum", "sync"}), versionStr(), brewVersion())
			if err != nil {
				return err
			}

			pw := &backend.StreamPW{W: app.W.Out}
			steps := []struct {
				label string
				fn    func() error
			}{
				{"upgrade", func() error { return app.Registry.Primary().Upgrade(app.Ctx, nil, pw) }},
				{"autoremove", func() error { return app.Registry.Primary().Autoremove(app.Ctx, pw) }},
				{"cleanup", func() error { return app.Registry.Primary().Cleanup(app.Ctx, false) }},
			}
			for _, s := range steps {
				app.W.Printf("%s %s\n", theme.Muted.Render("→"), s.label)
				if err := s.fn(); err != nil {
					app.W.Errorf("%s %s: %v\n", theme.Err.Render("✗"), s.label, err)
					_ = app.Journal.End(app.Ctx, txID, 1, nil)
					return err
				}
				app.W.Printf("%s %s\n", theme.OK.Render("✓"), s.label)
			}
			return app.Journal.End(app.Ctx, txID, 0, nil)
		},
	}
}
