package brew

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hunchom/bodega/internal/index"
)

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
