package cmd

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/journal"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <pkg>...",
		Short: "Install one or more packages",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.Journal.Close()
			return runInstall(app, args)
		},
	}
}

// runInstall is shared by install and search --install.
func runInstall(app *AppCtx, names []string) error {
	if Flags.DryRun {
		app.W.Printf("%s would install %s\n", theme.Muted.Render("dry-run"), strings.Join(names, " "))
		return nil
	}
	txID, err := app.Journal.Begin(app.Ctx, "install",
		journal.Cmdline(append([]string{"yum", "install"}, names...)),
		versionStr(), brewVersion())
	if err != nil {
		return err
	}

	var txPkgs []journal.TxPackage
	exit := 0
	var last error

	for _, n := range names {
		// Resolve source for journaling.
		_, pkg, rerr := app.Registry.Resolve(app.Ctx, n)
		src := "formula"
		if pkg != nil {
			src = string(pkg.Source)
		}
		_ = rerr

		if app.W.IsTTY() {
			app.W.Printf("%s %s\n", theme.Muted.Render("installing"), theme.Bold.Render(n))
		}

		var buf bytes.Buffer
		pw := &backend.StreamPW{W: &buf}
		err := app.Registry.Primary().Install(app.Ctx, []string{n}, pw)
		if err != nil {
			last = err
			exit = 1
			app.W.Errorf("%s\n", theme.Err.Render("✗ "+n+": "+err.Error()))
			if app.Cfg.Defaults.Parallel == false || os.Getenv("YUM_DEBUG") != "" {
				app.W.Errorf("%s\n", buf.String())
			}
			continue
		}
		txPkgs = append(txPkgs, journal.TxPackage{
			Name: n, ToVersion: versionOf(app, n), Source: src, Action: "installed",
		})
		app.W.Printf("%s %s\n", theme.OK.Render("✓"), n)
	}

	if err := app.Journal.End(app.Ctx, txID, exit, txPkgs); err != nil {
		return err
	}
	if last != nil {
		return fmt.Errorf("install: one or more packages failed")
	}
	return nil
}

func versionStr() string { return "dev" } // populated later from version pkg
func brewVersion() string {
	// best-effort; returns "" if brew not on PATH
	return ""
}

func versionOf(app *AppCtx, name string) string {
	p, err := app.Registry.Primary().Info(app.Ctx, name)
	if err != nil || p == nil {
		return ""
	}
	return p.Version
}
