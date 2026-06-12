package brew

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// installWithStubbedPrefix swaps out brewPrefix's global state so tests can
// pretend Homebrew lives under a writable tempdir. Cleanup resets the once
// and the cached prefix to their zero values so a subsequent test that
// relies on real filesystem probing isn't poisoned by our stub.
func installWithStubbedPrefix(t *testing.T, prefix string) {
	t.Helper()
	brewPrefixOnce = sync.Once{}
	brewPrefixCache = prefix
	// Seal the once so brewPrefix() returns our cached value without
	// re-running the OS probe inside Do.
	brewPrefixOnce.Do(func() {})
	disablePrefixCache = false

	t.Cleanup(func() {
		brewPrefixOnce = sync.Once{}
		brewPrefixCache = ""
		disablePrefixCache = false
	})
}

// installWithStubbedAPICache forces apiCache() to return the provided cache
// for the duration of the test. The once is sealed so subsequent apiCache()
// calls skip NewAPICache and use our fixture.
func installWithStubbedAPICache(t *testing.T, ac *APICache) {
	t.Helper()
	sharedAPICacheOnce = sync.Once{}
	sharedAPICache = ac
	sharedAPICacheOnce.Do(func() {})
	apiCacheDisabled = false
	// Disable the native index so the resolver uses our stubbed API cache rather
	// than the host's real index.db (or trying to build one over the network).
	prevIndexDisabled := indexDisabled
	indexDisabled = true

	t.Cleanup(func() {
		sharedAPICacheOnce = sync.Once{}
		sharedAPICache = nil
		apiCacheDisabled = false
		// Restore, don't hardcode: TestMain runs the whole package with the
		// index disabled; forcing false here leaked the host's real index
		// into every later test.
		indexDisabled = prevIndexDisabled
	})
}

func TestInstallNative_ErrNativeUnsupported_NoPrefix(t *testing.T) {
	// Force brewPrefix() to return "" — mimics a machine without Homebrew.
	prev := disablePrefixCache
	disablePrefixCache = true
	t.Cleanup(func() { disablePrefixCache = prev })

	b := &Brew{}
	_, err := b.InstallNative(context.Background(), []string{"ripgrep"}, InstallOpts{DryRun: true})
	if !errors.Is(err, ErrNativeUnsupported) {
		t.Fatalf("want ErrNativeUnsupported, got %v", err)
	}
}

func TestInstallNative_ErrNativeUnsupported_NoAPICache(t *testing.T) {
	installWithStubbedPrefix(t, t.TempDir())

	// apiCacheDisabled=true makes apiCache() return nil; indexDisabled keeps the
	// native index out so neither source is available.
	prev := apiCacheDisabled
	prevIdx := indexDisabled
	apiCacheDisabled = true
	indexDisabled = true
	t.Cleanup(func() { apiCacheDisabled = prev; indexDisabled = prevIdx })

	b := &Brew{}
	_, err := b.InstallNative(context.Background(), []string{"ripgrep"}, InstallOpts{DryRun: true})
	if !errors.Is(err, ErrNativeUnsupported) {
		t.Fatalf("want ErrNativeUnsupported, got %v", err)
	}
}

func TestInstallNative_DryRun_TopoOrderNoSideEffects(t *testing.T) {
	// Chain: a -> b -> c. InstallNative in DryRun should resolve everything,
	// emit events for each, and never hit the network or filesystem writes.
	withTestHost(t, "arm64", 15)

	tmp := t.TempDir()
	installWithStubbedPrefix(t, tmp)

	formulae := map[string]*APIFormula{
		"a": newFormula("a", []string{"b"}, nil),
		"b": newFormula("b", []string{"c"}, nil),
		"c": newFormula("c", nil, nil),
	}
	installWithStubbedAPICache(t, newAPICacheFromMaps(formulae, nil))

	var (
		mu     sync.Mutex
		events []InstallEvent
	)
	opts := InstallOpts{
		DryRun:   true,
		CacheDir: filepath.Join(tmp, "cache-should-not-be-created"),
		Progress: func(e InstallEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, e)
		},
	}

	b := &Brew{}
	res, err := b.InstallNative(context.Background(), []string{"a"}, opts)
	if err != nil {
		t.Fatalf("InstallNative: %v", err)
	}

	// DryRun lists every plan as "installed" (no actual work done).
	names := make([]string, len(res.Installed))
	for i, p := range res.Installed {
		names[i] = p.Name
	}
	want := []string{"c", "b", "a"}
	if !equalSlices(names, want) {
		t.Fatalf("Installed order = %v, want %v", names, want)
	}
	// Only "a" should carry IsRoot.
	for _, ip := range res.Installed {
		if ip.Name == "a" && !ip.IsRoot {
			t.Error("a should be root")
		}
		if ip.Name != "a" && ip.IsRoot {
			t.Errorf("%s should not be root", ip.Name)
		}
	}

	// No cache dir, no Cellar writes.
	if _, err := os.Stat(opts.CacheDir); !os.IsNotExist(err) {
		t.Errorf("DryRun should not create cache dir: %v", err)
	}
	if entries, err := os.ReadDir(tmp); err == nil {
		for _, e := range entries {
			if e.Name() == "Cellar" {
				t.Error("DryRun should not create Cellar")
			}
		}
	}

	// Events should include resolve phases for every plan plus a final "done".
	mu.Lock()
	defer mu.Unlock()
	phases := map[string]int{}
	seenPkgs := map[string]bool{}
	for _, e := range events {
		phases[e.Phase]++
		if e.Phase == "resolve" {
			seenPkgs[e.Package] = true
		}
	}
	for _, n := range want {
		if !seenPkgs[n] {
			t.Errorf("no resolve event for %s; events=%+v", n, events)
		}
	}
	if phases["done"] != 1 {
		t.Errorf("want 1 done event, got %d", phases["done"])
	}
}

func TestInstallNative_SkipsAlreadyInstalled(t *testing.T) {
	withTestHost(t, "arm64", 15)

	tmp := t.TempDir()
	installWithStubbedPrefix(t, tmp)

	formulae := map[string]*APIFormula{
		"a": newFormula("a", []string{"b"}, nil),
		"b": newFormula("b", nil, nil),
	}
	installWithStubbedAPICache(t, newAPICacheFromMaps(formulae, nil))

	// Pre-create Cellar/b/1.0.0 so it gets skipped; leave "a" missing so
	// it remains in the pending set.
	if err := os.MkdirAll(filepath.Join(tmp, "Cellar", "b", "1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	b := &Brew{}
	res, err := b.InstallNative(context.Background(), []string{"a"}, InstallOpts{DryRun: true})
	if err != nil {
		t.Fatalf("InstallNative: %v", err)
	}

	// b should be skipped, a should be in installed (DryRun).
	sort.Strings(res.Skipped)
	if !equalSlices(res.Skipped, []string{"b"}) {
		t.Fatalf("Skipped = %v, want [b]", res.Skipped)
	}
	if len(res.Installed) != 1 || res.Installed[0].Name != "a" {
		t.Fatalf("Installed = %+v, want just a", res.Installed)
	}
}

func TestInstallNative_EmitsExpectedEvents(t *testing.T) {
	withTestHost(t, "arm64", 15)

	tmp := t.TempDir()
	installWithStubbedPrefix(t, tmp)

	formulae := map[string]*APIFormula{
		"solo": newFormula("solo", nil, nil),
	}
	installWithStubbedAPICache(t, newAPICacheFromMaps(formulae, nil))

	var (
		mu     sync.Mutex
		events []InstallEvent
	)
	b := &Brew{}
	_, err := b.InstallNative(context.Background(), []string{"solo"}, InstallOpts{
		DryRun: true,
		Progress: func(e InstallEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, e)
		},
	})
	if err != nil {
		t.Fatalf("InstallNative: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawSoloResolve, sawDone bool
	for _, e := range events {
		if e.Phase == "resolve" && e.Package == "solo" && e.Version == "1.0.0" {
			sawSoloResolve = true
		}
		if e.Phase == "done" {
			sawDone = true
		}
	}
	if !sawSoloResolve {
		t.Errorf("no resolve event for solo@1.0.0: %+v", events)
	}
	if !sawDone {
		t.Errorf("no done event: %+v", events)
	}
}

func TestResolveBottleCacheDir_ExplicitWins(t *testing.T) {
	got, err := resolveBottleCacheDir("/custom/path")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/custom/path" {
		t.Fatalf("got %q, want /custom/path", got)
	}
}

func TestResolveBottleCacheDir_XDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/xdg")
	got, err := resolveBottleCacheDir("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/xdg", "bodega", "bottles")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveBottleCacheDir_HomeFallback(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/home/test")
	got, err := resolveBottleCacheDir("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/home", "test", ".cache", "bodega", "bottles")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBottleCachePath_IncludesTag(t *testing.T) {
	p := Plan{Name: "jq", Version: "1.7.1", Tag: "arm64_sequoia"}
	got := bottleCachePath("/cache", p)
	want := filepath.Join("/cache", "jq-1.7.1.arm64_sequoia.tar.gz")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCellarDirHasVersion(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "ripgrep", "14.1.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !cellarDirHasVersion(tmp, "ripgrep", "14.1.1") {
		t.Error("should detect existing version dir")
	}
	if cellarDirHasVersion(tmp, "ripgrep", "14.1.0") {
		t.Error("should reject mismatched version")
	}
	if cellarDirHasVersion(tmp, "missing", "1.0") {
		t.Error("should reject missing package")
	}
	if cellarDirHasVersion(tmp, "ripgrep", "") {
		t.Error("should reject empty version")
	}
}

func TestCachedBottleMatches(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bottle.tar.gz")
	body := []byte("hello bottle")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	// sha256Hex is defined in ghcr_test.go (same package) — reuse it so
	// we don't hand-roll a digest that goes stale.
	wantDigest := sha256Hex(body)

	ok, err := cachedBottleMatches(path, wantDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected cache hit")
	}

	// Mismatched digest should return false AND delete the stale file.
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err = cachedBottleMatches(path, "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected cache miss on digest mismatch")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale file should have been removed, stat=%v", err)
	}

	// Missing file is a clean miss, not an error.
	ok, err = cachedBottleMatches(filepath.Join(tmp, "nope"), "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected cache miss on missing file")
	}
}

// TestPruneStaleVersionLinks: after an upgrade links the new keg, prefix links
// that still point into an OLD version (files dropped/renamed in the new
// version) must be removed; links pointing into the kept version survive.
func TestPruneStaleVersionLinks(t *testing.T) {
	prefix := t.TempDir()
	cellarRoot := filepath.Join(prefix, "Cellar")
	mk := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Old keg 1.0 has oldonly + shared; new keg 2.0 has only shared.
	mk(filepath.Join(cellarRoot, "foo", "1.0", "bin", "oldonly"), "old")
	mk(filepath.Join(cellarRoot, "foo", "1.0", "bin", "shared"), "old")
	mk(filepath.Join(cellarRoot, "foo", "2.0", "bin", "shared"), "new")

	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// shared link already repointed to new keg by Link; oldonly still dangles
	// into the old keg.
	rel := func(to string) string { r, _ := filepath.Rel(binDir, to); return r }
	if err := os.Symlink(rel(filepath.Join(cellarRoot, "foo", "2.0", "bin", "shared")), filepath.Join(binDir, "shared")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel(filepath.Join(cellarRoot, "foo", "1.0", "bin", "oldonly")), filepath.Join(binDir, "oldonly")); err != nil {
		t.Fatal(err)
	}

	pruneStaleVersionLinks(prefix, cellarRoot, "foo", "2.0")

	// old-exclusive link gone
	if _, err := os.Lstat(filepath.Join(binDir, "oldonly")); !os.IsNotExist(err) {
		t.Fatalf("stale oldonly link survived: %v", err)
	}
	// new-version link untouched
	if _, err := os.Lstat(filepath.Join(binDir, "shared")); err != nil {
		t.Fatalf("shared link wrongly removed: %v", err)
	}
}

// TestInstallNativeWritesLinkedRecord: brew's var/homebrew/linked/<name>
// bookkeeping must be written on install and removed on uninstall, or
// `brew doctor` flags every native keg as unlinked.
func TestInstallNativeWritesLinkedRecord(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prefix := t.TempDir()
	installWithStubbedPrefix(t, prefix)
	if err := os.MkdirAll(filepath.Join(prefix, "Cellar"), 0o755); err != nil {
		t.Fatal(err)
	}

	payload := makeBottleTarGz(t, "ok", "2.0", map[string]string{"bin/ok": "#!/bin/sh\n"})
	sha := sha256Hex(payload)
	m := newGHCRMock(t, payload)
	httpClient = m.server.Client()
	tokenEndpoint = m.server.URL + "/token"
	t.Cleanup(func() { resetGHCRState(t) })
	fixtureIndex(t, `[{"name":"ok","versions":{"stable":"2.0"},"bottle":{"stable":{"files":{"all":{"url":"`+
		m.server.URL+"/v2/homebrew/core/ok/blobs/sha256:"+sha+`","sha256":"`+sha+`"}}}}}]`)

	b := &Brew{}
	if _, err := b.InstallNative(context.Background(), []string{"ok"}, InstallOpts{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	rec := filepath.Join(prefix, "var", "homebrew", "linked", "ok")
	tgt, err := os.Readlink(rec)
	if err != nil {
		t.Fatalf("linked record missing: %v", err)
	}
	if filepath.Base(tgt) != "2.0" {
		t.Fatalf("record -> %s, want version 2.0", tgt)
	}

	if _, err := b.UninstallNative(context.Background(), []string{"ok"}, UninstallOpts{Force: true}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Lstat(rec); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("linked record not removed on uninstall: %v", err)
	}
}
