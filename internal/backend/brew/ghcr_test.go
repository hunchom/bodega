package brew

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// resetGHCRState clears the per-formula token cache and restores the default
// http client / token endpoint between tests. Tests that mutate these globals
// must defer a call to this helper.
func resetGHCRState(t *testing.T) {
	t.Helper()
	tokenCache.Range(func(k, _ any) bool {
		tokenCache.Delete(k)
		return true
	})
	httpClient = http.DefaultClient
	tokenEndpoint = ghcrTokenEndpoint
}

// ghcrMock bundles a mock token endpoint and a mock blob endpoint served
// over one httptest.Server. The server routes on path prefix: "/token" goes
// to the token handler, everything else goes to the blob handler.
type ghcrMock struct {
	server        *httptest.Server
	tokenCalls    atomic.Int32
	blobCalls     atomic.Int32
	tokenStatus   int    // override status for token endpoint; 0 = default 200
	tokenBody     string // override body for token endpoint; "" = default JSON
	blobStatus    int    // override status for blob endpoint; 0 = default 200
	blobBody      []byte
	blobRedirect  string // if set, blob handler returns 302 to this URL
	requireAuth   bool   // if true, blob endpoint returns 401 when auth header missing
	blobAuthSeen  atomic.Bool
	retryAfterHit atomic.Int32 // if >0, return 429 that many times before succeeding
}

func newGHCRMock(t *testing.T, blobBody []byte) *ghcrMock {
	t.Helper()
	m := &ghcrMock{blobBody: blobBody}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		m.tokenCalls.Add(1)
		if m.tokenStatus != 0 {
			w.WriteHeader(m.tokenStatus)
			if m.tokenBody != "" {
				_, _ = w.Write([]byte(m.tokenBody))
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		body := m.tokenBody
		if body == "" {
			body = `{"token":"test-bearer-token"}`
		}
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		m.blobCalls.Add(1)
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			m.blobAuthSeen.Store(true)
		}
		if m.requireAuth && r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if n := m.retryAfterHit.Load(); n > 0 {
			m.retryAfterHit.Add(-1)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if m.blobRedirect != "" {
			http.Redirect(w, r, m.blobRedirect, http.StatusFound)
			return
		}
		if m.blobStatus != 0 {
			w.WriteHeader(m.blobStatus)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(m.blobBody)))
		_, _ = w.Write(m.blobBody)
	})
	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

// testServerWithBlob spins up a server that serves blobBody at the given
// path and returns (serverURL, blobPath). Useful for the redirect test.
func testServerWithBlob(t *testing.T, blobBody []byte) (string, string) {
	t.Helper()
	const path = "/cdn/blob"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(blobBody)))
		_, _ = w.Write(blobBody)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, path
}

// wireMock rewires the package globals so DownloadBlob targets the mock.
// Returns the blob URL the caller should pass to DownloadBlob.
func wireMock(t *testing.T, m *ghcrMock, formula string) string {
	t.Helper()
	httpClient = m.server.Client()
	tokenEndpoint = m.server.URL + "/token"
	return m.server.URL + "/v2/homebrew/core/" + formula + "/blobs/sha256:deadbeef"
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestDownloadBlobHappyPath(t *testing.T) {
	defer resetGHCRState(t)
	payload := []byte("hello bottle payload")
	m := newGHCRMock(t, payload)
	m.requireAuth = true
	url := wireMock(t, m, "ripgrep")

	dest := filepath.Join(t.TempDir(), "ripgrep.tar.gz")
	err := DownloadBlob(context.Background(), "ripgrep", url, sha256Hex(payload), dest, nil)
	if err != nil {
		t.Fatalf("DownloadBlob: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch: got %q want %q", got, payload)
	}
	if m.tokenCalls.Load() == 0 {
		t.Fatalf("expected token endpoint to be hit at least once")
	}
	if !m.blobAuthSeen.Load() {
		t.Fatalf("expected Authorization header on blob request")
	}

	// Second call for the same formula should reuse the cached token.
	before := m.tokenCalls.Load()
	if err := DownloadBlob(context.Background(), "ripgrep", url, sha256Hex(payload), dest+".2", nil); err != nil {
		t.Fatalf("second DownloadBlob: %v", err)
	}
	if m.tokenCalls.Load() != before {
		t.Fatalf("token endpoint hit again despite cache: before=%d after=%d", before, m.tokenCalls.Load())
	}
}

func TestDownloadBlobHashMismatch(t *testing.T) {
	defer resetGHCRState(t)
	payload := []byte("actual bytes on the wire")
	m := newGHCRMock(t, payload)
	url := wireMock(t, m, "jq")

	dest := filepath.Join(t.TempDir(), "jq.tar.gz")
	wrongDigest := sha256Hex([]byte("totally different payload"))

	err := DownloadBlob(context.Background(), "jq", url, wrongDigest, dest, nil)
	if err == nil {
		t.Fatalf("expected sha256 mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch in error, got: %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("dest file should not exist on hash mismatch; stat err=%v", statErr)
	}
	// No *.tmp-* files should linger either.
	entries, _ := os.ReadDir(filepath.Dir(dest))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestDownloadBlobRedirect(t *testing.T) {
	defer resetGHCRState(t)
	payload := []byte("bottle via redirect cdn")
	cdnURL, cdnPath := testServerWithBlob(t, payload)

	m := newGHCRMock(t, nil)
	m.blobRedirect = cdnURL + cdnPath
	url := wireMock(t, m, "fd")

	dest := filepath.Join(t.TempDir(), "fd.tar.gz")
	err := DownloadBlob(context.Background(), "fd", url, sha256Hex(payload), dest, nil)
	if err != nil {
		t.Fatalf("DownloadBlob redirect: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch after redirect: got %q want %q", got, payload)
	}
}

// TestDownloadBlobTokenRetryOn401 simulates a stale cached token: the
// blob endpoint refuses the first request with 401, we invalidate the
// cached token, re-fetch, and succeed on retry.
func TestDownloadBlobTokenRetryOn401(t *testing.T) {
	defer resetGHCRState(t)
	payload := []byte("retry-path payload")

	// Pre-seed the cache with a stale token. The blob handler will 401
	// the first time it sees that token and accept any other value.
	const staleToken = "stale-token"
	tokenCache.Store("wget", staleToken)

	// Rotating token server: each call returns a new distinct token.
	var tokenHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		n := tokenHits.Add(1)
		_ = json.NewEncoder(w).Encode(tokenResponse{Token: fmt.Sprintf("fresh-%d", n)})
	})
	var blobHits atomic.Int32
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		blobHits.Add(1)
		auth := r.Header.Get("Authorization")
		if auth == "Bearer "+staleToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		_, _ = w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	httpClient = srv.Client()
	tokenEndpoint = srv.URL + "/token"
	url := srv.URL + "/v2/homebrew/core/wget/blobs/sha256:deadbeef"

	dest := filepath.Join(t.TempDir(), "wget.tar.gz")
	if err := DownloadBlob(context.Background(), "wget", url, sha256Hex(payload), dest, nil); err != nil {
		t.Fatalf("DownloadBlob: %v", err)
	}
	if tokenHits.Load() == 0 {
		t.Fatalf("expected token endpoint to be hit on stale-token retry")
	}
	if blobHits.Load() < 2 {
		t.Fatalf("expected at least 2 blob hits (401 then OK), got %d", blobHits.Load())
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload mismatch: got %q want %q", got, payload)
	}
}

func TestDownloadBlobProgressCallback(t *testing.T) {
	defer resetGHCRState(t)
	// Large enough payload that io.Copy issues several Writes, and we
	// slow the reader so the 100ms throttle ticks at least once.
	payload := make([]byte, 1<<16)
	for i := range payload {
		payload[i] = byte(i)
	}
	// Custom server that streams in chunks with small sleeps so the
	// throttle fires deterministically.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/token") {
			_, _ = w.Write([]byte(`{"token":"t"}`))
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		flusher, _ := w.(http.Flusher)
		chunk := 4096
		for i := 0; i < len(payload); i += chunk {
			end := i + chunk
			if end > len(payload) {
				end = len(payload)
			}
			_, _ = w.Write(payload[i:end])
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(15 * time.Millisecond)
		}
	}))
	defer srv.Close()

	httpClient = srv.Client()
	tokenEndpoint = srv.URL + "/token"
	url := srv.URL + "/v2/homebrew/core/htop/blobs/sha256:deadbeef"

	dest := filepath.Join(t.TempDir(), "htop.tar.gz")
	var calls atomic.Int32
	var lastDownloaded atomic.Int64
	progress := func(downloaded, total int64) {
		calls.Add(1)
		lastDownloaded.Store(downloaded)
	}
	if err := DownloadBlob(context.Background(), "htop", url, sha256Hex(payload), dest, progress); err != nil {
		t.Fatalf("DownloadBlob: %v", err)
	}
	if calls.Load() == 0 {
		t.Fatalf("expected progress callback to fire at least once")
	}
	if lastDownloaded.Load() != int64(len(payload)) {
		t.Fatalf("final progress downloaded=%d want %d", lastDownloaded.Load(), len(payload))
	}
}

func TestDownloadBlobTokenEndpoint404(t *testing.T) {
	defer resetGHCRState(t)
	m := newGHCRMock(t, nil)
	m.tokenStatus = http.StatusNotFound
	url := wireMock(t, m, "no-such-formula")

	dest := filepath.Join(t.TempDir(), "x.tar.gz")
	err := DownloadBlob(context.Background(), "no-such-formula", url, sha256Hex(nil), dest, nil)
	if !errors.Is(err, ErrGHCRNoToken) {
		t.Fatalf("expected ErrGHCRNoToken, got %v", err)
	}
}

func TestDownloadBlobBlob404(t *testing.T) {
	defer resetGHCRState(t)
	m := newGHCRMock(t, nil)
	m.blobStatus = http.StatusNotFound
	url := wireMock(t, m, "legit-formula")

	dest := filepath.Join(t.TempDir(), "x.tar.gz")
	err := DownloadBlob(context.Background(), "legit-formula", url, sha256Hex(nil), dest, nil)
	if !errors.Is(err, ErrBottleNotFound) {
		t.Fatalf("expected ErrBottleNotFound, got %v", err)
	}
}

func TestDownloadBlobRetryAfter429(t *testing.T) {
	defer resetGHCRState(t)
	payload := []byte("after retry")
	m := newGHCRMock(t, payload)
	m.retryAfterHit.Store(2) // two 429s then success
	url := wireMock(t, m, "tmux")

	dest := filepath.Join(t.TempDir(), "tmux.tar.gz")
	if err := DownloadBlob(context.Background(), "tmux", url, sha256Hex(payload), dest, nil); err != nil {
		t.Fatalf("DownloadBlob: %v", err)
	}
	if m.blobCalls.Load() < 3 {
		t.Fatalf("expected at least 3 blob calls (2 429 + 1 OK), got %d", m.blobCalls.Load())
	}
}

func TestDownloadBlobArgValidation(t *testing.T) {
	defer resetGHCRState(t)
	cases := []struct {
		name    string
		formula string
		url     string
		digest  string
		dest    string
	}{
		{"missing formula", "", "http://x", "d", "/tmp/x"},
		{"missing url", "f", "", "d", "/tmp/x"},
		{"missing digest", "f", "http://x", "", "/tmp/x"},
		{"missing dest", "f", "http://x", "d", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := DownloadBlob(context.Background(), c.formula, c.url, c.digest, c.dest, nil)
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
		})
	}
}
