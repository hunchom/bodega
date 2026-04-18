package brew

import (
	"bytes"
	"context"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Bottle placeholders brew embeds into every tarball so the same bits
// can be relocated to either /usr/local (Intel) or /opt/homebrew
// (Apple Silicon) without re-linking. For text files we do a plain
// gsub-style byte replace — matching brew's own `Relocation#replace_text!`
// — and for Mach-O files we route through install_name_tool instead of
// touching bytes, letting it resize load commands transparently.
const (
	placeholderPrefix  = "@@HOMEBREW_PREFIX@@"
	placeholderCellar  = "@@HOMEBREW_CELLAR@@"
	placeholderOpt     = "@@HOMEBREW_OPT@@"
	placeholderLibrary = "@@HOMEBREW_LIBRARY@@"
	placeholderPerl    = "@@HOMEBREW_PERL@@"
)

// maxPatchFileSize caps the in-memory rewrite size. Any regular file
// bigger than this is skipped on the theory that brew bottles don't
// ship half-gig blobs with placeholder strings baked into them, and
// that a runaway file means something is wrong (corrupt tarball, user
// stashed a VM image in the Cellar, etc.). Callers that genuinely need
// to rewrite huge files should stream instead; for now we prefer to
// surface the surprise.
const maxPatchFileSize = 512 * 1024 * 1024

// RelocateOptions controls the concrete paths each placeholder expands
// to. Defaults for Opt and Library derive from Prefix; Perl defaults
// to the system perl because bottles that embed #!@@HOMEBREW_PERL@@
// shebangs tend to be happy with /usr/bin/perl on macOS.
type RelocateOptions struct {
	Prefix  string // e.g. /opt/homebrew
	Cellar  string // e.g. /opt/homebrew/Cellar
	Opt     string // defaults to Prefix+"/opt"
	Library string // defaults to Prefix+"/Library"
	Perl    string // defaults to /usr/bin/perl
}

// machOFixups tracks whether install_name_tool and codesign are
// available on PATH. The check runs exactly once per process; on
// non-darwin hosts (Linux CI) both binaries are missing and Relocate
// falls through to a text-only mode.
var (
	machOFixupsOnce sync.Once
	machOFixupsOK   bool
)

// MachOFixupsAvailable reports whether install_name_tool and codesign
// are present on PATH. The answer is cached for the lifetime of the
// process since PATH rarely mutates mid-run and the cost of repeat
// lookups adds up when patching a Cellar tree with hundreds of binaries.
func MachOFixupsAvailable() bool {
	machOFixupsOnce.Do(func() {
		_, errA := exec.LookPath("install_name_tool")
		_, errB := exec.LookPath("codesign")
		machOFixupsOK = errA == nil && errB == nil
	})
	return machOFixupsOK
}

// Extract unpacks a bottle tar.gz at tarGzPath into destDir and returns
// the absolute path to the inner <pkg>/<version> directory that brew
// bottles canonically wrap their payload in. destDir must already exist.
// We shell out to system tar because gnutar's handling of hardlinks,
// sparse files, xattrs, and resource forks has had three decades to
// settle and a Go reimplementation would be all downside.
func Extract(ctx context.Context, tarGzPath, destDir string) (string, error) {
	info, err := os.Stat(destDir)
	if err != nil {
		return "", fmt.Errorf("extract: stat dest: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("extract: dest %q is not a directory", destDir)
	}

	// Snapshot the top-level entries so we can diff after extraction and
	// identify the <pkg> directory the tarball introduced. Bottles are
	// structured as <pkg>/<version>/..., so we want to return the
	// <pkg>/<version> path on success.
	before, err := topLevelNames(destDir)
	if err != nil {
		return "", fmt.Errorf("extract: snapshot dest: %w", err)
	}

	cmd := exec.CommandContext(ctx, "tar", "-xzf", tarGzPath, "-C", destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("extract: tar -xzf %s: %w: %s", tarGzPath, err, trimOutput(out))
	}

	after, err := topLevelNames(destDir)
	if err != nil {
		return "", fmt.Errorf("extract: rescan dest: %w", err)
	}
	var pkgName string
	for name := range after {
		if _, existed := before[name]; existed {
			continue
		}
		// Prefer the first newly-introduced directory. Bottles always
		// produce a single top-level dir; if tar added a stray regular
		// file we'd rather error below than silently pick it.
		if after[name] {
			pkgName = name
			break
		}
	}
	if pkgName == "" {
		return "", fmt.Errorf("extract: tarball %s did not produce a new top-level directory in %s", tarGzPath, destDir)
	}

	pkgDir := filepath.Join(destDir, pkgName)
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return "", fmt.Errorf("extract: read pkg dir: %w", err)
	}
	// The <pkg> dir should contain exactly one <version> sub-dir. If
	// for whatever reason it doesn't we return the pkg dir itself so
	// the caller at least has a handle on the extracted payload.
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(pkgDir, e.Name()), nil
		}
	}
	return pkgDir, nil
}

// topLevelNames returns a map of entries directly under dir, with the
// value indicating whether each entry is a directory. Used by Extract
// to diff "before" and "after" so we can identify the package the
// tarball dropped in without relying on conventions that might drift.
func topLevelNames(dir string) (map[string]bool, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ents))
	for _, e := range ents {
		out[e.Name()] = e.IsDir()
	}
	return out, nil
}

// Relocate walks packageRoot, rewrites placeholder strings in every
// non-Mach-O regular file via plain gsub-style byte replacement, and
// fixes Mach-O dylib IDs + LC_LOAD_DYLIB entries via install_name_tool
// followed by an ad-hoc re-codesign. Mach-O files are NOT byte-patched;
// install_name_tool handles load-command resizing on its own, which is
// the only way to support prefixes longer than the placeholder
// (e.g. /opt/homebrew/Cellar at 20 bytes vs @@HOMEBREW_CELLAR@@ at 19).
//
// If the host lacks install_name_tool or codesign (e.g. Linux CI) the
// Mach-O fixup phase is skipped; text replacement still happens so
// scripts and pkg-config files get the correct paths. Callers can
// distinguish the two modes via MachOFixupsAvailable().
func Relocate(ctx context.Context, packageRoot string, opts RelocateOptions) error {
	opts = opts.withDefaults()
	if err := opts.validate(); err != nil {
		return err
	}
	repls := buildReplacements(opts)

	machOFiles := make([]string, 0, 32)
	walkErr := filepath.WalkDir(packageRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Symlinks never carry placeholder bytes in their contents;
		// filepath.WalkDir surfaces the link itself, not its target.
		// Rewriting the link target in place would require
		// os.Readlink/os.Symlink and isn't something brew bottles
		// rely on — skip.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		isMachO, patchErr := patchFile(path, repls)
		if patchErr != nil {
			return fmt.Errorf("patch %s: %w", path, patchErr)
		}
		if isMachO {
			machOFiles = append(machOFiles, path)
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	if !MachOFixupsAvailable() || len(machOFiles) == 0 {
		return nil
	}
	for _, f := range machOFiles {
		if err := fixMachO(ctx, f, opts); err != nil {
			return fmt.Errorf("mach-o fixup %s: %w", f, err)
		}
	}
	return nil
}

// withDefaults fills in derived paths for any zero fields on opts. We
// don't mutate the caller's struct; the returned copy is what the rest
// of Relocate operates on.
func (o RelocateOptions) withDefaults() RelocateOptions {
	if o.Opt == "" && o.Prefix != "" {
		o.Opt = o.Prefix + "/opt"
	}
	if o.Library == "" && o.Prefix != "" {
		o.Library = o.Prefix + "/Library"
	}
	if o.Perl == "" {
		o.Perl = "/usr/bin/perl"
	}
	return o
}

// validate rejects a RelocateOptions with any empty field. Doing the
// replacements with "" would silently delete every placeholder
// occurrence, which is almost certainly not what the caller wanted —
// fail loudly instead.
func (o RelocateOptions) validate() error {
	for name, v := range map[string]string{
		"Prefix":  o.Prefix,
		"Cellar":  o.Cellar,
		"Opt":     o.Opt,
		"Library": o.Library,
		"Perl":    o.Perl,
	} {
		if v == "" {
			return fmt.Errorf("relocate: %s must be non-empty", name)
		}
	}
	return nil
}

// replacement maps one placeholder string to its expansion. Text files
// use bytes.ReplaceAll, so the new slice can be any length — matching
// brew's Relocation#replace_text! behaviour.
type replacement struct {
	old []byte
	new []byte
}

// buildReplacements returns the five placeholder → path mappings in
// descending order of placeholder length. Ordering doesn't matter for
// our specific placeholder set (none is a prefix of another) but we
// keep the sort as a forward-compatibility guard in case new
// placeholders get added that overlap.
func buildReplacements(opts RelocateOptions) []replacement {
	// Order matches brew's sorted_keys in keg_relocate.rb: longest
	// placeholder first so overlapping keys resolve deterministically.
	return []replacement{
		{old: []byte(placeholderLibrary), new: []byte(opts.Library)},
		{old: []byte(placeholderCellar), new: []byte(opts.Cellar)},
		{old: []byte(placeholderPrefix), new: []byte(opts.Prefix)},
		{old: []byte(placeholderOpt), new: []byte(opts.Opt)},
		{old: []byte(placeholderPerl), new: []byte(opts.Perl)},
	}
}

// patchFile reads path, and — only if the file is NOT a Mach-O binary —
// replaces every placeholder occurrence with its expansion via
// bytes.ReplaceAll, then writes the result back via a rename-through-
// temp so an interrupted run never leaves a half-patched file. Mach-O
// files are reported to the caller but their bytes are left untouched
// so install_name_tool can handle them cleanly.
//
// Files without any placeholder are left untouched on disk — we detect
// that cheaply via bytes.Contains so patching a Cellar of a hundred
// thousand files doesn't rewrite every single one. The boolean return
// reports whether the file is a Mach-O (checked via magic bytes),
// regardless of whether any placeholder match was found, so the caller
// can queue it for install_name_tool + codesign.
func patchFile(path string, repls []replacement) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if info.Size() > maxPatchFileSize {
		// Large files get neither string-patched nor Mach-O-fixed up.
		// Real bottles don't have half-gig Mach-O binaries, so this
		// is conservative-safe; we'd rather skip than OOM the host.
		return false, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if isMachOMagic(data) {
		// Leave the bytes alone and let install_name_tool handle
		// placeholder rewrites via load-command edits. Byte-patching a
		// Mach-O would break any prefix longer than the placeholder.
		return true, nil
	}

	patched, changed := applyReplacements(data, repls)
	if !changed {
		return false, nil
	}

	tmp := path + ".bodega-tmp"
	if err := os.WriteFile(tmp, patched, info.Mode().Perm()); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp, info.Mode().Perm()); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return false, nil
}

// applyReplacements returns data with every occurrence of each
// replacement's old bytes swapped for its new bytes. The second return
// value reports whether anything actually changed; callers use it to
// skip a write syscall on files that don't need rewriting.
//
// We avoid allocating a new buffer when the input contains no
// placeholders at all, which is the common case for the vast majority
// of files in a bottle.
func applyReplacements(data []byte, repls []replacement) ([]byte, bool) {
	changed := false
	current := data
	for _, r := range repls {
		if !bytes.Contains(current, r.old) {
			continue
		}
		current = bytes.ReplaceAll(current, r.old, r.new)
		changed = true
	}
	if !changed {
		return data, false
	}
	return current, true
}

// isMachOMagic returns true when the first four bytes of b match one of
// the Mach-O magic numbers. Covers thin 32/64-bit in both endiannesses
// plus fat (universal) binaries, which are always stored big-endian on
// disk but also exist in a 64-bit variant (0xcafebabf) that debug/macho
// doesn't expose as a named constant.
func isMachOMagic(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	be := binary.BigEndian.Uint32(b[:4])
	switch be {
	case macho.Magic32, macho.Magic64, macho.MagicFat, 0xcafebabf:
		return true
	}
	le := binary.LittleEndian.Uint32(b[:4])
	switch le {
	case macho.Magic32, macho.Magic64:
		return true
	}
	return false
}

// fixMachO re-points every dylib reference in a Mach-O file at the
// post-relocation prefix. Two passes: first rewrite the dylib's own
// install name (LC_ID_DYLIB) so downstream binaries link against the
// right absolute path, then rewrite every LC_LOAD_DYLIB whose target
// still references a brew placeholder. install_name_tool refuses to
// touch files whose code signature it hasn't invalidated, so we
// unconditionally re-sign at the end.
func fixMachO(ctx context.Context, path string, opts RelocateOptions) error {
	if strings.HasSuffix(path, ".dylib") {
		id := dylibIDFor(path, opts)
		if id != "" {
			if out, err := runTool(ctx, "install_name_tool", "-id", id, path); err != nil {
				return fmt.Errorf("install_name_tool -id: %w: %s", err, trimOutput(out))
			}
		}
	}

	deps, err := listMachODeps(ctx, path)
	if err != nil {
		return err
	}
	for _, dep := range deps {
		rewritten := rewriteMachOPath(dep, opts)
		if rewritten == "" || rewritten == dep {
			continue
		}
		if out, err := runTool(ctx, "install_name_tool", "-change", dep, rewritten, path); err != nil {
			return fmt.Errorf("install_name_tool -change %s %s: %w: %s", dep, rewritten, err, trimOutput(out))
		}
	}

	// Ad-hoc re-sign. --preserve-metadata keeps any entitlements /
	// flags the original bottle baked in; macOS 11+ refuses to load
	// binaries whose signature no longer matches their bytes, so this
	// step is not optional on Apple Silicon.
	if out, err := runTool(ctx, "codesign", "--force", "--sign", "-", "--preserve-metadata=entitlements,requirements,flags", path); err != nil {
		// A corrupt / detached signature is not fatal for the overall
		// install — the user can re-sign later — but we still want to
		// surface it. The higher-level installer can decide whether
		// to degrade to a warning based on context.
		return fmt.Errorf("codesign: %w: %s", err, trimOutput(out))
	}
	return nil
}

// dylibIDFor computes the absolute install-name this dylib should
// carry under the final prefix. We walk up from packageRoot
// conventions: given .../Cellar/<pkg>/<ver>/lib/foo.dylib we want
// <Cellar>/<pkg>/<ver>/lib/foo.dylib. If the path doesn't contain
// "/Cellar/" we can't safely rewrite; return empty and let the caller
// skip -id.
func dylibIDFor(path string, opts RelocateOptions) string {
	idx := strings.Index(path, "/Cellar/")
	if idx < 0 {
		return ""
	}
	return opts.Cellar + path[idx+len("/Cellar"):]
}

// listMachODeps runs `otool -L` and returns the list of dependent
// library install-names the binary loads. The first line of otool -L
// output is the file path; subsequent lines carry each dep with its
// compatibility/current versions in parentheses which we strip.
func listMachODeps(ctx context.Context, path string) ([]string, error) {
	out, err := runTool(ctx, "otool", "-L", path)
	if err != nil {
		// otool on a non-Mach-O file exits non-zero; the caller
		// already gated on magic bytes, so any error here is a real
		// failure.
		return nil, fmt.Errorf("otool -L: %w: %s", err, trimOutput(out))
	}
	lines := strings.Split(string(out), "\n")
	deps := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			// First line echoes the filename; skip.
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Each dep line is:
		//   "<path> (compatibility version X, current version Y)"
		if paren := strings.Index(line, " ("); paren > 0 {
			line = line[:paren]
		}
		deps = append(deps, line)
	}
	return deps, nil
}

// rewriteMachOPath maps a dylib reference emitted by brew into its
// post-relocation absolute path. We handle the three prefixes brew
// actually uses in LC_LOAD_DYLIB entries: @@HOMEBREW_CELLAR@@,
// @@HOMEBREW_OPT@@, and @@HOMEBREW_PREFIX@@. Any reference that
// doesn't start with one of these is left alone; that covers system
// libraries (/usr/lib/...), @rpath/@loader_path entries, and already-
// relocated absolute paths (e.g. when the tarball was previously
// patched by a sibling tool).
func rewriteMachOPath(dep string, opts RelocateOptions) string {
	switch {
	case strings.HasPrefix(dep, placeholderCellar):
		return opts.Cellar + dep[len(placeholderCellar):]
	case strings.HasPrefix(dep, placeholderOpt):
		return opts.Opt + dep[len(placeholderOpt):]
	case strings.HasPrefix(dep, placeholderPrefix):
		return opts.Prefix + dep[len(placeholderPrefix):]
	}
	return dep
}

// runTool is the single exec.CommandContext call site so we can keep
// stdout/stderr capture consistent across install_name_tool, otool,
// and codesign. Combined output is returned verbatim on error so
// trimOutput can stitch it into the error message; on success we
// still return it in case the caller wants to inspect (e.g. otool's
// library listing).
func runTool(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

// trimOutput shortens tool output to the last few hundred bytes so
// error messages stay readable. Large binaries occasionally provoke
// pages of otool warnings that dwarf the actual failure reason.
func trimOutput(b []byte) string {
	const maxTail = 512
	b = bytes.TrimRight(b, "\n\r\t ")
	if len(b) <= maxTail {
		return string(b)
	}
	return "..." + string(b[len(b)-maxTail:])
}
