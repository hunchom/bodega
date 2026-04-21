package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend/brew"
	"github.com/hunchom/bodega/internal/ui/theme"
	"github.com/hunchom/bodega/internal/verify"
)

// apiDepResolver adapts brew.APICache to verify.DepResolver. It's the
// production backing for the missing-dep check; tests inject their own
// stub directly instead of going through this type.
type apiDepResolver struct{}

func (apiDepResolver) Deps(name string) ([]string, error) {
	ac := brew.SharedAPICache()
	if ac == nil {
		return nil, nil
	}
	p, err := ac.Lookup(name)
	if err != nil || p == nil {
		return nil, err
	}
	return p.Deps, nil
}

func newVerifyCmd() *cobra.Command {
	var fix bool
	c := &cobra.Command{
		Use:   "verify",
		Short: "Deep integrity check of the install tree",
		Long: "Walks the Homebrew prefix for missing runtime deps, broken symlinks,\n" +
			"orphaned Cellar versions, and stale pins. Exits 1 if any issues are found.",
		RunE: func(c *cobra.Command, _ []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.CloseJournal()

			rep, err := verify.Run(verify.Options{
				Prefix:  brew.Prefix(),
				Fix:     fix,
				APIDeps: apiDepResolver{},
			})
			if err != nil {
				return err
			}

			if app.W.JSON {
				if err := app.W.Print(rep); err != nil {
					return err
				}
				if !rep.Passed {
					return &ExitErr{Code: 1}
				}
				return nil
			}

			renderReport(app, rep)
			if !rep.Passed {
				return &ExitErr{Code: 1}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&fix, "fix", false, "remove broken symlinks automatically")
	return c
}

// allKinds is the list rendered in the human output so clean checks show
// as "✓ kind — none" alongside the failing groups. Keeps the output stable
// across runs; users can eyeball whether a new check category has landed.
var allKinds = []verify.IssueKind{
	verify.KindMissingDep,
	verify.KindBrokenSymlink,
	verify.KindOrphaned,
	verify.KindStalePin,
	verify.KindUnreadable,
}

func renderReport(app *AppCtx, rep *verify.Report) {
	groups := map[verify.IssueKind][]verify.Issue{}
	for _, is := range rep.Issues {
		groups[is.Kind] = append(groups[is.Kind], is)
	}

	if rep.Passed {
		app.W.Printf("%s %s\n", theme.OK.Render("✓"), "all good")
		return
	}

	for _, k := range allKinds {
		gs := groups[k]
		if len(gs) == 0 {
			app.W.Printf("%s %s — none\n", theme.OK.Render("✓"), string(k))
			continue
		}
		app.W.Printf("%s %s (%d)\n", theme.Err.Render("✗"), string(k), len(gs))
		// Stable ordering inside a group for deterministic output.
		sort.SliceStable(gs, func(i, j int) bool {
			if gs[i].Package != gs[j].Package {
				return gs[i].Package < gs[j].Package
			}
			return gs[i].Path < gs[j].Path
		})
		for _, is := range gs {
			app.W.Printf("  %s %s\n", theme.Muted.Render("•"), formatIssue(is))
		}
	}
}

func formatIssue(is verify.Issue) string {
	switch is.Kind {
	case verify.KindMissingDep:
		if is.Detail != "" {
			return fmt.Sprintf("%s — %s", is.Package, is.Detail)
		}
		return is.Package
	case verify.KindBrokenSymlink:
		if is.Detail != "" {
			return fmt.Sprintf("%s -> %s", is.Path, is.Detail)
		}
		return is.Path
	case verify.KindOrphaned:
		if is.Detail != "" {
			return fmt.Sprintf("%s (%s)", is.Path, is.Detail)
		}
		return is.Path
	case verify.KindStalePin:
		return is.Package
	case verify.KindUnreadable:
		if is.Path != "" {
			return fmt.Sprintf("%s: %s", is.Path, is.Detail)
		}
		return is.Detail
	}
	return fmt.Sprintf("%s %s %s", is.Package, is.Path, is.Detail)
}
