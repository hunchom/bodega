package verify

import (
	"os"
	"path/filepath"
	"testing"
)

// stubDeps is a test-only DepResolver backed by a plain map. Returning an
// empty slice for unknown names mirrors the production behaviour of
// brew.APICache.Lookup for a name that's installed but has no API entry (a
// tap-local formula, for instance): we shouldn't invent missing deps out of
// thin air when we can't resolve the formula at all.
type stubDeps map[string][]string

func (s stubDeps) Deps(name string) ([]string, error) {
	if d, ok := s[name]; ok {
		return d, nil
	}
	return nil, nil
}

// mkCellar creates $prefix/Cellar/<name>/<version> with an empty bin dir so
// the version folder is "real" on disk. Returns the version dir path for
// callers that want to drop extra fixtures inside.
func mkCellar(t *testing.T, prefix, name, version string) string {
	t.Helper()
	p := filepath.Join(prefix, "Cellar", name, version)
	if err := os.MkdirAll(filepath.Join(p, "bin"), 0o755); err != nil {
		t.Fatalf("mkCellar: %v", err)
	}
	return p
}

// mkOpt links $prefix/opt/<name> -> ../Cellar/<name>/<version>, mirroring
// what Homebrew does on install so we can exercise the orphaned-version
// check against realistic layouts.
func mkOpt(t *testing.T, prefix, name, version string) {
	t.Helper()
	optDir := filepath.Join(prefix, "opt")
	if err := os.MkdirAll(optDir, 0o755); err != nil {
		t.Fatalf("mkOpt: %v", err)
	}
	target := filepath.Join("..", "Cellar", name, version)
	link := filepath.Join(optDir, name)
	_ = os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("mkOpt symlink: %v", err)
	}
}

func TestRunCleanSystemPasses(t *testing.T) {
	prefix := t.TempDir()
	mkCellar(t, prefix, "git", "2.45.0")
	mkCellar(t, prefix, "openssl@3", "3.3.0")
	mkOpt(t, prefix, "git", "2.45.0")
	mkOpt(t, prefix, "openssl@3", "3.3.0")

	r, err := Run(Options{
		Prefix:  prefix,
		APIDeps: stubDeps{"git": {"openssl@3"}, "openssl@3": nil},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.Passed {
		t.Fatalf("clean tree should pass, got issues: %+v", r.Issues)
	}
	if len(r.Issues) != 0 {
		t.Fatalf("want 0 issues, got %d: %+v", len(r.Issues), r.Issues)
	}
}

func TestRunMissingDep(t *testing.T) {
	prefix := t.TempDir()
	mkCellar(t, prefix, "git", "2.45.0")
	mkOpt(t, prefix, "git", "2.45.0")
	// openssl@3 deliberately absent from Cellar.

	r, err := Run(Options{
		Prefix:  prefix,
		APIDeps: stubDeps{"git": {"openssl@3"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Passed {
		t.Fatal("expected Passed=false")
	}
	found := 0
	for _, is := range r.Issues {
		if is.Kind == KindMissingDep && is.Package == "openssl@3" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("want 1 missing-dep for openssl@3, got %d issues: %+v", found, r.Issues)
	}
}

func TestRunBrokenSymlink(t *testing.T) {
	prefix := t.TempDir()
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "old-thing")
	if err := os.Symlink("../Cellar/removed-pkg/1.0.0/bin/old-thing", link); err != nil {
		t.Fatal(err)
	}

	r, err := Run(Options{Prefix: prefix, APIDeps: stubDeps{}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Passed {
		t.Fatal("expected Passed=false")
	}
	var got *Issue
	for i := range r.Issues {
		if r.Issues[i].Kind == KindBrokenSymlink && r.Issues[i].Path == link {
			got = &r.Issues[i]
		}
	}
	if got == nil {
		t.Fatalf("expected broken-symlink for %s, got %+v", link, r.Issues)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("link should still exist without --fix: %v", err)
	}

	// With Fix=true the dangling link gets swept away.
	r2, err := Run(Options{Prefix: prefix, Fix: true, APIDeps: stubDeps{}})
	if err != nil {
		t.Fatalf("Run fix: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link should be gone with Fix=true, err=%v", err)
	}
	// Report still lists the issue (so the user knows what was fixed), but
	// the subsequent run on a clean tree passes.
	if len(r2.Issues) == 0 {
		t.Fatal("expected the fix run to still report the removed link")
	}
}

func TestRunOrphanedOlderVersion(t *testing.T) {
	prefix := t.TempDir()
	mkCellar(t, prefix, "foo", "1.0.0")
	mkCellar(t, prefix, "foo", "2.0.0")
	mkOpt(t, prefix, "foo", "2.0.0")

	r, err := Run(Options{Prefix: prefix, APIDeps: stubDeps{}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, is := range r.Issues {
		if is.Kind == KindOrphaned && is.Package == "foo" && filepath.Base(is.Path) == "1.0.0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected orphaned for foo 1.0.0, got %+v", r.Issues)
	}
}

func TestRunOrphanedNoOpt(t *testing.T) {
	prefix := t.TempDir()
	mkCellar(t, prefix, "foo", "1.0.0")
	// No opt link at all.

	r, err := Run(Options{Prefix: prefix, APIDeps: stubDeps{}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, is := range r.Issues {
		if is.Kind == KindOrphaned && is.Package == "foo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected orphaned for foo with no opt link, got %+v", r.Issues)
	}
}

func TestRunStalePin(t *testing.T) {
	prefix := t.TempDir()
	mkCellar(t, prefix, "git", "2.45.0")
	mkOpt(t, prefix, "git", "2.45.0")

	pinDir := filepath.Join(prefix, "var", "homebrew", "pinned")
	if err := os.MkdirAll(pinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pinned but not installed.
	if err := os.WriteFile(filepath.Join(pinDir, "ghost"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Pinned and installed — should not be reported.
	if err := os.WriteFile(filepath.Join(pinDir, "git"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Run(Options{Prefix: prefix, APIDeps: stubDeps{}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var stale []Issue
	for _, is := range r.Issues {
		if is.Kind == KindStalePin {
			stale = append(stale, is)
		}
	}
	if len(stale) != 1 {
		t.Fatalf("want 1 stale-pin, got %d: %+v", len(stale), stale)
	}
	if stale[0].Package != "ghost" {
		t.Fatalf("want stale-pin ghost, got %+v", stale[0])
	}
}

func TestRunSkipsPinsWhenDirMissing(t *testing.T) {
	prefix := t.TempDir()
	mkCellar(t, prefix, "git", "2.45.0")
	mkOpt(t, prefix, "git", "2.45.0")
	// No var/homebrew/pinned — this is fine.

	r, err := Run(Options{Prefix: prefix, APIDeps: stubDeps{}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, is := range r.Issues {
		if is.Kind == KindStalePin {
			t.Fatalf("unexpected stale-pin when pinned dir missing: %+v", is)
		}
	}
}

func TestRunReadsLocalFormulaRb(t *testing.T) {
	// Priority check: a .brew/<name>.rb on disk overrides the API resolver.
	prefix := t.TempDir()
	verDir := mkCellar(t, prefix, "weird", "0.1")
	mkOpt(t, prefix, "weird", "0.1")

	brewDir := filepath.Join(verDir, ".brew")
	if err := os.MkdirAll(brewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rb := `class Weird < Formula
  desc "synthetic"
  depends_on "pkgconf" => :build
  depends_on "libfoo"
  depends_on "libbar"
end
`
	if err := os.WriteFile(filepath.Join(brewDir, "weird.rb"), []byte(rb), 0o644); err != nil {
		t.Fatal(err)
	}
	// API would claim no deps; the .rb file should override.
	r, err := Run(Options{Prefix: prefix, APIDeps: stubDeps{"weird": nil}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var missing []string
	for _, is := range r.Issues {
		if is.Kind == KindMissingDep {
			missing = append(missing, is.Package)
		}
	}
	// pkgconf is a :build dep — runtime check should ignore it. libfoo and
	// libbar are real runtime deps and neither is installed.
	wantSet := map[string]bool{"libfoo": true, "libbar": true}
	for _, m := range missing {
		if !wantSet[m] {
			t.Fatalf("unexpected missing-dep %q (build deps should be skipped): %+v", m, r.Issues)
		}
		delete(wantSet, m)
	}
	if len(wantSet) != 0 {
		t.Fatalf("missing expected deps %v, issues=%+v", wantSet, r.Issues)
	}
}
