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
		// Homebrew revision suffix like 3.0.0_1 fails strict semver parsing.
		"legacy": {"3.0.0_1", "3.0.0"},
	})
	got, err := FindDuplicates(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1, got %+v", got)
	}
	// "3.0.0" parses as semver; "3.0.0_1" does not. Non-parseable sorts first,
	// so the tail must be the semver-valid entry.
	if got[0].Versions[len(got[0].Versions)-1] != "3.0.0" {
		t.Fatalf("versions=%v", got[0].Versions)
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
