package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

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
			rows := make([]row, 0, len(pkgs))
			var total int64
			for _, p := range pkgs {
				if len(args) == 1 && p.Name != args[0] {
					continue
				}
				sz := dirSize(filepath.Join(prefix, p.Name))
				total += sz
				rows = append(rows, row{p.Name, sz})
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
