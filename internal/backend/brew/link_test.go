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

// --- brew shallow-link (directory symlink) interplay ---------------------
//
// brew links a wholly-new subtree as ONE directory symlink into the keg
// (e.g. lib/cmake/zstd -> ../../Cellar/zstd/1.5.7/lib/cmake/zstd). Our deep
// linker must unwind those, never error through them and never write through
// them into the old keg.

func TestLink_UpgradeUnwindsOwnDirSymlink(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	oldKeg := mkCellar(t, tmp, "zstd", "1.5.7", []string{
		"lib/cmake/zstd/zstdConfig.cmake",
	})
	newKeg := mkCellar(t, tmp, "zstd", "1.5.7_1", []string{
		"lib/cmake/zstd/zstdConfig.cmake",
		"lib/cmake/zstd/extra.cmake", // new in this version
	})

	// brew-style shallow link left by the previous version.
	if err := os.MkdirAll(filepath.Join(prefix, "lib", "cmake"), 0o755); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(prefix, "lib", "cmake", "zstd")
	if err := os.Symlink(filepath.Join(oldKeg, "lib", "cmake", "zstd"), dirLink); err != nil {
		t.Fatal(err)
	}

	created, err := Link(prefix, newKeg, LinkOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("Link through dir symlink: %v", err)
	}

	st, err := os.Lstat(dirLink)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		t.Fatalf("%s: want real directory after unwind, mode=%v", dirLink, st.Mode())
	}
	for _, leaf := range []string{"zstdConfig.cmake", "extra.cmake"} {
		got := readlinkAbs(t, filepath.Join(dirLink, leaf))
		want := filepath.Join(newKeg, "lib", "cmake", "zstd", leaf)
		if got != want {
			t.Errorf("%s -> %s, want %s", leaf, got, want)
		}
	}
	// Must not have written through the symlink into the old keg.
	if _, err := os.Lstat(filepath.Join(oldKeg, "lib", "cmake", "zstd", "extra.cmake")); !errors.Is(err, os.ErrNotExist) {
		t.Error("extra.cmake leaked into the old keg through the dir symlink")
	}
	if len(created) == 0 {
		t.Error("created list empty; leaves should be journaled")
	}
}

func TestLink_UnwindPreservesOtherPackageLinks(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	kegA := mkCellar(t, tmp, "aaa", "1.0", []string{"share/foo/a.txt"})
	kegB := mkCellar(t, tmp, "bbb", "1.0", []string{"share/foo/b.txt"})

	if err := os.MkdirAll(filepath.Join(prefix, "share"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(kegA, "share", "foo"), filepath.Join(prefix, "share", "foo")); err != nil {
		t.Fatal(err)
	}

	created, err := Link(prefix, kegB, LinkOptions{})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	// aaa's file survives as an individual link; bbb's joins it.
	if got := readlinkAbs(t, filepath.Join(prefix, "share", "foo", "a.txt")); got != filepath.Join(kegA, "share", "foo", "a.txt") {
		t.Errorf("a.txt -> %s, want into kegA", got)
	}
	if got := readlinkAbs(t, filepath.Join(prefix, "share", "foo", "b.txt")); got != filepath.Join(kegB, "share", "foo", "b.txt") {
		t.Errorf("b.txt -> %s, want into kegB", got)
	}
	// The preserved a.txt link replicates pre-existing state — it must NOT be
	// journaled, or rollback of bbb would break aaa.
	for _, c := range created {
		if filepath.Base(c) == "a.txt" {
			t.Error("preserved link a.txt leaked into created list")
		}
	}
}

func TestLink_DanglingCellarDirSymlinkReplaced(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	newKeg := mkCellar(t, tmp, "zstd", "1.5.7_1", []string{"lib/cmake/zstd/zstdConfig.cmake"})

	// Old keg already cleaned up; its shallow link dangles.
	if err := os.MkdirAll(filepath.Join(prefix, "lib", "cmake"), 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(tmp, "Cellar", "zstd", "1.5.7", "lib", "cmake", "zstd")
	if err := os.Symlink(gone, filepath.Join(prefix, "lib", "cmake", "zstd")); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(prefix, newKeg, LinkOptions{Overwrite: true}); err != nil {
		t.Fatalf("Link over dangling dir symlink: %v", err)
	}
	got := readlinkAbs(t, filepath.Join(prefix, "lib", "cmake", "zstd", "zstdConfig.cmake"))
	if want := filepath.Join(newKeg, "lib", "cmake", "zstd", "zstdConfig.cmake"); got != want {
		t.Errorf("zstdConfig.cmake -> %s, want %s", got, want)
	}
}

func TestLink_DirSymlinkOutsideCellarCollides(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	keg := mkCellar(t, tmp, "bbb", "1.0", []string{"share/foo/b.txt"})

	// User-managed symlink to a dir outside the Cellar: hands off, even on
	// Overwrite.
	other := filepath.Join(tmp, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(prefix, "share"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(prefix, "share", "foo")); err != nil {
		t.Fatal(err)
	}

	_, err := Link(prefix, keg, LinkOptions{Overwrite: true})
	var lce *LinkCollisionError
	if !errors.As(err, &lce) {
		t.Fatalf("want LinkCollisionError, got %v", err)
	}
	if !isSymlink(t, filepath.Join(prefix, "share", "foo")) {
		t.Error("user symlink was clobbered")
	}
	if _, err := os.Lstat(filepath.Join(other, "b.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Error("wrote through user symlink into outside dir")
	}
}

func TestLink_DryRunDoesNotUnwind(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	oldKeg := mkCellar(t, tmp, "zstd", "1.5.7", []string{"lib/cmake/zstd/zstdConfig.cmake"})
	newKeg := mkCellar(t, tmp, "zstd", "1.5.7_1", []string{"lib/cmake/zstd/zstdConfig.cmake"})

	if err := os.MkdirAll(filepath.Join(prefix, "lib", "cmake"), 0o755); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(prefix, "lib", "cmake", "zstd")
	if err := os.Symlink(filepath.Join(oldKeg, "lib", "cmake", "zstd"), dirLink); err != nil {
		t.Fatal(err)
	}

	created, err := Link(prefix, newKeg, LinkOptions{Overwrite: true, DryRun: true})
	if err != nil {
		t.Fatalf("DryRun Link: %v", err)
	}
	if !isSymlink(t, dirLink) {
		t.Error("DryRun mutated the dir symlink")
	}
	found := false
	for _, c := range created {
		if filepath.Base(c) == "zstdConfig.cmake" {
			found = true
		}
	}
	if !found {
		t.Errorf("DryRun preview missing leaf; created=%v", created)
	}
}

func TestLink_FileTargetSymlinkAtDirPositionCollides(t *testing.T) {
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	kegA := mkCellar(t, tmp, "aaa", "1.0", []string{"lib/foo"})         // foo is a FILE
	kegB := mkCellar(t, tmp, "bbb", "1.0", []string{"lib/foo/bar.txt"}) // foo is a DIR

	// aaa's live leaf link occupies the path bbb needs as a directory.
	if err := os.MkdirAll(filepath.Join(prefix, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(prefix, "lib", "foo")
	if err := os.Symlink(filepath.Join(kegA, "lib", "foo"), leaf); err != nil {
		t.Fatal(err)
	}

	// Overwrite=false: genuine cross-package conflict — must collide, and
	// aaa's link must survive.
	_, err := Link(prefix, kegB, LinkOptions{})
	var lce *LinkCollisionError
	if !errors.As(err, &lce) {
		t.Fatalf("want LinkCollisionError, got %v", err)
	}
	if !isSymlink(t, leaf) {
		t.Fatal("aaa's live file link was destroyed with Overwrite=false")
	}

	// Overwrite=true: same-package file→dir shape changes are legitimate on
	// upgrade — replace and proceed.
	if _, err := Link(prefix, kegB, LinkOptions{Overwrite: true}); err != nil {
		t.Fatalf("Link Overwrite over file-target symlink: %v", err)
	}
	if got := readlinkAbs(t, filepath.Join(leaf, "bar.txt")); got != filepath.Join(kegB, "lib", "foo", "bar.txt") {
		t.Errorf("bar.txt -> %s, want into kegB", got)
	}
}

func TestLink_CascadingUnwindTwoLevels(t *testing.T) {
	// brew shallow-links at the TOPMOST wholly-new dir: the symlink commonly
	// sits levels above the leaves. Unwinding the outer dir preserves the
	// inner dir as a symlink, which must itself unwind in the same walk.
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	oldKeg := mkCellar(t, tmp, "zstd", "1.5.7", []string{"lib/cmake/zstd/zstdConfig.cmake"})
	newKeg := mkCellar(t, tmp, "zstd", "1.5.7_1", []string{"lib/cmake/zstd/zstdConfig.cmake"})

	if err := os.MkdirAll(filepath.Join(prefix, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Shallow link two levels above the leaf: lib/cmake -> old keg's lib/cmake.
	if err := os.Symlink(filepath.Join(oldKeg, "lib", "cmake"), filepath.Join(prefix, "lib", "cmake")); err != nil {
		t.Fatal(err)
	}

	// DryRun first: preview must succeed without touching the FS.
	dryCreated, err := Link(prefix, newKeg, LinkOptions{Overwrite: true, DryRun: true})
	if err != nil {
		t.Fatalf("DryRun cascade: %v", err)
	}
	if !isSymlink(t, filepath.Join(prefix, "lib", "cmake")) {
		t.Fatal("DryRun mutated the outer dir symlink")
	}

	created, err := Link(prefix, newKeg, LinkOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("cascading unwind: %v", err)
	}
	leaf := filepath.Join(prefix, "lib", "cmake", "zstd", "zstdConfig.cmake")
	if got := readlinkAbs(t, leaf); got != filepath.Join(newKeg, "lib", "cmake", "zstd", "zstdConfig.cmake") {
		t.Errorf("leaf -> %s, want into new keg", got)
	}
	for _, p := range []string{filepath.Join(prefix, "lib", "cmake"), filepath.Join(prefix, "lib", "cmake", "zstd")} {
		st, err := os.Lstat(p)
		if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			t.Errorf("%s: want real dir after cascade", p)
		}
	}
	// Preview and real run must agree on the created set.
	if len(dryCreated) != len(created) {
		t.Errorf("preview/real divergence: dry=%v real=%v", dryCreated, created)
	}
}

func TestLink_DryRunMatchesRealOnMergeCollision(t *testing.T) {
	// Both kegs own share/foo/common.txt; aaa shallow-linked the dir. With
	// Overwrite=false the real run collides on the preserved child — the
	// preview must report the same collision, not success.
	tmp := t.TempDir()
	prefix := filepath.Join(tmp, "prefix")
	kegA := mkCellar(t, tmp, "aaa", "1.0", []string{"share/foo/common.txt"})
	kegB := mkCellar(t, tmp, "bbb", "1.0", []string{"share/foo/common.txt"})

	if err := os.MkdirAll(filepath.Join(prefix, "share"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(kegA, "share", "foo"), filepath.Join(prefix, "share", "foo")); err != nil {
		t.Fatal(err)
	}

	_, dryErr := Link(prefix, kegB, LinkOptions{DryRun: true})
	var lce *LinkCollisionError
	if !errors.As(dryErr, &lce) {
		t.Fatalf("DryRun must report the collision the real run hits, got %v", dryErr)
	}
	if !isSymlink(t, filepath.Join(prefix, "share", "foo")) {
		t.Fatal("DryRun mutated the dir symlink")
	}

	_, realErr := Link(prefix, kegB, LinkOptions{})
	if !errors.As(realErr, &lce) {
		t.Fatalf("real run: want LinkCollisionError, got %v", realErr)
	}
}
