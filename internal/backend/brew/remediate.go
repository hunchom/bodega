package brew

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hunchom/bodega/internal/backend"
)

// brewRemedy is one known brew failure shape and its auto-fix. The regex's
// first capture group must be the formula/cask token the failure is about,
// unless attributeSingle is set (token-less errors attributed when the run
// involved exactly one package).
type brewRemedy struct {
	label string
	re    *regexp.Regexp
	// plan returns brew argv sequences that fix the failure for pkg.
	// nil plan means the verify re-run itself is the fix (transient).
	plan func(pkg string) [][]string
	// consume: drop the package instead of fixing (it's already fine /
	// there's nothing to act on).
	consume bool
	// transient: wait briefly before the verify re-run (locks, network).
	transient bool
	// attributeSingle: regex has no package capture; only match when the
	// run named exactly one package.
	attributeSingle bool
}

// pkgToken matches a formula or cask token in brew error output.
const pkgToken = `([A-Za-z0-9@._+-]+)`

// brewRemedies are tried in order against the failed run's output tail.
// One remediation pass per run — a remedy never re-triggers itself.
var brewRemedies = []brewRemedy{
	{
		// User deleted the .app manually; upgrade's uninstall step can't
		// find it and bails. Force-uninstall ignores missing artifacts,
		// then a fresh install brings the new version in.
		label: "app bundle missing — reinstalling cask",
		re:    regexp.MustCompile(`Error: ` + pkgToken + `: It seems the App source '[^']+' is not there`),
		plan: func(pkg string) [][]string {
			return [][]string{
				{"uninstall", "--cask", "--force", "--", pkg},
				{"install", "--cask", "--", pkg},
			}
		},
	},
	{
		// Corrupt/stale cached download. Scrub that package's cache; the
		// verify re-run refetches clean bytes.
		label: "corrupt cached download — scrubbing cache and refetching",
		re:    regexp.MustCompile(`Error: ` + pkgToken + `: SHA256 mismatch`),
		plan: func(pkg string) [][]string {
			return [][]string{{"cleanup", "-s", "--", pkg}}
		},
	},
	{
		// A keg vanished mid-flight (manual rm, crashed cleanup). Nothing
		// to uninstall — install fresh.
		label: "keg missing from Cellar — installing fresh",
		re:    regexp.MustCompile(`Error: No such keg: [^\n]*/Cellar/` + pkgToken),
		plan: func(pkg string) [][]string {
			return [][]string{{"install", "--", pkg}}
		},
	},
	{
		label:   "not installed — nothing to upgrade",
		re:      regexp.MustCompile(`Error: Cask '` + pkgToken + `' is not installed`),
		consume: true,
	},
	{
		// Real brew message: "Warning: Not upgrading <token>, the latest
		// version is already installed" (cask/upgrade.rb).
		label:   "already up to date",
		re:      regexp.MustCompile(`Warning: Not upgrading ` + pkgToken + `,`),
		consume: true,
	},
	{
		// Another brew/yum process holds the lock. Wait, then verify.
		label:     "another operation in progress — waiting and retrying",
		re:        regexp.MustCompile(`Error: Operation already in progress for ` + pkgToken),
		transient: true,
	},
	{
		// Transient network failure. The verify re-run is the retry; brew
		// resumes/skips whatever already finished.
		label:           "transient download failure — retrying",
		re:              regexp.MustCompile(`(?:curl: \(\d+\)|Download failed|Connection reset by peer|Couldn't connect to server|timed out|Temporary failure in name resolution)`),
		attributeSingle: false, // matched generically; no package consumed
		transient:       true,
	},
	{
		// Stale local tap/cask data referencing a renamed token.
		label: "definitions out of date — refreshing brew",
		re:    regexp.MustCompile(`Error: No available formula or cask with the name "` + pkgToken + `"`),
		plan: func(pkg string) [][]string {
			return [][]string{{"update", "--quiet"}}
		},
	},
	{
		// App exists but brew didn't install it (user dragged it in).
		// Adopt it instead of failing. Token-less error — only safe to
		// attribute when the run involved exactly one package.
		label: "app already present — adopting into brew",
		re:    regexp.MustCompile(`Error: It seems there is already an App at '[^']+'`),
		plan: func(pkg string) [][]string {
			return [][]string{{"install", "--cask", "--force", "--", pkg}}
		},
		attributeSingle: true,
	},
}

// brewHint maps recognized-but-not-auto-fixable failures to a one-line
// actionable hint appended to the error.
type brewHint struct {
	re   *regexp.Regexp
	hint string
}

var brewHints = []brewHint{
	{
		re:   regexp.MustCompile(`Command Line Tools.*(too outdated|are too old)|invalid active developer path`),
		hint: "update Apple's CLT: sudo rm -rf /Library/Developer/CommandLineTools && xcode-select --install",
	},
	{
		re:   regexp.MustCompile(`requires macOS version|macOS .* or (newer|later) is required|depends on macOS`),
		hint: "package needs a newer macOS; `yum pin <pkg>` to stop trying",
	},
	{
		re:   regexp.MustCompile(`Permission denied @ apply2files - (\S+)`),
		hint: "fix ownership: sudo chown -R $(whoami) /opt/homebrew",
	},
	{
		re:   regexp.MustCompile(`(is in use|Resource busy|couldn't be (moved|removed) because)`),
		hint: "the app looks open — quit it and re-run `yum upgrade`",
	},
	{
		re:   regexp.MustCompile(`Refusing to uninstall .* because it is required by`),
		hint: "other packages depend on it — `yum autoremove` after removing them, or remove with --force",
	},
	{
		re:   regexp.MustCompile(`Your Xcode \(.*\) is (too outdated|outdated)`),
		hint: "update Xcode from the App Store, then re-run",
	},
	{
		// Trusting a third-party tap is a security decision — hint, never auto.
		re:   regexp.MustCompile(`taps are not trusted`),
		hint: "trust the tap if you rely on it: brew trust <user>/<tap> — or remove it: brew untap <user>/<tap>",
	},
}

// brewHintFor returns the first matching hint for a failed run's output, or "".
func brewHintFor(tail string) string {
	for _, h := range brewHints {
		if h.re.MatchString(tail) {
			return h.hint
		}
	}
	return ""
}

// remedyMatch pairs a failing package with the remedy that recognizes it.
type remedyMatch struct {
	pkg    string
	remedy *brewRemedy
}

// matchRemedies scans a failed brew run's output tail for known failure
// shapes. At most one match per package (first remedy wins). names is the
// package set of the failed run, used for token-less attribution.
func matchRemedies(tail string, names []string) []remedyMatch {
	var out []remedyMatch
	seen := map[string]bool{}
	for i := range brewRemedies {
		r := &brewRemedies[i]
		if r.attributeSingle && r.plan != nil {
			if len(names) == 1 && r.re.MatchString(tail) && !seen[names[0]] {
				seen[names[0]] = true
				out = append(out, remedyMatch{pkg: names[0], remedy: r})
			}
			continue
		}
		if r.re.NumSubexp() == 0 {
			// Generic transient pattern — applies to the run, not a package.
			if r.re.MatchString(tail) && !seen["\x00run"] {
				seen["\x00run"] = true
				out = append(out, remedyMatch{pkg: "", remedy: r})
			}
			continue
		}
		for _, m := range r.re.FindAllStringSubmatch(tail, -1) {
			pkg := m[1]
			if pkg == "" || seen[pkg] || !isSafeKegName(pkg) {
				continue
			}
			seen[pkg] = true
			out = append(out, remedyMatch{pkg: pkg, remedy: r})
		}
	}
	return out
}

// remediateBrewFailure attempts the auto-fixes matched in tail, then runs a
// verify pass that must come back clean: `brew <sub> -- <names minus
// consumed>` for named runs, a bare re-run for a bulk (empty-names) run. On
// a clean verify the original failure is considered handled; the returned
// slice names the consumed packages (nothing was done for them — callers
// must exclude them from success reporting and the journal). Unrecognized
// failures, fix failures, and dirty verifies surface the original error —
// annotated with an actionable hint when we recognize the shape without
// being able to fix it.
func (b *Brew) remediateBrewFailure(ctx context.Context, w backend.ProgressWriter, sub string, names []string, tail string, orig error) ([]string, error) {
	matched := matchRemedies(tail, names)
	if len(matched) == 0 {
		return nil, withBrewHint(orig, tail)
	}

	consumed := map[string]bool{}
	wait := false
	actedAny := false
	for _, m := range matched {
		if m.remedy.consume {
			fwd(w, fmt.Sprintf("skipping %s: %s", m.pkg, m.remedy.label))
			consumed[m.pkg] = true
			continue
		}
		if m.pkg != "" {
			fwd(w, fmt.Sprintf("auto-fix %s: %s", m.pkg, m.remedy.label))
		} else {
			fwd(w, "auto-fix: "+m.remedy.label)
		}
		actedAny = true
		if m.remedy.transient {
			wait = true
		}
		if m.remedy.plan == nil {
			continue
		}
		for _, argv := range m.remedy.plan(m.pkg) {
			if err := b.stream(ctx, w, argv...); err != nil {
				return nil, withBrewHint(fmt.Errorf("%w (auto-fix %s: %v)", orig, m.pkg, err), tail)
			}
		}
	}

	// Verify with the consumed packages dropped — they're the ones brew
	// errors about on every run (e.g. not installed at all). A bulk run has
	// no names; when a fix ran, the bare re-run IS the verify (and for
	// cache-scrub style remedies, the actual fix).
	remaining := make([]string, 0, len(names))
	for _, n := range names {
		if !consumed[n] {
			remaining = append(remaining, n)
		}
	}
	if len(remaining) > 0 || (len(names) == 0 && actedAny) {
		if wait {
			sleepCtx(ctx, 2*time.Second)
		}
		if err := b.stream(ctx, w, brewArgs(sub, remaining)...); err != nil {
			return nil, withBrewHint(fmt.Errorf("%w (after auto-fix: %v)", orig, err), tail)
		}
	}

	var fixed []string
	for _, m := range matched {
		if !m.remedy.consume && m.pkg != "" {
			fixed = append(fixed, m.pkg)
		}
	}
	if len(fixed) > 0 {
		fwd(w, "auto-fixed: "+strings.Join(fixed, ", "))
	}
	var consumedList []string
	for n := range consumed {
		consumedList = append(consumedList, n)
	}
	sort.Strings(consumedList)
	return consumedList, nil
}

// withBrewHint annotates err with the first matching actionable hint, if any.
func withBrewHint(err error, tail string) error {
	if h := brewHintFor(tail); h != "" {
		return fmt.Errorf("%w\n  hint: %s", err, h)
	}
	return err
}
