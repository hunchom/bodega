package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/journal"
	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
	"github.com/hunchom/bodega/internal/version"
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
			defer app.CloseJournal()
			maybeRefreshTaps(app)
			return runInstall(app, args)
		},
	}
}

// runInstall is shared by install and search --install.
func runInstall(app *AppCtx, names []string) error {
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			return fmt.Errorf("install: empty package name")
		}
	}
	if Flags.DryRun {
		app.W.Printf("%s would install %s\n", theme.Muted.Render("dry-run"), strings.Join(names, " "))
		return nil
	}
	if err := app.ensureJournal(); err != nil {
		return err
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
	var installed []string
	failed := []map[string]string{}

	for _, n := range names {
		// Resolve source for journaling.
		_, pkg, rerr := app.Registry.Resolve(app.Ctx, n)
		src := "formula"
		if pkg != nil {
			src = string(pkg.Source)
		}
		_ = rerr

		useLive := app.W.IsTTY() && !app.W.JSON
		if useLive {
			app.W.Printf("%s %s %s\n", theme.Muted.Render("→"), theme.Muted.Render("installing"), theme.Bold.Render(n))
		}

		// Live TTY: spinners/bars + restyled brew passthrough, so big
		// downloads stop looking frozen. Otherwise buffer for the
		// on-failure dump.
		var buf bytes.Buffer
		var pw backend.ProgressWriter
		var lv *ui.Live
		if useLive {
			lv = ui.NewLive(app.W.Out)
			pw = &livePW{L: lv}
		} else {
			pw = &backend.StreamPW{W: &buf}
		}
		err := app.Registry.Primary().Install(app.Ctx, []string{n}, pw)
		if lv != nil {
			lv.Close()
		}
		if err != nil {
			last = err
			exit = 1
			failed = append(failed, map[string]string{"package": n, "error": err.Error()})
			if !app.W.JSON {
				app.W.Errorf("%s %s: %s\n", theme.Err.Render("✗"), n, err.Error())
				_ = app.ensureCfg()
				if buf.Len() > 0 && (app.Cfg == nil || !app.Cfg.Defaults.Parallel || os.Getenv("YUM_DEBUG") != "") {
					app.W.Errorf("%s\n", buf.String())
				}
			}
			continue
		}
		txPkgs = append(txPkgs, journal.TxPackage{
			Name: n, ToVersion: versionOf(app, n), Source: src, Action: "installed",
		})
		installed = append(installed, n)
		if !app.W.JSON && lv == nil { // Live already showed per-pkg ✓
			app.W.Printf("%s %s\n", theme.OK.Render("✓"), n)
		}
	}

	if err := app.Journal.End(app.Ctx, txID, exit, txPkgs); err != nil {
		return err
	}
	if app.W.JSON {
		if installed == nil {
			installed = []string{}
		}
		payload := map[string]any{
			"installed": installed,
			"failed":    failed,
		}
		if last != nil {
			payload["error"] = "install: one or more packages failed"
		}
		// Propagate a JSON write failure only when no more-specific install
		// error is already pending (that one is more informative).
		if perr := app.W.Print(payload); perr != nil && last == nil {
			return perr
		}
	}
	if last != nil {
		return fmt.Errorf("install: one or more packages failed")
	}
	return nil
}

func versionStr() string { return version.Version }

// brewVersion returns the first line of `brew --version` output, or "" if brew
// isn't on PATH or the probe fails. Stored in the journal so audits can tell
// which brew produced a given transaction.
func brewVersion() string {
	out, err := exec.Command("brew", "--version").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return line
}

func versionOf(app *AppCtx, name string) string {
	p, err := app.Registry.Primary().Info(app.Ctx, name)
	if err != nil || p == nil {
		return ""
	}
	return p.Version
}
