package index

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

const fxFormulae = `[
  {"name":"ripgrep","full_name":"ripgrep","tap":"homebrew/core","desc":"Recursive grep","license":"MIT","homepage":"https://example.com/rg","revision":0,
   "versions":{"stable":"14.1.0"},
   "dependencies":["pcre2"],"build_dependencies":["rust"],
   "bottle":{"stable":{"files":{"arm64_sequoia":{"cellar":":any","url":"https://ghcr.io/rg","sha256":"abc123"}}}}},
  {"name":"pcre2","full_name":"pcre2","tap":"homebrew/core","desc":"Perl compatible regex","versions":{"stable":"10.44"},
   "bottle":{"stable":{"files":{"arm64_sequoia":{"cellar":":any","url":"https://ghcr.io/pcre2","sha256":"def456"}}}}}
]`

const fxCasks = `[
  {"token":"firefox","name":["Mozilla Firefox"],"desc":"Web browser","homepage":"https://mozilla.org","version":"126.0","tap":"homebrew/cask"}
]`

func buildTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Rebuild(context.Background(), []byte(fxFormulae), []byte(fxCasks), "F-etag", "C-etag", time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRebuildAndLookup(t *testing.T) {
	s := buildTestStore(t)
	if s.FormulaCount() != 2 {
		t.Fatalf("formula count = %d", s.FormulaCount())
	}
	f, err := s.Lookup("ripgrep")
	if err != nil || f == nil {
		t.Fatalf("lookup ripgrep: %v %v", f, err)
	}
	if f.StableVersion != "14.1.0" || f.Desc != "Recursive grep" {
		t.Fatalf("bad formula: %+v", f)
	}
	if len(f.Deps) != 1 || f.Deps[0] != "pcre2" {
		t.Fatalf("runtime deps: %v", f.Deps)
	}
	if len(f.BuildDeps) != 1 || f.BuildDeps[0] != "rust" {
		t.Fatalf("build deps: %v", f.BuildDeps)
	}
	if miss, _ := s.Lookup("nope"); miss != nil {
		t.Fatal("expected clean miss")
	}
}

func TestBottleLookup(t *testing.T) {
	s := buildTestStore(t)
	b, err := s.Bottle("ripgrep", "arm64_sequoia")
	if err != nil || b == nil {
		t.Fatalf("bottle: %v %v", b, err)
	}
	if b.URL != "https://ghcr.io/rg" || b.SHA256 != "abc123" {
		t.Fatalf("bad bottle: %+v", b)
	}
	if miss, _ := s.Bottle("ripgrep", "x86_64_linux"); miss != nil {
		t.Fatal("expected nil bottle for missing tag")
	}
}

func TestReverseDeps(t *testing.T) {
	s := buildTestStore(t)
	users, err := s.ReverseDeps("pcre2")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0] != "ripgrep" {
		t.Fatalf("reverse deps of pcre2 = %v", users)
	}
	// build-only dep must NOT count as a reverse runtime consumer.
	if u, _ := s.ReverseDeps("rust"); len(u) != 0 {
		t.Fatalf("rust is build-only, got consumers %v", u)
	}
}

func TestLookupCask(t *testing.T) {
	s := buildTestStore(t)
	c, err := s.LookupCask("firefox")
	if err != nil || c == nil {
		t.Fatalf("cask: %v %v", c, err)
	}
	if c.Version != "126.0" || len(c.Names) != 1 || c.Names[0] != "Mozilla Firefox" {
		t.Fatalf("bad cask: %+v", c)
	}
}

func TestSearch(t *testing.T) {
	s := buildTestStore(t)
	// prefix match on name
	m, err := s.Search("rip", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsMatch(m, "ripgrep", "formula") {
		t.Fatalf("search 'rip' missed ripgrep: %+v", m)
	}
	// description term, across source (cask)
	m, _ = s.Search("browser", 10)
	if !containsMatch(m, "firefox", "cask") {
		t.Fatalf("search 'browser' missed firefox cask: %+v", m)
	}
}

func TestBuiltAtAndETag(t *testing.T) {
	s := buildTestStore(t)
	bt, ok := s.BuiltAt()
	if !ok || bt.Unix() != 1700000000 {
		t.Fatalf("built_at = %v %v", bt, ok)
	}
	if s.ETag("formula") != "F-etag" || s.ETag("cask") != "C-etag" {
		t.Fatalf("etags = %q %q", s.ETag("formula"), s.ETag("cask"))
	}
	if s.SchemaStale() {
		t.Fatal("freshly built index reports schema stale")
	}
}

func TestRebuildIsWholesale(t *testing.T) {
	s := buildTestStore(t)
	// Rebuild with a smaller set — old rows must be gone, not merged.
	if _, err := s.Rebuild(context.Background(), []byte(`[{"name":"jq","versions":{"stable":"1.7"}}]`), []byte(`[]`), "", "", time.Unix(1700000100, 0)); err != nil {
		t.Fatal(err)
	}
	if s.FormulaCount() != 1 {
		t.Fatalf("expected wholesale replace, count=%d", s.FormulaCount())
	}
	if f, _ := s.Lookup("ripgrep"); f != nil {
		t.Fatal("stale ripgrep survived rebuild")
	}
	if f, _ := s.Lookup("jq"); f == nil {
		t.Fatal("jq missing after rebuild")
	}
}

func containsMatch(ms []Match, name, source string) bool {
	for _, m := range ms {
		if m.Name == name && m.Source == source {
			return true
		}
	}
	return false
}
