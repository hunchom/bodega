package cmd

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/journal"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newRemoveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "remove <pkg>...",
		Aliases: []string{"erase"},
		Short:   "Remove packages",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			return runMutate(app, "remove", args, func(names []string, pw backend.ProgressWriter) error {
				return app.Registry.Primary().Remove(app.Ctx, names, pw)
			}, "removed")
		},
	}
	return c
}

func newReinstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reinstall <pkg>...",
		Short: "Reinstall packages",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			return runMutate(app, "reinstall", args, func(names []string, pw backend.ProgressWriter) error {
				return app.Registry.Primary().Reinstall(app.Ctx, names, pw)
			}, "reinstalled")
		},
	}
}

func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "upgrade [pkg]...",
		Aliases: []string{"update"},
		Short:   "Update taps and upgrade packages",
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			maybeRefreshTaps(app)
			return runMutate(app, "upgrade", args, func(names []string, pw backend.ProgressWriter) error {
				return app.Registry.Primary().Upgrade(app.Ctx, names, pw)
			}, "upgraded")
		},
	}
}

func newAutoremoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "autoremove",
		Short: "Remove orphaned dependencies",
		RunE: func(c *cobra.Command, _ []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			if Flags.DryRun {
				app.W.Printf("%s would autoremove orphaned deps\n", theme.Muted.Render("dry-run"))
				return nil
			}
			if err := app.ensureJournal(); err != nil {
				return err
			}
			txID, err := app.Journal.Begin(app.Ctx, "autoremove",
				journal.Cmdline([]string{"yum", "autoremove"}), versionStr(), brewVersion())
			if err != nil {
				return err
			}

			app.W.Printf("%s %s\n", theme.Muted.Render("→"), "scanning for orphaned dependencies")
			var buf bytes.Buffer
			pw := &backend.StreamPW{W: &buf}
			runErr := app.Registry.Primary().Autoremove(app.Ctx, pw)

			if runErr != nil {
				app.W.Errorf("%s %s\n", theme.Err.Render("✗"), runErr.Error())
				app.W.Errorf("%s\n", buf.String())
				_ = app.Journal.End(app.Ctx, txID, 1, nil)
				return fmt.Errorf("autoremove: failed")
			}

			removed := parseUninstalled(buf.Bytes())
			var txPkgs []journal.TxPackage
			for _, n := range removed {
				txPkgs = append(txPkgs, journal.TxPackage{Name: n, Source: "formula", Action: "removed"})
			}
			if err := app.Journal.End(app.Ctx, txID, 0, txPkgs); err != nil {
				return err
			}

			switch len(removed) {
			case 0:
				app.W.Printf("%s %s\n", theme.OK.Render("✓"), "nothing to remove")
			default:
				app.W.Printf("%s %s %d %s: %s\n",
					theme.OK.Render("✓"),
					"removed",
					len(removed),
					pluralize("package", len(removed)),
					strings.Join(removed, ", "),
				)
			}
			return nil
		},
	}
}

// parseUninstalled scans brew stdout for lines like
// "Uninstalling /opt/homebrew/Cellar/<name>/<version>..." and returns the
// set of formula names in the order they appeared. If we can't pull any
// names out, callers fall back to a plain "✓ done".
func parseUninstalled(b []byte) []string {
	var names []string
	seen := map[string]bool{}
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimSpace(line)
		_, rest, ok := strings.Cut(line, "Uninstalling ")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		// Forms we tolerate:
		//   Uninstalling /opt/homebrew/Cellar/foo/1.2.3...
		//   Uninstalling foo... (1 files, 1KB)
		// Strip a trailing "..." and any parenthetical suffix.
		if i := strings.Index(rest, "("); i >= 0 {
			rest = strings.TrimSpace(rest[:i])
		}
		rest = strings.TrimSuffix(rest, "...")
		rest = strings.TrimSpace(rest)
		if rest == "" {
			continue
		}
		var name string
		if strings.Contains(rest, "/Cellar/") {
			// Path form: .../Cellar/<name>/<version>
			segs := strings.Split(rest, "/")
			for i := 0; i < len(segs)-1; i++ {
				if segs[i] == "Cellar" {
					name = segs[i+1]
					break
				}
			}
		} else {
			// Bare-name form.
			name = strings.Fields(rest)[0]
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func pluralize(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func runMutate(app *AppCtx, verb string, names []string, doer func([]string, backend.ProgressWriter) error, action string) error {
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			return fmt.Errorf("%s: empty package name", verb)
		}
	}
	if Flags.DryRun {
		app.W.Printf("%s would %s %s\n", theme.Muted.Render("dry-run"), verb, strings.Join(names, " "))
		return nil
	}
	if err := app.ensureJournal(); err != nil {
		return err
	}
	txID, err := app.Journal.Begin(app.Ctx, verb,
		journal.Cmdline(append([]string{"yum", verb}, names...)), versionStr(), brewVersion())
	if err != nil {
		return err
	}

	var txPkgs []journal.TxPackage
	exit := 0
	var buf bytes.Buffer
	pw := &backend.StreamPW{W: &buf}

	if err := doer(names, pw); err != nil {
		exit = 1
		app.W.Errorf("%s %s\n", theme.Err.Render("✗"), err.Error())
		_ = app.ensureCfg()
		if app.Cfg == nil || !app.Cfg.Defaults.Parallel {
			app.W.Errorf("%s\n", buf.String())
		}
	} else {
		for _, n := range names {
			txPkgs = append(txPkgs, journal.TxPackage{Name: n, Source: "formula", Action: action})
			app.W.Printf("%s %s\n", theme.OK.Render("✓"), n)
		}
	}
	if err := app.Journal.End(app.Ctx, txID, exit, txPkgs); err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("%s: failed", verb)
	}
	return nil
}
