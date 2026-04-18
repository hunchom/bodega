package brew

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// newFormula is a fixture helper that builds an *APIFormula with a single
// arm64_sequoia bottle, the most common shape in the tests below. Individual
// tests override the Bottle map when they need to exercise tag selection.
func newFormula(name string, deps []string, buildDeps []string) *APIFormula {
	f := &APIFormula{
		Name:              name,
		FullName:          name,
		Dependencies:      deps,
		BuildDependencies: buildDeps,
	}
	f.Versions.Stable = "1.0.0"
	f.Bottle.Stable.Files = map[string]APIBottleFile{
		"arm64_sequoia": {
			URL:    "https://ghcr.io/v2/homebrew/core/" + name + "/blobs/sha256:deadbeef",
			SHA256: "deadbeef",
		},
	}
	return f
}

// withTestHost pins the host info for the duration of the test. It also
// forces the sync.Once to re-run on the next call to BottleTagPreference by
// resetting the package-level once/cache. Restoring happens via t.Cleanup.
func withTestHost(t *testing.T, arch string, major int) {
	t.Helper()
	prev := testHostOverride
	testHostOverride = &hostInfo{arch: arch, major: major}
	// Reset detection so BottleTagPreference picks up the override.
	hostOnce = sync.Once{}
	hostPrefs = nil
	t.Cleanup(func() {
		testHostOverride = prev
		hostOnce = sync.Once{}
		hostPrefs = nil
	})
}

func resolveFixture(t *testing.T, formulae map[string]*APIFormula, roots []string) ([]Plan, error) {
	t.Helper()
	// Default to a predictable host so tag picking is deterministic across
	// machines running the test.
	withTestHost(t, "arm64", 15)
	cache := newAPICacheFromMaps(formulae, nil)
	return Resolve(context.Background(), cache, roots)
}

func TestResolveSingleRootNoDeps(t *testing.T) {
	formulae := map[string]*APIFormula{
		"a": newFormula("a", nil, nil),
	}
	plans, err := resolveFixture(t, formulae, []string{"a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("want 1 plan, got %d: %+v", len(plans), plans)
	}
	if plans[0].Name != "a" || !plans[0].IsRoot {
		t.Fatalf("plan[0] = %+v", plans[0])
	}
	if plans[0].Tag != "arm64_sequoia" {
		t.Fatalf("tag = %q, want arm64_sequoia", plans[0].Tag)
	}
}

func TestResolveChainLeavesFirst(t *testing.T) {
	formulae := map[string]*APIFormula{
		"a": newFormula("a", []string{"b"}, nil),
		"b": newFormula("b", []string{"c"}, nil),
		"c": newFormula("c", nil, nil),
	}
	plans, err := resolveFixture(t, formulae, []string{"a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names := planNames(plans)
	want := []string{"c", "b", "a"}
	if !equalSlices(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	// Only "a" should be a root.
	for _, p := range plans {
		if p.Name == "a" && !p.IsRoot {
			t.Fatalf("a should be root")
		}
		if p.Name != "a" && p.IsRoot {
			t.Fatalf("%s should not be root", p.Name)
		}
	}
}

func TestResolveDiamondDedupes(t *testing.T) {
	formulae := map[string]*APIFormula{
		"a": newFormula("a", []string{"b", "c"}, nil),
		"b": newFormula("b", []string{"d"}, nil),
		"c": newFormula("c", []string{"d"}, nil),
		"d": newFormula("d", nil, nil),
	}
	plans, err := resolveFixture(t, formulae, []string{"a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names := planNames(plans)
	if len(names) != 4 {
		t.Fatalf("want 4 unique, got %d: %v", len(names), names)
	}
	// Assert partial ordering: d precedes b and c; b and c precede a.
	pos := map[string]int{}
	for i, n := range names {
		pos[n] = i
	}
	if pos["d"] >= pos["b"] || pos["d"] >= pos["c"] {
		t.Fatalf("d must come before b and c: %v", names)
	}
	if pos["b"] >= pos["a"] || pos["c"] >= pos["a"] {
		t.Fatalf("b,c must come before a: %v", names)
	}
}

func TestResolveCycleDetected(t *testing.T) {
	formulae := map[string]*APIFormula{
		"a": newFormula("a", []string{"b"}, nil),
		"b": newFormula("b", []string{"a"}, nil),
	}
	_, err := resolveFixture(t, formulae, []string{"a"})
	if err == nil {
		t.Fatal("expected cycle error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cycle") {
		t.Fatalf("error missing 'cycle': %v", err)
	}
	if !strings.Contains(msg, "a") || !strings.Contains(msg, "b") {
		t.Fatalf("error should name both a and b: %v", err)
	}
}

func TestResolveUnknownFormula(t *testing.T) {
	formulae := map[string]*APIFormula{
		"a": newFormula("a", nil, nil),
	}
	_, err := resolveFixture(t, formulae, []string{"nosuch"})
	if !errors.Is(err, ErrFormulaNotFound) {
		t.Fatalf("want ErrFormulaNotFound, got %v", err)
	}
}

func TestResolveUnknownTransitiveDep(t *testing.T) {
	formulae := map[string]*APIFormula{
		"a": newFormula("a", []string{"phantom"}, nil),
	}
	_, err := resolveFixture(t, formulae, []string{"a"})
	if !errors.Is(err, ErrFormulaNotFound) {
		t.Fatalf("want ErrFormulaNotFound for transitive, got %v", err)
	}
}

func TestResolveTagFallback(t *testing.T) {
	// Host claims sequoia (15); formula only ships arm64_sonoma. Should
	// still resolve because sequoia's preference list falls back through
	// sonoma, ventura, monterey, big_sur, all.
	f := &APIFormula{Name: "a", FullName: "a"}
	f.Versions.Stable = "1.0"
	f.Bottle.Stable.Files = map[string]APIBottleFile{
		"arm64_sonoma": {
			URL:    "https://example.test/a.tar.gz",
			SHA256: "cafe",
		},
	}
	formulae := map[string]*APIFormula{"a": f}

	plans, err := resolveFixture(t, formulae, []string{"a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plans[0].Tag != "arm64_sonoma" {
		t.Fatalf("tag = %q, want arm64_sonoma", plans[0].Tag)
	}
}

func TestResolveTagAllOnly(t *testing.T) {
	f := &APIFormula{Name: "a", FullName: "a"}
	f.Versions.Stable = "1.0"
	f.Bottle.Stable.Files = map[string]APIBottleFile{
		"all": {URL: "https://example.test/a.tar.gz", SHA256: "beef"},
	}
	formulae := map[string]*APIFormula{"a": f}

	plans, err := resolveFixture(t, formulae, []string{"a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plans[0].Tag != "all" {
		t.Fatalf("tag = %q, want all", plans[0].Tag)
	}
}

func TestResolveNoBottle(t *testing.T) {
	f := &APIFormula{Name: "a", FullName: "a"}
	f.Versions.Stable = "1.0"
	// bottle.stable.files left empty
	formulae := map[string]*APIFormula{"a": f}

	_, err := resolveFixture(t, formulae, []string{"a"})
	if !errors.Is(err, ErrNoBottle) {
		t.Fatalf("want ErrNoBottle, got %v", err)
	}
}

func TestResolveIntelTag(t *testing.T) {
	f := &APIFormula{Name: "a", FullName: "a"}
	f.Versions.Stable = "1.0"
	f.Bottle.Stable.Files = map[string]APIBottleFile{
		"arm64_sonoma": {URL: "https://example.test/arm.tar.gz", SHA256: "aaaa"},
		"sonoma":       {URL: "https://example.test/intel.tar.gz", SHA256: "bbbb"},
	}
	formulae := map[string]*APIFormula{"a": f}

	// Force amd64 + sonoma.
	withTestHost(t, "amd64", 14)
	cache := newAPICacheFromMaps(formulae, nil)
	plans, err := Resolve(context.Background(), cache, []string{"a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plans[0].Tag != "sonoma" {
		t.Fatalf("tag = %q, want sonoma (not arm64_sonoma)", plans[0].Tag)
	}
	if plans[0].BottleURL != "https://example.test/intel.tar.gz" {
		t.Fatalf("url = %q, want intel bottle", plans[0].BottleURL)
	}
}

func TestResolveIgnoresBuildDeps(t *testing.T) {
	formulae := map[string]*APIFormula{
		"a": newFormula("a", []string{"x"}, []string{"y"}),
		"x": newFormula("x", nil, nil),
		// Deliberately omit y. If Resolve walked build_dependencies this
		// would blow up with ErrFormulaNotFound; we want it to be ignored.
	}
	plans, err := resolveFixture(t, formulae, []string{"a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names := planNames(plans)
	want := []string{"x", "a"}
	if !equalSlices(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	for _, n := range names {
		if n == "y" {
			t.Fatal("build dep y should not appear in plan")
		}
	}
}

func TestBottleTagPreferenceArm64Sequoia(t *testing.T) {
	withTestHost(t, "arm64", 15)
	prefs := BottleTagPreference()
	want := []string{
		"arm64_sequoia",
		"arm64_sonoma",
		"arm64_ventura",
		"arm64_monterey",
		"arm64_big_sur",
		"all",
	}
	if !equalSlices(prefs, want) {
		t.Fatalf("prefs = %v, want %v", prefs, want)
	}
}

func TestBottleTagPreferenceIntelSonoma(t *testing.T) {
	withTestHost(t, "amd64", 14)
	prefs := BottleTagPreference()
	want := []string{
		"sonoma",
		"ventura",
		"monterey",
		"big_sur",
		"all",
	}
	if !equalSlices(prefs, want) {
		t.Fatalf("prefs = %v, want %v", prefs, want)
	}
}

func TestBottleTagPreferenceIntelCatalina(t *testing.T) {
	withTestHost(t, "amd64", 10)
	prefs := BottleTagPreference()
	want := []string{"catalina", "all"}
	if !equalSlices(prefs, want) {
		t.Fatalf("prefs = %v, want %v", prefs, want)
	}
}

// planNames extracts the Name field from each Plan so tests can assert on
// ordering without caring about URLs/hashes.
func planNames(plans []Plan) []string {
	out := make([]string, len(plans))
	for i, p := range plans {
		out[i] = p.Name
	}
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
