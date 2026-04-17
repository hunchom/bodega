package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/ui/theme"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "System and brew health check",
		RunE: func(c *cobra.Command, _ []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()

			warns, _ := app.Registry.Primary().Doctor(app.Ctx)

			// PATH check for our own binary
			if _, err := os.Stat(os.ExpandEnv("$HOME/.local/bin/yum")); err == nil {
				path := os.Getenv("PATH")
				if !strings.Contains(path, "/.local/bin") {
					warns = append(warns, "Warning: $HOME/.local/bin not in PATH")
				}
			}

			// Broken symlinks in /opt/homebrew/bin
			_ = filepath.Walk("/opt/homebrew/bin", func(p string, info os.FileInfo, _ error) error {
				if info == nil {
					return nil
				}
				if info.Mode()&os.ModeSymlink != 0 {
					if _, err := os.Stat(p); err != nil {
						warns = append(warns, "Warning: broken symlink "+p)
					}
				}
				return nil
			})

			if app.W.JSON {
				return app.W.Print(warns)
			}
			if len(warns) == 0 {
				app.W.Println(theme.OK.Render("✓ system healthy"))
				return nil
			}
			for _, w := range warns {
				app.W.Printf("%s %s\n", theme.Warn.Render("•"), w)
			}
			return nil
		},
	}
}
