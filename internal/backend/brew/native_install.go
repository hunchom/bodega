package brew

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// ErrNativeUnsupported is returned by InstallNative when the host can't run a
// bottle install in-process — most commonly when Homebrew's prefix can't be
// found or the API cache hasn't been warmed. Callers MUST check this with
// errors.Is and fall back to the `brew install` subprocess path.
var ErrNativeUnsupported = errors.New("native install unavailable on this host")

// maxDownloadConcurrency caps the parallel bottle fetch pool. GHCR rate-limits
// aggressively on parallel requests from a single IP; eight is empirically the
// sweet spot where we saturate a fast connection without tripping 429s.
const maxDownloadConcurrency = 8

// InstallOpts controls native install behavior. The zero value is valid: all
// fields have safe defaults resolved at call time.
type InstallOpts struct {
	// Overwrite lets Link replace an existing symlink whose destination
	// doesn't match ours. Non-symlink collisions are still errors.
	Overwrite bool

	// DryRun short-circuits after resolve + link preview. No network calls
	// and no filesystem writes happen. Returned InstalledPackage entries
	// carry the symlinks that would have been created.
	DryRun bool

	// Concurrency caps the parallel bottle download pool. Defaults to
	// runtime.GOMAXPROCS(0), capped at maxDownloadConcurrency to stay under
	// GHCR's per-IP rate limits.
	Concurrency int

	// CacheDir is where bottle tarballs are stashed between download and
	// extract. Defaults to $XDG_CACHE_HOME/bodega/bottles or
	// ~/.cache/bodega/bottles. Created with 0o755 if missing.
	CacheDir string

	// Progress, if non-nil, is invoked from any goroutine with a phase
	// update. Implementations must be safe for concurrent use — InstallNative
	// serialises calls internally, but callers are free to assume the
	// callback itself is re-entered.
	Progress func(event InstallEvent)
}

// InstallEvent is a UX-facing update. Callers format it for stdout / TUI.
type InstallEvent struct {
	// Phase is one of: "resolve", "download", "extract", "relocate",
	// "link", "done". New phases may be added over time; unknown values
	// should be rendered as a generic progress line.
	Phase string

	// Package is the formula name this event pertains to. Empty for
	// whole-operation phases like "resolve" and "done".
	Package string

	// Version is the formula version being installed, when known.
	Version string

	// Current is the downloaded byte count for "download" phases; zero
	// for everything else.
	Current int64

	// Total is the total byte count for "download" phases; -1 when the
	// server didn't provide a Content-Length.
	Total int64

	// Message is a short human-readable note. Always non-empty.
	Message string
}

// InstallResult is what InstallNative returns to the caller. The journal /
// UX layer turns it into persistent records and success/failure output.
type InstallResult struct {
	// Installed is every package that completed all four phases
	// (download, extract, relocate, link) successfully, in topological
	// order (dependencies first).
	Installed []InstalledPackage

	// Skipped is names that were already present in the Cellar at the
	// expected version and needed no work.
	Skipped []string

	// Failed is per-package failures, naming the phase that tripped and
	// the underlying error.
	Failed []FailedPackage
}

// InstalledPackage is a single successful install.
type InstalledPackage struct {
	Name        string
	Version     string
	CellarDir   string    // absolute path to <prefix>/Cellar/<name>/<version>
	Symlinks    []string  // absolute symlink paths; pass to Unlink for rollback
	InstalledAt time.Time // wall clock of link completion
	IsRoot      bool      // true when the user explicitly asked for this name
}

// FailedPackage records a per-package failure and the phase it occurred in.
type FailedPackage struct {
	Name  string
	Phase string // "download" | "extract" | "relocate" | "link"
	Err   error
}

// InstallNative installs names and their transitive bottle dependencies
// without shelling out to `brew install`. Formulae already present in the
// Cellar at the resolved version are skipped. When a step fails for one
// formula its later steps are aborted but sibling formulae continue; the
// aggregated error is returned alongside a populated InstallResult.
//
// Pre-flight returns (nil, ErrNativeUnsupported) when Homebrew isn't
// installed or the API cache is cold — callers bear the fallback path.
func (b *Brew) InstallNative(ctx context.Context, names []string, opts InstallOpts) (*InstallResult, error) {
	prefix := brewPrefix()
	if prefix == "" {
		return nil, ErrNativeUnsupported
	}
	cache := apiCache()
	if cache == nil {
		return nil, ErrNativeUnsupported
	}
	// Warming the formula map here gives us an early, cheap failure: if
	// brew's API cache file is missing we want to degrade gracefully
	// before touching the network or the filesystem.
	if _, err := cache.LoadFormulae(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNativeUnsupported, err)
	}

	cacheDir, err := resolveBottleCacheDir(opts.CacheDir)
	if err != nil {
		return nil, err
	}
	if !opts.DryRun {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("native install: mkdir cache: %w", err)
		}
	}

	conc := opts.Concurrency
	if conc <= 0 {
		conc = runtime.GOMAXPROCS(0)
	}
	if conc > maxDownloadConcurrency {
		conc = maxDownloadConcurrency
	}

	emit := progressEmitter(opts.Progress)

	// Phase 1: resolve.
	plans, err := Resolve(ctx, cache, names)
	if err != nil {
		return nil, fmt.Errorf("native install: resolve: %w", err)
	}

	result := &InstallResult{}

	// Phase 2: filter already-installed, announce each remaining plan.
	pending := make([]Plan, 0, len(plans))
	cellarRoot := filepath.Join(prefix, "Cellar")
	for _, p := range plans {
		emit(InstallEvent{
			Phase:   "resolve",
			Package: p.Name,
			Version: p.Version,
			Total:   -1,
			Message: fmt.Sprintf("resolved %s %s (%s)", p.Name, p.Version, p.Tag),
		})
		if cellarDirHasVersion(cellarRoot, p.Name, p.Version) {
			result.Skipped = append(result.Skipped, p.Name)
			continue
		}
		pending = append(pending, p)
	}

	// DryRun: skip network / FS writes entirely. Call Link in DryRun mode
	// against a (possibly nonexistent) cellar dir so the caller can still
	// see a rough symlink preview when upgrading already-resolved paths.
	if opts.DryRun {
		for _, p := range pending {
			cellarPkgDir := filepath.Join(cellarRoot, p.Name, p.Version)
			var syms []string
			if _, err := os.Stat(cellarPkgDir); err == nil {
				// Only bother when the dir exists — otherwise Link has
				// nothing to walk and would emit no preview lines.
				syms, _ = Link(prefix, cellarPkgDir, LinkOptions{Overwrite: opts.Overwrite, DryRun: true})
			}
			result.Installed = append(result.Installed, InstalledPackage{
				Name:        p.Name,
				Version:     p.Version,
				CellarDir:   cellarPkgDir,
				Symlinks:    syms,
				InstalledAt: time.Now(),
				IsRoot:      p.IsRoot,
			})
		}
		emit(InstallEvent{Phase: "done", Total: -1, Message: "dry-run: no changes applied"})
		return result, nil
	}

	// Phase 3: parallel downloads.
	downloads := runDownloads(ctx, pending, cacheDir, conc, emit)

	// Phase 4: per-plan extract + relocate + link, sequential in topo order.
	var failErrs []error
	for _, p := range pending {
		d := downloads[p.Name]
		if d.err != nil {
			result.Failed = append(result.Failed, FailedPackage{
				Name: p.Name, Phase: "download", Err: d.err,
			})
			failErrs = append(failErrs, fmt.Errorf("%s: download: %w", p.Name, d.err))
			continue
		}

		emit(InstallEvent{
			Phase:   "extract",
			Package: p.Name,
			Version: p.Version,
			Total:   -1,
			Message: fmt.Sprintf("extracting %s %s", p.Name, p.Version),
		})
		root, err := Extract(ctx, d.path, cellarRoot)
		if err != nil {
			result.Failed = append(result.Failed, FailedPackage{
				Name: p.Name, Phase: "extract", Err: err,
			})
			failErrs = append(failErrs, fmt.Errorf("%s: extract: %w", p.Name, err))
			continue
		}

		emit(InstallEvent{
			Phase:   "relocate",
			Package: p.Name,
			Version: p.Version,
			Total:   -1,
			Message: fmt.Sprintf("relocating %s %s", p.Name, p.Version),
		})
		relocErr := Relocate(ctx, root, RelocateOptions{
			Prefix: prefix,
			Cellar: cellarRoot,
		})
		if relocErr != nil {
			// Remove the partially-relocated cellar dir so a retry
			// starts from a clean slate. We don't touch anything
			// outside the package's own version dir.
			_ = os.RemoveAll(root)
			result.Failed = append(result.Failed, FailedPackage{
				Name: p.Name, Phase: "relocate", Err: relocErr,
			})
			failErrs = append(failErrs, fmt.Errorf("%s: relocate: %w", p.Name, relocErr))
			continue
		}

		emit(InstallEvent{
			Phase:   "link",
			Package: p.Name,
			Version: p.Version,
			Total:   -1,
			Message: fmt.Sprintf("linking %s %s", p.Name, p.Version),
		})
		syms, linkErr := Link(prefix, root, LinkOptions{Overwrite: opts.Overwrite})
		if linkErr != nil {
			// Roll back any symlinks that did land before the error,
			// then drop the cellar dir so the package looks
			// uninstalled again.
			_ = Unlink(syms)
			_ = os.RemoveAll(root)
			result.Failed = append(result.Failed, FailedPackage{
				Name: p.Name, Phase: "link", Err: linkErr,
			})
			failErrs = append(failErrs, fmt.Errorf("%s: link: %w", p.Name, linkErr))
			continue
		}

		result.Installed = append(result.Installed, InstalledPackage{
			Name:        p.Name,
			Version:     p.Version,
			CellarDir:   root,
			Symlinks:    syms,
			InstalledAt: time.Now(),
			IsRoot:      p.IsRoot,
		})
		// Drop any stale `brew info` cache entry so the next yum info
		// call re-reads the newly-installed version metadata.
		invalidateCache([]string{p.Name})
	}

	emit(InstallEvent{
		Phase:   "done",
		Total:   -1,
		Message: fmt.Sprintf("installed %d, skipped %d, failed %d", len(result.Installed), len(result.Skipped), len(result.Failed)),
	})

	if len(failErrs) > 0 {
		return result, errors.Join(failErrs...)
	}
	return result, nil
}

// downloadResult is one entry in the parallel fetch map. A nil err with an
// empty path is possible only for a skipped plan (shouldn't occur — we
// filter before dispatch — but defensive anyway).
type downloadResult struct {
	path string
	err  error
}

// runDownloads fans out bottle fetches into a bounded worker pool keyed by
// formula name. Returns a map keyed by name so the caller can iterate plans
// in topological order and look up each download's outcome.
func runDownloads(ctx context.Context, plans []Plan, cacheDir string, conc int, emit func(InstallEvent)) map[string]downloadResult {
	results := make(map[string]downloadResult, len(plans))
	if len(plans) == 0 {
		return results
	}

	// A resultsMu guards writes to the shared map; the map itself is
	// read only after all workers finish so we don't need a sync.Map.
	var resultsMu sync.Mutex

	jobs := make(chan Plan)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for p := range jobs {
			target := bottleCachePath(cacheDir, p)

			emit(InstallEvent{
				Phase:   "download",
				Package: p.Name,
				Version: p.Version,
				Total:   -1,
				Message: fmt.Sprintf("downloading %s %s", p.Name, p.Version),
			})

			if have, err := cachedBottleMatches(target, p.SHA256); err == nil && have {
				// Cache hit — emit a terminal event so UIs don't
				// show a frozen bar for already-downloaded files.
				emit(InstallEvent{
					Phase:   "download",
					Package: p.Name,
					Version: p.Version,
					Current: fileSize(target),
					Total:   fileSize(target),
					Message: fmt.Sprintf("cached %s %s", p.Name, p.Version),
				})
				resultsMu.Lock()
				results[p.Name] = downloadResult{path: target}
				resultsMu.Unlock()
				continue
			}

			progress := downloadProgressAdapter(emit, p.Name, p.Version)
			err := DownloadBlob(ctx, p.Name, p.BottleURL, p.SHA256, target, progress)
			resultsMu.Lock()
			if err != nil {
				results[p.Name] = downloadResult{err: err}
			} else {
				results[p.Name] = downloadResult{path: target}
			}
			resultsMu.Unlock()
		}
	}

	for i := 0; i < conc; i++ {
		wg.Add(1)
		go worker()
	}

	// Feed the pool. We break out on context cancellation so a ctrl-C
	// doesn't wait for every queued job to drain.
	go func() {
		defer close(jobs)
		for _, p := range plans {
			select {
			case <-ctx.Done():
				return
			case jobs <- p:
			}
		}
	}()

	wg.Wait()

	// Fill in any plan that got skipped by ctx cancellation so the caller
	// sees a deterministic map shape.
	for _, p := range plans {
		if _, ok := results[p.Name]; !ok {
			results[p.Name] = downloadResult{err: ctxErrOrCancelled(ctx)}
		}
	}
	return results
}

// bottleCachePath is the deterministic on-disk filename for a bottle. We
// include the tag so swapping between arm64_sequoia and arm64_sonoma at
// different invocations doesn't silently pick up the wrong tarball.
func bottleCachePath(cacheDir string, p Plan) string {
	return filepath.Join(cacheDir, fmt.Sprintf("%s-%s.%s.tar.gz", p.Name, p.Version, p.Tag))
}

// cachedBottleMatches reports whether target already exists on disk with the
// expected sha256. A mismatch or any I/O error returns (false, nil) so the
// caller treats the file as absent and re-downloads. A digest mismatch on a
// cached file is common during an upgrade where the formula was re-bottled.
func cachedBottleMatches(target, wantDigest string) (bool, error) {
	f, err := os.Open(target)
	if err != nil {
		return false, nil
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, nil
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantDigest {
		// Drop the stale cache file so we don't waste disk on a bottle
		// we'll never reuse. Ignore the error — worst case the next
		// download overwrites via its own rename-through-temp.
		_ = os.Remove(target)
		return false, nil
	}
	return true, nil
}

// fileSize is a convenience wrapper for emitting progress events that quote
// the same number for Current and Total on a cache-hit.
func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// downloadProgressAdapter wraps the user's Progress callback into the
// func(downloaded, total int64) shape DownloadBlob expects, stamping each
// call with the current formula name + phase.
func downloadProgressAdapter(emit func(InstallEvent), name, version string) func(int64, int64) {
	if emit == nil {
		return nil
	}
	return func(downloaded, total int64) {
		emit(InstallEvent{
			Phase:   "download",
			Package: name,
			Version: version,
			Current: downloaded,
			Total:   total,
		})
	}
}

// progressEmitter returns a func that forwards events to cb under a mutex so
// concurrent download workers can't interleave writes on a non-thread-safe
// caller-supplied callback. A nil cb returns a no-op — callers shouldn't
// have to nil-check at every call site.
func progressEmitter(cb func(InstallEvent)) func(InstallEvent) {
	if cb == nil {
		return func(InstallEvent) {}
	}
	var mu sync.Mutex
	return func(ev InstallEvent) {
		mu.Lock()
		defer mu.Unlock()
		cb(ev)
	}
}

// resolveBottleCacheDir picks a directory for bottle downloads. Explicit
// opts.CacheDir wins; otherwise we honour XDG_CACHE_HOME, then fall back to
// ~/.cache. The returned path is not created — callers decide whether to
// MkdirAll (skipped for DryRun).
func resolveBottleCacheDir(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if base := os.Getenv("XDG_CACHE_HOME"); base != "" {
		return filepath.Join(base, "bodega", "bottles"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("native install: cannot resolve cache dir: %w", err)
	}
	return filepath.Join(home, ".cache", "bodega", "bottles"), nil
}

// cellarDirHasVersion reports whether <cellarRoot>/<name>/<version> exists
// as a directory. Used as a fast skip check: if brew already has the bits
// at the right version we don't need to touch GHCR.
func cellarDirHasVersion(cellarRoot, name, version string) bool {
	if version == "" {
		return false
	}
	st, err := os.Stat(filepath.Join(cellarRoot, name, version))
	if err != nil {
		return false
	}
	return st.IsDir()
}

// ctxErrOrCancelled returns ctx.Err() when the context is done, otherwise a
// generic "cancelled" error. Guarantees the returned error is non-nil so
// callers can surface a reason for the skip.
func ctxErrOrCancelled(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("download skipped")
}
