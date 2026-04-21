package cmd

import (
	"bytes"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/backend/brew"
	"github.com/hunchom/bodega/internal/journal"
	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newServicesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "services",
		Short: "Manage brew-installed launchd services",
		RunE:  runServicesList,
	}
	c.AddCommand(
		newServicesListCmd(),
		newServicesStartCmd(),
		newServicesStopCmd(),
		newServicesRestartCmd(),
		newServicesRunCmd(),
		newServicesCleanupCmd(),
	)
	return c
}

func newServicesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List brew-managed services",
		RunE:    runServicesList,
	}
}

func runServicesList(_ *cobra.Command, _ []string) error {
	app, err := boot()
	if err != nil {
		return err
	}
	defer app.CloseJournal()

	bb, ok := app.Registry.Primary().(*brew.Brew)
	if !ok {
		return fmt.Errorf("services: brew backend unavailable")
	}
	svcs, err := bb.ListServices(app.Ctx)
	if err != nil {
		return err
	}

	if app.W.JSON {
		if svcs == nil {
			svcs = []brew.Service{}
		}
		return app.W.Print(svcs)
	}

	if len(svcs) == 0 {
		app.W.Println(theme.Muted.Render("no services yet"))
		return nil
	}

	rows := make([][]string, 0, len(svcs))
	for _, s := range svcs {
		file := s.File
		if file == "" {
			file = theme.Muted.Render("(none)")
		}
		user := s.User
		if user == "" {
			user = theme.Muted.Render("-")
		}
		rows = append(rows, []string{
			s.Name,
			renderServiceStatus(s.Status),
			user,
			file,
		})
	}
	app.W.Printf("%s", (ui.Table{
		Headers: []string{"name", "status", "user", "file"},
		Rows:    rows,
	}).Render())
	return nil
}

func renderServiceStatus(s brew.ServiceStatus) string {
	switch s {
	case brew.SvcStarted:
		return theme.OK.Render(string(s))
	case brew.SvcError:
		return theme.Err.Render(string(s))
	case brew.SvcStopped:
		return theme.Muted.Render(string(s))
	case brew.SvcScheduled:
		return theme.Warn.Render(string(s))
	}
	return string(s)
}

func newServicesStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <name>",
		Short: "Start a service at login and now",
		Args:  cobra.ExactArgs(1),
		RunE:  serviceAction("start"),
	}
}

func newServicesStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop a running service",
		Args:  cobra.ExactArgs(1),
		RunE:  serviceAction("stop"),
	}
}

func newServicesRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <name>",
		Short: "Restart a service",
		Args:  cobra.ExactArgs(1),
		RunE:  serviceAction("restart"),
	}
}

func newServicesRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <name>",
		Short: "Run a service without enabling at login",
		Args:  cobra.ExactArgs(1),
		RunE:  serviceAction("run"),
	}
}

func newServicesCleanupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup",
		Short: "Remove stale launchd service entries",
		Args:  cobra.NoArgs,
		RunE:  serviceAction("cleanup"),
	}
}

// serviceAction returns a cobra RunE that journals a services mutation
// and streams brew's output. Name is args[0] for every subcommand except
// cleanup, which takes no args.
func serviceAction(action string) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, args []string) error {
		app, err := boot()
		if err != nil {
			return err
		}
		defer app.CloseJournal()

		bb, ok := app.Registry.Primary().(*brew.Brew)
		if !ok {
			return fmt.Errorf("services: brew backend unavailable")
		}

		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		return runServicesMutate(app, bb, action, name)
	}
}

func runServicesMutate(app *AppCtx, bb *brew.Brew, action, name string) error {
	verb := "services-" + action
	cmdParts := []string{"yum", "services", action}
	if name != "" {
		cmdParts = append(cmdParts, name)
	}

	if Flags.DryRun {
		target := name
		if target == "" {
			target = "(none)"
		}
		app.W.Printf("%s would %s %s\n", theme.Muted.Render("dry-run"), verb, target)
		return nil
	}
	if err := app.ensureJournal(); err != nil {
		return err
	}
	txID, err := app.Journal.Begin(app.Ctx, verb,
		journal.Cmdline(cmdParts), versionStr(), brewVersion())
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	pw := &backend.StreamPW{W: &buf}
	runErr := bb.ServiceAction(app.Ctx, action, name, pw)

	exit := 0
	var txPkgs []journal.TxPackage
	if runErr != nil {
		exit = 1
		app.W.Errorf("%s %s\n", theme.Err.Render("✗"), runErr.Error())
		app.W.Errorf("%s\n", buf.String())
	} else if name != "" {
		// Cleanup has no single target, so journal with empty packages.
		txPkgs = append(txPkgs, journal.TxPackage{
			Name:   name,
			Source: "service",
			Action: action,
		})
	}
	if err := app.Journal.End(app.Ctx, txID, exit, txPkgs); err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("%s: failed", verb)
	}
	target := name
	if target == "" {
		target = action
	} else {
		target = action + " " + name
	}
	app.W.Printf("%s %s\n", theme.OK.Render("✓"), target)
	return nil
}
