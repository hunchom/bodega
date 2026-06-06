package brew

import (
	"context"
	"fmt"
	"sync"

	"github.com/hunchom/bodega/internal/backend"
	"github.com/hunchom/bodega/internal/index"
)

// The native package index (internal/index) replaces our dependence on the
// `brew` binary for metadata refresh: `yum update` fetches + verifies Homebrew's
// signed JSON index ourselves and stores it in a fast local SQLite db, instead
// of shelling out to `brew update`.

var (
	sharedIndexOnce   sync.Once
	sharedIndexStore  *index.Store
	indexDisabled     bool         // tests only — forces the index unavailable
	testIndexOverride *index.Store // tests only — injects a fixture index
)

// sharedIndex lazily opens the process-wide native index. A nil return means
// the index couldn't be opened; callers degrade gracefully.
func sharedIndex() *index.Store {
	if testIndexOverride != nil {
		return testIndexOverride
	}
	if indexDisabled {
		return nil
	}
	sharedIndexOnce.Do(func() {
		st, err := index.Open(index.DefaultPath())
		if err == nil {
			sharedIndexStore = st
		}
	})
	return sharedIndexStore
}

// indexSource is the refresh source chain: fetch from formulae.brew.sh, falling
// back to brew's on-disk cache when offline (bootstrap on a host that has brew).
func indexSource() index.Source {
	return index.ChainSource{Sources: []index.Source{
		index.NewNetworkSource(),
		index.NewBrewCacheSource(),
	}}
}

// IndexStale reports whether the native index needs a refresh (empty, schema-
// stale, or older than the freshness window).
func (b *Brew) IndexStale() bool {
	st := sharedIndex()
	if st == nil {
		return false
	}
	return st.NeedsRefresh(index.DefaultMaxAge)
}

// indexFormulaSource adapts the native index Store to the resolver's
// FormulaSource interface.
type indexFormulaSource struct{ st *index.Store }

func (s indexFormulaSource) ResolveFormula(name string) (*ResolvedFormula, error) {
	f, err := s.st.Lookup(name)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, nil
	}
	bs, err := s.st.Bottles(name)
	if err != nil {
		return nil, err
	}
	bottles := make(map[string]BottleFile, len(bs))
	for _, b := range bs {
		bottles[b.Tag] = BottleFile{URL: b.URL, SHA256: b.SHA256}
	}
	return &ResolvedFormula{Name: f.Name, Version: f.StableVersion, Deps: f.Deps, Bottles: bottles}, nil
}

// readyIndex returns the native index Store if it holds data, building it once
// (network, or brew-cache bootstrap when offline) if it's cold. Returns nil when
// no index could be made ready — callers then fall back to brew's cache.
func readyIndex(ctx context.Context) *index.Store {
	st := sharedIndex()
	if st == nil {
		return nil
	}
	if st.FormulaCount() == 0 {
		_, _ = st.EnsureFresh(ctx, indexSource(), index.DefaultMaxAge)
	}
	if st.FormulaCount() > 0 {
		return st
	}
	return nil
}

// formulaSource picks the formula data source for resolution: the native index
// when populated, falling back to brew's API cache. Returns ErrNativeUnsupported
// when neither is available.
func (b *Brew) formulaSource(ctx context.Context) (FormulaSource, error) {
	if st := readyIndex(ctx); st != nil {
		return indexFormulaSource{st: st}, nil
	}
	if ac := apiCache(); ac != nil {
		if _, err := ac.LoadFormulae(); err == nil {
			return ac, nil
		}
	}
	return nil, ErrNativeUnsupported
}

// availableFormulae lists every formula name in the index as a Package.
func availableFormulae(st *index.Store) ([]backend.Package, error) {
	names, err := st.AllFormulaNames()
	if err != nil {
		return nil, err
	}
	out := make([]backend.Package, 0, len(names))
	for _, n := range names {
		out = append(out, backend.Package{Name: n, Source: backend.SrcFormula})
	}
	return out, nil
}

// leavesNative computes installed formulae that no other installed formula
// depends on at runtime — the native equivalent of `brew leaves`.
func leavesNative(st *index.Store) []backend.Package {
	installed := cellarFormulaSet()
	depended := map[string]bool{}
	for name := range installed {
		deps, err := st.Deps(name)
		if err != nil {
			continue
		}
		for _, d := range deps {
			if installed[d] {
				depended[d] = true
			}
		}
	}
	var out []backend.Package
	for name := range installed {
		if !depended[name] {
			out = append(out, backend.Package{Name: name, Source: backend.SrcFormula})
		}
	}
	sortPackagesByName(out)
	return out
}

// hasHostBottle reports whether the formula has a bottle for any of the host's
// preferred tags — i.e. it's installable via the native path on this machine.
func hasHostBottle(st *index.Store, name string) bool {
	bs, err := st.Bottles(name)
	if err != nil || len(bs) == 0 {
		return false
	}
	have := make(map[string]bool, len(bs))
	for _, b := range bs {
		have[b.Tag] = true
	}
	for _, tag := range BottleTagPreference() {
		if have[tag] {
			return true
		}
	}
	return false
}

// indexFormulaToPackage adapts an index Formula to a backend.Package.
func indexFormulaToPackage(f *index.Formula) *backend.Package {
	return &backend.Package{
		Name:      f.Name,
		Source:    backend.SrcFormula,
		Desc:      f.Desc,
		License:   f.License,
		Homepage:  f.Homepage,
		Tap:       f.Tap,
		Version:   f.StableVersion,
		Deps:      f.Deps,
		BuildDeps: f.BuildDeps,
	}
}

// indexCaskToPackage adapts an index Cask to a backend.Package.
func indexCaskToPackage(c *index.Cask) *backend.Package {
	p := &backend.Package{
		Name:     c.Token,
		Source:   backend.SrcCask,
		Desc:     c.Desc,
		Homepage: c.Homepage,
		Version:  c.Version,
		Tap:      c.Tap,
	}
	if p.Desc == "" && len(c.Names) > 0 {
		p.Desc = c.Names[0]
	}
	return p
}

// RefreshIndex refreshes the native package index. force=true always fetches
// (this is `yum update`); otherwise it refreshes only when stale. Returns
// whether a rebuild actually ran. Never invokes the `brew` binary.
func (b *Brew) RefreshIndex(ctx context.Context, force bool) (bool, error) {
	st := sharedIndex()
	if st == nil {
		return false, fmt.Errorf("native index unavailable")
	}
	if force {
		res, err := st.Refresh(ctx, indexSource())
		return res != nil && err == nil, err
	}
	return st.EnsureFresh(ctx, indexSource(), index.DefaultMaxAge)
}
