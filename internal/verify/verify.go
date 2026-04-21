// Package verify implements `yum verify` — a deep integrity check of a
// Homebrew-style install tree. It's intentionally decoupled from the cmd
// layer so the checks can be exercised against a synthetic prefix in tests
// without spinning up cobra or booting an AppCtx.
package verify

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hunchom/bodega/internal/backend/brew"
)

// IssueKind enumerates the distinct problem categories `yum verify` reports.
// Values are stable strings so they survive JSON round-trips and can be
// grepped for by tooling.
type IssueKind string

const (
	KindMissingDep    IssueKind = "missing-dep"
	KindBrokenSymlink IssueKind = "broken-symlink"
	KindOrphaned      IssueKind = "orphaned"
	KindStalePin      IssueKind = "stale-pin"
	KindUnreadable    IssueKind = "unreadable"
)

// Issue is one finding in the verify report. Package names the formula the
// finding is attributed to (may be empty for filesystem-level issues);
// Path is the filesystem location if applicable; Detail carries any extra
// context the UI needs to make the message useful (e.g. "required by git").
type Issue struct {
	Kind    IssueKind `json:"kind"`
	Package string    `json:"package,omitempty"`
	Path    string    `json:"path,omitempty"`
	Detail  string    `json:"detail,omitempty"`
}

// Report is what Run returns. Passed is false iff Issues is non-empty.
type Report struct {
	Issues []Issue `json:"issues"`
	Passed bool    `json:"passed"`
}

// DepResolver is how verify resolves a formula's runtime deps when there's
// no local .brew/<name>.rb to parse. In production this is backed by
// brew.APICache; tests inject a stub.
type DepResolver interface {
	Deps(name string) ([]string, error)
}

// Options configures a verify run. Prefix defaults to brew.Prefix() when
// empty. Fix=true enables safe auto-remediation — currently just broken
// symlink removal. APIDeps is required for the missing-dep check to be
// useful; nil is treated as "no deps known" and short-circuits that check.
type Options struct {
	Prefix  string
	Fix     bool
	APIDeps DepResolver
}

// Run executes every check against opts.Prefix and returns a Report. It
// returns a non-nil error only for catastrophic problems (e.g. the prefix
// doesn't exist at all); individual check failures become Issues.
func Run(opts Options) (*Report, error) {
	prefix := opts.Prefix
	if prefix == "" {
		prefix = brew.Prefix()
	}
	if prefix == "" {
		return &Report{Passed: true}, nil
	}

	var issues []Issue
	issues = append(issues, checkMissingDeps(prefix, opts.APIDeps)...)
	issues = append(issues, checkBrokenSymlinks(prefix, opts.Fix)...)
	issues = append(issues, checkOrphaned(prefix)...)
	issues = append(issues, checkStalePins(prefix)...)

	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		if issues[i].Package != issues[j].Package {
			return issues[i].Package < issues[j].Package
		}
		return issues[i].Path < issues[j].Path
	})
	return &Report{Issues: issues, Passed: len(issues) == 0}, nil
}

// installedFormulae returns every formula name present under
// $prefix/Cellar — one entry per directory, regardless of how many versions
// are installed. The bool map it also returns is a quick existence set for
// dep resolution below.
func installedFormulae(prefix string) ([]string, map[string]bool) {
	entries, err := os.ReadDir(filepath.Join(prefix, "Cellar"))
	if err != nil {
		return nil, map[string]bool{}
	}
	set := make(map[string]bool, len(entries))
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || strings.HasPrefix(n, ".") {
			continue
		}
		set[n] = true
		names = append(names, n)
	}
	sort.Strings(names)
	return names, set
}

// depsRe pulls the "foo" out of `depends_on "foo"` while skipping
// build-time-only deps (the `=> :build` and `=> :test` variants). Homebrew
// uses other arrow targets too (`:recommended`, `:optional`); those *are*
// runtime unless explicitly excluded at install time, so we keep them.
var depsRe = regexp.MustCompile(`(?m)^\s*depends_on\s+"([^"]+)"(?:\s*=>\s*(:[a-z]+|\[[^\]]+\]))?`)

// onLinuxRe matches a full `on_linux do ... end` block, non-greedy, so we
// can drop deps gated to other OSes before running depsRe. We intentionally
// only strip Linux-only blocks because bodega targets macOS; on a Linux
// host you'd want the reverse (drop `on_macos do`), but the current
// codebase has no Linux build path.
var onLinuxRe = regexp.MustCompile(`(?s)\bon_linux\s+do\b.*?\n\s*end\b`)

// readLocalDeps parses $prefix/Cellar/<name>/<ver>/.brew/<name>.rb for
// runtime depends_on directives. Returns (nil, false) if the file doesn't
// exist so the caller can fall back to the API resolver. Parsing a formula
// Ruby file with a regex is obviously not a full Ruby parse — it's good
// enough for the stable declarative subset Homebrew actually uses.
func readLocalDeps(prefix, name string) ([]string, bool) {
	cellar := filepath.Join(prefix, "Cellar", name)
	versions, err := os.ReadDir(cellar)
	if err != nil {
		return nil, false
	}
	var best string
	for _, v := range versions {
		n := v.Name()
		if !v.IsDir() || strings.HasPrefix(n, ".") {
			continue
		}
		if n > best {
			best = n
		}
	}
	if best == "" {
		return nil, false
	}
	rb := filepath.Join(cellar, best, ".brew", name+".rb")
	data, err := os.ReadFile(rb)
	if err != nil {
		return nil, false
	}
	// Strip on_linux blocks so deps gated to Linux don't produce phantom
	// "missing" reports on macOS.
	cleaned := onLinuxRe.ReplaceAllString(string(data), "")
	var out []string
	for _, m := range depsRe.FindAllStringSubmatch(cleaned, -1) {
		if isNonRuntimeArrow(m[2]) {
			continue
		}
		out = append(out, m[1])
	}
	return out, true
}

// isNonRuntimeArrow returns true when the `=> ...` suffix on a depends_on
// line marks the dep as build- or test-only. Accepts bare symbols
// (`:build`) and array forms (`[:build, :test]`). A dep tagged as both
// build AND test but nothing else is not needed at runtime; a dep tagged
// build + runtime (unusual but legal) still counts as runtime.
func isNonRuntimeArrow(arrow string) bool {
	arrow = strings.TrimSpace(arrow)
	if arrow == "" {
		return false
	}
	if arrow == ":build" || arrow == ":test" {
		return true
	}
	if strings.HasPrefix(arrow, "[") && strings.HasSuffix(arrow, "]") {
		inner := strings.TrimSuffix(strings.TrimPrefix(arrow, "["), "]")
		for _, tok := range strings.Split(inner, ",") {
			tok = strings.TrimSpace(tok)
			if tok != ":build" && tok != ":test" {
				return false
			}
		}
		return true
	}
	return false
}

func checkMissingDeps(prefix string, resolver DepResolver) []Issue {
	names, installed := installedFormulae(prefix)
	if len(names) == 0 {
		return nil
	}
	// missing: dep -> list of packages that need it. Aggregating first lets
	// the UI group like "openssl@3 — required by git, curl" without needing
	// a second pass.
	missing := map[string][]string{}
	for _, name := range names {
		deps, ok := readLocalDeps(prefix, name)
		if !ok && resolver != nil {
			d, err := resolver.Deps(name)
			if err == nil {
				deps = d
			}
		}
		for _, d := range deps {
			if d == "" || installed[d] {
				continue
			}
			missing[d] = append(missing[d], name)
		}
	}
	var out []Issue
	for dep, requiredBy := range missing {
		sort.Strings(requiredBy)
		out = append(out, Issue{
			Kind:    KindMissingDep,
			Package: dep,
			Detail:  "required by " + strings.Join(requiredBy, ", "),
		})
	}
	return out
}

// symlinkDirs is the shortlist of directories verify walks for broken
// links. Keep it surgical — walking everything under $prefix is slow and
// usually turns up noise from opt/ version pinning.
func symlinkDirs() []string {
	return []string{"bin", "sbin", "lib", "include", "share", "opt"}
}

func checkBrokenSymlinks(prefix string, fix bool) []Issue {
	var out []Issue
	for _, sub := range symlinkDirs() {
		dir := filepath.Join(prefix, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // missing subdir is fine
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			lst, err := os.Lstat(p)
			if err != nil || lst.Mode()&os.ModeSymlink == 0 {
				continue
			}
			if _, err := os.Stat(p); err == nil {
				continue
			}
			// Best-effort resolve of the raw link target for a useful
			// Detail; ignore errors because Lstat already succeeded.
			tgt, _ := os.Readlink(p)
			issue := Issue{Kind: KindBrokenSymlink, Path: p, Detail: tgt}
			if fix {
				_ = os.Remove(p)
			}
			out = append(out, issue)
		}
	}
	return out
}

// checkOrphaned walks Cellar/<pkg>/<ver> and flags versions that the
// opt/<pkg> link doesn't point at. A package with no opt link at all is
// reported once (per version) so the user sees the whole story.
func checkOrphaned(prefix string) []Issue {
	cellarRoot := filepath.Join(prefix, "Cellar")
	pkgs, err := os.ReadDir(cellarRoot)
	if err != nil {
		return nil
	}
	var out []Issue
	for _, p := range pkgs {
		name := p.Name()
		if !p.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(cellarRoot, name))
		if err != nil {
			out = append(out, Issue{Kind: KindUnreadable, Package: name, Path: filepath.Join(cellarRoot, name), Detail: err.Error()})
			continue
		}
		var verNames []string
		for _, v := range versions {
			n := v.Name()
			if !v.IsDir() || strings.HasPrefix(n, ".") {
				continue
			}
			verNames = append(verNames, n)
		}
		if len(verNames) == 0 {
			continue
		}

		optLink := filepath.Join(prefix, "opt", name)
		linkedVer := ""
		if tgt, err := os.Readlink(optLink); err == nil {
			// Target is usually "../Cellar/<name>/<ver>"; take the basename
			// to pick the version.
			linkedVer = filepath.Base(tgt)
		}

		for _, v := range verNames {
			verPath := filepath.Join(cellarRoot, name, v)
			if linkedVer == "" {
				out = append(out, Issue{
					Kind:    KindOrphaned,
					Package: name,
					Path:    verPath,
					Detail:  "no opt link",
				})
				continue
			}
			if v < linkedVer {
				out = append(out, Issue{
					Kind:    KindOrphaned,
					Package: name,
					Path:    verPath,
					Detail:  "older than linked " + linkedVer,
				})
			}
		}
	}
	return out
}

func checkStalePins(prefix string) []Issue {
	pinDir := filepath.Join(prefix, "var", "homebrew", "pinned")
	entries, err := os.ReadDir(pinDir)
	if err != nil {
		// Not every install has pins — a missing dir is fine.
		return nil
	}
	_, installed := installedFormulae(prefix)
	var out []Issue
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}
		if !installed[n] {
			out = append(out, Issue{
				Kind:    KindStalePin,
				Package: n,
				Path:    filepath.Join(pinDir, n),
				Detail:  "pinned but not installed",
			})
		}
	}
	return out
}
