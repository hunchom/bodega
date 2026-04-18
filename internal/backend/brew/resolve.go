package brew

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Plan is one node in the install graph. Resolve produces a []Plan in
// dependency-leaves-first order so the orchestrator can walk it in a single
// pass, knowing every entry it visits has already had its runtime deps
// installed (or marked for install) earlier in the slice.
type Plan struct {
	Name      string // formula name
	Version   string // stable version
	Tag       string // bottle tag (arm64_sequoia, arm64_sonoma, all, …)
	BottleURL string // absolute https URL
	SHA256    string // hex digest (lowercase)
	IsRoot    bool   // true if explicitly requested; false if transitive dep
}

// Sentinel errors. Callers distinguish them with errors.Is.
var (
	// ErrNoBottle signals that the formula has no bottle file matching the
	// host's preference list. Callers should fall back to a source build or
	// skip.
	ErrNoBottle = errors.New("no compatible bottle available")

	// ErrFormulaNotFound signals that a name isn't in the APICache. Callers
	// typically bubble this up as-is since a missing formula is user error.
	ErrFormulaNotFound = errors.New("formula not found in api cache")
)

// Resolve computes the ordered install plan for the given roots.
//
// Ordering: dependencies (leaves) come first. If B depends on A, A appears
// before B in the returned slice. Each formula appears exactly once even if
// it's reached through multiple paths. Runtime dependencies only —
// build_dependencies are skipped because bottles are pre-built.
//
// Bottle tag selection falls back from the most specific match to "all"
// following BottleTagPreference. Returns ErrNoBottle wrapped with the
// formula name if nothing matches.
//
// Returns ErrFormulaNotFound wrapped with the missing name if a root or
// transitive dep isn't present in the APICache.
//
// Cycles produce a descriptive error listing the cycle path; callers can
// surface this to users verbatim.
func Resolve(ctx context.Context, cache *APICache, roots []string) ([]Plan, error) {
	if cache == nil {
		return nil, fmt.Errorf("resolve: nil api cache")
	}
	formulae, err := cache.LoadFormulae()
	if err != nil {
		return nil, fmt.Errorf("resolve: load formulae: %w", err)
	}

	prefs := BottleTagPreference()

	var (
		order    []Plan
		resolved = make(map[string]struct{})
		visiting = make(map[string]struct{})
		stack    []string
		visit    func(name string, isRoot bool) error
	)

	visit = func(name string, isRoot bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, done := resolved[name]; done {
			// Already in order — but if we hit it again as a root, upgrade
			// the IsRoot flag so the orchestrator knows the user asked for it.
			if isRoot {
				for i := range order {
					if order[i].Name == name {
						order[i].IsRoot = true
						break
					}
				}
			}
			return nil
		}
		if _, inProgress := visiting[name]; inProgress {
			// Reconstruct the cycle path by slicing the stack from the first
			// occurrence of name onward. This gives users something like
			// "A -> B -> A".
			start := 0
			for i, s := range stack {
				if s == name {
					start = i
					break
				}
			}
			path := append([]string{}, stack[start:]...)
			path = append(path, name)
			return fmt.Errorf("resolve: dependency cycle: %s", strings.Join(path, " -> "))
		}

		f, ok := formulae[name]
		if !ok || f == nil {
			return fmt.Errorf("resolve: %s: %w", name, ErrFormulaNotFound)
		}

		visiting[name] = struct{}{}
		stack = append(stack, name)

		for _, dep := range f.Dependencies {
			if dep == "" {
				continue
			}
			if err := visit(dep, false); err != nil {
				return err
			}
		}

		stack = stack[:len(stack)-1]
		delete(visiting, name)

		tag, file, ok := pickBottle(f, prefs)
		if !ok {
			return fmt.Errorf("resolve: %s: %w", name, ErrNoBottle)
		}

		order = append(order, Plan{
			Name:      name,
			Version:   f.Versions.Stable,
			Tag:       tag,
			BottleURL: file.URL,
			SHA256:    strings.ToLower(file.SHA256),
			IsRoot:    isRoot,
		})
		resolved[name] = struct{}{}
		return nil
	}

	for _, r := range roots {
		if r == "" {
			continue
		}
		if err := visit(r, true); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// pickBottle walks prefs in order and returns the first tag present in the
// formula's bottle file map. Formulae publish exactly one URL per tag, so
// the first hit is always the best.
func pickBottle(f *APIFormula, prefs []string) (string, APIBottleFile, bool) {
	files := f.Bottle.Stable.Files
	if len(files) == 0 {
		return "", APIBottleFile{}, false
	}
	for _, tag := range prefs {
		if bf, ok := files[tag]; ok && bf.URL != "" {
			return tag, bf, true
		}
	}
	return "", APIBottleFile{}, false
}

// BottleTagPreference returns the host's preferred bottle-tag list, most
// specific first. Exported so the CLI can print it under --debug and so
// tests can assert against a known ordering.
//
// Order for arm64 macOS N: arm64_<codenameN>, arm64_<codenameN-1>, ..., all.
// Order for amd64 macOS N (>=11): <codenameN>, ..., <codenameN-1>, ..., all.
// Order for amd64 macOS 10.15 (Catalina): catalina, all. Brew doesn't arch-
// prefix intel pre-11 because no arm64 build exists for those releases.
//
// Forward fallback across majors is deliberately NOT allowed: if the host
// is on sonoma we won't try arm64_sequoia, because a bottle built against a
// newer SDK may link against symbols unavailable on the older OS.
func BottleTagPreference() []string {
	hostOnce.Do(detectHost)
	return append([]string(nil), hostPrefs...)
}

// Codename ordering, newest-first. The ":1" element is the integer macOS
// major version for that codename so we can slice this list at the host's
// current major and walk downward. Pre-11 releases are intentionally
// omitted; brew bottles for those use bare "catalina" (handled specially).
var macosCodenames = []struct {
	major int
	name  string
}{
	{26, "tahoe"},
	{15, "sequoia"},
	{14, "sonoma"},
	{13, "ventura"},
	{12, "monterey"},
	{11, "big_sur"},
}

var (
	hostOnce  sync.Once
	hostPrefs []string

	// testHostOverride lets tests force a specific host without mocking
	// sw_vers. When non-nil, hostOnce.Do still runs but detectHost uses the
	// override instead of runtime/exec. Tests should set this before first
	// call; BottleTagPreference caches forever once computed.
	testHostOverride *hostInfo
)

type hostInfo struct {
	arch  string // "arm64" or "amd64"
	major int    // macOS major version, 15 for sequoia, 14 for sonoma, ...
}

// detectHost populates hostPrefs from runtime.GOARCH + sw_vers. On non-darwin
// (tests, cross-builds) we fall back to a safe default — current arm64 codename
// list — so the function is always usable.
func detectHost() {
	info := detectHostInfo()
	hostPrefs = tagPreference(info)
}

// detectHostInfo returns the host architecture + macOS major version, honouring
// testHostOverride if set. Factored out so tests can exercise tagPreference
// without monkeypatching sync.Once.
func detectHostInfo() hostInfo {
	if testHostOverride != nil {
		return *testHostOverride
	}
	info := hostInfo{arch: normalizeArch(runtime.GOARCH)}
	if runtime.GOOS == "darwin" {
		info.major = darwinMajor()
	}
	return info
}

// normalizeArch folds Go's arch identifiers into the prefix brew uses in its
// bottle tags. Apple Silicon is arm64; Intel is amd64 in Go but brew uses no
// arch prefix at all for intel, so we return "amd64" for internal dispatch
// and strip it at tag-construction time.
func normalizeArch(goarch string) string {
	switch goarch {
	case "arm64":
		return "arm64"
	case "amd64":
		return "amd64"
	default:
		return goarch
	}
}

// darwinMajor shells out to sw_vers -productVersion and parses the leading
// major version integer. 10.x (pre-BigSur) returns 10. Failure returns 0,
// which causes tagPreference to fall back to ["all"] only.
func darwinMajor() int {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(out))
	// Pre-11 ships as "10.15.7"; modern releases as "15.2" or "15".
	// We want the first dotted component.
	dot := strings.IndexByte(s, '.')
	if dot > 0 {
		s = s[:dot]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// tagPreference builds the ordered tag list for a host. Pure function so
// tests can pin arch+major and assert the slice directly.
func tagPreference(h hostInfo) []string {
	var out []string
	// Slice codenames from the host's major downward. If the host is newer
	// than our newest known codename we start at the newest (best-effort);
	// if the host is unknown (major==0) or older than 11 we fall through.
	start := -1
	for i, c := range macosCodenames {
		if c.major <= h.major {
			start = i
			break
		}
	}

	if start >= 0 && h.major >= 11 {
		for _, c := range macosCodenames[start:] {
			if h.arch == "arm64" {
				out = append(out, "arm64_"+c.name)
			} else {
				out = append(out, c.name)
			}
		}
	} else if h.arch == "amd64" && h.major == 10 {
		// Catalina-era intel hosts — brew tags them bare "catalina".
		out = append(out, "catalina")
	}

	// "all" is brew's noarch bottle and is always the last-resort match.
	out = append(out, "all")
	return out
}
