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

func TestVersionHelpersPlain(t *testing.T) {
	NoColor = true
	defer func() { NoColor = false; Load() }()
	Load()
	if got := InstalledVersion("1.2.3"); got != "1.2.3" {
		t.Fatalf("InstalledVersion with NoColor = %q, want plain", got)
	}
	if got := LatestVersion("1.2.3"); got != "1.2.3" {
		t.Fatalf("LatestVersion with NoColor = %q, want plain", got)
	}
}

func TestVersionHelpersStyled(t *testing.T) {
	NoColor = false
	Load()
	if got := InstalledVersion("1.2.3"); got == "1.2.3" {
		t.Fatalf("InstalledVersion expected ANSI escapes, got plain")
	}
	if got := LatestVersion("1.2.3"); got == "1.2.3" {
		t.Fatalf("LatestVersion expected ANSI escapes, got plain")
	}
}
