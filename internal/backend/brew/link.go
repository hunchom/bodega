package brew

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// linkSubdirs is the set of cellar subdirectories brew stitches into the
// prefix. Order matters only insofar as it determines the order of entries
// in the returned journal; we match brew's ordering so rollback walks the
// same shape the upstream code would.
var linkSubdirs = []string{
	"bin",
	"sbin",
	"lib",
	"include",
	"share",
	"etc",
	"var",
	"opt",
	"Frameworks",
}

// LinkOptions controls link behavior.
type LinkOptions struct {
	Overwrite bool // if a link target already exists, remove + replace it
	DryRun    bool // if true, don't touch FS; just return what would be done
}

// LinkCollisionError is returned when Link finds an existing file or a
// mismatched symlink at a target path and Overwrite is false. ExistingLink
// is populated only when the offender was itself a symlink; for regular
// files and directories it's empty.
type LinkCollisionError struct {
	Target       string // absolute path that conflicts
	ExistingLink string // if target was a symlink, its destination; else ""
	WantLink     string // the cellar path we wanted to link to
}

func (e *LinkCollisionError) Error() string {
	if e.ExistingLink != "" {
		return fmt.Sprintf("link collision at %s: existing symlink -> %s, wanted -> %s",
			e.Target, e.ExistingLink, e.WantLink)
	}
	return fmt.Sprintf("link collision at %s: non-symlink file exists, wanted -> %s",
		e.Target, e.WantLink)
}

// Link creates symlinks from prefix into cellarPkgDir (the versioned cellar
// path like /opt/homebrew/Cellar/ripgrep/14.1.1). Returns the list of
// absolute symlink paths created, in creation order, so the journal can
// record them for precise rollback.
//
// Collision semantics:
//   - If a target path exists and is already a symlink to the same cellar
//     location, it's a no-op (not a collision).
//   - If it's a symlink to a DIFFERENT location, and Overwrite is false,
//     return a *LinkCollisionError naming the offender. On Overwrite=true,
//     unlink and replace.
//   - If it's a regular file or directory (not a symlink), always error —
//     never clobber user data.
//
// Also creates the $PREFIX/opt/<pkg> symlink pointing at cellarPkgDir.
//
// Brew itself shallow-links wholly-new subtrees (one directory symlink into
// the keg, e.g. lib/cmake/zstd -> ../../Cellar/zstd/1.5.7/lib/cmake/zstd).
// We always deep-link leaves, so a prefix previously linked by brew has
// directory symlinks sitting where we need real directories; linkWalk
// unwinds those brew-style (see ensureRealDir) instead of colliding.
//
// TODO(perf): consider adopting brew's "shallow then deep" strategy — it
// produces fewer inodes. Deep-linking is correct either way.
func Link(prefix, cellarPkgDir string, opts LinkOptions) ([]string, error) {
	if prefix == "" {
		return nil, fmt.Errorf("link: empty prefix")
	}
	if cellarPkgDir == "" {
		return nil, fmt.Errorf("link: empty cellar dir")
	}

	// Cellar root for ensureRealDir's containment check: Cellar/<pkg>/<ver>.
	cellarRoot := filepath.Dir(filepath.Dir(cellarPkgDir))
	dirs := &dirState{ready: map[string]bool{}, dryUnwound: map[string]string{}}

	var created []string
	for _, sub := range linkSubdirs {
		srcRoot := filepath.Join(cellarPkgDir, sub)
		st, err := os.Lstat(srcRoot)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return created, fmt.Errorf("link: stat %s: %w", srcRoot, err)
		}
		if !st.IsDir() {
			// Odd: a regular file at a known subdir name. Skip it rather
			// than link — brew's linker makes the same choice.
			continue
		}

		dstRoot := filepath.Join(prefix, sub)
		made, err := linkWalk(srcRoot, dstRoot, cellarRoot, opts, dirs)
		created = append(created, made...)
		if err != nil {
			return created, err
		}
	}

	// $PREFIX/opt/<pkg> -> cellarPkgDir. pkgName is the parent dir of the
	// versioned cellar path (e.g. Cellar/ripgrep/14.1.1 -> "ripgrep").
	pkgName := filepath.Base(filepath.Dir(cellarPkgDir))
	if pkgName != "" && pkgName != "." && pkgName != string(filepath.Separator) {
		optLink := filepath.Join(prefix, "opt", pkgName)
		if !opts.DryRun {
			if err := os.MkdirAll(filepath.Dir(optLink), 0o755); err != nil {
				return created, fmt.Errorf("link: mkdir %s: %w", filepath.Dir(optLink), err)
			}
		}
		made, err := linkOne(optLink, cellarPkgDir, opts)
		if made {
			created = append(created, optLink)
		}
		if err != nil {
			return created, err
		}
	}

	return created, nil
}

// dirState caches per-Link directory work: ready marks prefix dirs verified
// (or created) as real directories; dryUnwound maps dirs a DryRun would
// have unwound to the old symlink's resolved target ("" when nothing would
// be preserved), so the preview can model the preserved children instead of
// Lstat-ing through the still-present symlink into an old keg.
type dirState struct {
	ready      map[string]bool
	dryUnwound map[string]string
}

// linkWalk recursively mirrors srcRoot's directory tree under dstRoot,
// creating a symlink at every leaf (regular file or symlink inside the
// cellar). Parent directories in the prefix are created — and brew-style
// directory symlinks unwound — by ensureRealDir. Returns the paths created,
// in creation order.
func linkWalk(srcRoot, dstRoot, cellarRoot string, opts LinkOptions, dirs *dirState) ([]string, error) {
	var created []string
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstRoot, rel)
		if err := ensureRealDir(filepath.Dir(dst), dstRoot, cellarRoot, opts, dirs); err != nil {
			return err
		}
		if oldDir, unwound := dirs.dryUnwound[filepath.Dir(dst)]; opts.DryRun && unwound {
			// Parent would have been unwound; Lstat would otherwise follow
			// the still-present symlink into the old keg and misreport a
			// collision. Model linkOne against the children the real unwind
			// would preserve so preview and real run agree.
			made, err := previewLeafUnderUnwound(dst, path, oldDir, opts)
			if made {
				created = append(created, dst)
			}
			return err
		}
		made, err := linkOne(dst, path, opts)
		if made {
			created = append(created, dst)
		}
		return err
	})
	return created, err
}

// ensureRealDir makes dir a real directory the deep-link walk can descend
// into, ensuring its parents first. Brew shallow-links whole subtrees as
// directory symlinks; when one sits where we need a real dir it's unwound
// (see unwindDirSymlink). Paths at or above dstRoot keep plain MkdirAll
// semantics — brew never symlinks prefix top-level dirs. Results are cached
// in dirs.ready so each dir is checked once per Link call.
func ensureRealDir(dir, dstRoot, cellarRoot string, opts LinkOptions, dirs *dirState) error {
	if dirs.ready[dir] {
		return nil
	}
	rel, err := filepath.Rel(dstRoot, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if !opts.DryRun {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("link: mkdir %s: %w", dir, err)
			}
		}
		dirs.ready[dir] = true
		return nil
	}

	parent := filepath.Dir(dir)
	if err := ensureRealDir(parent, dstRoot, cellarRoot, opts, dirs); err != nil {
		return err
	}
	if oldParent, unwound := dirs.dryUnwound[parent]; opts.DryRun && unwound {
		// Everything under a would-be-unwound dir is virtual; checking the
		// real FS would look through the old symlink. The real run preserves
		// the old target's children and then re-unwinds any that are dirs —
		// mirror that cascade so deeper previews stay accurate.
		oldTarget := ""
		if oldParent != "" {
			cand := filepath.Join(oldParent, filepath.Base(dir))
			if st, err := os.Stat(cand); err == nil {
				if st.IsDir() {
					oldTarget = cand
				} else if !opts.Overwrite {
					return &LinkCollisionError{Target: dir, WantLink: "(directory)"}
				}
			}
		}
		dirs.dryUnwound[dir] = oldTarget
		dirs.ready[dir] = true
		return nil
	}

	st, statErr := os.Lstat(dir)
	switch {
	case errors.Is(statErr, fs.ErrNotExist):
		if !opts.DryRun {
			err := os.Mkdir(dir, 0o755)
			if err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("link: mkdir %s: %w", dir, err)
			}
			if errors.Is(err, fs.ErrExist) {
				// Raced-in entry between Lstat and Mkdir. Trusting it blind
				// would let a raced-in symlink route every leaf write through
				// an unvetted target — re-check and only accept a real dir.
				st2, err2 := os.Lstat(dir)
				if err2 != nil {
					return fmt.Errorf("link: stat %s: %w", dir, err2)
				}
				if st2.Mode()&os.ModeSymlink != 0 || !st2.IsDir() {
					return &LinkCollisionError{Target: dir, WantLink: "(directory)"}
				}
			}
		}
	case statErr != nil:
		return fmt.Errorf("link: stat %s: %w", dir, statErr)
	case st.Mode()&os.ModeSymlink != 0:
		oldTarget, err := unwindDirSymlink(dir, cellarRoot, opts)
		if err != nil {
			return err
		}
		if opts.DryRun {
			dirs.dryUnwound[dir] = oldTarget
		}
	case st.IsDir():
		// Already a real directory.
	default:
		// Regular file where a directory must go — user data, never clobber.
		return &LinkCollisionError{Target: dir, WantLink: "(directory)"}
	}
	dirs.ready[dir] = true
	return nil
}

// previewLeafUnderUnwound is linkOne's DryRun stand-in for leaves whose
// parent dir would be unwound: the preserved child with the same name (if
// any) is what the real run's linkOne would find. Keeps preview verdicts —
// no-op, create, collide — identical to the real run.
func previewLeafUnderUnwound(dst, cellarFile, oldDir string, opts LinkOptions) (bool, error) {
	if oldDir == "" {
		return true, nil // dangling/non-dir old target → nothing preserved
	}
	oldChild := filepath.Join(oldDir, filepath.Base(dst))
	if _, err := os.Lstat(oldChild); err != nil {
		return true, nil // no preserved sibling with this name
	}
	if oldChild == cellarFile {
		return false, nil // preserved link already points at us → no-op
	}
	if !opts.Overwrite {
		relDest, err := filepath.Rel(filepath.Dir(dst), cellarFile)
		if err != nil {
			relDest = cellarFile
		}
		exist, err := filepath.Rel(filepath.Dir(dst), oldChild)
		if err != nil {
			exist = oldChild
		}
		return false, &LinkCollisionError{Target: dst, ExistingLink: exist, WantLink: relDest}
	}
	return true, nil
}

// unwindDirSymlink replaces a directory symlink pointing into the Cellar
// with a real directory, re-linking the old target's children individually —
// brew's own conflict resolution when a shallow-linked subtree gains a second
// owner or gets upgraded. Preserved child links replicate state that existed
// before this Link call, so they are deliberately NOT journaled: rollback
// must not remove another keg's links.
//
// Only directory targets (unwind-preserve) and dangling targets (stale,
// replace) are unwind candidates. A LIVE non-directory target is another
// keg's leaf link sitting where we need a directory — a genuine conflict
// that collides unless Overwrite. Symlinks resolving outside the Cellar are
// user data and always collide. DryRun classifies but doesn't touch the
// filesystem; the returned string is the old target dir ("" when nothing
// would be preserved) for preview bookkeeping.
func unwindDirSymlink(dir, cellarRoot string, opts LinkOptions) (string, error) {
	raw, err := os.Readlink(dir)
	if err != nil {
		return "", fmt.Errorf("link: readlink %s: %w", dir, err)
	}
	resolved := raw
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(dir), raw)
	}
	resolved = filepath.Clean(resolved)
	if rel, err := filepath.Rel(cellarRoot, resolved); err != nil ||
		rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &LinkCollisionError{Target: dir, ExistingLink: raw, WantLink: "(directory)"}
	}

	st, statErr := os.Stat(dir) // follows the link
	isDir := statErr == nil && st.IsDir()
	switch {
	case statErr == nil && !isDir && !opts.Overwrite:
		return "", &LinkCollisionError{Target: dir, ExistingLink: raw, WantLink: "(directory)"}
	case statErr != nil && !errors.Is(statErr, fs.ErrNotExist):
		return "", fmt.Errorf("link: stat %s: %w", dir, statErr)
	}

	if opts.DryRun {
		if isDir {
			return resolved, nil
		}
		return "", nil
	}

	// Children of the old target get individual links so a still-installed
	// keg's shallow-linked files survive the unwind. Preserving them is the
	// whole point — a ReadDir failure aborts rather than silently orphaning
	// another keg's links.
	var children []os.DirEntry
	if isDir {
		children, err = os.ReadDir(resolved)
		if err != nil {
			return "", fmt.Errorf("link: read unwind target %s: %w", resolved, err)
		}
	}
	// Re-check immediately before the swap: a raced-in regular file must
	// not be deleted (never-clobber invariant).
	if st, err := os.Lstat(dir); err != nil || st.Mode()&os.ModeSymlink == 0 {
		return "", &LinkCollisionError{Target: dir, WantLink: "(directory)"}
	}
	if err := os.Remove(dir); err != nil {
		return "", fmt.Errorf("link: remove %s: %w", dir, err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", fmt.Errorf("link: mkdir %s: %w", dir, err)
	}
	for _, c := range children {
		childDst := filepath.Join(dir, c.Name())
		childSrc := filepath.Join(resolved, c.Name())
		relDest, err := filepath.Rel(dir, childSrc)
		if err != nil {
			relDest = childSrc
		}
		if err := os.Symlink(relDest, childDst); err != nil {
			return "", fmt.Errorf("link: preserve %s: %w", childDst, err)
		}
	}
	if isDir {
		return resolved, nil
	}
	return "", nil
}

// linkOne creates a symlink at target pointing at cellarFile. Returns
// (true, nil) when a new link was created (so the caller can append it to
// the journal), (false, nil) for idempotent no-ops (target already points
// at the right place), and (false, err) on collision or FS error.
//
// The created symlink's target is relative to filepath.Dir(target), which
// matches brew's behavior and keeps the prefix portable.
func linkOne(target, cellarFile string, opts LinkOptions) (bool, error) {
	relDest, err := filepath.Rel(filepath.Dir(target), cellarFile)
	if err != nil {
		// Fall back to absolute if Rel fails (different volumes, etc.).
		relDest = cellarFile
	}

	st, statErr := os.Lstat(target)
	switch {
	case statErr != nil && errors.Is(statErr, fs.ErrNotExist):
		// Clean slate — create it.
		if opts.DryRun {
			return true, nil
		}
		if err := os.Symlink(relDest, target); err != nil {
			return false, fmt.Errorf("link: symlink %s -> %s: %w", target, relDest, err)
		}
		return true, nil

	case statErr != nil:
		return false, fmt.Errorf("link: stat %s: %w", target, statErr)

	case st.Mode()&os.ModeSymlink != 0:
		// Existing symlink — compare destinations.
		cur, err := os.Readlink(target)
		if err != nil {
			return false, fmt.Errorf("link: readlink %s: %w", target, err)
		}
		if cur == relDest {
			return false, nil
		}
		// Also treat an absolute-target match as a no-op, so callers that
		// previously linked with absolute paths don't trip the collision
		// check on upgrade.
		if filepath.IsAbs(cur) && cur == cellarFile {
			return false, nil
		}
		if !opts.Overwrite {
			return false, &LinkCollisionError{
				Target:       target,
				ExistingLink: cur,
				WantLink:     relDest,
			}
		}
		if opts.DryRun {
			return true, nil
		}
		if err := os.Remove(target); err != nil {
			return false, fmt.Errorf("link: remove %s: %w", target, err)
		}
		if err := os.Symlink(relDest, target); err != nil {
			return false, fmt.Errorf("link: symlink %s -> %s: %w", target, relDest, err)
		}
		return true, nil

	default:
		// Regular file or directory — never clobber user data, even on
		// Overwrite.
		return false, &LinkCollisionError{
			Target:   target,
			WantLink: relDest,
		}
	}
}

// Unlink removes the symlinks created by a prior Link call. Pass the same
// slice that Link returned. Non-symlinks and missing links are silently
// skipped — rollback must be idempotent so a half-failed install can be
// retried safely.
func Unlink(symlinks []string) error {
	for _, p := range symlinks {
		st, err := os.Lstat(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("unlink: stat %s: %w", p, err)
		}
		if st.Mode()&os.ModeSymlink == 0 {
			// Not ours — someone replaced the symlink with a real file
			// between link and unlink. Leave it alone.
			continue
		}
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("unlink: remove %s: %w", p, err)
		}
	}
	return nil
}
