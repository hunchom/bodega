package brew

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Prefix exposes the cached Homebrew install prefix to callers outside this
// package. Returns "" when neither /opt/homebrew nor /usr/local has a Cellar.
func Prefix() string {
	if p := findDuplicatesPrefix; p != "" {
		return p
	}
	return brewPrefix()
}

// findDuplicatesPrefix lets tests point FindDuplicates/PruneDuplicate at a
// fake prefix under t.TempDir() without touching the real filesystem. Empty
// in production.
var findDuplicatesPrefix string

// Duplicate describes a formula with more than one version present in the
// Cellar.
type Duplicate struct {
	Name        string   `json:"name"`
	Versions    []string `json:"versions"`     // oldest -> newest
	CellarPaths []string `json:"cellar_paths"` // aligned with Versions
	Linked      string   `json:"linked"`       // the version $PREFIX/opt/<name> resolves to, or ""
}

// FindDuplicates scans $PREFIX/Cellar for formulae with ≥2 installed version
// directories. When names is non-empty the scan is restricted to that set.
func FindDuplicates(names []string) ([]Duplicate, error) {
	prefix := Prefix()
	if prefix == "" {
		return nil, nil
	}
	cellarRoot := filepath.Join(prefix, "Cellar")

	var candidates []string
	if len(names) > 0 {
		candidates = names
	} else {
		entries, err := os.ReadDir(cellarRoot)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("duplicates: read %s: %w", cellarRoot, err)
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			candidates = append(candidates, e.Name())
		}
	}
	sort.Strings(candidates)

	var out []Duplicate
	for _, name := range candidates {
		pkgDir := filepath.Join(cellarRoot, name)
		versions := versionDirs(pkgDir)
		if len(versions) < 2 {
			continue
		}
		sortVersions(versions)
		paths := make([]string, len(versions))
		for i, v := range versions {
			paths[i] = filepath.Join(pkgDir, v)
		}
		out = append(out, Duplicate{
			Name:        name,
			Versions:    versions,
			CellarPaths: paths,
			Linked:      linkedVersion(prefix, name),
		})
	}
	return out, nil
}

// PruneDuplicate keeps keepVersion for dup and removes every other Cellar
// version directory. After the stale versions are gone it re-runs Link so
// $PREFIX/opt/<name> and the prefix symlinks point at the survivor. Returns
// the versions that were removed, in the order encountered.
func PruneDuplicate(dup Duplicate, keepVersion string) ([]string, error) {
	prefix := Prefix()
	if prefix == "" {
		return nil, fmt.Errorf("prune: no homebrew prefix")
	}
	var keepDir string
	var removed []string
	for i, v := range dup.Versions {
		if v == keepVersion {
			keepDir = dup.CellarPaths[i]
			continue
		}
		if err := os.RemoveAll(dup.CellarPaths[i]); err != nil {
			return removed, fmt.Errorf("prune %s@%s: %w", dup.Name, v, err)
		}
		removed = append(removed, v)
	}
	if keepDir == "" {
		return removed, fmt.Errorf("prune %s: keep version %q not present", dup.Name, keepVersion)
	}
	if _, err := Link(prefix, keepDir, LinkOptions{Overwrite: true}); err != nil {
		return removed, fmt.Errorf("prune %s: relink %s: %w", dup.Name, keepVersion, err)
	}
	return removed, nil
}

// versionDirs returns the non-dot subdirectory names of pkgDir. Non-dirs and
// hidden entries are skipped.
func versionDirs(pkgDir string) []string {
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// sortVersions sorts in place, oldest first. Strings that parse as semver
// compare by semver; unparseable versions fall back to lexicographic order
// against each other, and sort before semver-parseable ones so the newest
// semver-valid entry still lands at the tail.
func sortVersions(vs []string) {
	sort.SliceStable(vs, func(i, j int) bool {
		vi, ei := semver.NewVersion(vs[i])
		vj, ej := semver.NewVersion(vs[j])
		switch {
		case ei == nil && ej == nil:
			return vi.LessThan(vj)
		case ei == nil && ej != nil:
			return false
		case ei != nil && ej == nil:
			return true
		default:
			return vs[i] < vs[j]
		}
	})
}

// linkedVersion resolves $PREFIX/opt/<name> and returns the final path
// component of its target — that's brew's convention for "this version is
// linked". Empty string when the symlink is missing or broken.
func linkedVersion(prefix, name string) string {
	opt := filepath.Join(prefix, "opt", name)
	target, err := os.Readlink(opt)
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(opt), target)
	}
	if _, err := os.Stat(target); err != nil {
		return ""
	}
	return filepath.Base(filepath.Clean(target))
}
