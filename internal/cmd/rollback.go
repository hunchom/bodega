package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
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

	pw := &backend.StreamPW{W: app.W.Out}
	for _, s := range steps {
		var err error
		switch s.Verb {
		case "remove":
			err = app.Registry.Primary().Remove(app.Ctx, []string{s.Pkg.Name}, pw)
		case "install":
			err = app.Registry.Primary().Install(app.Ctx, []string{s.Pkg.Name}, pw)
		case "downgrade":
			app.W.Errorf("%s cannot downgrade %s (brew does not retain old tarballs)\n", theme.Warn.Render("•"), s.Pkg.Name)
			continue
		case "pin":
			err = app.Registry.Primary().Pin(app.Ctx, s.Pkg.Name, true)
		case "unpin":
			err = app.Registry.Primary().Pin(app.Ctx, s.Pkg.Name, false)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	var a string
	_, _ = fmt.Scanln(&a)
	return a == "y" || a == "Y" || a == "yes"
}
