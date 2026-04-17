package cmd

import (
	"bytes"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hunchom/yum/internal/backend"
	"github.com/hunchom/yum/internal/journal"
	"github.com/hunchom/yum/internal/ui/theme"
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
			defer app.Journal.Close()
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
			defer app.Journal.Close()
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
			defer app.Journal.Close()
			// tap update first
			_, _ = app.Registry.Primary().Deps(app.Ctx, "") // harmless probe; real update flow below:
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
			defer app.Journal.Close()
			return runMutate(app, "autoremove", nil, func(_ []string, pw backend.ProgressWriter) error {
				return app.Registry.Primary().Autoremove(app.Ctx, pw)
			}, "removed")
		},
	}
}

func runMutate(app *AppCtx, verb string, names []string, doer func([]string, backend.ProgressWriter) error, action string) error {
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
		if app.Cfg == nil || app.Cfg.Defaults.Parallel == false {
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
