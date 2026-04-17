package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newHistoryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "history",
		Short: "Transaction history",
		RunE: func(c *cobra.Command, _ []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			if err := app.ensureJournal(); err != nil {
				return err
			}
			txs, err := app.Journal.Recent(app.Ctx, 50)
			if err != nil {
				return err
			}
			if app.W.JSON {
				return app.W.Print(txs)
			}
			if len(txs) == 0 {
				app.W.Println(theme.Muted.Render("no history yet"))
				return nil
			}
			rows := make([][]string, 0, len(txs))
			for _, t := range txs {
				status := theme.OK.Render("✓")
				if t.ExitCode != 0 {
					status = theme.Err.Render("✗")
				}
				rows = append(rows, []string{
					strconv.FormatInt(t.ID, 10),
					t.StartedAt.Format("2006-01-02 15:04"),
					t.Verb,
					status,
					t.Cmdline,
				})
			}
			app.W.Printf("%s", (ui.Table{
				Headers: []string{"id", "when", "verb", "", "cmd"},
				Rows:    rows,
			}).Render())
			return nil
		},
	}
	c.AddCommand(newHistoryInfoCmd())
	c.AddCommand(newHistoryUndoCmd())
	return c
}

func newHistoryInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <id>",
		Short: "Show details of a transaction",
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
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return err
			}
			tx, err := app.Journal.Get(app.Ctx, id)
			if err != nil {
				return err
			}
			if app.W.JSON {
				return app.W.Print(tx)
			}
			p := ui.Panel{
				Title: fmt.Sprintf("tx #%d — %s", tx.ID, tx.Verb),
				Fields: []ui.Field{
					{Key: "started", Value: tx.StartedAt.Format("2006-01-02 15:04:05")},
					{Key: "ended", Value: tx.EndedAt.Format("2006-01-02 15:04:05")},
					{Key: "exit", Value: strconv.Itoa(tx.ExitCode)},
					{Key: "cmd", Value: tx.Cmdline},
				},
			}
			app.W.Printf("%s", p.Render())
			for _, pk := range tx.Packages {
				app.W.Printf("  %s %-25s %s → %s\n", pk.Action, pk.Name, emptyDash(pk.FromVersion), emptyDash(pk.ToVersion))
			}
			return nil
		},
	}
}

func newHistoryUndoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "undo <id>",
		Short: "Roll back a transaction",
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
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return err
			}
			return runRollback(app, id)
		},
	}
}
