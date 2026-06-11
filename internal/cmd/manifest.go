package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/backend/brew"
	"github.com/hunchom/bodega/internal/journal"
	"github.com/hunchom/bodega/internal/ui/theme"
)

type Manifest struct {
	Generated string   `toml:"generated"`
	Taps      []string `toml:"taps"`
	Formulae  []string `toml:"formulae"`
	Casks     []string `toml:"casks"`
	Pinned    []string `toml:"pinned"`
}

func newManifestCmd() *cobra.Command {
	c := &cobra.Command{Use: "manifest", Short: "Export/apply a system manifest"}
	c.AddCommand(&cobra.Command{
		Use:   "export [file]",
		Short: "Snapshot current install state",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()

			// Propagate read errors — a backup that silently records an empty
			// install set is worse than no backup.
			taps, err := app.Registry.Primary().Taps(app.Ctx)
			if err != nil {
				return fmt.Errorf("manifest export: taps: %w", err)
			}
			form, err := app.Registry.Primary().List(app.Ctx, backend.ListInstalled)
			if err != nil {
				return fmt.Errorf("manifest export: formulae: %w", err)
			}
			casks, err := app.Registry.Primary().List(app.Ctx, backend.ListCasks)
			if err != nil {
				return fmt.Errorf("manifest export: casks: %w", err)
			}
			pins, err := app.Registry.Primary().List(app.Ctx, backend.ListPinned)
			if err != nil {
				return fmt.Errorf("manifest export: pinned: %w", err)
			}

			m := Manifest{
				Generated: time.Now().UTC().Format(time.RFC3339),
				Taps:      taps,
			}
			for _, p := range form {
				m.Formulae = append(m.Formulae, p.Name)
			}
			for _, p := range casks {
				m.Casks = append(m.Casks, p.Name)
			}
			for _, p := range pins {
				m.Pinned = append(m.Pinned, p.Name)
			}

			b, err := toml.Marshal(m)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return os.WriteFile(args[0], b, 0o644)
			}
			_, err = app.W.Out.Write(b)
			return err
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "apply <file>",
		Short: "Reconcile system to manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			var m Manifest
			if err := toml.Unmarshal(b, &m); err != nil {
				return err
			}

			if Flags.DryRun {
				app.W.Printf("%s would install %d formulae, %d casks; pin %d\n",
					theme.Muted.Render("dry-run"),
					len(m.Formulae), len(m.Casks), len(m.Pinned))
				return nil
			}

			if err := app.ensureJournal(); err != nil {
				return err
			}
			txID, err := app.Journal.Begin(app.Ctx, "manifest-apply",
				journal.Cmdline([]string{"yum", "manifest", "apply", args[0]}),
				versionStr(), brewVersion())
			if err != nil {
				return err
			}
			var txPkgs []journal.TxPackage

			pw := &backend.StreamPW{W: app.W.Out}
			if len(m.Formulae) > 0 {
				app.W.Printf("%s installing %d formulae\n", theme.Muted.Render("→"), len(m.Formulae))
				if err := app.Registry.Primary().Install(app.Ctx, m.Formulae, pw); err != nil {
					// Journal whatever actually installed so undo can reverse it.
					txPkgs = appendSucceeded(txPkgs, err, "formula")
					_ = app.Journal.End(app.Ctx, txID, 1, txPkgs)
					return err
				}
				for _, n := range m.Formulae {
					txPkgs = append(txPkgs, journal.TxPackage{Name: n, Source: "formula", Action: "installed"})
				}
			}
			if len(m.Casks) > 0 {
				app.W.Printf("%s installing %d casks\n", theme.Muted.Render("→"), len(m.Casks))
				if err := app.Registry.Primary().Install(app.Ctx, m.Casks, pw); err != nil {
					txPkgs = appendSucceeded(txPkgs, err, "cask")
					_ = app.Journal.End(app.Ctx, txID, 1, txPkgs)
					return err
				}
				for _, n := range m.Casks {
					txPkgs = append(txPkgs, journal.TxPackage{Name: n, Source: "cask", Action: "installed"})
				}
			}
			var pinFailed []string
			for _, p := range m.Pinned {
				if err := app.Registry.Primary().Pin(app.Ctx, p, true); err != nil {
					pinFailed = append(pinFailed, p)
					app.W.Errorf("%s pin %s: %v\n", theme.Err.Render("✗"), p, err)
					continue
				}
				txPkgs = append(txPkgs, journal.TxPackage{Name: p, Source: "formula", Action: "pinned"})
			}
			exit := 0
			if len(pinFailed) > 0 {
				exit = 1
			}
			if err := app.Journal.End(app.Ctx, txID, exit, txPkgs); err != nil {
				return err
			}
			if len(pinFailed) > 0 {
				return fmt.Errorf("manifest: %d pin(s) failed: %s", len(pinFailed), strings.Join(pinFailed, ", "))
			}
			app.W.Printf("%s %s\n", theme.OK.Render("✓"), "manifest applied")
			return nil
		},
	})
	return c
}

// appendSucceeded records the packages a partially-failed batch Install did land
// (from *brew.PartialError) so `history undo` can reverse them.
func appendSucceeded(txPkgs []journal.TxPackage, err error, source string) []journal.TxPackage {
	var pe *brew.PartialError
	if errors.As(err, &pe) {
		for _, n := range pe.Succeeded {
			txPkgs = append(txPkgs, journal.TxPackage{Name: n, Source: source, Action: "installed"})
		}
	}
	return txPkgs
}
