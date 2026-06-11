package brew

import (
	"context"
	"strings"
	"testing"

	"github.com/hunchom/bodega/internal/runner"
)

// A streamed mutation that exits non-zero must surface brew's real stderr line,
// not a bald "brew upgrade: exit 1". This is the headline bug: `yum update` →
// `brew upgrade: exit 1` with no reason.
// With the index disabled (TestMain), an explicit upgrade routes through the
// brew path; a failure must surface brew's real stderr line, not "exit N".
func TestUpgradeSurfacesBrewStderr(t *testing.T) {
	fake := &runner.Fake{
		Stderr:   map[string]string{"brew upgrade -- foo": "Warning: noise\nError: cask 'foo' is not installed\n"},
		ExitCode: map[string]int{"brew upgrade -- foo": 1},
	}
	b := &Brew{R: fake}
	_, err := b.Upgrade(context.Background(), []string{"foo"}, nil)
	if err == nil {
		t.Fatal("expected upgrade error")
	}
	if !strings.Contains(err.Error(), "Error: cask 'foo' is not installed") {
		t.Fatalf("error dropped brew reason: %q", err.Error())
	}
}

// When brew prints nothing, fall back to the exit-code form rather than an
// empty "brew upgrade: ".
func TestUpgradeFallsBackToExitCode(t *testing.T) {
	fake := &runner.Fake{ExitCode: map[string]int{"brew upgrade -- foo": 1}}
	b := &Brew{R: fake}
	_, err := b.Upgrade(context.Background(), []string{"foo"}, nil)
	if err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("want exit-code fallback, got %v", err)
	}
}

// RefreshTaps shares the same stderr-capture path.
func TestRefreshTapsSurfacesStderr(t *testing.T) {
	fake := &runner.Fake{
		Stderr:   map[string]string{"brew update --quiet": "Error: could not fetch homebrew/core\n"},
		ExitCode: map[string]int{"brew update --quiet": 1},
	}
	b := &Brew{R: fake}
	err := b.RefreshTaps(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "could not fetch homebrew/core") {
		t.Fatalf("refresh dropped brew reason: %v", err)
	}
}

func TestLastBrewMessage(t *testing.T) {
	if got := lastBrewMessage([]byte("a\nError: boom\n"), []byte("out")); got != "Error: boom" {
		t.Fatalf("stderr last line: %q", got)
	}
	if got := lastBrewMessage([]byte("  \n"), []byte("first\nlast\n")); got != "last" {
		t.Fatalf("stdout fallback: %q", got)
	}
	if got := lastBrewMessage(nil, nil); got != "" {
		t.Fatalf("empty: %q", got)
	}
}
