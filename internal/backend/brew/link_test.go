package brew

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// mkCellar builds a fake versioned cellar dir with the given relative file
// paths under tmp/Cellar/<pkg>/<version>. Every file gets non-empty contents
// so mode bits are unambiguous.
func mkCellar(t *testing.T, root, pkg, version string, files []string) string {
	t.Helper()
	cellarPkgDir := filepath.Join(root, "Cellar", pkg, version)
	for _, f := range files {
		full := filepath.Join(cellarPkgDir, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte("body:"+f), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return cellarPkgDir
}

// readlinkAbs resolves target's symlink pointer to an absolute path, whether
// the underlying symlink is relative or absolute.
func readlinkAbs(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.Readlink(p)
	if err != nil {
		t.Fatalf("readlink %s: %v", p, err)
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(p), raw))
}

func isSymlink(t *testing.T, p string) bool {
	t.Helper()
	st, err := os.Lstat(p)
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeSymlink != 0
}

func TestLink_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	cellar := mkCellar(t, tmp, "ripgrep", "14.1.1", []string{
		"bin/rg",
		"share/man/man1/rg.1",
	})

	created, err := Link(prefix, cellar, LinkOptions{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	wantLinks := []string{
		filepath.Join(prefix, "bin", "rg"),
		filepath.Join(prefix, "share", "man", "man1", "rg.1"),
		filepath.Join(prefix, "opt", "ripgrep"),
	}
	sort.Strings(created)
	sort.Strings(wantLinks)
	if len(created) != len(wantLinks) {
		t.Fatalf("created=%v want=%v", created, wantLinks)
	}
	for i, p := range wantLinks {
		if created[i] != p {
			t.Errorf("created[%d]=%s want %s", i, created[i], p)
		}
	}

	// Every link must resolve to the cellar.
	got := readlinkAbs(t, filepath.Join(prefix, "bin", "rg"))
	want := filepath.Join(cellar, "bin", "rg")
	if got != want {
		t.Errorf("bin/rg -> %s, want %s", got, want)
	}
	got = readlinkAbs(t, filepath.Join(prefix, "share", "man", "man1", "rg.1"))
	want = filepath.Join(cellar, "share", "man", "man1", "rg.1")
	if got != want {
		t.Errorf("man page -> %s, want %s", got, want)
	}
	got = readlinkAbs(t, filepath.Join(prefix, "opt", "ripgrep"))
	if got != filepath.Clean(cellar) {
		t.Errorf("opt/ripgrep -> %s, want %s", got, cellar)
	}
}

func TestLink_IdenticalSymlinkNoOp(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	cellar := mkCellar(t, tmp, "ripgrep", "14.1.1", []string{"bin/rg"})

	// First link.
	first, err := Link(prefix, cellar, LinkOptions{})
	if err != nil {
		t.Fatalf("first Link: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("first Link returned no entries")
	}

	// Re-link into the same prefix. Should succeed and return an empty set
	// (or only the opt link if we tracked a change, but nothing should be
	// freshly created here).
	second, err := Link(prefix, cellar, LinkOptions{})
	if err != nil {
		t.Fatalf("second Link: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("idempotent relink should report 0 new links, got %v", second)
	}
}

func TestLink_DifferentSymlinkCollides(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	cellar := mkCellar(t, tmp, "ripgrep", "14.1.1", []string{"bin/rg"})

	// Plant a pre-existing symlink pointing somewhere else.
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bogus := filepath.Join(tmp, "bogus")
	if err := os.WriteFile(bogus, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bogus, filepath.Join(binDir, "rg")); err != nil {
		t.Fatal(err)
	}

	_, err := Link(prefix, cellar, LinkOptions{Overwrite: false})
	if err == nil {
		t.Fatal("expected LinkCollisionError, got nil")
	}
	var cerr *LinkCollisionError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *LinkCollisionError, got %T: %v", err, err)
	}
	if cerr.Target != filepath.Join(binDir, "rg") {
		t.Errorf("collision Target=%s, want %s", cerr.Target, filepath.Join(binDir, "rg"))
	}
	if cerr.ExistingLink == "" {
		t.Error("collision ExistingLink should be populated for symlink offenders")
	}
}

func TestLink_DifferentSymlinkOverwrite(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	cellar := mkCellar(t, tmp, "ripgrep", "14.1.1", []string{"bin/rg"})

	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bogus := filepath.Join(tmp, "bogus")
	if err := os.WriteFile(bogus, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bogus, filepath.Join(binDir, "rg")); err != nil {
		t.Fatal(err)
	}

	_, err := Link(prefix, cellar, LinkOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("overwrite Link: %v", err)
	}
	got := readlinkAbs(t, filepath.Join(binDir, "rg"))
	want := filepath.Join(cellar, "bin", "rg")
	if got != want {
		t.Errorf("bin/rg -> %s, want %s", got, want)
	}
}

func TestLink_RegularFileAlwaysErrors(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	cellar := mkCellar(t, tmp, "ripgrep", "14.1.1", []string{"bin/rg"})

	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(binDir, "rg")
	if err := os.WriteFile(userFile, []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Even with Overwrite=true, a regular file must never be clobbered.
	_, err := Link(prefix, cellar, LinkOptions{Overwrite: true})
	if err == nil {
		t.Fatal("expected collision error for regular file, got nil")
	}
	var cerr *LinkCollisionError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *LinkCollisionError, got %T: %v", err, err)
	}
	if cerr.ExistingLink != "" {
		t.Errorf("ExistingLink should be empty for regular-file collision, got %q", cerr.ExistingLink)
	}
	// File must still be intact.
	body, err := os.ReadFile(userFile)
	if err != nil {
		t.Fatalf("user file vanished: %v", err)
	}
	if string(body) != "user data" {
		t.Errorf("user file body=%q, want %q", string(body), "user data")
	}
}

func TestLink_DryRun(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	cellar := mkCellar(t, tmp, "ripgrep", "14.1.1", []string{
		"bin/rg",
		"share/man/man1/rg.1",
	})

	created, err := Link(prefix, cellar, LinkOptions{DryRun: true})
	if err != nil {
		t.Fatalf("DryRun Link: %v", err)
	}
	if len(created) == 0 {
		t.Error("DryRun returned no planned entries")
	}
	// No FS side-effects.
	if _, err := os.Stat(prefix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("DryRun created prefix dir: stat err=%v", err)
	}
	for _, p := range created {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("DryRun produced FS entry at %s", p)
		}
	}
}

func TestUnlink_RemovesAndIdempotent(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	cellar := mkCellar(t, tmp, "ripgrep", "14.1.1", []string{
		"bin/rg",
		"share/man/man1/rg.1",
	})

	created, err := Link(prefix, cellar, LinkOptions{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Unlink(created); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	for _, p := range created {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("post-Unlink %s still exists: err=%v", p, err)
		}
	}
	// Double Unlink must not error.
	if err := Unlink(created); err != nil {
		t.Fatalf("second Unlink should be idempotent, got: %v", err)
	}
}

func TestLink_OptSymlink(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	cellar := mkCellar(t, tmp, "ripgrep", "14.1.1", []string{"bin/rg"})

	_, err := Link(prefix, cellar, LinkOptions{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	opt := filepath.Join(prefix, "opt", "ripgrep")
	if !isSymlink(t, opt) {
		t.Fatalf("opt/ripgrep is not a symlink")
	}
	got := readlinkAbs(t, opt)
	if got != filepath.Clean(cellar) {
		t.Errorf("opt/ripgrep -> %s, want %s", got, cellar)
	}
	// Resolve the symlink and confirm it lands at the cellar dir as a real
	// directory.
	resolved, err := filepath.EvalSymlinks(opt)
	if err != nil {
		t.Fatalf("EvalSymlinks %s: %v", opt, err)
	}
	cellarReal, err := filepath.EvalSymlinks(cellar)
	if err != nil {
		t.Fatalf("EvalSymlinks %s: %v", cellar, err)
	}
	if resolved != cellarReal {
		t.Errorf("EvalSymlinks(opt)=%s, want %s", resolved, cellarReal)
	}
}

func TestLink_RelativeSymlinkTargets(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	cellar := mkCellar(t, tmp, "ripgrep", "14.1.1", []string{
		"bin/rg",
		"share/man/man1/rg.1",
	})

	if _, err := Link(prefix, cellar, LinkOptions{}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	checks := []string{
		filepath.Join(prefix, "bin", "rg"),
		filepath.Join(prefix, "share", "man", "man1", "rg.1"),
		filepath.Join(prefix, "opt", "ripgrep"),
	}
	for _, p := range checks {
		raw, err := os.Readlink(p)
		if err != nil {
			t.Fatalf("readlink %s: %v", p, err)
		}
		if filepath.IsAbs(raw) {
			t.Errorf("%s -> %s: expected relative target, got absolute", p, raw)
		}
	}
}
