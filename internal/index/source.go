package index

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// FetchResult carries verified index payloads. Payloads are the raw JSON arrays
// already checked against the pinned key — an unverified payload never leaves a
// Source.
type FetchResult struct {
	FormulaPayload []byte
	CaskPayload    []byte
	FormulaETag    string
	CaskETag       string
	NotModified    bool // both files unchanged since prev ETags; no rebuild needed
}

// Source produces verified index payloads. prevFormulaETag/prevCaskETag enable a
// conditional fetch; an implementation that can't do conditional requests
// ignores them.
type Source interface {
	Fetch(ctx context.Context, prevFormulaETag, prevCaskETag string) (*FetchResult, error)
}

// DefaultFormulaURL / DefaultCaskURL are Homebrew's public API endpoints.
const (
	DefaultFormulaURL = "https://formulae.brew.sh/api/formula.jws.json"
	DefaultCaskURL    = "https://formulae.brew.sh/api/cask.jws.json"
)

// maxIndexBytes caps a single index payload read. Real formula/cask JWS files
// are tens of MB; the ceiling stops a hostile/compromised origin from
// exhausting memory before the signature is even checked.
const maxIndexBytes = 256 << 20

// NetworkSource fetches + verifies the index straight from formulae.brew.sh.
// This is what makes bodega independent of a local brew install.
type NetworkSource struct {
	FormulaURL string
	CaskURL    string
	Client     *http.Client
}

// NewNetworkSource builds a NetworkSource with sane defaults.
func NewNetworkSource() *NetworkSource {
	return &NetworkSource{
		FormulaURL: DefaultFormulaURL,
		CaskURL:    DefaultCaskURL,
		Client:     &http.Client{Timeout: 60 * time.Second},
	}
}

func (n *NetworkSource) Fetch(ctx context.Context, prevF, prevC string) (*FetchResult, error) {
	fBody, fETag, fMod, err := n.get(ctx, n.FormulaURL, prevF)
	if err != nil {
		return nil, fmt.Errorf("fetch formula index: %w", err)
	}
	cBody, cETag, cMod, err := n.get(ctx, n.CaskURL, prevC)
	if err != nil {
		return nil, fmt.Errorf("fetch cask index: %w", err)
	}
	// Both unchanged → nothing to rebuild.
	if !fMod && !cMod {
		return &FetchResult{NotModified: true, FormulaETag: prevF, CaskETag: prevC}, nil
	}
	// A wholesale rebuild needs BOTH payloads. Re-fetch any that returned 304.
	if !fMod {
		if fBody, fETag, _, err = n.get(ctx, n.FormulaURL, ""); err != nil {
			return nil, fmt.Errorf("refetch formula index: %w", err)
		}
	}
	if !cMod {
		if cBody, cETag, _, err = n.get(ctx, n.CaskURL, ""); err != nil {
			return nil, fmt.Errorf("refetch cask index: %w", err)
		}
	}

	fPayload, err := VerifyHomebrew(fBody)
	if err != nil {
		return nil, fmt.Errorf("formula index: %w", err)
	}
	cPayload, err := VerifyHomebrew(cBody)
	if err != nil {
		return nil, fmt.Errorf("cask index: %w", err)
	}
	return &FetchResult{
		FormulaPayload: fPayload, CaskPayload: cPayload,
		FormulaETag: fETag, CaskETag: cETag,
	}, nil
}

// get returns (body, etag, modified, err). modified is false on a 304.
func (n *NetworkSource) get(ctx context.Context, url, prevETag string) ([]byte, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", false, err
	}
	if prevETag != "" {
		req.Header.Set("If-None-Match", prevETag)
	}
	resp, err := n.Client.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, prevETag, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes+1))
	if err != nil {
		return nil, "", false, err
	}
	if int64(len(body)) > maxIndexBytes {
		return nil, "", false, fmt.Errorf("index body exceeds %d bytes", maxIndexBytes)
	}
	return body, resp.Header.Get("ETag"), true, nil
}

// BrewCacheSource reads brew's local JWS cache (offline bootstrap on a host that
// already ran brew). No conditional fetch; verifies the on-disk payloads.
type BrewCacheSource struct {
	Dir string // defaults to <HOMEBREW_CACHE or ~/Library/Caches/Homebrew>/api
}

// NewBrewCacheSource locates brew's api cache without invoking brew.
func NewBrewCacheSource() *BrewCacheSource {
	base := os.Getenv("HOMEBREW_CACHE")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "Library", "Caches", "Homebrew")
	}
	return &BrewCacheSource{Dir: filepath.Join(base, "api")}
}

func (b *BrewCacheSource) Fetch(ctx context.Context, _, _ string) (*FetchResult, error) {
	fBody, err := os.ReadFile(filepath.Join(b.Dir, "formula.jws.json"))
	if err != nil {
		return nil, err
	}
	cBody, err := os.ReadFile(filepath.Join(b.Dir, "cask.jws.json"))
	if err != nil {
		return nil, err
	}
	fPayload, err := VerifyHomebrew(fBody)
	if err != nil {
		return nil, fmt.Errorf("brew-cache formula index: %w", err)
	}
	cPayload, err := VerifyHomebrew(cBody)
	if err != nil {
		return nil, fmt.Errorf("brew-cache cask index: %w", err)
	}
	return &FetchResult{FormulaPayload: fPayload, CaskPayload: cPayload}, nil
}

// ChainSource tries each source in order, returning the first that succeeds.
// Used as [NetworkSource, BrewCacheSource]: fetch from the network, fall back to
// brew's cache when offline.
type ChainSource struct {
	Sources []Source
}

func (c ChainSource) Fetch(ctx context.Context, prevF, prevC string) (*FetchResult, error) {
	var lastErr error
	for _, s := range c.Sources {
		res, err := s.Fetch(ctx, prevF, prevC)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no index source available")
	}
	return nil, lastErr
}
