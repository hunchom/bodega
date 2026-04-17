package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hunchom/yum/internal/ui"
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
			defer app.Journal.Close()

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

			p := ui.Panel{
				Title: pkg.Name,
				Fields: []ui.Field{
					{Key: "version", Value: pkg.Version},
					{Key: "source", Value: string(pkg.Source)},
					{Key: "tap", Value: emptyDash(pkg.Tap)},
					{Key: "desc", Value: pkg.Desc},
					{Key: "homepage", Value: pkg.Homepage},
					{Key: "license", Value: emptyDash(pkg.License)},
					{Key: "deps", Value: emptyDash(strings.Join(pkg.Deps, ", "))},
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
