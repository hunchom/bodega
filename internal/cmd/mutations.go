package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/backend/brew"
	"github.com/hunchom/bodega/internal/journal"
	"github.com/hunchom/bodega/internal/ui"
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
			return runMutate(app, "remove", args, func(names []string, pw backend.ProgressWriter) ([]string, error) {
				return names, app.Registry.Primary().Remove(app.Ctx, names, pw)
			}, "removed", false)
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
			return runMutate(app, "reinstall", args, func(names []string, pw backend.ProgressWriter) ([]string, error) {
				return names, app.Registry.Primary().Reinstall(app.Ctx, names, pw)
			}, "reinstalled", true)
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
			return runMutate(app, "upgrade", args, func(names []string, pw backend.ProgressWriter) ([]string, error) {
				return app.Registry.Primary().Upgrade(app.Ctx, names, pw)
			}, "upgraded", true)
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

// runMutate runs a single batch mutation (remove/reinstall/upgrade) inside one
// journal transaction. When live is true and we're not in --json mode, brew's
// output streams straight to the terminal so a long upgrade doesn't look
// frozen; otherwise it's buffered and dumped on failure. A *brew.PartialError
// from the doer is journaled package-by-package so partial on-disk changes stay
// undoable via `yum history undo`.
func runMutate(app *AppCtx, verb string, names []string, doer func([]string, backend.ProgressWriter) ([]string, error), action string, live bool) error {
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
	var pw backend.ProgressWriter
	var lv *ui.Live
	switch {
	case live && !app.W.JSON && app.W.IsTTY():
		// Live TUI block: per-package spinners/bars for native installs,
		// restyled passthrough for brew subprocess output.
		lv = ui.NewLive(app.W.Out)
		pw = &livePW{L: lv}
	case live && !app.W.JSON:
		pw = &backend.StreamPW{W: app.W.Out} // stream plain; nothing to re-dump on failure
	default:
		pw = &backend.StreamPW{W: &buf} // buffer for on-failure dump / quiet under --json
	}
	var failed []map[string]string
	var runErr error

	affected, err := doer(names, pw)
	if lv != nil {
		lv.Close()
	}
	if err != nil {
		exit = 1
		runErr = err
		// A partial failure carries the names that actually changed on disk —
		// journal those as successes so they remain undoable; mark the rest failed.
		succeeded := map[string]bool{}
		var pe *brew.PartialError
		if errors.As(err, &pe) {
			for _, n := range pe.Succeeded {
				if !succeeded[n] {
					succeeded[n] = true
					txPkgs = append(txPkgs, journal.TxPackage{Name: n, Source: "formula", Action: action})
				}
			}
		}
		for _, n := range names {
			if !succeeded[n] {
				failed = append(failed, map[string]string{"package": n, "error": err.Error()})
			}
		}
		if !app.W.JSON {
			app.W.Errorf("%s %s\n", theme.Err.Render("✗"), err.Error())
			// Show brew's captured output on failure unless suppressed. Gated like
			// install.go: hidden under Defaults.Parallel unless YUM_DEBUG is set.
			// (live mode already streamed it, so buf is empty there.)
			if buf.Len() > 0 {
				_ = app.ensureCfg()
				if app.Cfg == nil || !app.Cfg.Defaults.Parallel || os.Getenv("YUM_DEBUG") != "" {
					app.W.Errorf("%s\n", buf.String())
				}
			}
			for _, n := range names {
				if succeeded[n] && lv == nil { // Live already showed per-pkg ✓
					app.W.Printf("%s %s\n", theme.OK.Render("✓"), n)
				}
			}
		}
	} else {
		// Journal what was actually affected — for a no-arg bulk upgrade the
		// backend resolves its own set, so `names` is empty but `affected` isn't.
		for _, n := range affected {
			txPkgs = append(txPkgs, journal.TxPackage{Name: n, Source: "formula", Action: action})
			if !app.W.JSON && lv == nil { // Live already showed per-pkg ✓
				app.W.Printf("%s %s\n", theme.OK.Render("✓"), n)
			}
		}
	}
	if err := app.Journal.End(app.Ctx, txID, exit, txPkgs); err != nil {
		return err
	}
	if app.W.JSON {
		succeededNames := []string{}
		if exit == 0 {
			succeededNames = affected
		} else {
			for _, p := range txPkgs {
				succeededNames = append(succeededNames, p.Name)
			}
		}
		if failed == nil {
			failed = []map[string]string{}
		}
		payload := map[string]any{
			action:   succeededNames,
			"failed": failed,
		}
		// A no-arg bulk upgrade has no per-package names, so without this the
		// JSON would read {"upgraded":[],"failed":[]} on a real failure — a lie.
		if exit != 0 {
			payload["error"] = fmt.Sprintf("%s: %v", verb, runErr)
		}
		if perr := app.W.Print(payload); perr != nil && exit == 0 {
			return perr
		}
	}
	if exit != 0 {
		// Human mode printed the ✗ line; JSON mode carried the error in
		// the payload. Either way don't double-print via main's "yum:"
		// handler — exit nonzero quietly.
		return &ExitErr{Code: 1}
	}
	return nil
}
