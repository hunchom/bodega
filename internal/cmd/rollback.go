package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/journal"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newRollbackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rollback [id]",
		Short: "Undo last transaction (or specific id)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			if err := app.ensureJournal(); err != nil {
				return err
			}

			var id int64
			if len(args) == 1 {
				if _, err := fmt.Sscan(args[0], &id); err != nil {
					return err
				}
			} else {
				txs, err := app.Journal.Recent(app.Ctx, 1)
				if err != nil {
					return err
				}
				if len(txs) == 0 {
					app.W.Println(theme.Muted.Render("nothing to undo"))
					return nil
				}
				id = txs[0].ID
			}
			return runRollback(app, id)
		},
	}
}

func runRollback(app *AppCtx, id int64) error {
	steps, err := app.Journal.PlanRollback(app.Ctx, id)
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		app.W.Println(theme.Muted.Render("nothing to undo"))
		return nil
	}

	for _, s := range steps {
		app.W.Printf("%s %s %s\n", theme.Muted.Render("→"), s.Verb, s.Pkg.Name)
	}
	if Flags.DryRun {
		app.W.Printf("%s rollback preview only\n", theme.Muted.Render("dry-run"))
		return nil
	}
	if !Flags.Yes && !confirm("apply rollback?") {
		return nil
	}

	// Journal the undo so it's auditable and itself reversible. Record the
	// target id in the cmdline for traceability.
	if err := app.ensureJournal(); err != nil {
		return err
	}
	txID, err := app.Journal.Begin(app.Ctx, "rollback",
		journal.Cmdline([]string{"yum", "rollback", fmt.Sprint(id)}), versionStr(), brewVersion())
	if err != nil {
		return err
	}

	pw := &backend.StreamPW{W: app.W.Out}
	var txPkgs []journal.TxPackage
	var reverted, skipped []string
	var failed []map[string]string
	exit := 0

	// Continue through every step rather than bailing on the first failure —
	// a partial undo must attempt the rest and report exactly what reverted.
	for _, s := range steps {
		var stepErr error
		var action string
		switch s.Verb {
		case "remove":
			stepErr = app.Registry.Primary().Remove(app.Ctx, []string{s.Pkg.Name}, pw)
			action = "removed"
		case "install":
			stepErr = app.Registry.Primary().Install(app.Ctx, []string{s.Pkg.Name}, pw)
			action = "installed"
		case "downgrade":
			app.W.Errorf("%s cannot downgrade %s (brew does not retain old tarballs)\n", theme.Warn.Render("•"), s.Pkg.Name)
			skipped = append(skipped, s.Pkg.Name)
			continue
		case "pin":
			stepErr = app.Registry.Primary().Pin(app.Ctx, s.Pkg.Name, true)
			action = "pinned"
		case "unpin":
			stepErr = app.Registry.Primary().Pin(app.Ctx, s.Pkg.Name, false)
			action = "unpinned"
		}
		if stepErr != nil {
			exit = 1
			failed = append(failed, map[string]string{"package": s.Pkg.Name, "error": stepErr.Error()})
			app.W.Errorf("%s %s: %v\n", theme.Err.Render("✗"), s.Pkg.Name, stepErr)
			continue
		}
		reverted = append(reverted, s.Pkg.Name)
		txPkgs = append(txPkgs, journal.TxPackage{Name: s.Pkg.Name, Source: string(s.Pkg.Source), Action: action})
	}

	if err := app.Journal.End(app.Ctx, txID, exit, txPkgs); err != nil {
		return err
	}

	// Summarize so the user knows the undo was only partial.
	if len(reverted) > 0 {
		app.W.Printf("%s reverted %d: %s\n", theme.OK.Render("✓"), len(reverted), strings.Join(reverted, ", "))
	}
	if len(skipped) > 0 {
		app.W.Printf("%s skipped %d (cannot downgrade): %s\n", theme.Warn.Render("•"), len(skipped), strings.Join(skipped, ", "))
	}
	if len(failed) > 0 {
		names := make([]string, 0, len(failed))
		for _, f := range failed {
			names = append(names, f["package"])
		}
		return fmt.Errorf("rollback: %d step(s) failed: %s", len(failed), strings.Join(names, ", "))
	}
	// Nothing reverted and everything was an un-revertable downgrade → not a
	// clean success; signal so it can't masquerade as one.
	if len(reverted) == 0 && len(skipped) > 0 {
		return fmt.Errorf("rollback: nothing reverted (%d downgrade step(s) cannot be undone)", len(skipped))
	}
	return nil
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	var a string
	_, _ = fmt.Scanln(&a)
	return a == "y" || a == "Y" || a == "yes"
}
