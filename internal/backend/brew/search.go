package brew

import (
	"sort"
	"strings"

	"github.com/hunchom/bodega/internal/backend"
)

// SearchOpts tunes the behaviour of SearchRich. Zero value reproduces the
// default `yum search` UX: match name/desc/tap, no dep expansion, no cap.
type SearchOpts struct {
	// NameOnly restricts matching to formula/cask names. Disables desc, tap,
	// and dep expansion regardless of IncludeDeps.
	NameOnly bool
	// IncludeDeps adds formulae whose dependency list references any already
	// matched formula. Ignored when NameOnly is true.
	IncludeDeps bool
	// Limit caps the returned slice. Zero (or negative) means no cap.
	Limit int
}

// SearchMatchKind tags why a Result showed up. The command layer uses it to
// decide whether to prefix the row with a "★" (non-name match).
type SearchMatchKind int

const (
	MatchName SearchMatchKind = iota
	MatchDesc
	MatchTap
	MatchDep
)

func (k SearchMatchKind) String() string {
	switch k {
	case MatchName:
		return "name"
	case MatchDesc:
		return "desc"
	case MatchTap:
		return "tap"
	case MatchDep:
		return "dep"
	}
	return "unknown"
}

// Result is one ranked search hit. Rank is 0 for the best match (exact name)
// and increases monotonically down to 7 (dep match). Popularity is a
// placeholder for a future analytics-driven tiebreaker; the JWS cache doesn't
// carry install counts, so it's currently always zero and the alphabetical
// fallback runs instead. Keeping the field means callers that plumb in an
// analytics source later don't have to change the signature.
type Result struct {
	Pkg        backend.Package
	Rank       int
	MatchKind  SearchMatchKind
	Popularity int
}

// RichSearcher is an optional backend hook for rank-aware search. Brew
// implements it; other backends can adopt it incrementally. The cmd layer
// type-asserts on Registry.Primary() to access this and falls back to the
// flat NativeSearcher/Search path when the assertion fails.
type RichSearcher interface {
	SearchRich(q string, opts SearchOpts) ([]Result, error)
}

// Rank constants — keep these exported so tests and the cmd layer can assert
// against them without hard-coding magic numbers.
const (
	RankExact     = 0
	RankPrefix    = 1
	RankSubstring = 2
	// RankAlias is reserved for brew alias matches. We don't track aliases
	// yet, so no Result currently lands here — the constant exists so the
	// ranks in Desc/Tap/Dep don't shift when aliases land.
	RankAlias = 4
	RankDesc  = 5
	RankTap   = 6
	RankDep   = 7
)

// rankMatch scores name against q. Returns (rank, true) on hit, (0, false)
// on miss. Caller is expected to have already lowercased q; name is
// lowercased inside so callers don't have to duplicate that work when they
// already have both the original and lower-cased string handy.
func rankMatch(name, q string) (int, bool) {
	if name == "" || q == "" {
		return 0, false
	}
	n := strings.ToLower(name)
	switch {
	case n == q:
		return RankExact, true
	case strings.HasPrefix(n, q):
		return RankPrefix, true
	case strings.Contains(n, q):
		return RankSubstring, true
	}
	return 0, false
}

// SearchRich is the ranked counterpart to SearchNames. It returns one Result
// per matched entry, sorted best-first. See SearchOpts for the knobs and
// package docs for ranking rules.
//
// This function is cache-only: it walks the maps already loaded by
// LoadFormulae / LoadCasks and never shells out. If both caches fail to
// load we still return the error from formulae (casks are secondary).
func (c *APICache) SearchRich(q string, opts SearchOpts) ([]Result, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, nil
	}

	// byKey dedups across all match kinds, keeping the lowest rank seen.
	// Key is "source:name" so a formula and a cask with the same token
	// don't collide.
	byKey := make(map[string]*Result)

	record := func(pkg backend.Package, rank int, kind SearchMatchKind) {
		key := string(pkg.Source) + ":" + pkg.Name
		if cur, ok := byKey[key]; ok {
			if rank < cur.Rank {
				cur.Rank = rank
				cur.MatchKind = kind
			}
			return
		}
		byKey[key] = &Result{Pkg: pkg, Rank: rank, MatchKind: kind}
	}

	formulae, ferr := c.LoadFormulae()
	if ferr == nil {
		// nameHits tracks formulae matched by name (any rank) so we can seed
		// the dep expansion without re-scanning the map.
		nameHits := make(map[string]struct{})
		for _, f := range formulae {
			if f == nil {
				continue
			}
			matched := false
			if r, ok := rankMatch(f.Name, q); ok {
				record(*formulaToPackage(f), r, MatchName)
				nameHits[f.Name] = struct{}{}
				matched = true
			} else if f.FullName != "" && f.FullName != f.Name {
				if r, ok := rankMatch(f.FullName, q); ok {
					record(*formulaToPackage(f), r, MatchName)
					nameHits[f.Name] = struct{}{}
					matched = true
				}
			}
			if opts.NameOnly {
				continue
			}
			if !matched && f.Desc != "" && strings.Contains(strings.ToLower(f.Desc), q) {
				record(*formulaToPackage(f), RankDesc, MatchDesc)
				matched = true
			}
			if !matched && f.Tap != "" && strings.Contains(strings.ToLower(f.Tap), q) {
				record(*formulaToPackage(f), RankTap, MatchTap)
			}
		}

		if opts.IncludeDeps && !opts.NameOnly && len(nameHits) > 0 {
			for _, f := range formulae {
				if f == nil {
					continue
				}
				if _, self := nameHits[f.Name]; self {
					continue
				}
				for _, d := range f.Dependencies {
					if _, ok := nameHits[d]; ok {
						record(*formulaToPackage(f), RankDep, MatchDep)
						break
					}
				}
			}
		}
	}

	if casks, err := c.LoadCasks(); err == nil {
		for _, ck := range casks {
			if ck == nil {
				continue
			}
			matched := false
			if r, ok := rankMatch(ck.Token, q); ok {
				record(*caskToPackage(ck), r, MatchName)
				matched = true
			}
			if opts.NameOnly {
				continue
			}
			if !matched && ck.Desc != "" && strings.Contains(strings.ToLower(ck.Desc), q) {
				record(*caskToPackage(ck), RankDesc, MatchDesc)
				matched = true
			}
			if !matched && ck.Tap != "" && strings.Contains(strings.ToLower(ck.Tap), q) {
				record(*caskToPackage(ck), RankTap, MatchTap)
			}
		}
	} else if ferr != nil {
		// Both caches failed — surface the first failure.
		return nil, ferr
	}

	out := make([]Result, 0, len(byKey))
	for _, r := range byKey {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		// Popularity tiebreaker: brew's JWS payload doesn't carry install
		// counts, so Popularity is always 0 today; this branch is dormant
		// until we wire in analytics. Higher popularity wins.
		if out[i].Popularity != out[j].Popularity {
			return out[i].Popularity > out[j].Popularity
		}
		return out[i].Pkg.Name < out[j].Pkg.Name
	})

	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// SearchRich is Brew's RichSearcher implementation. Delegates to the shared
// APICache; when the cache is disabled (tests) we return a typed error so
// the command layer can fall back to the flat SearchNative path.
func (b *Brew) SearchRich(q string, opts SearchOpts) ([]Result, error) {
	ac := apiCache()
	if ac == nil {
		return nil, errAPICacheDisabled
	}
	return ac.SearchRich(q, opts)
}

// errAPICacheDisabled is returned when the API cache is turned off (tests)
// or can't locate ~/Library/Caches/Homebrew/api. The cmd layer uses it to
// decide whether to fall back to the flat search path.
var errAPICacheDisabled = &searchErr{msg: "brew api cache disabled"}

type searchErr struct{ msg string }

func (e *searchErr) Error() string { return e.msg }
