package ui

import (
	"os"
	"testing"

	"github.com/hunchom/bodega/internal/ui/theme"
)

func TestTableGolden(t *testing.T) {
	theme.NoColor = true
	theme.Load()
	defer func() { theme.NoColor = false; theme.Load() }()

	tbl := Table{
		Headers: []string{"name", "ver", "size"},
		Aligns:  []Align{AlignLeft, AlignLeft, AlignRight},
		Rows: [][]string{
			{"ripgrep", "14.1.0", "5 MB"},
			{"jq", "1.7.1", "1 MB"},
		},
	}
	got := tbl.Render()
	want, _ := os.ReadFile("testdata/table_basic.txt")
	if got != string(want) {
		t.Fatalf("mismatch\n--- got ---\n%s\n--- want ---\n%s", got, string(want))
	}
}
