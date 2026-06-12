package brew

import (
	"context"
	"strings"
	"testing"

	"github.com/hunchom/bodega/internal/runner"
)

func callKeys(f *runner.Fake) []string {
	var out []string
	for _, c := range f.Calls {
		out = append(out, strings.Join(append([]string{c.Name}, c.Args...), " "))
	}
	return out
}

// TestUpgradeAutoFixMissingAppSource: the docker-desktop failure — user
// deleted the .app, upgrade's uninstall step bails. Auto-fix must force-
// uninstall + reinstall the cask, verify clean, and report success.
func TestUpgradeAutoFixMissingAppSource(t *testing.T) {
	fake := &runner.Fake{
		StderrOnce:   map[string]string{"brew upgrade -- docker-desktop": "Error: docker-desktop: It seems the App source '/Applications/Docker.app' is not there.\n"},
		ExitCodeOnce: map[string]int{"brew upgrade -- docker-desktop": 1},
	}
	b := &Brew{R: fake}
	pw := &pinCapturePW{}
	upgraded, err := b.Upgrade(context.Background(), []string{"docker-desktop"}, pw)
	if err != nil {
		t.Fatalf("auto-fix should have handled it: %v", err)
	}
	if len(upgraded) != 1 || upgraded[0] != "docker-desktop" {
		t.Fatalf("upgraded=%v", upgraded)
	}
	keys := callKeys(fake)
	want := []string{
		"brew upgrade -- docker-desktop",
		"brew uninstall --cask --force -- docker-desktop",
		"brew install --cask -- docker-desktop",
		"brew upgrade -- docker-desktop", // verify
	}
	if strings.Join(keys, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(keys, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(pw.buf.String(), "auto-fix docker-desktop") {
		t.Errorf("missing auto-fix progress line: %q", pw.buf.String())
	}
}

// TestUpgradeAutoFixConsumesNotInstalled: "Cask 'x' is not installed" can't
// be fixed by re-running — drop the package and succeed without it.
func TestUpgradeAutoFixConsumesNotInstalled(t *testing.T) {
	fake := &runner.Fake{
		StderrOnce:   map[string]string{"brew upgrade -- ghost": "Error: Cask 'ghost' is not installed.\n"},
		ExitCodeOnce: map[string]int{"brew upgrade -- ghost": 1},
	}
	b := &Brew{R: fake}
	if _, err := b.Upgrade(context.Background(), []string{"ghost"}, nil); err != nil {
		t.Fatalf("consume remedy should succeed: %v", err)
	}
	if n := len(fake.Calls); n != 1 {
		t.Fatalf("consumed package must not be retried; %d calls: %v", n, callKeys(fake))
	}
}

// TestUpgradeAutoFixShaMismatchScrubsCache: corrupt cache → cleanup -s, then
// the verify re-run refetches.
func TestUpgradeAutoFixShaMismatchScrubsCache(t *testing.T) {
	fake := &runner.Fake{
		StderrOnce:   map[string]string{"brew upgrade -- foo": "Error: foo: SHA256 mismatch\nExpected: aa\nActual: bb\n"},
		ExitCodeOnce: map[string]int{"brew upgrade -- foo": 1},
	}
	b := &Brew{R: fake}
	if _, err := b.Upgrade(context.Background(), []string{"foo"}, nil); err != nil {
		t.Fatalf("scrub+retry should succeed: %v", err)
	}
	keys := strings.Join(callKeys(fake), "\n")
	if !strings.Contains(keys, "brew cleanup -s -- foo") {
		t.Fatalf("missing cache scrub, calls:\n%s", keys)
	}
}

// TestUpgradeUnknownErrorKeepsErrorWithHint: unfixable shapes surface the
// original error plus an actionable hint; no extra brew calls.
func TestUpgradeUnknownErrorKeepsErrorWithHint(t *testing.T) {
	fake := &runner.Fake{
		Stderr:   map[string]string{"brew upgrade -- foo": "Error: foo: Permission denied @ apply2files - /opt/homebrew/lib/x\n"},
		ExitCode: map[string]int{"brew upgrade -- foo": 1},
	}
	b := &Brew{R: fake}
	_, err := b.Upgrade(context.Background(), []string{"foo"}, nil)
	if err == nil {
		t.Fatal("unfixable failure must still error")
	}
	if !strings.Contains(err.Error(), "hint: fix ownership") {
		t.Fatalf("missing hint: %v", err)
	}
	if n := len(fake.Calls); n != 1 {
		t.Fatalf("no remediation calls expected, got %d: %v", n, callKeys(fake))
	}
}

// TestUpgradeAutoFixFailureSurfacesOriginal: when the fix itself fails, the
// original error comes back annotated — never swallowed.
func TestUpgradeAutoFixFailureSurfacesOriginal(t *testing.T) {
	fake := &runner.Fake{
		StderrOnce:   map[string]string{"brew upgrade -- dd": "Error: dd: It seems the App source '/Applications/D.app' is not there.\n"},
		ExitCodeOnce: map[string]int{"brew upgrade -- dd": 1},
		Stderr:       map[string]string{"brew uninstall --cask --force -- dd": "Error: nope\n"},
		ExitCode:     map[string]int{"brew uninstall --cask --force -- dd": 1},
	}
	b := &Brew{R: fake}
	_, err := b.Upgrade(context.Background(), []string{"dd"}, nil)
	if err == nil {
		t.Fatal("failed fix must surface an error")
	}
	if !strings.Contains(err.Error(), "App source") || !strings.Contains(err.Error(), "auto-fix dd") {
		t.Fatalf("error must carry original + fix failure: %v", err)
	}
}

// TestUpgradeAutoFixDirtyVerifyFails: fixes ran but the verify pass still
// errors — surface, don't claim success.
func TestUpgradeAutoFixDirtyVerifyFails(t *testing.T) {
	fake := &runner.Fake{
		Stderr:   map[string]string{"brew upgrade -- dd": "Error: dd: It seems the App source '/Applications/D.app' is not there.\n"},
		ExitCode: map[string]int{"brew upgrade -- dd": 1}, // fails EVERY time, incl. verify
	}
	b := &Brew{R: fake}
	_, err := b.Upgrade(context.Background(), []string{"dd"}, nil)
	if err == nil {
		t.Fatal("dirty verify must fail")
	}
	if !strings.Contains(err.Error(), "after auto-fix") {
		t.Fatalf("want verify annotation, got: %v", err)
	}
}

func TestMatchRemediesAttributionAndSafety(t *testing.T) {
	// Token-less "already an App" error: attributable only for single-name runs.
	tail := "Error: It seems there is already an App at '/Applications/G.app'.\n"
	if m := matchRemedies(tail, []string{"a", "b"}); len(m) != 0 {
		t.Fatalf("multi-name run must not attribute token-less errors: %+v", m)
	}
	if m := matchRemedies(tail, []string{"ghostty"}); len(m) != 1 || m[0].pkg != "ghostty" {
		t.Fatalf("single-name attribution failed: %+v", m)
	}
	// Captured tokens are argv-safe by construction (charclass + isSafeKegName).
	bad := "Error: No such keg: /opt/homebrew/Cellar/xxx;rm -rf /\n"
	for _, m := range matchRemedies(bad, nil) {
		if !isSafeKegName(m.pkg) {
			t.Fatalf("unsafe token captured: %q", m.pkg)
		}
	}
}

// TestUpgradeAutoFixAlreadyUpToDateRealMessage: brew's actual cask message is
// "Warning: Not upgrading <token>, the latest version is already installed" —
// the token, not "Not", must be consumed.
func TestUpgradeAutoFixAlreadyUpToDateRealMessage(t *testing.T) {
	fake := &runner.Fake{
		StderrOnce: map[string]string{"brew upgrade -- firefox badcask": "Warning: Not upgrading firefox, the latest version is already installed\nError: badcask: SHA256 mismatch\n"},
		ExitCodeOnce: map[string]int{
			"brew upgrade -- firefox badcask": 1,
		},
	}
	b := &Brew{R: fake}
	upgraded, err := b.Upgrade(context.Background(), []string{"firefox", "badcask"}, nil)
	if err != nil {
		t.Fatalf("remediation should handle both: %v", err)
	}
	// firefox consumed (already current) -> excluded from the acted set; the
	// verify re-run covers badcask only.
	if len(upgraded) != 1 || upgraded[0] != "badcask" {
		t.Fatalf("upgraded=%v want [badcask]", upgraded)
	}
	keys := strings.Join(callKeys(fake), "\n")
	if !strings.Contains(keys, "brew upgrade -- badcask") {
		t.Fatalf("verify must drop the consumed package, calls:\n%s", keys)
	}
	if strings.Contains(keys, "-- Not") {
		t.Fatalf("captured bogus token 'Not', calls:\n%s", keys)
	}
}

// TestUpgradeAutoFixTransientNetworkRetries: curl/network failure shapes are
// run-level (no package token) and must trigger the verify re-run.
func TestUpgradeAutoFixTransientNetworkRetries(t *testing.T) {
	fake := &runner.Fake{
		StderrOnce:   map[string]string{"brew upgrade -- foo": "curl: (56) Recv failure: Connection reset by peer\nError: foo: Failed to download resource\n"},
		ExitCodeOnce: map[string]int{"brew upgrade -- foo": 1},
	}
	b := &Brew{R: fake}
	if _, err := b.Upgrade(context.Background(), []string{"foo"}, nil); err != nil {
		t.Fatalf("transient retry should succeed: %v", err)
	}
	if n := len(fake.Calls); n != 2 {
		t.Fatalf("want original + verify retry, got %d calls: %v", n, callKeys(fake))
	}
}

// TestUpgradeBulkColdIndexVerifyRuns: a no-arg bulk upgrade on the brew path
// has empty names; after a fix runs, the bare re-run IS the verify — skipping
// it would report false success.
func TestUpgradeBulkColdIndexVerifyRuns(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	installWithStubbedPrefix(t, t.TempDir())
	fake := &runner.Fake{
		StderrOnce:   map[string]string{"brew upgrade": "Error: foo: SHA256 mismatch\n"},
		ExitCodeOnce: map[string]int{"brew upgrade": 1},
	}
	b := &Brew{R: fake}
	if _, err := b.Upgrade(context.Background(), nil, nil); err != nil {
		t.Fatalf("bulk remediation should succeed: %v", err)
	}
	keys := callKeys(fake)
	want := []string{"brew upgrade", "brew cleanup -s -- foo", "brew upgrade"}
	if strings.Join(keys, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(keys, "\n"), strings.Join(want, "\n"))
	}
}
