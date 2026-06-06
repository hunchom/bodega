package brew

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/runner"
)

// infoCacheTTL bounds the freshness of cached `brew info --json=v2` payloads.
// Five minutes is short enough that a user who just ran `brew update` will
// see the new data on their next interactive lookup, but long enough that
// repeated info calls within a single session (e.g. batch scripts) hit the
// disk cache instead of paying the 200-600ms Ruby reload each time.
const infoCacheTTL = 5 * time.Minute

type Brew struct {
	R runner.Runner
}

func New(r runner.Runner) *Brew { return &Brew{R: r} }

// brewPrefix returns the Homebrew install prefix, cached for the lifetime of
// the process. We try /opt/homebrew (Apple Silicon) first; that's a single
// os.Stat instead of exec("brew --prefix") on the hot path. If neither of
// the two standard paths exist we return "" and callers fall back to their
// previous `brew` subprocess behaviour. Tests can set disablePrefixCache to
// force the subprocess path.
var (
	brewPrefixOnce     sync.Once
	brewPrefixCache    string
	disablePrefixCache bool // tests only — bypasses Cellar fast path
)

// sharedAPICache is a process-wide APICache. Because the formula/cask maps
// are tens of megabytes we want exactly one copy per run; every Brew
// instance shares it. apiCacheDisabled is a test hook that forces callers
// past the API cache and into the subprocess path.
var (
	sharedAPICacheOnce sync.Once
	sharedAPICache     *APICache
	apiCacheDisabled   bool // tests only — bypasses API cache fast path
)

func apiCache() *APICache {
	if apiCacheDisabled {
		return nil
	}
	sharedAPICacheOnce.Do(func() { sharedAPICache = NewAPICache() })
	return sharedAPICache
}

func brewPrefix() string {
	if disablePrefixCache {
		return ""
	}
	brewPrefixOnce.Do(func() {
		for _, p := range []string{"/opt/homebrew", "/usr/local"} {
			if st, err := os.Stat(p + "/Cellar"); err == nil && st.IsDir() {
				brewPrefixCache = p
				return
			}
		}
	})
	return brewPrefixCache
}

func (b *Brew) Name() string { return "brew" }

// PartialError reports that a batch mutation partially succeeded: Succeeded
// names the packages that actually changed on disk before Err stopped the rest.
// Callers journal Succeeded so `yum history undo` can still reverse real
// changes instead of recording an all-failed transaction. errors.As/Is unwrap
// to the underlying failure.
type PartialError struct {
	Succeeded []string
	Err       error
}

func (e *PartialError) Error() string { return e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

// SearchNative walks brew's JWS cache in-process and returns matches by
// substring. It's a strict subset of what `brew search` does (no fuzzy
// matching, no descriptions-only flag), but on the hot path it replaces a
// 500ms subprocess with a 50-150ms map scan. Returns (nil, err) when the
// API cache is unavailable so the caller can fall back to Search.
func (b *Brew) SearchNative(ctx context.Context, q string) ([]backend.Package, error) {
	if st := readyIndex(ctx); st != nil {
		ms, err := st.Search(q, 200)
		if err == nil {
			out := make([]backend.Package, 0, len(ms))
			for _, m := range ms {
				src := backend.SrcFormula
				if m.Source == "cask" {
					src = backend.SrcCask
				}
				out = append(out, backend.Package{Name: m.Name, Source: src, Desc: m.Desc})
			}
			return out, nil
		}
	}
	if ac := apiCache(); ac != nil {
		return ac.SearchNames(q)
	}
	return nil, fmt.Errorf("no search index available")
}

func (b *Brew) Search(ctx context.Context, q string) ([]backend.Package, error) {
	out, err := b.R.Run(ctx, "brew", "search", q)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("search", q, out)
	}
	var pkgs []backend.Package
	sc := bufio.NewScanner(strings.NewReader(string(out.Stdout)))
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		if name == "" || strings.HasPrefix(name, "=") {
			continue
		}
		src := backend.SrcFormula
		if strings.HasSuffix(name, " (cask)") {
			src = backend.SrcCask
			name = strings.TrimSuffix(name, " (cask)")
		}
		pkgs = append(pkgs, backend.Package{Name: name, Source: src})
	}
	return pkgs, nil
}

func (b *Brew) Info(ctx context.Context, name string) (*backend.Package, error) {
	// 1) Our own disk cache of prior `brew info --json=v2` responses. This is
	// the tightest hit path — sub-millisecond on warm disk — because the
	// cached payload already carries installed metadata.
	if data, ok := readCache(name, infoCacheTTL); ok {
		if p, err := parseInfoV2(data, name); err == nil {
			return p, nil
		}
		// Parse failed on cached data — treat it as a miss and re-fetch.
	}
	// 2) our native index (no brew). A single indexed SQLite query; overlay
	// the installed version from the Cellar so the user sees what they have.
	if st := readyIndex(ctx); st != nil {
		if f, err := st.Lookup(name); err == nil && f != nil {
			p := indexFormulaToPackage(f)
			overlayInstalled(p)
			return p, nil
		}
		if ck, err := st.LookupCask(name); err == nil && ck != nil {
			p := indexCaskToPackage(ck)
			overlayInstalled(p)
			return p, nil
		}
	}
	// 3) brew's own API cache, when present — fallback for a cold index.
	if ac := apiCache(); ac != nil {
		if p, err := ac.Lookup(name); err == nil && p != nil {
			overlayInstalled(p)
			return p, nil
		}
	}
	// 3) Last resort — ask brew itself.
	out, err := b.R.Run(ctx, "brew", "info", "--json=v2", name)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("info", name, out)
	}
	p, err := parseInfoV2(out.Stdout, name)
	if err != nil {
		return nil, err
	}
	_ = writeCache(name, out.Stdout)
	return p, nil
}

// overlayInstalled fills in p.Version / p.InstalledOn from the filesystem
// when the package is actually installed. For formulae we look at
// /<prefix>/Cellar/<name>/<ver>; for casks, /<prefix>/Caskroom/<name>/<ver>.
// If nothing is installed we leave the stable version in place so `yum info`
// still displays something useful.
func overlayInstalled(p *backend.Package) {
	if p == nil {
		return
	}
	prefix := brewPrefix()
	if prefix == "" {
		return
	}
	var dir string
	switch p.Source {
	case backend.SrcFormula:
		dir = prefix + "/Cellar/" + p.Name
	case backend.SrcCask:
		dir = prefix + "/Caskroom/" + p.Name
	default:
		return
	}
	st, err := os.Stat(dir)
	if err != nil {
		return
	}
	if v := latestVersionDir(dir); v != "" {
		p.Version = v
	}
	if mt := st.ModTime(); !mt.IsZero() {
		p.InstalledOn = &mt
	}
}

func (b *Brew) List(ctx context.Context, f backend.ListFilter) ([]backend.Package, error) {
	switch f {
	case "", backend.ListInstalled:
		if pkgs, ok := listCellarFormulae(); ok {
			return pkgs, nil
		}
		return b.parseListVersions(ctx, "--formula")
	case backend.ListCasks:
		if pkgs, ok := listCaskroom(); ok {
			return pkgs, nil
		}
		return b.parseListVersions(ctx, "--cask")
	case backend.ListOutdated:
		return b.Outdated(ctx)
	case backend.ListLeaves:
		if st := readyIndex(ctx); st != nil {
			return leavesNative(st), nil
		}
		out, err := b.R.Run(ctx, "brew", "leaves")
		if err != nil {
			return nil, err
		}
		if out.ExitCode != 0 {
			return nil, brewErr("leaves", "", out)
		}
		return linesToPkgs(out.Stdout, backend.SrcFormula), nil
	case backend.ListPinned:
		if pkgs, ok := listPinned(); ok {
			return pkgs, nil
		}
		out, err := b.R.Run(ctx, "brew", "list", "--pinned")
		if err != nil {
			return nil, err
		}
		if out.ExitCode != 0 {
			return nil, brewErr("list --pinned", "", out)
		}
		pkgs := linesToPkgs(out.Stdout, backend.SrcFormula)
		for i := range pkgs {
			pkgs[i].Pinned = true
		}
		return pkgs, nil
	case backend.ListAvailable:
		if st := readyIndex(ctx); st != nil {
			return availableFormulae(st)
		}
		out, err := b.R.Run(ctx, "brew", "formulae")
		if err != nil {
			return nil, err
		}
		if out.ExitCode != 0 {
			return nil, brewErr("formulae", "", out)
		}
		return linesToPkgs(out.Stdout, backend.SrcFormula), nil
	}
	return nil, fmt.Errorf("unknown list filter: %q", f)
}

// listCellarFormulae reads /<prefix>/Cellar/ directly. Each entry is a
// formula name; the newest non-dot subdir is its installed version.
// Returns (nil, false) if the prefix isn't discoverable or the Cellar
// dir can't be read, which punts back to the `brew list` subprocess.
func listCellarFormulae() ([]backend.Package, bool) {
	prefix := brewPrefix()
	if prefix == "" {
		return nil, false
	}
	entries, err := os.ReadDir(prefix + "/Cellar")
	if err != nil {
		return nil, false
	}
	pkgs := make([]backend.Package, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		pkgs = append(pkgs, backend.Package{
			Name:    e.Name(),
			Source:  backend.SrcFormula,
			Version: latestVersionDir(prefix + "/Cellar/" + e.Name()),
		})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	return pkgs, true
}

// sortPackagesByName sorts in place by Name.
func sortPackagesByName(pkgs []backend.Package) {
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
}

// providesNative scans installed Cellar bin dirs for an executable named cmd
// and returns the owning formula names. ok is false when the prefix isn't
// discoverable (caller falls back to brew).
func providesNative(cmd string) (owners []string, ok bool) {
	prefix := brewPrefix()
	if prefix == "" {
		return nil, false
	}
	cellar := prefix + "/Cellar"
	names, err := os.ReadDir(cellar)
	if err != nil {
		return nil, false
	}
	for _, n := range names {
		if !n.IsDir() || strings.HasPrefix(n.Name(), ".") {
			continue
		}
		vers, err := os.ReadDir(cellar + "/" + n.Name())
		if err != nil {
			continue
		}
		for _, v := range vers {
			if !v.IsDir() {
				continue
			}
			if st, err := os.Stat(cellar + "/" + n.Name() + "/" + v.Name() + "/bin/" + cmd); err == nil && !st.IsDir() {
				owners = append(owners, n.Name())
				break
			}
		}
	}
	sort.Strings(owners)
	return owners, true
}

// cellarFormulaSet returns the set of installed formula names from the Cellar.
func cellarFormulaSet() map[string]bool {
	set := map[string]bool{}
	if pkgs, ok := listCellarFormulae(); ok {
		for _, p := range pkgs {
			set[p.Name] = true
		}
	}
	return set
}

// listCaskroom does the same thing as listCellarFormulae for casks.
// /<prefix>/Caskroom/<token>/<version>.
func listCaskroom() ([]backend.Package, bool) {
	prefix := brewPrefix()
	if prefix == "" {
		return nil, false
	}
	entries, err := os.ReadDir(prefix + "/Caskroom")
	if err != nil {
		return nil, false
	}
	pkgs := make([]backend.Package, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		pkgs = append(pkgs, backend.Package{
			Name:    e.Name(),
			Source:  backend.SrcCask,
			Version: latestVersionDir(prefix + "/Caskroom/" + e.Name()),
		})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	return pkgs, true
}

// listPinned reads /<prefix>/var/homebrew/pinned/, a directory of symlinks
// named after pinned formulae.
func listPinned() ([]backend.Package, bool) {
	prefix := brewPrefix()
	if prefix == "" {
		return nil, false
	}
	entries, err := os.ReadDir(prefix + "/var/homebrew/pinned")
	if err != nil {
		if os.IsNotExist(err) {
			// Nothing pinned yet — that's a success, just empty.
			return []backend.Package{}, true
		}
		return nil, false
	}
	pkgs := make([]backend.Package, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		pkgs = append(pkgs, backend.Package{
			Name:   e.Name(),
			Source: backend.SrcFormula,
			Pinned: true,
		})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	return pkgs, true
}

// latestVersionDir picks the lexicographically largest non-dot entry under a
// formula/cask install dir. `brew list --versions` joins every installed
// version with a space; we approximate that by picking one, which matches
// the common case of a single installed version.
func latestVersionDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range entries {
		n := e.Name()
		if n == "" || strings.HasPrefix(n, ".") {
			continue
		}
		if n > best {
			best = n
		}
	}
	return best
}

func (b *Brew) parseListVersions(ctx context.Context, flag string) ([]backend.Package, error) {
	out, err := b.R.Run(ctx, "brew", "list", flag, "--versions")
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("list "+flag, "", out)
	}
	var pkgs []backend.Package
	sc := bufio.NewScanner(strings.NewReader(string(out.Stdout)))
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) == 0 {
			continue
		}
		p := backend.Package{Name: parts[0], Source: backend.SrcFormula}
		if flag == "--cask" {
			p.Source = backend.SrcCask
		}
		if len(parts) > 1 {
			p.Version = parts[len(parts)-1]
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

func (b *Brew) Outdated(ctx context.Context) ([]backend.Package, error) {
	st := readyIndex(ctx)
	if st == nil {
		return b.outdatedBrew(ctx)
	}
	var out []backend.Package
	// Formulae: installed Cellar version vs the index's current full version
	// (stable plus revision). outdated callers refresh first, so a differing
	// version is genuinely behind.
	if installed, ok := listCellarFormulae(); ok {
		for _, p := range installed {
			f, err := st.Lookup(p.Name)
			if err != nil || f == nil {
				continue
			}
			full := f.StableVersion
			if f.Revision > 0 {
				full = f.StableVersion + "_" + strconv.Itoa(f.Revision)
			}
			if full != "" && p.Version != full {
				out = append(out, backend.Package{Name: p.Name, Source: backend.SrcFormula, Version: p.Version, Latest: full})
			}
		}
	}
	// Casks: skip `version :latest` tokens (brew only upgrades those with
	// --greedy; their version string is always "latest").
	if casks, ok := listCaskroom(); ok {
		for _, p := range casks {
			ck, err := st.LookupCask(p.Name)
			if err != nil || ck == nil || ck.Version == "" || ck.Version == "latest" {
				continue
			}
			if p.Version != ck.Version {
				out = append(out, backend.Package{Name: p.Name, Source: backend.SrcCask, Version: p.Version, Latest: ck.Version})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (b *Brew) outdatedBrew(ctx context.Context) ([]backend.Package, error) {
	out, err := b.R.Run(ctx, "brew", "outdated", "--json=v2")
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("outdated", "", out)
	}
	return parseOutdatedV2(out.Stdout)
}

func (b *Brew) Deps(ctx context.Context, name string) (*backend.DepTree, error) {
	st := readyIndex(ctx)
	if st == nil {
		return b.depsBrew(ctx, name)
	}
	// DAG view: each transitive runtime dep expands once; a global visited set
	// also guards against dependency cycles.
	visited := map[string]bool{}
	var build func(n string) *backend.DepTree
	build = func(n string) *backend.DepTree {
		node := &backend.DepTree{Name: n}
		if visited[n] {
			return node
		}
		visited[n] = true
		deps, err := st.Deps(n)
		if err != nil {
			return node
		}
		for _, d := range deps {
			node.Children = append(node.Children, build(d))
		}
		return node
	}
	return build(name), nil
}

func (b *Brew) depsBrew(ctx context.Context, name string) (*backend.DepTree, error) {
	out, err := b.R.Run(ctx, "brew", "deps", "--tree", name)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("deps", name, out)
	}
	return parseDepsTree(out.Stdout, name), nil
}

func (b *Brew) ReverseDeps(ctx context.Context, name string) ([]string, error) {
	if st := readyIndex(ctx); st != nil {
		consumers, err := st.ReverseDeps(name)
		if err != nil {
			return nil, err
		}
		// `uses --installed`: keep only consumers actually in the Cellar.
		installed := cellarFormulaSet()
		var out []string
		for _, c := range consumers {
			if installed[c] {
				out = append(out, c)
			}
		}
		return out, nil
	}
	out, err := b.R.Run(ctx, "brew", "uses", "--installed", name)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("uses", name, out)
	}
	var names []string
	for l := range strings.SplitSeq(string(out.Stdout), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			names = append(names, l)
		}
	}
	return names, nil
}

// Provides reports which installed formulae ship an executable named cmd, by
// scanning <prefix>/Cellar/<name>/<ver>/bin directly. The command→formula map
// for *uninstalled* formulae isn't in the JSON API, so this is installed-only
// (the `brew which-formula` subprocess remains the fallback when there's no
// discoverable prefix).
func (b *Brew) Provides(ctx context.Context, cmd string) ([]string, error) {
	if owners, ok := providesNative(cmd); ok {
		return owners, nil
	}
	out, err := b.R.Run(ctx, "brew", "which-formula", cmd)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("which-formula", cmd, out)
	}
	return strings.Fields(string(out.Stdout)), nil
}

// Taps lists installed taps by reading <prefix>/Library/Taps/<user>/homebrew-<repo>
// directly — no `brew tap`. Falls back to the subprocess only when the prefix
// isn't discoverable.
func (b *Brew) Taps(ctx context.Context) ([]string, error) {
	if taps, ok := listTaps(); ok {
		return taps, nil
	}
	out, err := b.R.Run(ctx, "brew", "tap")
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("tap", "", out)
	}
	var taps []string
	for l := range strings.SplitSeq(string(out.Stdout), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			taps = append(taps, l)
		}
	}
	return taps, nil
}

// listTaps reads the Taps tree. Each <user>/homebrew-<repo> dir is the tap
// "<user>/<repo>". Returns (nil,false) when the prefix or Taps dir is absent.
func listTaps() ([]string, bool) {
	prefix := brewPrefix()
	if prefix == "" {
		return nil, false
	}
	root := prefix + "/Library/Taps"
	users, err := os.ReadDir(root)
	if err != nil {
		return nil, false
	}
	var taps []string
	for _, u := range users {
		if !u.IsDir() || strings.HasPrefix(u.Name(), ".") {
			continue
		}
		repos, err := os.ReadDir(root + "/" + u.Name())
		if err != nil {
			continue
		}
		for _, r := range repos {
			if !r.IsDir() || !strings.HasPrefix(r.Name(), "homebrew-") {
				continue
			}
			taps = append(taps, u.Name()+"/"+strings.TrimPrefix(r.Name(), "homebrew-"))
		}
	}
	sort.Strings(taps)
	return taps, true
}

func (b *Brew) Pin(ctx context.Context, name string, pin bool) error {
	cmd := "pin"
	if !pin {
		cmd = "unpin"
	}
	out, err := b.R.Run(ctx, "brew", cmd, name)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return brewErr(cmd, name, out)
	}
	invalidateCache([]string{name})
	return nil
}

func (b *Brew) Cleanup(ctx context.Context, deep bool) error {
	args := []string{"cleanup"}
	if deep {
		args = append(args, "--prune=all")
	}
	out, err := b.R.Run(ctx, "brew", args...)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return brewErr("cleanup", "", out)
	}
	clearCache()
	return nil
}

func (b *Brew) Doctor(ctx context.Context) ([]string, error) {
	// brew doctor exits non-zero when it finds problems — that's expected, so we
	// ignore ExitCode. But a Run-level error (brew not on PATH) is real: surface
	// it instead of returning zero warnings, which would read as "healthy".
	out, err := b.R.Run(ctx, "brew", "doctor")
	if err != nil {
		return nil, fmt.Errorf("brew doctor: %w", err)
	}
	var warns []string
	// Findings land on stdout and/or stderr depending on brew version; scan both.
	combined := string(out.Stdout) + "\n" + string(out.Stderr)
	sc := bufio.NewScanner(strings.NewReader(combined))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "Warning:") || strings.HasPrefix(l, "Error:") {
			warns = append(warns, l)
		}
	}
	return warns, nil
}

func linesToPkgs(b []byte, src backend.Source) []backend.Package {
	var pkgs []backend.Package
	for l := range strings.SplitSeq(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		pkgs = append(pkgs, backend.Package{Name: l, Source: src})
	}
	return pkgs
}

func (b *Brew) stream(ctx context.Context, w backend.ProgressWriter, args ...string) error {
	var sink io.Writer = io.Discard
	if w != nil {
		sink = w
	}
	// Tee stdout+stderr into capped tail buffers so a non-zero exit can report
	// brew's real error line — not a bald "exit N". sink still gets live output.
	var outTail, errTail tailWriter
	r, err := b.R.Stream(ctx,
		io.MultiWriter(sink, &outTail),
		io.MultiWriter(sink, &errTail),
		"brew", args...)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		if msg := lastBrewMessage(errTail.Bytes(), outTail.Bytes()); msg != "" {
			return fmt.Errorf("brew %s: %s", args[0], msg)
		}
		return fmt.Errorf("brew %s: exit %d", args[0], r.ExitCode)
	}
	return nil
}

func (b *Brew) Install(ctx context.Context, names []string, w backend.ProgressWriter) error {
	// Try native bottle install first. It's ~5-10× faster than `brew install`
	// because we skip Ruby startup, fetch deps in parallel, and avoid
	// reparsing the formula DB. ErrNativeUnsupported means the host can't
	// run the native path (no Homebrew prefix, cold API cache) — fall back
	// to the subprocess silently. Any other error is a real failure.
	res, err := b.InstallNative(ctx, names, InstallOpts{
		Progress: func(ev InstallEvent) {
			if w == nil || ev.Message == "" {
				return
			}
			fmt.Fprintln(w, ev.Message)
		},
	})
	if err == nil {
		invalidateCache(names)
		return nil
	}
	if !errors.Is(err, ErrNativeUnsupported) {
		// Native path ran and may have installed some roots before failing.
		// Surface them so the caller journals real on-disk changes.
		if done := installedRoots(res); len(done) > 0 {
			invalidateCache(done)
			return &PartialError{Succeeded: done, Err: err}
		}
		return err
	}
	if err := b.stream(ctx, w, append([]string{"install"}, names...)...); err != nil {
		return err
	}
	invalidateCache(names)
	return nil
}

// installedRoots returns the user-requested (root) formula names that an
// InstallNative result actually installed. Nil-safe.
func installedRoots(res *InstallResult) []string {
	if res == nil {
		return nil
	}
	var done []string
	for _, p := range res.Installed {
		if p.IsRoot {
			done = append(done, p.Name)
		}
	}
	return done
}
func (b *Brew) Remove(ctx context.Context, names []string, w backend.ProgressWriter) error {
	// Native uninstall first — same fast-path pattern as Install. No
	// subprocess, no Ruby boot, 10-100ms vs brew's ~2s for a single
	// formula. ErrNativeUnsupported means "not a standard Homebrew
	// layout" — degrade to the subprocess silently. Any other error is
	// a real failure and must be surfaced.
	res, err := b.UninstallNative(ctx, names, UninstallOpts{
		Progress: func(ev UninstallEvent) {
			if w == nil || ev.Message == "" {
				return
			}
			fmt.Fprintln(w, ev.Message)
		},
	})
	if err == nil {
		invalidateCache(names)
		return nil
	}
	if !errors.Is(err, ErrNativeUnsupported) {
		// Native teardown ran and may have removed some packages before
		// failing on another. Surface them so the caller journals the real
		// removals instead of an all-failed transaction.
		if res != nil && len(res.Removed) > 0 {
			invalidateCache(res.Removed)
			return &PartialError{Succeeded: res.Removed, Err: err}
		}
		return err
	}
	if err := b.stream(ctx, w, append([]string{"uninstall"}, names...)...); err != nil {
		return err
	}
	invalidateCache(names)
	return nil
}
func (b *Brew) Reinstall(ctx context.Context, names []string, w backend.ProgressWriter) error {
	if err := b.stream(ctx, w, append([]string{"reinstall"}, names...)...); err != nil {
		return err
	}
	invalidateCache(names)
	return nil
}
func (b *Brew) Upgrade(ctx context.Context, names []string, w backend.ProgressWriter) error {
	if err := b.stream(ctx, w, append([]string{"upgrade"}, names...)...); err != nil {
		return err
	}
	invalidateCache(names)
	return nil
}
func (b *Brew) Autoremove(ctx context.Context, w backend.ProgressWriter) error {
	if err := b.stream(ctx, w, "autoremove"); err != nil {
		return err
	}
	clearCache()
	return nil
}

// Helper JSON types (narrow — ignore fields we don't use).
type infoV2 struct {
	Formulae []struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Desc     string `json:"desc"`
		License  string `json:"license"`
		Homepage string `json:"homepage"`
		Tap      string `json:"tap"`
		Versions struct {
			Stable string `json:"stable"`
		} `json:"versions"`
		Dependencies []string `json:"dependencies"`
		BuildDeps    []string `json:"build_dependencies"`
		Installed    []struct {
			Version string `json:"version"`
			Time    int64  `json:"time"`
		} `json:"installed"`
		Pinned bool `json:"pinned"`
	} `json:"formulae"`
	Casks []struct {
		Token    string   `json:"token"`
		Name     []string `json:"name"`
		Desc     string   `json:"desc"`
		Homepage string   `json:"homepage"`
		Version  string   `json:"version"`
		Tap      string   `json:"tap"`
	} `json:"casks"`
}

var _ = json.Unmarshal // keep the import visible in parse.go callers

// lastBrewMessage returns the last non-empty line of stderr (usually brew's
// "Error: ..." line), falling back to stdout. Empty when both are blank.
// Shared by brewErr (Run path) and stream (Stream path) so streamed mutations
// surface the same real reason as buffered ones, not a bald "exit N".
func lastBrewMessage(stderr, stdout []byte) string {
	for _, src := range [][]byte{stderr, stdout} {
		msg := ""
		for l := range strings.SplitSeq(string(src), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				msg = l
			}
		}
		if msg != "" {
			return msg
		}
	}
	return ""
}

// tailWriter keeps only the last `cap` bytes written. Enough to recover brew's
// final error line without buffering an entire upgrade log in memory.
type tailWriter struct {
	buf []byte
}

const tailWriterCap = 8 << 10

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailWriterCap {
		t.buf = t.buf[len(t.buf)-tailWriterCap:]
	}
	return len(p), nil
}

func (t *tailWriter) Bytes() []byte { return t.buf }

// brewErr turns a non-zero brew invocation into a user-facing error. It prefers
// the last non-empty stderr line (which is usually the "Error: ..." message
// brew prints) and falls back to a canned message when stderr is empty.
func brewErr(sub, arg string, r *runner.Result) error {
	msg := lastBrewMessage(r.Stderr, r.Stdout)
	if arg != "" {
		if msg == "" {
			return fmt.Errorf("brew %s %s: exit %d", sub, arg, r.ExitCode)
		}
		return fmt.Errorf("brew %s %s: %s", sub, arg, msg)
	}
	if msg == "" {
		return fmt.Errorf("brew %s: exit %d", sub, r.ExitCode)
	}
	return fmt.Errorf("brew %s: %s", sub, msg)
}
