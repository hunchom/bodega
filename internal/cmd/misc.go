package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/ui/theme"
)

func newProvidesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "provides <cmd>",
		Aliases: []string{"whatprovides"},
		Short:   "Find which formula installs a command",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			names, err := app.Registry.Primary().Provides(app.Ctx, args[0])
			if err != nil {
				return err
			}
			if app.W.JSON {
				return app.W.Print(names)
			}
			for _, n := range names {
				app.W.Println(n)
			}
			return nil
		},
	}
}

func newRepolistCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repolist",
		Short: "List active taps",
		RunE: func(c *cobra.Command, _ []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			taps, err := app.Registry.Primary().Taps(app.Ctx)
			if err != nil {
				return err
			}
			if app.W.JSON {
				return app.W.Print(taps)
			}

			// Width of the divider: the widest of the header word "tap" or
			// any tap name, clamped to at least the sample width used in the
			// spec (30) so single-tap machines don't get a stubby line.
			w := len("tap")
			for _, t := range taps {
				if n := len(t); n > w {
					w = n
				}
			}
			if w < 30 {
				w = 30
			}
			app.W.Printf(" %s\n", theme.Header.Render("tap"))
			app.W.Printf(" %s\n", theme.Muted.Render(strings.Repeat("─", w)))
			for _, t := range taps {
				app.W.Printf(" %s\n", t)
			}
			app.W.Println()
			noun := "taps"
			if len(taps) == 1 {
				noun = "tap"
			}
			app.W.Printf(" %s\n", theme.Muted.Render(fmt.Sprintf("%d %s", len(taps), noun)))
			return nil
		},
	}
}

func newCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean [all]",
		Short: "Remove old versions and caches",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			deep := len(args) == 1 && args[0] == "all"
			return app.Registry.Primary().Cleanup(app.Ctx, deep)
		},
	}
}

func newPinCmd(pin bool) *cobra.Command {
	use := "pin"
	if !pin {
		use = "unpin"
	}
	return &cobra.Command{
		Use:   use + " <pkg>",
		Short: fmt.Sprintf("%s a package version", use),
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			return app.Registry.Primary().Pin(app.Ctx, args[0], pin)
		},
	}
}
