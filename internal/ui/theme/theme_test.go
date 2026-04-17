package theme

import "testing"

func TestStylesRender(t *testing.T) {
	got := Accent.Render("x")
	if got == "" {
		t.Fatal("accent rendered empty")
	}
	if got == "x" && !NoColor {
		t.Fatalf("expected ANSI escapes when color is on, got %q", got)
	}
}

func TestNoColorDisablesANSI(t *testing.T) {
	NoColor = true
	defer func() { NoColor = false; Load() }()
	Load()
	got := Accent.Render("x")
	if got != "x" {
		t.Fatalf("expected plain %q, got %q", "x", got)
	}
}
