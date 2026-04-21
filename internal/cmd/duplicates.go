package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend/brew"
	"github.com/hunchom/bodega/internal/journal"
	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newDuplicatesCmd() *cobra.Command {
	var prune bool
	c := &cobra.Command{
		Use:   "duplicates [pkg]...",
		Short: "List packages with multiple installed Cellar versions",
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()

			dups, err := brew.FindDuplicates(args)
			if err != nil {
				return err
			}

			if prune {
				return runPruneDuplicates(app, dups)
			}

			if app.W.JSON {
				if dups == nil {
					dups = []brew.Duplicate{}
				}
				return app.W.Print(dups)
			}
			if len(dups) == 0 {
				app.W.Println(theme.Muted.Render("no duplicate versions"))
				return nil
			}
			rows := make([][]string, 0, len(dups))
			for _, d := range dups {
				linked := d.Linked
				if linked == "" {
					linked = "-"
				}
				rows = append(rows, []string{d.Name, strings.Join(d.Versions, ", "), linked})
			}
			tbl := ui.Table{
				Headers: []string{"name", "versions", "linked"},
				Rows:    rows,
			}
			app.W.Printf("%s", tbl.Render())
			return nil
		},
	}
	c.Flags().BoolVar(&prune, "prune", false, "remove older versions and relink the newest")
	return c
}

func runPruneDuplicates(app *AppCtx, dups []brew.Duplicate) error {
	if len(dups) == 0 {
		if app.W.JSON {
			return app.W.Print([]brew.Duplicate{})
		}
		app.W.Println(theme.Muted.Render("no duplicate versions"))
		return nil
	}
	if Flags.DryRun {
		for _, d := range dups {
			keep := d.Versions[len(d.Versions)-1]
			var drop []string
			for _, v := range d.Versions {
				if v != keep {
					drop = append(drop, v)
				}
			}
			app.W.Printf("%s would keep %s@%s, remove %s\n",
				theme.Muted.Render("dry-run"), d.Name, keep, strings.Join(drop, ", "))
		}
		return nil
	}

	if err := app.ensureJournal(); err != nil {
		return err
	}
	txID, err := app.Journal.Begin(app.Ctx, "prune-duplicates",
		journal.Cmdline([]string{"yum", "duplicates", "--prune"}),
		versionStr(), brewVersion())
	if err != nil {
		return err
	}

	var txPkgs []journal.TxPackage
	exit := 0
	var firstErr error
	for _, d := range dups {
		keep := d.Versions[len(d.Versions)-1]
		removed, err := brew.PruneDuplicate(d, keep)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			exit = 1
			app.W.Errorf("%s %s: %s\n", theme.Err.Render("✗"), d.Name, err.Error())
			continue
		}
		for _, v := range removed {
			txPkgs = append(txPkgs, journal.TxPackage{
				Name:        d.Name,
				Source:      "formula",
				Action:      "removed",
				FromVersion: v,
				ToVersion:   keep,
			})
		}
		app.W.Printf("%s %s: kept %s, removed %s\n",
			theme.OK.Render("✓"), d.Name, keep, strings.Join(removed, ", "))
	}

	if err := app.Journal.End(app.Ctx, txID, exit, txPkgs); err != nil {
		return err
	}
	if firstErr != nil {
		return fmt.Errorf("duplicates --prune: %w", firstErr)
	}
	return nil
}
