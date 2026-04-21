package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newLogCmd() *cobra.Command {
	var limit int
	c := &cobra.Command{
		Use:   "log <pkg>",
		Short: "Show per-package event history",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			if err := app.ensureJournal(); err != nil {
				return err
			}
			name := args[0]
			events, err := app.Journal.PackageLog(app.Ctx, name, limit)
			if err != nil {
				return err
			}
			if app.W.JSON {
				return app.W.Print(events)
			}
			if len(events) == 0 {
				app.W.Println(theme.Muted.Render(fmt.Sprintf("no events for %s", name)))
				return nil
			}
			rows := make([][]string, 0, len(events))
			for _, e := range events {
				rows = append(rows, []string{
					strconv.FormatInt(e.TxID, 10),
					e.StartedAt.Local().Format("2006-01-02 15:04"),
					colorAction(e.Action),
					fmt.Sprintf("%s → %s", emptyDash(e.FromVersion), emptyDash(e.ToVersion)),
					e.Verb,
				})
			}
			app.W.Printf("%s", (ui.Table{
				Headers: []string{"tx", "when", "action", "from → to", "verb"},
				Rows:    rows,
			}).Render())
			return nil
		},
	}
	c.Flags().IntVar(&limit, "limit", 50, "max events to show")
	return c
}

func colorAction(a string) string {
	switch a {
	case "installed", "upgraded":
		return theme.OK.Render(a)
	case "reinstalled":
		return theme.Warn.Render(a)
	case "removed":
		return theme.Err.Render(a)
	default:
		return a
	}
}
