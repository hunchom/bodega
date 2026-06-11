package brew

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hunchom/bodega/internal/index"
	"github.com/hunchom/bodega/internal/runner"
)

// TestUpgradeReturnsUpgradedNames: Upgrade reports the set it acted on so a
// no-arg bulk upgrade can be journaled. A source-only formula routes to brew
// (fake runner) and comes back in the returned slice.
func TestUpgradeReturnsUpgradedNames(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prefix := t.TempDir()
	installWithStubbedPrefix(t, prefix)
	fixtureIndex(t, `[{"name":"foo","versions":{"stable":"2.0"}}]`) // no bottle → via brew

	b := &Brew{R: &runner.Fake{}} // exit 0 for "brew upgrade -- foo"
	upgraded, err := b.Upgrade(context.Background(), []string{"foo"}, nil)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(upgraded) != 1 || upgraded[0] != "foo" {
		t.Fatalf("upgraded=%v want [foo]", upgraded)
	}
}

// pinCapturePW captures progress output for assertions.
type pinCapturePW struct{ buf strings.Builder }

func (p *pinCapturePW) Write(b []byte) (int, error) { return p.buf.Write(b) }
func (p *pinCapturePW) Step(string)                 {}

// pinFailDoer fails every request so a test that should make no network call
// surfaces a fast error instead of hanging if the no-call assumption breaks.
type pinFailDoer struct{}

func (pinFailDoer) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("network disabled in test")
}

// TestUpgradeSkipsExplicitlyNamedPinned: `yum upgrade <pinned>` must honor the
// pin even when the formula is named, never installing a new version.
func TestUpgradeSkipsExplicitlyNamedPinned(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prefix := t.TempDir()
	installWithStubbedPrefix(t, prefix)

	pinnedDir := filepath.Join(prefix, "var", "homebrew", "pinned")
	if err := os.MkdirAll(pinnedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pinnedDir, "foo"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureIndex(t, `[{"name":"foo","versions":{"stable":"2.0"},"bottle":{"stable":{"files":{"all":{"url":"https://ghcr/foo","sha256":"aa"}}}}}]`)

	prev := httpClient
	httpClient = pinFailDoer{}
	t.Cleanup(func() { httpClient = prev })

	pw := &pinCapturePW{}
	b := &Brew{}
	if _, err := b.Upgrade(context.Background(), []string{"foo"}, pw); err != nil {
		t.Fatalf("pinned formula must be skipped, not installed; got err: %v", err)
	}
	if !strings.Contains(pw.buf.String(), "skipping pinned foo") {
		t.Fatalf("want 'skipping pinned foo', got %q", pw.buf.String())
	}
}

// fixtureIndex builds an in-temp index from a formula JSON payload and installs
// it as the process-wide override for the test's duration.
func fixtureIndex(t *testing.T, formulaJSON string) {
	t.Helper()
	st, err := index.Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Rebuild(context.Background(), []byte(formulaJSON), []byte(`[]`), "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	testIndexOverride = st
	t.Cleanup(func() { testIndexOverride = nil; st.Close() })
}

func mkKeg(t *testing.T, prefix, name, ver string, onRequest bool) {
	t.Helper()
	dir := filepath.Join(prefix, "Cellar", name, ver)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	receipt := `{"installed_on_request":false}`
	if onRequest {
		receipt = `{"installed_on_request":true}`
	}
	if err := os.WriteFile(filepath.Join(dir, "INSTALL_RECEIPT.json"), []byte(receipt), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAutoremoveNativeRemovesOnlyOrphans(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prefix := t.TempDir()
	installWithStubbedPrefix(t, prefix)
	fixtureIndex(t, `[
	  {"name":"leaf","dependencies":["dep"]},
	  {"name":"dep"},
	  {"name":"orphan"}
	]`)

	mkKeg(t, prefix, "leaf", "1.0", true)    // user-requested
	mkKeg(t, prefix, "dep", "1.0", false)    // dependency of leaf → needed
	mkKeg(t, prefix, "orphan", "1.0", false) // nobody needs it, not requested

	b := &Brew{}
	if err := b.Autoremove(context.Background(), nil); err != nil {
		t.Fatalf("autoremove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "orphan")); !os.IsNotExist(err) {
		t.Fatal("orphan should have been removed")
	}
	for _, keep := range []string{"leaf", "dep"} {
		if _, err := os.Stat(filepath.Join(prefix, "Cellar", keep, "1.0")); err != nil {
			t.Fatalf("%s should have been kept: %v", keep, err)
		}
	}
}

func TestCleanupNativePrunesOldVersions(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prefix := t.TempDir()
	installWithStubbedPrefix(t, prefix)

	// Two versions of foo; opt/foo points at the newer one (the linked keg).
	for _, v := range []string{"1.0", "2.0"} {
		d := filepath.Join(prefix, "Cellar", "foo", v, "bin")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "foo"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(prefix, "opt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(prefix, "Cellar", "foo", "2.0"), filepath.Join(prefix, "opt", "foo")); err != nil {
		t.Fatal(err)
	}

	b := &Brew{}
	if err := b.Cleanup(context.Background(), false); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "foo", "1.0")); !os.IsNotExist(err) {
		t.Fatal("old version 1.0 should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "foo", "2.0")); err != nil {
		t.Fatal("linked version 2.0 should have been kept")
	}
}
