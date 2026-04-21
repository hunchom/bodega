package cmd

import (
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
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

			taps, _ := app.Registry.Primary().Taps(app.Ctx)
			form, _ := app.Registry.Primary().List(app.Ctx, backend.ListInstalled)
			casks, _ := app.Registry.Primary().List(app.Ctx, backend.ListCasks)
			pins, _ := app.Registry.Primary().List(app.Ctx, backend.ListPinned)

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
					_ = app.Journal.End(app.Ctx, txID, 1, txPkgs)
					return err
				}
				for _, n := range m.Casks {
					txPkgs = append(txPkgs, journal.TxPackage{Name: n, Source: "cask", Action: "installed"})
				}
			}
			for _, p := range m.Pinned {
				if err := app.Registry.Primary().Pin(app.Ctx, p, true); err == nil {
					txPkgs = append(txPkgs, journal.TxPackage{Name: p, Source: "formula", Action: "pinned"})
				}
			}
			if err := app.Journal.End(app.Ctx, txID, 0, txPkgs); err != nil {
				return err
			}
			app.W.Printf("%s %s\n", theme.OK.Render("✓"), "manifest applied")
			return nil
		},
	})
	return c
}
