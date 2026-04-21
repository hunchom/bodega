package cmd

import (
	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/backend/brew"
	"github.com/hunchom/bodega/internal/ui"
)

// defaultHumanLimit caps the table rendered on a TTY. JSON output is
// uncapped unless --limit is set explicitly.
const defaultHumanLimit = 50

func newSearchCmd() *cobra.Command {
	var (
		install      bool
		deps         bool
		nameOnly     bool
		limit        int
		limitChanged bool
	)
	c := &cobra.Command{
		Use:   "search <term>",
		Short: "Search across formulae and casks",
		Long:  "Searches formula/cask names, descriptions, and taps. Results are ranked by relevance.",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()

			limitChanged = c.Flags().Changed("limit")
			term := args[0]

			// Prefer the rich, ranked path. The brew backend exports it via
			// RichSearcher; anything else falls back to the flat native
			// search so we don't lose non-brew backends.
			results, ok := richSearch(app.Registry.Primary(), term, brew.SearchOpts{
				NameOnly:    nameOnly,
				IncludeDeps: deps,
				Limit:       effectiveLimit(limit, limitChanged, app.W.JSON),
			})
			if !ok {
				pkgs, err := app.Registry.Search(app.Ctx, term)
				if err != nil {
					return err
				}
				return renderFlatSearch(app, pkgs, install)
			}

			if app.W.JSON {
				// Keep the JSON shape stable: a flat []backend.Package array.
				// Rank / match_kind are human-only polish; callers scripting
				// against `--json` don't want their keys to drift.
				pkgs := make([]backend.Package, 0, len(results))
				for _, r := range results {
					pkgs = append(pkgs, r.Pkg)
				}
				return app.W.Print(pkgs)
			}

			if len(results) == 0 {
				app.W.Errorln("no matches")
				return nil
			}

			if !install || !app.W.IsTTY() {
				app.W.Printf("%s", renderSearchTable(results))
				return nil
			}

			// Interactive picker: reuse the first column (with the ★ prefix
			// stripped) so the install path still gets a clean formula name.
			opts := make([]struct{ Name, Desc string }, 0, len(results))
			for _, r := range results {
				opts = append(opts, struct{ Name, Desc string }{
					Name: r.Pkg.Name,
					Desc: string(r.Pkg.Source) + " — " + r.Pkg.Desc,
				})
			}
			sel, err := ui.Pick("select a package", opts)
			if err != nil {
				return err
			}
			if sel == "" {
				return nil
			}
			return runInstall(app, []string{sel})
		},
	}
	c.Flags().BoolVarP(&install, "install", "i", false, "interactive picker → install selection")
	c.Flags().BoolVar(&deps, "deps", false, "also match formulae that depend on a match")
	c.Flags().BoolVar(&nameOnly, "name-only", false, "match formula/cask names only (no desc/tap)")
	c.Flags().IntVar(&limit, "limit", defaultHumanLimit, "cap results (0 = no cap)")
	return c
}

// richSearch attempts the ranked path on the backend's primary. Returns
// (results, true) on success and (nil, false) when rich search is
// unavailable or failed so the caller can fall back to the flat path.
func richSearch(b backend.Backend, q string, opts brew.SearchOpts) ([]brew.Result, bool) {
	rs, ok := b.(brew.RichSearcher)
	if !ok {
		return nil, false
	}
	res, err := rs.SearchRich(q, opts)
	if err != nil {
		return nil, false
	}
	return res, true
}

// effectiveLimit resolves the user-facing --limit flag against the output
// mode. JSON defaults to uncapped; the human/TTY path defaults to
// defaultHumanLimit. Either default can be overridden by passing --limit
// explicitly (including --limit 0 for uncapped).
func effectiveLimit(limit int, changed, isJSON bool) int {
	if changed {
		return limit
	}
	if isJSON {
		return 0
	}
	return defaultHumanLimit
}

// renderSearchTable renders the ranked results as a four-column table. The
// ★ prefix on the name column signals a match that came from description,
// tap, or a dep edge rather than the name itself.
func renderSearchTable(results []brew.Result) string {
	rows := make([][]string, 0, len(results))
	for _, r := range results {
		name := r.Pkg.Name
		if r.MatchKind != brew.MatchName {
			name = "★ " + name
		}
		ver := r.Pkg.Version
		if ver == "" {
			ver = r.Pkg.Latest
		}
		rows = append(rows, []string{
			name,
			ver,
			string(r.Pkg.Source),
			truncate(r.Pkg.Desc, 50),
		})
	}
	return (ui.Table{
		Headers: []string{"name", "ver", "src", "desc"},
		Aligns:  []ui.Align{ui.AlignLeft, ui.AlignLeft, ui.AlignLeft, ui.AlignLeft},
		Rows:    rows,
	}).Render()
}

// renderFlatSearch preserves the old human / JSON / picker UX for backends
// that don't implement RichSearcher. Kept as its own helper so the common
// path above stays readable.
func renderFlatSearch(app *AppCtx, pkgs []backend.Package, install bool) error {
	if app.W.JSON {
		return app.W.Print(pkgs)
	}
	if len(pkgs) == 0 {
		app.W.Errorln("no matches")
		return nil
	}
	if !install || !app.W.IsTTY() {
		rows := make([][]string, 0, len(pkgs))
		for _, p := range pkgs {
			rows = append(rows, []string{p.Name, string(p.Source), truncate(p.Desc, 50)})
		}
		app.W.Printf("%s", (ui.Table{
			Headers: []string{"name", "src", "desc"},
			Aligns:  []ui.Align{ui.AlignLeft, ui.AlignLeft, ui.AlignLeft},
			Rows:    rows,
		}).Render())
		return nil
	}
	opts := make([]struct{ Name, Desc string }, 0, len(pkgs))
	for _, p := range pkgs {
		opts = append(opts, struct{ Name, Desc string }{Name: p.Name, Desc: string(p.Source) + " — " + p.Desc})
	}
	sel, err := ui.Pick("select a package", opts)
	if err != nil {
		return err
	}
	if sel == "" {
		return nil
	}
	return runInstall(app, []string{sel})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
