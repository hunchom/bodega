package brew

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
// TODO(perf): brew uses a "shallow then deep" strategy — if a directory
// subtree is entirely new it creates a single directory symlink instead of
// recursing. We always deep-link leaves, which is correct but produces more
// inodes than brew's native linker. Revisit once the rest of the native
// install path is proven.
func Link(prefix, cellarPkgDir string, opts LinkOptions) ([]string, error) {
	if prefix == "" {
		return nil, fmt.Errorf("link: empty prefix")
	}
	if cellarPkgDir == "" {
		return nil, fmt.Errorf("link: empty cellar dir")
	}

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
		made, err := linkWalk(srcRoot, dstRoot, opts)
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

// linkWalk recursively mirrors srcRoot's directory tree under dstRoot,
// creating a symlink at every leaf (regular file or symlink inside the
// cellar). Parent directories in the prefix are created with MkdirAll as
// needed. Returns the paths created, in creation order.
func linkWalk(srcRoot, dstRoot string, opts LinkOptions) ([]string, error) {
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
		if !opts.DryRun {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("link: mkdir %s: %w", filepath.Dir(dst), err)
			}
		}
		made, err := linkOne(dst, path, opts)
		if made {
			created = append(created, dst)
		}
		return err
	})
	return created, err
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
