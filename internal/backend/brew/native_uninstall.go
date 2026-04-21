package brew

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// UninstallOpts controls native uninstall behavior. The zero value is valid.
type UninstallOpts struct {
	// Force ignores the reverse-dep guard — removes a formula even if other
	// installed formulae declare it as a runtime dependency. Mirrors
	// `brew uninstall --force`.
	Force bool

	// IgnoreDeps skips the reverse-dep scan entirely (analogous to
	// `brew uninstall --ignore-dependencies`). Slightly different from
	// Force: Force still runs the scan and would report if asked to, but
	// proceeds regardless; IgnoreDeps doesn't walk the Cellar looking for
	// consumers at all. In practice the user-visible effect is identical.
	IgnoreDeps bool

	// DryRun short-circuits every filesystem mutation. The returned
	// UninstallResult still names which packages would be removed so the
	// caller can render a plan.
	DryRun bool

	// Progress, if non-nil, is invoked synchronously with each phase update.
	// Safe to be nil — UninstallNative nil-checks at every call site.
	Progress func(UninstallEvent)
}

// UninstallEvent is a UX-facing progress update. Phases are:
//   - "resolve": per-package inspection of the cellar dir.
//   - "unlink":  prefix symlinks being torn down.
//   - "remove":  cellar version dirs being deleted.
//   - "done":    terminal summary event.
type UninstallEvent struct {
	Phase   string
	Package string
	Current int
	Total   int
	Message string
}

// UninstallResult is returned to the caller. Removed is the packages that
// successfully came down; Skipped is packages that were never installed;
// Failed maps name -> error for partial failures.
type UninstallResult struct {
	Removed []string
	Skipped []string
	Failed  map[string]error
}

// UninstallNative removes one or more installed formulae in-process — no
// `brew uninstall` subprocess. Returns ErrNativeUnsupported when the host
// isn't a standard Homebrew layout (no prefix discoverable); the caller
// then falls back to the subprocess path. All other errors (reverse-dep
// guard trips, I/O failures) are returned directly.
//
// Rollback note: if unlink succeeds but a Cellar RemoveAll fails partway,
// we can't safely replay the symlinks — they're gone. The caller should
// `brew reinstall <name>` to recover in that rare case.
func (b *Brew) UninstallNative(ctx context.Context, names []string, opts UninstallOpts) (*UninstallResult, error) {
	prefix := brewPrefix()
	if prefix == "" {
		return nil, ErrNativeUnsupported
	}

	emit := uninstallProgressEmitter(opts.Progress)
	result := &UninstallResult{Failed: map[string]error{}}
	cellarRoot := filepath.Join(prefix, "Cellar")

	// Phase 1: resolve. Split into present / absent up front so we can
	// return deterministic Skipped lists and bail early on reverse-dep
	// collisions without having mutated anything.
	type pkgPlan struct {
		name      string
		pkgDir    string   // $PREFIX/Cellar/<name>
		versions  []string // version dir names, unsorted
		verPaths  []string // absolute version dir paths, aligned with versions
	}
	var plans []pkgPlan
	for i, name := range names {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		pkgDir := filepath.Join(cellarRoot, name)
		versions := versionDirs(pkgDir)
		emit(UninstallEvent{
			Phase:   "resolve",
			Package: name,
			Current: i + 1,
			Total:   len(names),
			Message: fmt.Sprintf("resolving %s", name),
		})
		if len(versions) == 0 {
			// Either the package isn't installed at all, or it's a cask /
			// oddball layout we can't handle. We treat both as "skip" so
			// the caller (Brew.Remove) can fall through to brew subprocess
			// when *every* requested name was unresolvable.
			result.Skipped = append(result.Skipped, name)
			continue
		}
		paths := make([]string, len(versions))
		for j, v := range versions {
			paths[j] = filepath.Join(pkgDir, v)
		}
		plans = append(plans, pkgPlan{
			name:     name,
			pkgDir:   pkgDir,
			versions: versions,
			verPaths: paths,
		})
	}

	// If nothing resolved — i.e. every requested name is absent — there's
	// nothing for us to do natively. Defer to the subprocess path so users
	// see brew's own error messages for typos / casks / nonexistent names.
	if len(plans) == 0 && len(names) > 0 {
		// All names were skipped. Return the result so the caller can
		// decide whether to fall through. For yum remove's current wiring
		// this is still a success — nothing was installed, nothing to do.
		emit(UninstallEvent{
			Phase:   "done",
			Total:   len(names),
			Message: fmt.Sprintf("skipped %d (not installed)", len(result.Skipped)),
		})
		return result, nil
	}

	// Phase 2: reverse-dep guard. Walk every other installed formula's
	// .brew/<n>.rb to find consumers. We bail on the *first* protected
	// package so the user sees a clean "foo is required by bar" message
	// and no partial mutation.
	if !opts.Force && !opts.IgnoreDeps {
		targets := make(map[string]bool, len(plans))
		for _, p := range plans {
			targets[p.name] = true
		}
		consumers, err := findConsumers(prefix, targets)
		if err != nil {
			return result, err
		}
		for _, p := range plans {
			users := consumers[p.name]
			if len(users) == 0 {
				continue
			}
			sort.Strings(users)
			return result, fmt.Errorf("%s is required by %s", p.name, strings.Join(users, ", "))
		}
	}

	// Phase 3: per-package teardown.
	for _, p := range plans {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		// 3a: collect every prefix symlink that resolves into any of
		// this package's version dirs. We do this *before* touching
		// anything so a scan error doesn't leave us half-unlinked.
		syms, err := collectSymlinks(prefix, p.verPaths)
		if err != nil {
			result.Failed[p.name] = fmt.Errorf("scan symlinks: %w", err)
			continue
		}
		// Also tear down $PREFIX/opt/<name> when it points into the
		// cellar dir we're removing. Include it in syms so the unlink
		// loop handles it uniformly.
		optLink := filepath.Join(prefix, "opt", p.name)
		if optPointsInto(optLink, p.pkgDir) {
			syms = append(syms, optLink)
		}

		emit(UninstallEvent{
			Phase:   "unlink",
			Package: p.name,
			Current: len(syms),
			Total:   len(syms),
			Message: fmt.Sprintf("unlinking %d symlinks for %s", len(syms), p.name),
		})

		if opts.DryRun {
			// Record the plan and move on — no FS writes.
			result.Removed = append(result.Removed, p.name)
			continue
		}

		if err := Unlink(syms); err != nil {
			result.Failed[p.name] = fmt.Errorf("unlink: %w", err)
			continue
		}

		// 3b: remove each version dir, then the empty parent. If a
		// version dir fails we stop that package's teardown (the
		// already-unlinked symlinks are lost — the comment at the top
		// tells users to `brew reinstall` to recover).
		removeErr := error(nil)
		for i, verDir := range p.verPaths {
			emit(UninstallEvent{
				Phase:   "remove",
				Package: p.name,
				Current: i + 1,
				Total:   len(p.verPaths),
				Message: fmt.Sprintf("removing %s %s", p.name, p.versions[i]),
			})
			if err := os.RemoveAll(verDir); err != nil {
				removeErr = fmt.Errorf("remove %s: %w", verDir, err)
				break
			}
		}
		if removeErr != nil {
			result.Failed[p.name] = removeErr
			continue
		}
		// Sweep the now-empty parent. If something else landed in here
		// between our version scan and the RemoveAll (e.g. brew ran
		// concurrently) we leave it alone — a readdir returning a
		// non-empty slice means "not ours to delete".
		if leftovers, err := os.ReadDir(p.pkgDir); err == nil && len(leftovers) == 0 {
			_ = os.Remove(p.pkgDir)
		}

		result.Removed = append(result.Removed, p.name)
		invalidateCache([]string{p.name})
	}

	emit(UninstallEvent{
		Phase:   "done",
		Total:   len(names),
		Message: fmt.Sprintf("removed %d, skipped %d, failed %d", len(result.Removed), len(result.Skipped), len(result.Failed)),
	})

	if len(result.Failed) > 0 {
		errs := make([]error, 0, len(result.Failed))
		for name, err := range result.Failed {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
		return result, errors.Join(errs...)
	}
	return result, nil
}

// collectSymlinks scans the usual prefix subdirs for symlinks whose resolved
// target lives under any of verPaths. It walks recursively so we also catch
// links buried in share/man/man1/, lib/pkgconfig/, etc. The returned paths
// are absolute and suitable for passing to Unlink.
func collectSymlinks(prefix string, verPaths []string) ([]string, error) {
	// Build a prefix-set of version dirs (with trailing separator) so
	// HasPrefix matches the dir boundary and doesn't false-positive on
	// /foo/bar-extra when we're looking for /foo/bar.
	wants := make([]string, len(verPaths))
	for i, v := range verPaths {
		wants[i] = v + string(os.PathSeparator)
	}
	// opt is scanned separately in the caller because the opt link points
	// *at* the version dir, not inside it, and rel-target resolution below
	// would miss that edge case.
	scanDirs := []string{"bin", "sbin", "lib", "include", "share", "etc", "var", "Frameworks"}
	var found []string
	for _, sub := range scanDirs {
		root := filepath.Join(prefix, sub)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil {
				// Don't abort the whole scan on one unreadable dir —
				// skip it. Unlinking what we *can* reach is better
				// than refusing to uninstall over a permission blip.
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			tgt, err := os.Readlink(path)
			if err != nil {
				return nil
			}
			if !filepath.IsAbs(tgt) {
				tgt = filepath.Join(filepath.Dir(path), tgt)
			}
			tgt = filepath.Clean(tgt)
			for _, w := range wants {
				if strings.HasPrefix(tgt+string(os.PathSeparator), w) || tgt+string(os.PathSeparator) == w {
					found = append(found, path)
					return nil
				}
			}
			return nil
		})
		if err != nil {
			return found, err
		}
	}
	return found, nil
}

// optPointsInto reports whether $PREFIX/opt/<name> is a symlink whose
// resolved target sits inside pkgDir ($PREFIX/Cellar/<name>). This is the
// guard that prevents us from nuking an opt link that some user sidestepped
// into pointing at a custom location.
func optPointsInto(optLink, pkgDir string) bool {
	st, err := os.Lstat(optLink)
	if err != nil || st.Mode()&os.ModeSymlink == 0 {
		return false
	}
	tgt, err := os.Readlink(optLink)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(tgt) {
		tgt = filepath.Join(filepath.Dir(optLink), tgt)
	}
	tgt = filepath.Clean(tgt)
	pkgDir = filepath.Clean(pkgDir)
	// Target is typically $PREFIX/Cellar/<name>/<ver> — i.e. strictly
	// below pkgDir. A target that equals pkgDir itself is weird but we
	// still treat it as "ours" so a corrupt setup gets cleaned.
	return tgt == pkgDir || strings.HasPrefix(tgt, pkgDir+string(os.PathSeparator))
}

// findConsumers walks $PREFIX/Cellar and reports which installed formulae
// declare any of `targets` as a runtime dependency. Returns a map keyed by
// target name -> list of consumer names. Packages named in `targets` aren't
// scanned (they can depend on each other without triggering the guard —
// we're uninstalling them all as a batch).
func findConsumers(prefix string, targets map[string]bool) (map[string][]string, error) {
	cellarRoot := filepath.Join(prefix, "Cellar")
	entries, err := os.ReadDir(cellarRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]string{}, nil
		}
		return nil, err
	}
	out := map[string][]string{}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || strings.HasPrefix(n, ".") {
			continue
		}
		if targets[n] {
			continue
		}
		deps, found, err := ParseRuntimeDeps(prefix, n)
		if err != nil || !found {
			continue
		}
		for _, d := range deps {
			if targets[d] {
				out[d] = append(out[d], n)
			}
		}
	}
	return out, nil
}

// uninstallProgressEmitter wraps a user-supplied Progress callback into a
// nil-safe call site. Uninstall is single-goroutine so no mutex is needed —
// this is purely a nil-check elision.
func uninstallProgressEmitter(cb func(UninstallEvent)) func(UninstallEvent) {
	if cb == nil {
		return func(UninstallEvent) {}
	}
	return cb
}
