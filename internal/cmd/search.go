package cmd

import (
	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/ui"
)

func newSearchCmd() *cobra.Command {
	var install bool
	c := &cobra.Command{
		Use:   "search <term>",
		Short: "Search across formulae and casks",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()

			pkgs, err := app.Registry.Search(app.Ctx, args[0])
			if err != nil {
				return err
			}

			if app.W.JSON {
				return app.W.Print(pkgs)
			}

			if len(pkgs) == 0 {
				app.W.Errorln("no matches")
				return nil
			}

			// Non-TTY or non-install: just render a table.
			if !install || !app.W.IsTTY() {
				rows := make([][]string, 0, len(pkgs))
				for _, p := range pkgs {
					rows = append(rows, []string{p.Name, string(p.Source), truncate(p.Desc, 50)})
				}
				app.W.Printf("%s", (ui.Table{
					Headers: []string{"name", "src", "desc"},
					Aligns:  []ui.Align{ui.AlignLeft, ui.AlignLeft, ui.AlignLeft},
					Rows:    rows,
				}).Render())
				return nil
			}

			// Interactive: open picker, install selection.
			opts := make([]struct{ Name, Desc string }, 0, len(pkgs))
			for _, p := range pkgs {
				opts = append(opts, struct{ Name, Desc string }{Name: p.Name, Desc: string(p.Source) + " — " + p.Desc})
			}
			sel, err := ui.Pick("select a package", opts)
			if err != nil {
				return err
			}
			if sel == "" {
				return nil
			}
			return runInstall(app, []string{sel})
		},
	}
	c.Flags().BoolVarP(&install, "install", "i", false, "interactive picker → install selection")
	return c
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
