package cmd

import (
	"github.com/spf13/cobra"

	sv "github.com/hunchom/bodega/internal/semver"
	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newOutdatedCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "outdated",
		Aliases: []string{"check-update"},
		Short:   "List outdated packages",
		RunE: func(c *cobra.Command, _ []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			maybeRefreshTaps(app)

			pkgs, err := app.Registry.Primary().Outdated(app.Ctx)
			if err != nil {
				return err
			}
			if app.W.JSON {
				return app.W.Print(pkgs)
			}
			if len(pkgs) == 0 {
				app.W.Println(theme.OK.Render("everything up to date"))
				return nil
			}

			rows := make([][]string, 0, len(pkgs))
			for _, p := range pkgs {
				arrow := " → "
				switch sv.Diff(p.Version, p.Latest) {
				case sv.Major:
					arrow = " " + theme.Err.Render("→") + " "
				case sv.Minor:
					arrow = " " + theme.Warn.Render("→") + " "
				case sv.Patch:
					arrow = " " + theme.OK.Render("→") + " "
				}
				rows = append(rows, []string{p.Name, p.Version + arrow + p.Latest, string(p.Source)})
			}
			tbl := ui.Table{
				Headers: []string{"name", "change", "src"},
				Aligns:  []ui.Align{ui.AlignLeft, ui.AlignLeft, ui.AlignLeft},
				Rows:    rows,
			}
			app.W.Printf("%s", tbl.Render())
			return nil
		},
	}
}
