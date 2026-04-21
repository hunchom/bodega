package cmd

import (
	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/tui/browse"
)

// newBrowseCmd wires the interactive TUI. It intentionally keeps the cobra
// surface thin: dependency wiring lives in boot() and the browse package
// itself does the rest.
func newBrowseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "browse",
		Short: "Interactive package browser",
		RunE: func(c *cobra.Command, _ []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			if err := app.ensureJournal(); err != nil {
				return err
			}
			return browse.Run(app.Registry, app.Journal, app.W.Out)
		},
	}
}
