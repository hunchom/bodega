package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <pkg>",
		Short: "Show package info",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()

			_, pkg, err := app.Registry.Resolve(app.Ctx, args[0])
			if err != nil {
				return err
			}
			if pkg == nil {
				return fmt.Errorf("not found: %s", args[0])
			}
			if app.W.JSON {
				return app.W.Print(pkg)
			}

			// Version color follows the browse TUI: amber when the
			// package is actually installed on disk, dim when we're
			// just echoing upstream's latest. InstalledOn is the
			// best signal we have for "on disk right now".
			verStr := pkg.Version
			switch {
			case verStr == "":
				verStr = "-"
			case pkg.InstalledOn != nil:
				verStr = theme.InstalledVersion(verStr)
			default:
				verStr = theme.LatestVersion(verStr)
			}

			deps := emptyDash(strings.Join(pkg.Deps, ", "))
			if len(pkg.Deps) > 0 {
				deps = theme.Muted.Render(strings.Join(pkg.Deps, ", "))
			}

			p := ui.Panel{
				Title: pkg.Name,
				Fields: []ui.Field{
					{Key: "version", Value: verStr},
					{Key: "source", Value: string(pkg.Source)},
					{Key: "tap", Value: emptyDash(pkg.Tap)},
					{Key: "desc", Value: pkg.Desc},
					{Key: "homepage", Value: pkg.Homepage},
					{Key: "license", Value: emptyDash(pkg.License)},
					{Key: "deps", Value: deps},
					{Key: "pinned", Value: fmt.Sprintf("%v", pkg.Pinned)},
				},
			}
			app.W.Printf("%s", p.Render())
			return nil
		},
	}
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
