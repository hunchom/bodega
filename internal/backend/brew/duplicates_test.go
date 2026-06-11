package brew

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakePrefix writes a plausible Cellar layout under t.TempDir and points
// Prefix() at it for the duration of the test.
func fakePrefix(t *testing.T, entries map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	for pkg, versions := range entries {
		for _, v := range versions {
			dir := filepath.Join(root, "Cellar", pkg, v, "bin")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", dir, err)
			}
			if err := os.WriteFile(filepath.Join(dir, pkg), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatalf("write bin: %v", err)
			}
		}
	}
	findDuplicatesPrefix = root
	t.Cleanup(func() { findDuplicatesPrefix = "" })
	return root
}

// symlinkOpt points $PREFIX/opt/<pkg> at Cellar/<pkg>/<version> relatively.
func symlinkOpt(t *testing.T, prefix, pkg, version string) {
	t.Helper()
	optDir := filepath.Join(prefix, "opt")
	if err := os.MkdirAll(optDir, 0o755); err != nil {
		t.Fatalf("mkdir opt: %v", err)
	}
	rel := filepath.Join("..", "Cellar", pkg, version)
	if err := os.Symlink(rel, filepath.Join(optDir, pkg)); err != nil {
		t.Fatalf("symlink opt/%s: %v", pkg, err)
	}
}

func TestCompareKegVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int // -1 a older, +1 a newer, 0 equal
	}{
		{"1.9", "1.10", -1},      // numeric, not lexical
		{"1.10", "1.9", 1},       //
		{"1.2.3", "1.2.3_1", -1}, // revision bump is newer
		{"1.2.3_1", "1.2.3", 1},  //
		{"1.2.3_2", "1.2.3_1", 1},
		{"1.2", "1.2.0", 0},              // trailing zero
		{"1.2.3", "1.2.3", 0},            // equal
		{"1.2.3-beta", "1.2.3", -1},      // semver prerelease sorts below release
		{"2024-01-15", "2024-02-01", -1}, // date-ish, lexical fallback
		{"3.0.0", "3.0.0_2", -1},
	}
	for _, c := range cases {
		if got := compareKegVersions(c.a, c.b); sign(got) != c.want {
			t.Errorf("compareKegVersions(%q,%q)=%d want sign %d", c.a, c.b, got, c.want)
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

func TestSortVersionsRevisionAware(t *testing.T) {
	vs := []string{"1.10.0", "1.2.3_1", "1.9.0", "1.2.3"}
	sortVersions(vs)
	// oldest->newest: 1.2.3 < 1.2.3_1 (revision) < 1.9.0 < 1.10.0 (numeric).
	want := []string{"1.2.3", "1.2.3_1", "1.9.0", "1.10.0"}
	if !reflect.DeepEqual(vs, want) {
		t.Fatalf("sorted=%v want=%v", vs, want)
	}
}

// TestPruneKeepsNewestRevisionFallback: with a revision duplicate and NO opt
// link, Cleanup's fallback (newest per sortVersions) must keep 1.2.3_1, not the
// older 1.2.3.
func TestPruneKeepsNewestRevisionFallback(t *testing.T) {
	prefix := fakePrefix(t, map[string][]string{"foo": {"1.2.3", "1.2.3_1"}})
	dups, err := FindDuplicates(nil)
	if err != nil || len(dups) != 1 {
		t.Fatalf("FindDuplicates: dups=%v err=%v", dups, err)
	}
	keep := dups[0].Versions[len(dups[0].Versions)-1] // Cleanup's no-opt-link fallback
	if keep != "1.2.3_1" {
		t.Fatalf("fallback keep=%q want 1.2.3_1", keep)
	}
	removed, err := PruneDuplicate(dups[0], keep)
	if err != nil {
		t.Fatalf("PruneDuplicate: %v", err)
	}
	if len(removed) != 1 || removed[0] != "1.2.3" {
		t.Fatalf("removed=%v want [1.2.3]", removed)
	}
	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "foo", "1.2.3_1")); err != nil {
		t.Fatalf("newer revision wrongly pruned: %v", err)
	}
}

func TestFindDuplicates_TwoVersions(t *testing.T) {
	prefix := fakePrefix(t, map[string][]string{
		"hello": {"2.12.0", "2.12.3"},
		"solo":  {"1.0.0"},
	})
	symlinkOpt(t, prefix, "hello", "2.12.3")

	got, err := FindDuplicates(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 duplicate, got %d (%+v)", len(got), got)
	}
	d := got[0]
	if d.Name != "hello" {
		t.Fatalf("name=%q", d.Name)
	}
	if !reflect.DeepEqual(d.Versions, []string{"2.12.0", "2.12.3"}) {
		t.Fatalf("versions=%v", d.Versions)
	}
	if d.Linked != "2.12.3" {
		t.Fatalf("linked=%q", d.Linked)
	}
	if len(d.CellarPaths) != 2 {
		t.Fatalf("cellar paths=%v", d.CellarPaths)
	}
}

func TestFindDuplicates_NameFilter(t *testing.T) {
	fakePrefix(t, map[string][]string{
		"hello": {"1.0.0", "2.0.0"},
		"world": {"3.0.0", "4.0.0"},
	})
	got, err := FindDuplicates([]string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "hello" {
		t.Fatalf("want only hello, got %+v", got)
	}
}

func TestFindDuplicates_MissingLink(t *testing.T) {
	fakePrefix(t, map[string][]string{
		"foo": {"1.0.0", "1.0.1"},
	})
	got, err := FindDuplicates(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Linked != "" {
		t.Fatalf("want empty linked, got %+v", got)
	}
}

func TestFindDuplicates_NoDuplicates(t *testing.T) {
	fakePrefix(t, map[string][]string{
		"alpha": {"1.0.0"},
		"beta":  {"2.0.0"},
	})
	got, err := FindDuplicates(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0, got %+v", got)
	}
}

func TestFindDuplicates_NonSemverFallback(t *testing.T) {
	fakePrefix(t, map[string][]string{
		// Homebrew revision suffix: 3.0.0_1 is a revision bump of 3.0.0 and must
		// sort as the newer keg even though _1 isn't valid semver.
		"legacy": {"3.0.0_1", "3.0.0"},
	})
	got, err := FindDuplicates(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1, got %+v", got)
	}
	// Newest (tail) must be the revision bump.
	if got[0].Versions[len(got[0].Versions)-1] != "3.0.0_1" {
		t.Fatalf("versions=%v want tail 3.0.0_1", got[0].Versions)
	}
}

func TestPruneDuplicate_RemovesOld(t *testing.T) {
	prefix := fakePrefix(t, map[string][]string{
		"hello": {"2.12.0", "2.12.3"},
	})
	symlinkOpt(t, prefix, "hello", "2.12.3")

	dups, err := FindDuplicates([]string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := PruneDuplicate(dups[0], "2.12.3")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed, []string{"2.12.0"}) {
		t.Fatalf("removed=%v", removed)
	}
	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "hello", "2.12.0")); !os.IsNotExist(err) {
		t.Fatalf("old version still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(prefix, "Cellar", "hello", "2.12.3")); err != nil {
		t.Fatalf("kept version missing: %v", err)
	}
}

func TestPruneDuplicate_UnknownKeepVersion(t *testing.T) {
	prefix := fakePrefix(t, map[string][]string{
		"hello": {"1.0.0", "2.0.0"},
	})
	symlinkOpt(t, prefix, "hello", "2.0.0")
	dups, err := FindDuplicates([]string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PruneDuplicate(dups[0], "9.9.9"); err == nil {
		t.Fatal("expected error for missing keep version")
	}
}
