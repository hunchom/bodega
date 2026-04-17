package cmd

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/ui"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [installed|available|updates|leaves|pinned|casks]",
		Short: "List packages",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.Journal.Close()

			sel := backend.ListInstalled
			if len(args) == 1 {
				switch args[0] {
				case "installed", "available", "updates", "outdated", "leaves", "pinned", "casks":
					if args[0] == "updates" || args[0] == "outdated" {
						sel = backend.ListOutdated
					} else {
						sel = backend.ListFilter(args[0])
					}
				default:
					return errUnknownSelector(args[0])
				}
			}
			pkgs, err := app.Registry.Primary().List(app.Ctx, sel)
			if err != nil {
				return err
			}

			if app.W.JSON {
				return app.W.Print(pkgs)
			}

			rows := make([][]string, 0, len(pkgs))
			for _, p := range pkgs {
				ver := p.Version
				if ver == "" {
					ver = "-"
				}
				rows = append(rows, []string{p.Name, ver, string(p.Source)})
			}
			tbl := ui.Table{
				Headers: []string{"name", "ver", "src"},
				Aligns:  []ui.Align{ui.AlignLeft, ui.AlignLeft, ui.AlignLeft},
				Rows:    rows,
			}
			app.W.Printf("%s", tbl.Render())
			app.W.Printf("%s\n", dim(strconv.Itoa(len(pkgs))+" packages"))
			return nil
		},
	}
}

func errUnknownSelector(s string) error { return &selErr{sel: s} }

type selErr struct{ sel string }

func (e *selErr) Error() string { return "unknown list selector: " + e.sel }

func dim(s string) string { return s } // placeholder for theme.Muted if we want it here
