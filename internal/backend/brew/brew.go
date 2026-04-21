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

// SearchNative walks brew's JWS cache in-process and returns matches by
// substring. It's a strict subset of what `brew search` does (no fuzzy
// matching, no descriptions-only flag), but on the hot path it replaces a
// 500ms subprocess with a 50-150ms map scan. Returns (nil, err) when the
// API cache is unavailable so the caller can fall back to Search.
func (b *Brew) SearchNative(ctx context.Context, q string) ([]backend.Package, error) {
	ac := apiCache()
	if ac == nil {
		return nil, fmt.Errorf("brew api cache disabled")
	}
	return ac.SearchNames(q)
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
	// 2) brew's own API cache. No subprocess, no network; one mmap-ish
	// read of ~/Library/Caches/Homebrew/api/*.jws.json and a map lookup.
	// We overlay installed version metadata by scanning the Cellar so the
	// user sees the version they actually have, not just the latest stable.
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

func (b *Brew) Provides(ctx context.Context, cmd string) ([]string, error) {
	out, err := b.R.Run(ctx, "brew", "which-formula", cmd)
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("which-formula", cmd, out)
	}
	return strings.Fields(string(out.Stdout)), nil
}

func (b *Brew) Taps(ctx context.Context) ([]string, error) {
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
	out, _ := b.R.Run(ctx, "brew", "doctor")
	var warns []string
	sc := bufio.NewScanner(strings.NewReader(string(out.Stdout)))
	for sc.Scan() {
		l := sc.Text()
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
	r, err := b.R.Stream(ctx, sink, sink, "brew", args...)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
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
	_, err := b.InstallNative(ctx, names, InstallOpts{
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
		return err
	}
	if err := b.stream(ctx, w, append([]string{"install"}, names...)...); err != nil {
		return err
	}
	invalidateCache(names)
	return nil
}
func (b *Brew) Remove(ctx context.Context, names []string, w backend.ProgressWriter) error {
	// Native uninstall first — same fast-path pattern as Install. No
	// subprocess, no Ruby boot, 10-100ms vs brew's ~2s for a single
	// formula. ErrNativeUnsupported means "not a standard Homebrew
	// layout" — degrade to the subprocess silently. Any other error is
	// a real failure and must be surfaced.
	_, err := b.UninstallNative(ctx, names, UninstallOpts{
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

// brewErr turns a non-zero brew invocation into a user-facing error. It prefers
// the last non-empty stderr line (which is usually the "Error: ..." message
// brew prints) and falls back to a canned message when stderr is empty.
func brewErr(sub, arg string, r *runner.Result) error {
	msg := ""
	for l := range strings.SplitSeq(string(r.Stderr), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			msg = l
		}
	}
	if msg == "" {
		for l := range strings.SplitSeq(string(r.Stdout), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				msg = l
			}
		}
	}
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
