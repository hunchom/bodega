package brew

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hunchom/bodega/internal/backend"
)

// APIFormula mirrors the subset of brew's formula.jws.json payload fields we
// need. The upstream payload carries ~40 keys per formula; we only decode the
// ones that feed the yum info / search UX.
type APIFormula struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Tap      string `json:"tap"`
	Desc     string `json:"desc"`
	License  string `json:"license"`
	Homepage string `json:"homepage"`
	Versions struct {
		Stable string `json:"stable"`
	} `json:"versions"`
	Dependencies      []string  `json:"dependencies"`
	BuildDependencies []string  `json:"build_dependencies"`
	Bottle            APIBottle `json:"bottle"`
}

// APIBottle is the bottle section of a formula payload. The stable channel is
// the only one we care about for native installs; head-built bottles aren't
// published to the core tap's GHCR.
type APIBottle struct {
	Stable APIBottleChannel `json:"stable"`
}

// APIBottleChannel carries the rebuild counter plus the per-tag file map.
// root_url is irrelevant because each file entry is a fully-qualified URL.
type APIBottleChannel struct {
	Rebuild int                      `json:"rebuild"`
	RootURL string                   `json:"root_url"`
	Files   map[string]APIBottleFile `json:"files"`
}

// APIBottleFile is one per-tag bottle entry. The URL is absolute and the
// sha256 is the lowercase hex digest of the tarball payload.
type APIBottleFile struct {
	Cellar string `json:"cellar"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// APICask mirrors the subset of brew's cask.jws.json payload fields we need.
type APICask struct {
	Token    string   `json:"token"`
	Name     []string `json:"name"`
	Desc     string   `json:"desc"`
	Homepage string   `json:"homepage"`
	Version  string   `json:"version"`
	Tap      string   `json:"tap"`
}

// APICache reads brew's local formula/cask JWS cache. Instances are
// goroutine-safe; the first call to LoadFormulae / LoadCasks populates an
// in-memory map keyed by name, subsequent calls check the on-disk file's
// mtime and re-decode only if it has changed. A miss (missing file,
// malformed payload) returns an error and leaves the cache empty so the
// caller can fall back to a subprocess.
type APICache struct {
	root string

	muF       sync.Mutex
	formulae  map[string]*APIFormula
	formulaMt time.Time

	muC    sync.Mutex
	casks  map[string]*APICask
	caskMt time.Time
}

// NewAPICache constructs an APICache rooted at ~/Library/Caches/Homebrew/api.
// If HOME isn't discoverable the returned cache will fail every Load; callers
// should treat that as a soft miss.
func NewAPICache() *APICache {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return &APICache{}
	}
	return &APICache{root: filepath.Join(home, "Library", "Caches", "Homebrew", "api")}
}

// newAPICacheFromMaps builds an APICache pre-populated with the given
// formula/cask maps, bypassing all disk I/O. It's intended for tests that
// want to exercise Lookup / Resolve against synthetic fixtures without
// having to spin up a temp JWS file. The returned cache's root is empty so
// any reload attempt will fail; both maps are treated as fresh on first
// access because their mtime matches the zero time.
func newAPICacheFromMaps(formulae map[string]*APIFormula, casks map[string]*APICask) *APICache {
	c := &APICache{}
	if formulae == nil {
		formulae = map[string]*APIFormula{}
	}
	if casks == nil {
		casks = map[string]*APICask{}
	}
	c.formulae = formulae
	c.casks = casks
	return c
}

// jwsEnvelope is the outer shape of a *.jws.json file. We only care about the
// payload; the protected header and signature are irrelevant for local reads.
type jwsEnvelope struct {
	Payload string `json:"payload"`
}

// unwrapJWS peels the {"payload":"..."} envelope and returns the inner JSON
// bytes. Brew currently ships the payload as a JSON-string-escaped JSON array
// (after the outer Unmarshal you already have valid JSON). Older versions
// base64-encoded the payload; if the string doesn't look like JSON we fall
// back to base64 decoding. Either path yields raw JSON bytes.
func unwrapJWS(raw []byte) ([]byte, error) {
	var env jwsEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("jws envelope: %w", err)
	}
	s := strings.TrimLeft(env.Payload, " \t\r\n")
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") {
		return []byte(env.Payload), nil
	}
	dec, err := base64.StdEncoding.DecodeString(env.Payload)
	if err == nil {
		return dec, nil
	}
	if dec, err2 := base64.RawStdEncoding.DecodeString(env.Payload); err2 == nil {
		return dec, nil
	}
	return nil, fmt.Errorf("payload is neither raw JSON nor base64")
}

// LoadFormulae returns a name-keyed map of every formula in brew's API cache.
// Memoized: repeat calls reuse the in-memory map until formula.jws.json's
// mtime changes. An empty root, missing file, or malformed payload all
// return a non-nil error and leave the cached map nil so subsequent calls
// re-attempt rather than stick on a stale failure.
func (c *APICache) LoadFormulae() (map[string]*APIFormula, error) {
	if c == nil {
		return nil, fmt.Errorf("brew api cache unavailable")
	}
	// Fast path: a test or caller may have pre-populated the map via
	// newAPICacheFromMaps. In that case we honour it and skip the disk read
	// entirely — there's no backing file to stat.
	if c.root == "" {
		c.muF.Lock()
		m := c.formulae
		c.muF.Unlock()
		if m != nil {
			return m, nil
		}
		return nil, fmt.Errorf("brew api cache unavailable")
	}
	path := filepath.Join(c.root, "formula.jws.json")

	c.muF.Lock()
	defer c.muF.Unlock()

	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if c.formulae != nil && st.ModTime().Equal(c.formulaMt) {
		return c.formulae, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	payload, err := unwrapJWS(raw)
	if err != nil {
		return nil, err
	}
	var arr []*APIFormula
	if err := json.Unmarshal(payload, &arr); err != nil {
		return nil, fmt.Errorf("formula payload: %w", err)
	}
	m := make(map[string]*APIFormula, len(arr))
	for _, f := range arr {
		if f == nil {
			continue
		}
		if f.Name != "" {
			m[f.Name] = f
		}
		if f.FullName != "" && f.FullName != f.Name {
			m[f.FullName] = f
		}
	}
	c.formulae = m
	c.formulaMt = st.ModTime()
	return m, nil
}

// LoadCasks is LoadFormulae's twin for casks. Keyed by token (e.g. "firefox").
func (c *APICache) LoadCasks() (map[string]*APICask, error) {
	if c == nil || c.root == "" {
		return nil, fmt.Errorf("brew api cache unavailable")
	}
	path := filepath.Join(c.root, "cask.jws.json")

	c.muC.Lock()
	defer c.muC.Unlock()

	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if c.casks != nil && st.ModTime().Equal(c.caskMt) {
		return c.casks, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	payload, err := unwrapJWS(raw)
	if err != nil {
		return nil, err
	}
	var arr []*APICask
	if err := json.Unmarshal(payload, &arr); err != nil {
		return nil, fmt.Errorf("cask payload: %w", err)
	}
	m := make(map[string]*APICask, len(arr))
	for _, cask := range arr {
		if cask == nil || cask.Token == "" {
			continue
		}
		m[cask.Token] = cask
	}
	c.casks = m
	c.caskMt = st.ModTime()
	return m, nil
}

// Lookup tries formulae first then casks, returning a backend.Package built
// from the API entry. Returns (nil, nil) for a clean miss (name not found
// in either map but the files loaded fine); returns an error only for
// envelope / I/O failures. That lets callers treat "not in API cache" as a
// soft miss and fall through to the brew subprocess.
func (c *APICache) Lookup(name string) (*backend.Package, error) {
	formulae, ferr := c.LoadFormulae()
	if ferr == nil {
		if f, ok := formulae[name]; ok {
			return formulaToPackage(f), nil
		}
	}
	casks, cerr := c.LoadCasks()
	if cerr == nil {
		if ck, ok := casks[name]; ok {
			return caskToPackage(ck), nil
		}
	}
	if ferr != nil && cerr != nil {
		return nil, ferr
	}
	return nil, nil
}

// SearchNames scans the in-memory maps for entries whose name or description
// contains q (case-insensitive). Results are deduped by source+name, sorted
// by (source, name), and capped to a modest size so interactive UIs don't
// drown in matches.
func (c *APICache) SearchNames(q string) ([]backend.Package, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, nil
	}
	var out []backend.Package
	seen := make(map[string]struct{})

	if formulae, err := c.LoadFormulae(); err == nil {
		for _, f := range formulae {
			if !strings.Contains(strings.ToLower(f.Name), q) &&
				!strings.Contains(strings.ToLower(f.FullName), q) {
				continue
			}
			key := "formula:" + f.Name
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, backend.Package{
				Name:   f.Name,
				Source: backend.SrcFormula,
				Desc:   f.Desc,
				Tap:    f.Tap,
			})
		}
	}
	if casks, err := c.LoadCasks(); err == nil {
		for _, ck := range casks {
			if !strings.Contains(strings.ToLower(ck.Token), q) {
				continue
			}
			key := "cask:" + ck.Token
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, backend.Package{
				Name:   ck.Token,
				Source: backend.SrcCask,
				Desc:   ck.Desc,
				Tap:    ck.Tap,
			})
		}
	}
	return out, nil
}

// formulaToPackage builds a backend.Package from an API formula entry. The
// stable version is used as the "latest" version; callers that care about
// the installed version should overlay a Cellar check on top.
func formulaToPackage(f *APIFormula) *backend.Package {
	return &backend.Package{
		Name:      f.Name,
		Source:    backend.SrcFormula,
		Desc:      f.Desc,
		License:   f.License,
		Homepage:  f.Homepage,
		Tap:       f.Tap,
		Version:   f.Versions.Stable,
		Deps:      f.Dependencies,
		BuildDeps: f.BuildDependencies,
	}
}

// caskToPackage builds a backend.Package from an API cask entry. The cask
// payload's name[] is usually a human-readable title; we fall back to it
// for Desc if the short desc field is empty.
func caskToPackage(c *APICask) *backend.Package {
	p := &backend.Package{
		Name:     c.Token,
		Source:   backend.SrcCask,
		Desc:     c.Desc,
		Homepage: c.Homepage,
		Version:  c.Version,
		Tap:      c.Tap,
	}
	if p.Desc == "" && len(c.Name) > 0 {
		p.Desc = c.Name[0]
	}
	return p
}
