package brew

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// uninstallWithStubbedPrefix plants prefix as brewPrefix()'s cached value
// for the duration of the test. We seal the sync.Once so brewPrefix() skips
// its OS probe and returns our tempdir instead.
func uninstallWithStubbedPrefix(t *testing.T, prefix string) {
	t.Helper()
	brewPrefixOnce = sync.Once{}
	brewPrefixCache = prefix
	brewPrefixOnce.Do(func() {})
	disablePrefixCache = false
	t.Cleanup(func() {
		brewPrefixOnce = sync.Once{}
		brewPrefixCache = ""
		disablePrefixCache = false
	})
}

// plantFormula stamps out $prefix/Cellar/<name>/<ver>/{bin,lib}/<file> with
// plausible contents, links bin/lib artifacts into $prefix/{bin,lib}, and
// (optionally) wires up $prefix/opt/<name> -> Cellar/<name>/<ver>. Returns
// the absolute cellar version dir. Mimics what native_install.Link produces
// — using Link directly would do the same work but goes through relocate
// helpers we don't want to involve in an uninstall test.
func plantFormula(t *testing.T, prefix, name, version string, linkOpt bool) string {
	t.Helper()
	verDir := filepath.Join(prefix, "Cellar", name, version)
	binDir := filepath.Join(verDir, "bin")
	libDir := filepath.Join(verDir, "lib")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	bin := filepath.Join(binDir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho "+name+"\n"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	lib := filepath.Join(libDir, "lib"+name+".a")
	if err := os.WriteFile(lib, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write lib: %v", err)
	}

	// Prefix symlinks — relative targets, matching what our own Link
	// function produces in production.
	prefBin := filepath.Join(prefix, "bin")
	prefLib := filepath.Join(prefix, "lib")
	if err := os.MkdirAll(prefBin, 0o755); err != nil {
		t.Fatalf("mkdir prefix/bin: %v", err)
	}
	if err := os.MkdirAll(prefLib, 0o755); err != nil {
		t.Fatalf("mkdir prefix/lib: %v", err)
	}
	relBin, _ := filepath.Rel(prefBin, bin)
	relLib, _ := filepath.Rel(prefLib, lib)
	// Replace any previous symlink — multi-version tests call plantFormula
	// twice so the newer version's link overwrites the older one, exactly
	// as brew itself would on a sequential install.
	_ = os.Remove(filepath.Join(prefBin, name))
	_ = os.Remove(filepath.Join(prefLib, "lib"+name+".a"))
	if err := os.Symlink(relBin, filepath.Join(prefBin, name)); err != nil {
		t.Fatalf("symlink bin: %v", err)
	}
	if err := os.Symlink(relLib, filepath.Join(prefLib, "lib"+name+".a")); err != nil {
		t.Fatalf("symlink lib: %v", err)
	}
	if linkOpt {
		optDir := filepath.Join(prefix, "opt")
		if err := os.MkdirAll(optDir, 0o755); err != nil {
			t.Fatalf("mkdir opt: %v", err)
		}
		// Replace any existing opt link — multi-version tests stack calls.
		_ = os.Remove(filepath.Join(optDir, name))
		target := filepath.Join("..", "Cellar", name, version)
		if err := os.Symlink(target, filepath.Join(optDir, name)); err != nil {
			t.Fatalf("symlink opt: %v", err)
		}
	}
	return verDir
}

// writeRb drops a $prefix/Cellar/<name>/<ver>/.brew/<name>.rb with the given
// body so reverse-dep scanning can read it.
func writeRb(t *testing.T, prefix, name, version, body string) {
	t.Helper()
	dir := filepath.Join(prefix, "Cellar", name, version, ".brew")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .brew: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".rb"), []byte(body), 0o644); err != nil {
		t.Fatalf("write rb: %v", err)
	}
}

func TestUninstallNative_Basic(t *testing.T) {
	prefix := t.TempDir()
	uninstallWithStubbedPrefix(t, prefix)
	plantFormula(t, prefix, "foo", "1.0.0", true)

	b := &Brew{}
	res, err := b.UninstallNative(context.Background(), []string{"foo"}, UninstallOpts{})
	if err != nil {
		t.Fatalf("UninstallNative: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "foo" {
		t.Fatalf("Removed=%v", res.Removed)
	}
	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "foo")); !os.IsNotExist(err) {
		t.Fatalf("cellar dir still present: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(prefix, "bin", "foo")); !os.IsNotExist(err) {
		t.Fatalf("bin symlink still present: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(prefix, "lib", "libfoo.a")); !os.IsNotExist(err) {
		t.Fatalf("lib symlink still present: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(prefix, "opt", "foo")); !os.IsNotExist(err) {
		t.Fatalf("opt symlink still present: %v", err)
	}
}

func TestUninstallNative_MultipleVersions(t *testing.T) {
	prefix := t.TempDir()
	uninstallWithStubbedPrefix(t, prefix)
	plantFormula(t, prefix, "foo", "1.0.0", false)
	plantFormula(t, prefix, "foo", "1.1.0", true) // opt points at newest

	b := &Brew{}
	if _, err := b.UninstallNative(context.Background(), []string{"foo"}, UninstallOpts{}); err != nil {
		t.Fatalf("UninstallNative: %v", err)
	}
	// Both versions gone.
	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "foo")); !os.IsNotExist(err) {
		t.Fatalf("cellar dir still present: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(prefix, "opt", "foo")); !os.IsNotExist(err) {
		t.Fatalf("opt symlink still present")
	}
}

func TestUninstallNative_ReverseDepGuard(t *testing.T) {
	prefix := t.TempDir()
	uninstallWithStubbedPrefix(t, prefix)
	plantFormula(t, prefix, "openssl", "3.0.0", true)
	plantFormula(t, prefix, "bar", "2.0.0", true)
	writeRb(t, prefix, "bar", "2.0.0", `class Bar < Formula
  depends_on "openssl"
end
`)

	b := &Brew{}
	_, err := b.UninstallNative(context.Background(), []string{"openssl"}, UninstallOpts{})
	if err == nil {
		t.Fatal("expected reverse-dep error, got nil")
	}
	if !strings.Contains(err.Error(), "bar") || !strings.Contains(err.Error(), "required by") {
		t.Fatalf("error should mention required-by bar, got: %v", err)
	}
	// Openssl should still be on disk — we aborted before any mutation.
	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "openssl", "3.0.0")); err != nil {
		t.Fatalf("openssl removed despite guard: %v", err)
	}

	// With Force=true it goes through.
	if _, err := b.UninstallNative(context.Background(), []string{"openssl"}, UninstallOpts{Force: true}); err != nil {
		t.Fatalf("Force uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "openssl")); !os.IsNotExist(err) {
		t.Fatalf("openssl still present after Force: %v", err)
	}
}

func TestUninstallNative_IgnoreDeps(t *testing.T) {
	prefix := t.TempDir()
	uninstallWithStubbedPrefix(t, prefix)
	plantFormula(t, prefix, "openssl", "3.0.0", true)
	plantFormula(t, prefix, "bar", "2.0.0", true)
	writeRb(t, prefix, "bar", "2.0.0", `class Bar < Formula
  depends_on "openssl"
end
`)

	b := &Brew{}
	if _, err := b.UninstallNative(context.Background(), []string{"openssl"}, UninstallOpts{IgnoreDeps: true}); err != nil {
		t.Fatalf("IgnoreDeps uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "openssl")); !os.IsNotExist(err) {
		t.Fatalf("openssl still present: %v", err)
	}
}

func TestUninstallNative_DryRun(t *testing.T) {
	prefix := t.TempDir()
	uninstallWithStubbedPrefix(t, prefix)
	plantFormula(t, prefix, "foo", "1.0.0", true)

	b := &Brew{}
	res, err := b.UninstallNative(context.Background(), []string{"foo"}, UninstallOpts{DryRun: true})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	// Removed should be populated so the caller sees a plan.
	if len(res.Removed) != 1 || res.Removed[0] != "foo" {
		t.Fatalf("Removed=%v", res.Removed)
	}
	// But nothing actually gone.
	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "foo", "1.0.0")); err != nil {
		t.Fatalf("cellar vanished in DryRun: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(prefix, "bin", "foo")); err != nil {
		t.Fatalf("bin symlink vanished in DryRun: %v", err)
	}
}

func TestUninstallNative_NotInstalled(t *testing.T) {
	prefix := t.TempDir()
	uninstallWithStubbedPrefix(t, prefix)

	b := &Brew{}
	res, err := b.UninstallNative(context.Background(), []string{"ghost"}, UninstallOpts{})
	if err != nil {
		t.Fatalf("UninstallNative: %v", err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "ghost" {
		t.Fatalf("Skipped=%v", res.Skipped)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("Removed=%v, want empty", res.Removed)
	}
}

func TestUninstallNative_NoPrefix(t *testing.T) {
	prev := disablePrefixCache
	disablePrefixCache = true
	t.Cleanup(func() { disablePrefixCache = prev })

	b := &Brew{}
	_, err := b.UninstallNative(context.Background(), []string{"foo"}, UninstallOpts{})
	if !errors.Is(err, ErrNativeUnsupported) {
		t.Fatalf("want ErrNativeUnsupported, got %v", err)
	}
}

func TestUninstallNative_Progress(t *testing.T) {
	prefix := t.TempDir()
	uninstallWithStubbedPrefix(t, prefix)
	plantFormula(t, prefix, "foo", "1.0.0", true)

	var (
		mu     sync.Mutex
		phases []string
	)
	b := &Brew{}
	_, err := b.UninstallNative(context.Background(), []string{"foo"}, UninstallOpts{
		Progress: func(ev UninstallEvent) {
			mu.Lock()
			defer mu.Unlock()
			phases = append(phases, ev.Phase)
		},
	})
	if err != nil {
		t.Fatalf("UninstallNative: %v", err)
	}
	got := map[string]bool{}
	for _, p := range phases {
		got[p] = true
	}
	for _, want := range []string{"resolve", "unlink", "remove", "done"} {
		if !got[want] {
			keys := make([]string, 0, len(got))
			for k := range got {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			t.Fatalf("missing phase %q, saw %v", want, keys)
		}
	}
}
