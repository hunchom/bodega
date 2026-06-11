package brew

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// SharedAPICache exposes the process-wide API cache to callers outside this
// package (e.g. internal/verify) that want to resolve formula metadata
// without going through Brew's full Info path. Returns nil when tests have
// disabled it.
func SharedAPICache() *APICache { return apiCache() }

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

// sortVersions sorts in place, oldest first, the Homebrew way — so the newest
// keg lands at the tail and Cleanup keeps it.
func sortVersions(vs []string) {
	sort.SliceStable(vs, func(i, j int) bool {
		return compareKegVersions(vs[i], vs[j]) < 0
	})
}

// compareKegVersions orders two Cellar version-dir names the Homebrew way for
// the cases that matter: the optional _<revision> suffix is split off as a
// tiebreaker, and the base is compared via semver (so 1.10 > 1.9, and
// prereleases sort below their release) with a numeric-token fallback for
// non-semver strings (dates, p-levels, two-part versions). Returns -1 if a is
// older, +1 if newer, 0 if equal.
func compareKegVersions(a, b string) int {
	ab, ar := splitRevision(a)
	bb, br := splitRevision(b)
	if c := compareVersionBase(ab, bb); c != 0 {
		return c
	}
	switch {
	case ar < br:
		return -1
	case ar > br:
		return 1
	}
	return 0
}

// splitRevision peels a trailing _<n> Homebrew revision off v. A revision is a
// real bump (1.2.3_1 is newer than 1.2.3) but isn't valid semver, so it has to
// come off before the base comparison.
func splitRevision(v string) (base string, rev int) {
	if i := strings.LastIndexByte(v, '_'); i > 0 {
		if n, err := strconv.Atoi(v[i+1:]); err == nil {
			return v[:i], n
		}
	}
	return v, 0
}

// compareVersionBase compares two revision-stripped versions. Both-semver →
// semver compare; otherwise numeric per dotted component so 1.10 > 1.9 and
// 1.2 == 1.2.0.
func compareVersionBase(a, b string) int {
	if va, ea := semver.NewVersion(a); ea == nil {
		if vb, eb := semver.NewVersion(b); eb == nil {
			return va.Compare(vb)
		}
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := max(len(as), len(bs))
	for i := range n {
		at, bt := "0", "0"
		if i < len(as) {
			at = as[i]
		}
		if i < len(bs) {
			bt = bs[i]
		}
		if c := compareVersionToken(at, bt); c != 0 {
			return c
		}
	}
	return 0
}

// compareVersionToken compares one dotted component: numeric when both parse as
// ints (1.10 > 1.9), else lexical.
func compareVersionToken(a, b string) int {
	an, ae := strconv.Atoi(a)
	bn, be := strconv.Atoi(b)
	if ae == nil && be == nil {
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
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
