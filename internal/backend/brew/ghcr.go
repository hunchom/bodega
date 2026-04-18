package brew

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// userAgent is the User-Agent we send on every GHCR request. GHCR rejects
// bare "Go-http-client" on some endpoints, so we identify as bodega. The
// version is a build-time const — callers stamp it during release.
const userAgent = "bodega/dev (+https://github.com/hunchom/bodega)"

// GHCR bottle blob media types. Brew has historically alternated between
// the OCI and Docker layer types; GHCR serves whichever was pushed. We ask
// for both and let the server pick.
const (
	mediaTypeOCILayer    = "application/vnd.oci.image.layer.v1.tar+gzip"
	mediaTypeDockerLayer = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

// ghcrTokenEndpoint is the anonymous-read token endpoint. GHCR requires a
// bearer token even for public pulls; this returns a ~5 minute JWT that we
// cache per formula for the life of the process.
const ghcrTokenEndpoint = "https://ghcr.io/token"

// overallDownloadTimeout bounds a single DownloadBlob call end-to-end. Ten
// minutes is enough for a slow connection to pull a 300MB bottle (the
// largest Homebrew bottle in practice) while still preventing a wedged
// request from blocking a yum invocation forever.
const overallDownloadTimeout = 10 * time.Minute

// maxRetries is how many times we retry a 429/503 before giving up. We also
// honor Retry-After when the server provides one.
const maxRetries = 3

// Package-level sentinel errors. Callers use errors.Is to branch on them
// and fall back to `brew install` when we can't resolve a bottle ourselves.
var (
	// ErrGHCRNoToken is returned when GHCR's token endpoint says the
	// repository does not exist (404). That typically means the formula
	// name is wrong, the bottle was yanked, or we're looking at a tap
	// that isn't published to ghcr.io/homebrew/core.
	ErrGHCRNoToken = errors.New("ghcr: token endpoint returned 404")

	// ErrBottleNotFound is returned when the blob endpoint returns 404
	// even after a successful token fetch. That means the digest in the
	// formula JSON doesn't match anything on GHCR — usually a sign the
	// local API cache is stale.
	ErrBottleNotFound = errors.New("ghcr: bottle blob not found")
)

// tokenCache holds per-formula bearer tokens keyed by formula name. GHCR
// tokens are scope-specific (repository:homebrew/core/<formula>:pull), so
// we can't share one across formulae even though the token endpoint is
// the same. sync.Map is the right shape here: writes are rare (once per
// formula per process), reads are hot, and the value type never changes.
var tokenCache sync.Map // map[string]string

// tokenResponse is the shape of the JSON body returned by ghcr.io/token.
// GHCR also returns "access_token" as an alias for "token"; we decode both
// so a future server-side rename doesn't break us.
type tokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

// httpDoer is the minimal interface DownloadBlob needs from its transport.
// Tests can swap in a stubbed client; production uses http.DefaultClient.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// httpClient is the transport DownloadBlob uses. Indirecting through a
// package-level variable lets tests inject a client that points at an
// httptest.Server without forcing every test to rewrite URLs.
var httpClient httpDoer = http.DefaultClient

// tokenEndpoint is overridden by tests to point at a mock GHCR. Defaults to
// the real endpoint.
var tokenEndpoint = ghcrTokenEndpoint

// DownloadBlob fetches a bottle blob from GHCR to destPath.
//
// The URL format is https://ghcr.io/v2/homebrew/core/<formula>/blobs/sha256:<digest>.
// GHCR requires a bearer token even for public reads; we fetch an anonymous
// token from ghcr.io/token and cache it per-formula for this process.
// Writes are atomic (temp file + rename). Streams sha256 in-flight; returns
// an error without finalizing the file if the digest doesn't match.
// progress may be nil; if non-nil, called periodically with (downloaded, total).
func DownloadBlob(ctx context.Context, formula, bottleURL, sha256Digest, destPath string, progress func(downloaded, total int64)) error {
	if formula == "" {
		return fmt.Errorf("ghcr: formula name required")
	}
	if bottleURL == "" {
		return fmt.Errorf("ghcr: bottle URL required")
	}
	if sha256Digest == "" {
		return fmt.Errorf("ghcr: sha256 digest required")
	}
	if destPath == "" {
		return fmt.Errorf("ghcr: destination path required")
	}

	// Bound the whole operation. Individual HTTP calls already respect
	// the caller's ctx, but without an outer cap a genuinely slow CDN
	// could tie up the caller for hours; 10 minutes is the cutoff.
	ctx, cancel := context.WithTimeout(ctx, overallDownloadTimeout)
	defer cancel()

	token, err := getToken(ctx, formula)
	if err != nil {
		return err
	}

	resp, err := fetchBlob(ctx, bottleURL, token)
	if err != nil {
		// 401 here means the cached token expired mid-flight; drop the
		// cached value and try exactly once more. That re-fetch has its
		// own retry budget built into getToken.
		if errors.Is(err, errUnauthorized) {
			tokenCache.Delete(formula)
			token, terr := getToken(ctx, formula)
			if terr != nil {
				return terr
			}
			resp, err = fetchBlob(ctx, bottleURL, token)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	defer resp.Body.Close()

	return streamToFile(ctx, resp, sha256Digest, destPath, formula, progress)
}

// getToken returns a cached or freshly-fetched bearer token for a formula.
// Tokens are scoped per formula (GHCR's OAuth "scope" query parameter), so
// we key the cache on formula name. A 404 from the token endpoint yields
// ErrGHCRNoToken so the caller can fall back to `brew install`.
func getToken(ctx context.Context, formula string) (string, error) {
	if v, ok := tokenCache.Load(formula); ok {
		if s, ok := v.(string); ok && s != "" {
			return s, nil
		}
	}
	tok, err := fetchToken(ctx, formula)
	if err != nil {
		return "", err
	}
	tokenCache.Store(formula, tok)
	return tok, nil
}

// fetchToken performs the unauthenticated GET against ghcr.io/token with the
// scope query set to the formula's repository. GHCR returns a JSON envelope
// with a "token" field that's a short-lived JWT.
func fetchToken(ctx context.Context, formula string) (string, error) {
	q := fmt.Sprintf("?service=ghcr.io&scope=repository:homebrew/core/%s:pull", formula)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenEndpoint+q, nil)
	if err != nil {
		return "", fmt.Errorf("ghcr: build token request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := doWithRetry(req)
	if err != nil {
		return "", fmt.Errorf("ghcr: token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrGHCRNoToken
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ghcr: token endpoint status %d", resp.StatusCode)
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("ghcr: decode token body: %w", err)
	}
	tok := tr.Token
	if tok == "" {
		tok = tr.AccessToken
	}
	if tok == "" {
		return "", fmt.Errorf("ghcr: token body missing token field")
	}
	return tok, nil
}

// errUnauthorized is returned by fetchBlob when GHCR replies with 401 so the
// caller can invalidate the cached token and retry exactly once.
var errUnauthorized = errors.New("ghcr: unauthorized")

// fetchBlob issues the authenticated GET against the bottle URL and returns
// the open response. The caller is responsible for closing Body. We follow
// redirects (GHCR 302s to a signed CDN URL); the stdlib client's default
// CheckRedirect preserves our Authorization header only to the same host,
// so we re-send it explicitly via a custom CheckRedirect.
func fetchBlob(ctx context.Context, bottleURL, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bottleURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ghcr: build blob request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+token)
	// Ask for both layer media types. GHCR negotiates and returns the
	// variant that was pushed; Homebrew has historically alternated.
	req.Header.Set("Accept", mediaTypeOCILayer+", "+mediaTypeDockerLayer)

	resp, err := doWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("ghcr: blob request: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, errUnauthorized
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrBottleNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("ghcr: blob status %d", resp.StatusCode)
	}
	return resp, nil
}

// doWithRetry sends a request honoring Retry-After on 429 and 503 responses.
// We retry at most maxRetries times with exponential backoff; other statuses
// (including 404, 401) return immediately so the caller can special-case them.
func doWithRetry(req *http.Request) (*http.Response, error) {
	var lastErr error
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt == maxRetries {
				return nil, err
			}
			if !sleepCtx(req.Context(), backoff) {
				return nil, req.Context().Err()
			}
			backoff *= 2
			continue
		}
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable {
			return resp, nil
		}
		// Drain and close so the connection can be reused, then honor
		// Retry-After if present; otherwise fall back to our backoff.
		wait := parseRetryAfter(resp.Header.Get("Retry-After"), backoff)
		resp.Body.Close()
		if attempt == maxRetries {
			return nil, fmt.Errorf("ghcr: retry budget exhausted at status %d", resp.StatusCode)
		}
		if !sleepCtx(req.Context(), wait) {
			return nil, req.Context().Err()
		}
		backoff *= 2
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("ghcr: retry loop exited without response")
}

// parseRetryAfter accepts both the integer-seconds and HTTP-date forms of
// the Retry-After header. Anything malformed falls back to the caller's
// default backoff so we never stall indefinitely on a bogus value.
func parseRetryAfter(h string, fallback time.Duration) time.Duration {
	if h == "" {
		return fallback
	}
	if n, err := strconv.Atoi(h); err == nil && n >= 0 {
		d := time.Duration(n) * time.Second
		// Cap at a minute — we don't want a server advising a 24h
		// wait to block a yum invocation. Caller's overall timeout
		// is the ultimate ceiling.
		if d > time.Minute {
			return time.Minute
		}
		return d
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d < 0 {
			return fallback
		}
		if d > time.Minute {
			return time.Minute
		}
		return d
	}
	return fallback
}

// sleepCtx is a context-aware time.Sleep. Returns false if the context
// expired before the sleep completed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// streamToFile copies resp.Body to a temp file in the same directory as
// destPath, hashing as it goes, invoking the progress callback at most
// every 100ms, and renaming atomically on success. On hash mismatch or
// any write error the temp file is removed.
func streamToFile(ctx context.Context, resp *http.Response, wantDigest, destPath, formula string, progress func(downloaded, total int64)) (retErr error) {
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("ghcr: mkdir %s: %w", destDir, err)
	}
	tmp, err := os.CreateTemp(destDir, filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("ghcr: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	// Guarantee cleanup on any error exit. On success we've already
	// renamed the file out from under this path, so Remove is a no-op.
	defer func() {
		if retErr != nil {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	total := resp.ContentLength // -1 when Content-Length missing
	h := sha256.New()

	var downloaded int64
	var lastReport time.Time
	reportEvery := 100 * time.Millisecond

	// progressWriter fans bytes into the hash, the temp file, and a
	// download counter that drives the progress callback. We avoid
	// io.MultiWriter here so we can throttle the progress call without
	// throttling the write.
	writer := &progressWriter{
		dst:  tmp,
		hash: h,
		onWrite: func(n int) {
			downloaded += int64(n)
			if progress == nil {
				return
			}
			now := time.Now()
			if now.Sub(lastReport) >= reportEvery {
				progress(downloaded, total)
				lastReport = now
			}
		},
	}

	// Ensure ctx cancellation interrupts a long copy. The net/http
	// Response body already respects ctx via the request, but a slow
	// CDN that trickles bytes won't trip that — we also poll ctx.Err
	// in progressWriter.Write.
	writer.ctx = ctx

	if _, err := io.Copy(writer, resp.Body); err != nil {
		return fmt.Errorf("ghcr: copy body: %w", err)
	}
	// Final progress tick so callers see a 100% event even if the
	// ticker never fired on a fast download.
	if progress != nil {
		progress(downloaded, total)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("ghcr: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("ghcr: close temp: %w", err)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantDigest {
		return fmt.Errorf("bottle sha256 mismatch for %s: got %s want %s", formula, got, wantDigest)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("ghcr: rename %s: %w", destPath, err)
	}
	return nil
}

// progressWriter tees writes into both the temp file and the hash, invokes
// the progress callback, and checks context cancellation on every chunk.
// io.MultiWriter would work for the first two, but we'd lose the chunk
// boundaries we need for throttling the progress callback.
type progressWriter struct {
	ctx     context.Context
	dst     io.Writer
	hash    io.Writer
	onWrite func(n int)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.dst.Write(p)
	if n > 0 {
		// Hash what actually landed on disk, not the full slice — if
		// dst.Write short-wrote we want the digest to match what we
		// persisted.
		_, _ = w.hash.Write(p[:n])
		w.onWrite(n)
	}
	return n, err
}
