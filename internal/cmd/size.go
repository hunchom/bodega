package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/ui"
	"github.com/hunchom/bodega/internal/ui/theme"
)

func newSizeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "size [pkg]",
		Short: "Disk usage per installed package",
		RunE: func(c *cobra.Command, args []string) error {
			app, err := boot()
			if err != nil {
				return err
			}
			defer app.Journal.Close()

			pkgs, err := app.Registry.Primary().List(app.Ctx, backend.ListInstalled)
			if err != nil {
				return err
			}
			prefix := brewCellar()
			type row struct {
				name string
				size int64
			}
			// Filter to the target package first so we only spawn work we
			// actually need.
			targets := make([]backend.Package, 0, len(pkgs))
			for _, p := range pkgs {
				if len(args) == 1 && p.Name != args[0] {
					continue
				}
				targets = append(targets, p)
			}

			// Parallel walk: dirSize is almost pure stat-bound I/O, so we
			// saturate GOMAXPROCS workers easily. On an M-series with ~200
			// formulae this drops from ~1.4s serial to ~150-200ms.
			workers := runtime.GOMAXPROCS(0)
			if workers > len(targets) {
				workers = len(targets)
			}
			if workers < 1 {
				workers = 1
			}

			jobs := make(chan backend.Package, len(targets))
			results := make(chan row, len(targets))
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for p := range jobs {
						results <- row{name: p.Name, size: dirSize(filepath.Join(prefix, p.Name))}
					}
				}()
			}
			for _, p := range targets {
				jobs <- p
			}
			close(jobs)
			wg.Wait()
			close(results)

			rows := make([]row, 0, len(targets))
			var total int64
			for r := range results {
				total += r.size
				rows = append(rows, r)
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].size > rows[j].size })

			if app.W.JSON {
				out := make([]map[string]any, len(rows))
				for i, r := range rows {
					out[i] = map[string]any{"name": r.name, "bytes": r.size}
				}
				return app.W.Print(map[string]any{"packages": out, "total_bytes": total})
			}

			max := int64(1)
			for _, r := range rows {
				if r.size > max {
					max = r.size
				}
			}

			tbl := [][]string{}
			for _, r := range rows {
				bar := strings.Repeat("█", int(r.size*20/max))
				tbl = append(tbl, []string{r.name, ui.HumanBytes(r.size), theme.Accent.Render(bar)})
			}
			app.W.Printf("%s", (ui.Table{
				Headers: []string{"name", "size", ""},
				Aligns:  []ui.Align{ui.AlignLeft, ui.AlignRight, ui.AlignLeft},
				Rows:    tbl,
			}).Render())
			app.W.Printf("%s %s\n", theme.Muted.Render("total"), ui.HumanBytes(total))
			return nil
		},
	}
}

func brewCellar() string {
	// Prefer Apple Silicon, fallback to Intel. Kept local here rather
	// than importing brew.brewPrefix() because that's intentionally
	// unexported; both implementations prefer /opt/homebrew.
	if st, err := os.Stat("/opt/homebrew/Cellar"); err == nil && st.IsDir() {
		return "/opt/homebrew/Cellar"
	}
	return "/usr/local/Cellar"
}

func dirSize(p string) int64 {
	var total int64
	_ = filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
