package cmd

import (
	"bytes"
	"io"

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

			human := !app.W.JSON

			// sync always refreshes: it's an explicit "update everything". A
			// refresh failure aborts — sync claims to "update", so it must not
			// proceed against stale metadata and then report success.
			if !Flags.NoRefresh {
				Flags.Refresh = true
			}
			if err := refreshTaps(app); err != nil {
				if human {
					app.W.Errorf("%s update: %v\n", theme.Err.Render("✗"), err)
				} else {
					_ = app.W.Print(map[string]any{
						"steps":  []map[string]any{{"step": "update", "ok": false, "error": err.Error()}},
						"failed": []string{"update"},
						"error":  "sync: update: " + err.Error(),
					})
				}
				return err
			}

			if err := app.ensureJournal(); err != nil {
				return err
			}
			txID, err := app.Journal.Begin(app.Ctx, "sync",
				journal.Cmdline([]string{"yum", "sync"}), versionStr(), brewVersion())
			if err != nil {
				return err
			}

			// autoremove output is captured so we can journal which orphans came
			// down — that's the one part of a sync `yum history undo` can reverse
			// (upgrades can't downgrade; cleanup isn't reversible).
			var autoBuf bytes.Buffer
			upgradePW := &backend.StreamPW{W: syncSink(human, app.W.Out, nil)}
			autoPW := &backend.StreamPW{W: syncSink(human, app.W.Out, &autoBuf)}

			type syncStep struct {
				label string
				fn    func() error
			}
			steps := []syncStep{
				{"upgrade", func() error { return app.Registry.Primary().Upgrade(app.Ctx, nil, upgradePW) }},
				{"autoremove", func() error { return app.Registry.Primary().Autoremove(app.Ctx, autoPW) }},
			}
			// cleanup is gated by the auto_cleanup config (default true).
			_ = app.ensureCfg()
			if app.Cfg == nil || app.Cfg.Defaults.AutoCleanup {
				steps = append(steps, syncStep{"cleanup", func() error { return app.Registry.Primary().Cleanup(app.Ctx, false) }})
			}

			var txPkgs []journal.TxPackage
			results := make([]map[string]any, 0, len(steps))
			var failed []string
			var stepErr error

			for _, s := range steps {
				if human {
					app.W.Printf("%s %s\n", theme.Muted.Render("→"), s.label)
				}
				if err := s.fn(); err != nil {
					stepErr = err
					failed = append(failed, s.label)
					results = append(results, map[string]any{"step": s.label, "ok": false, "error": err.Error()})
					if human {
						app.W.Errorf("%s %s: %v\n", theme.Err.Render("✗"), s.label, err)
					}
					break
				}
				if s.label == "autoremove" {
					for _, n := range parseUninstalled(autoBuf.Bytes()) {
						txPkgs = append(txPkgs, journal.TxPackage{Name: n, Source: "formula", Action: "removed"})
					}
				}
				results = append(results, map[string]any{"step": s.label, "ok": true})
				if human {
					app.W.Printf("%s %s\n", theme.OK.Render("✓"), s.label)
				}
			}

			exit := 0
			if stepErr != nil {
				exit = 1
			}
			if err := app.Journal.End(app.Ctx, txID, exit, txPkgs); err != nil {
				return err
			}

			if !human {
				payload := map[string]any{"steps": results, "failed": failed}
				if stepErr != nil {
					payload["error"] = "sync: " + stepErr.Error()
				}
				if perr := app.W.Print(payload); perr != nil && stepErr == nil {
					return perr
				}
			}
			return stepErr
		},
	}
}

// syncSink builds the writer brew output streams to during a sync step. Human
// runs stream live to stdout; --json keeps stdout clean (JSON only). capture,
// when non-nil, always receives a copy so we can parse package names regardless
// of mode.
func syncSink(live bool, out io.Writer, capture *bytes.Buffer) io.Writer {
	var ws []io.Writer
	if live {
		ws = append(ws, out)
	}
	if capture != nil {
		ws = append(ws, capture)
	}
	switch len(ws) {
	case 0:
		return io.Discard
	case 1:
		return ws[0]
	default:
		return io.MultiWriter(ws...)
	}
}
