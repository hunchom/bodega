package ui

import (
	"os"
	"testing"

	"github.com/hunchom/bodega/internal/ui/theme"
)

func TestPanelGolden(t *testing.T) {
	theme.NoColor = true
	theme.Load()
	defer func() { theme.NoColor = false; theme.Load() }()

	p := Panel{
		Title: "ripgrep",
		Fields: []Field{
			{"version", "14.1.0"},
			{"tap", "homebrew/core"},
			{"size", "5.2 MB"},
		},
	}
	got := p.Render()
	want, _ := os.ReadFile("testdata/panel_basic.txt")
	if got != string(want) {
		t.Fatalf("mismatch\n--- got ---\n%s\n--- want ---\n%s", got, string(want))
	}
}
