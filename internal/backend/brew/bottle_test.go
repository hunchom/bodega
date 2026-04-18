package brew

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// makeBottleTarGz builds a minimal bottle-shaped tar.gz in memory. Each
// file in contents is written under pkg/version/relpath, matching the
// top-level layout brew itself emits. Returned byte slice is gzip'd;
// callers write it to disk before handing the path to Extract.
func makeBottleTarGz(t *testing.T, pkg, version string, contents map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// Emit the directory entries brew's tarballs carry; tar -xzf is
	// happy either way but explicit dirs make `tar tf` output look
	// canonical.
	dirs := map[string]struct{}{
		pkg:                         {},
		filepath.Join(pkg, version): {},
	}
	for rel := range contents {
		dir := filepath.Dir(rel)
		for dir != "." && dir != "/" {
			dirs[filepath.Join(pkg, version, dir)] = struct{}{}
			dir = filepath.Dir(dir)
		}
	}
	for d := range dirs {
		if err := tw.WriteHeader(&tar.Header{
			Name:     d + "/",
			Mode:     0o755,
			Typeflag: tar.TypeDir,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for rel, body := range contents {
		full := filepath.Join(pkg, version, rel)
		if err := tw.WriteHeader(&tar.Header{
			Name: full,
			Mode: 0o644,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractProducesPkgVersionDir(t *testing.T) {
	dest := t.TempDir()
	tarBytes := makeBottleTarGz(t, "ripgrep", "14.1.1", map[string]string{
		"README":      "hello\n",
		"bin/ripgrep": "#!/bin/sh\necho hi\n",
		"lib/foo.txt": "@@HOMEBREW_PREFIX@@/share\n",
	})
	archive := filepath.Join(dest, "ripgrep-14.1.1.tar.gz")
	if err := os.WriteFile(archive, tarBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	extractDir := filepath.Join(dest, "cellar")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := Extract(context.Background(), archive, extractDir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	want := filepath.Join(extractDir, "ripgrep", "14.1.1")
	if root != want {
		t.Fatalf("extracted root=%q want=%q", root, want)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "ripgrep")); err != nil {
		t.Fatalf("expected bin/ripgrep: %v", err)
	}
}

func TestExtractRejectsMissingDest(t *testing.T) {
	_, err := Extract(context.Background(), "/nonexistent.tar.gz", "/definitely/does/not/exist/anywhere")
	if err == nil {
		t.Fatal("expected error for missing destDir")
	}
}

func TestBuildReplacementsPopulatesAllFive(t *testing.T) {
	opts := RelocateOptions{
		Prefix: "/opt/homebrew",
		Cellar: "/opt/homebrew/Cellar",
	}.withDefaults()
	repls := buildReplacements(opts)
	if len(repls) != 5 {
		t.Fatalf("got %d replacements, want 5", len(repls))
	}
	// Every placeholder must appear as an old-key exactly once.
	wantOld := map[string]bool{
		placeholderPrefix:  false,
		placeholderCellar:  false,
		placeholderOpt:     false,
		placeholderLibrary: false,
		placeholderPerl:    false,
	}
	for _, r := range repls {
		k := string(r.old)
		if _, ok := wantOld[k]; !ok {
			t.Errorf("unexpected placeholder in replacements: %q", k)
			continue
		}
		if wantOld[k] {
			t.Errorf("duplicate placeholder: %q", k)
		}
		wantOld[k] = true
		if len(r.new) == 0 {
			t.Errorf("empty replacement for %q", k)
		}
	}
	for k, saw := range wantOld {
		if !saw {
			t.Errorf("missing replacement for %q", k)
		}
	}
}

func TestValidateRejectsEmptyField(t *testing.T) {
	err := RelocateOptions{
		Prefix:  "",
		Cellar:  "/c",
		Opt:     "/o",
		Library: "/l",
		Perl:    "/p",
	}.validate()
	if err == nil {
		t.Fatal("expected error for empty Prefix")
	}
}

func TestApplyReplacementsPlainReplace(t *testing.T) {
	// Exercise the Apple Silicon case explicitly: /opt/homebrew/Cellar
	// is 20 bytes vs the 19-byte placeholder, so the replacement
	// genuinely grows the file. That's now the required behaviour —
	// matching brew's own Relocation#replace_text!.
	opts := RelocateOptions{
		Prefix: "/opt/homebrew",
		Cellar: "/opt/homebrew/Cellar",
	}.withDefaults()
	repls := buildReplacements(opts)

	in := []byte("cd @@HOMEBREW_PREFIX@@/bin && @@HOMEBREW_CELLAR@@/zlib\n")
	out, changed := applyReplacements(in, repls)
	if !changed {
		t.Fatal("expected changed=true")
	}
	want := []byte("cd /opt/homebrew/bin && /opt/homebrew/Cellar/zlib\n")
	if !bytes.Equal(out, want) {
		t.Fatalf("got %q\nwant %q", out, want)
	}
	if bytes.Contains(out, []byte("@@HOMEBREW")) {
		t.Fatalf("placeholder still present: %q", out)
	}
}

func TestApplyReplacementsUnchangedFile(t *testing.T) {
	opts := RelocateOptions{
		Prefix: "/opt/homebrew",
		Cellar: "/opt/homebrew/Cellar",
	}.withDefaults()
	repls := buildReplacements(opts)

	in := []byte("no placeholder here, just plain text")
	out, changed := applyReplacements(in, repls)
	if changed {
		t.Fatal("expected changed=false on clean input")
	}
	if !bytes.Equal(in, out) {
		t.Fatal("clean input should round-trip byte-for-byte")
	}
}

func TestApplyReplacementsLengthGrowsOnAppleSilicon(t *testing.T) {
	// Confirms that the Cellar replacement genuinely expands the file
	// when the prefix is longer than the placeholder — regression guard
	// against accidentally re-introducing length preservation.
	opts := RelocateOptions{
		Prefix: "/opt/homebrew",
		Cellar: "/opt/homebrew/Cellar",
	}.withDefaults()
	repls := buildReplacements(opts)
	in := []byte("@@HOMEBREW_CELLAR@@")
	out, _ := applyReplacements(in, repls)
	if len(out) <= len(in) {
		t.Fatalf("expected replacement to grow file, got len=%d (in=%d)", len(out), len(in))
	}
	if string(out) != "/opt/homebrew/Cellar" {
		t.Fatalf("unexpected content: %q", out)
	}
}

func TestIsMachOMagic(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{"macho64_le", leU32(0xfeedfacf), true},
		{"macho64_be", beU32(0xfeedfacf), true},
		{"macho32_le", leU32(0xfeedface), true},
		{"fat_be", beU32(0xcafebabe), true},
		{"fat64_be", beU32(0xcafebabf), true},
		{"elf", []byte{0x7f, 'E', 'L', 'F'}, false},
		{"plain_text", []byte("Hello"), false},
		{"short", []byte{0xfe}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isMachOMagic(c.in); got != c.want {
				t.Fatalf("isMachOMagic(%x) = %v want %v", c.in, got, c.want)
			}
		})
	}
}

// leU32 returns v encoded little-endian in a 4-byte slice. Used to
// construct test payloads whose first four bytes are a specific Mach-O
// magic value without worrying about host endianness.
func leU32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

// beU32 is leU32's big-endian twin.
func beU32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func TestPatchFileSkipsLargeFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big")
	// Create a sparse file bigger than maxPatchFileSize via Truncate;
	// we never actually write a half-gig blob. Truncate sets the
	// reported size without allocating all the bytes on most
	// filesystems.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxPatchFileSize + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()

	opts := RelocateOptions{
		Prefix: "/opt/homebrew",
		Cellar: "/opt/homebrew/Cellar",
	}.withDefaults()
	repls := buildReplacements(opts)
	isMachO, err := patchFile(path, repls)
	if err != nil {
		t.Fatalf("patchFile: %v", err)
	}
	if isMachO {
		t.Fatal("truncated zero-filled file should not register as Mach-O")
	}
	// Verify the file wasn't rewritten.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != maxPatchFileSize+1 {
		t.Fatalf("size changed: %d", info.Size())
	}
}

func TestPatchFileNonMachOPlainText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	body := "Hello, @@HOMEBREW_PREFIX@@/share\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := RelocateOptions{
		Prefix: "/opt/homebrew",
		Cellar: "/opt/homebrew/Cellar",
	}.withDefaults()
	repls := buildReplacements(opts)
	isMachO, err := patchFile(path, repls)
	if err != nil {
		t.Fatalf("patchFile: %v", err)
	}
	if isMachO {
		t.Fatal("plain text file should not be flagged Mach-O")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("/opt/homebrew")) {
		t.Fatalf("prefix not patched: %q", got)
	}
	if bytes.Contains(got, []byte("@@HOMEBREW_PREFIX@@")) {
		t.Fatalf("placeholder still present: %q", got)
	}
	// File size shrinks by (len(placeholder) - len(replacement)) per
	// occurrence; 19 - 13 = 6 bytes shorter here.
	if len(got) != len(body)-6 {
		t.Fatalf("unexpected length: got %d want %d", len(got), len(body)-6)
	}
}

func TestPatchFileMachODoesNotModifyBytes(t *testing.T) {
	// Mach-O files must be left byte-identical so install_name_tool can
	// handle load-command resizing cleanly. This is the core invariant
	// that makes Apple Silicon's 20-byte Cellar work at all.
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-macho")
	// Construct a fake Mach-O: 64-bit little-endian magic, then some
	// bytes that include a placeholder. isMachOMagic only checks the
	// first four bytes so this is enough to trigger the skip branch.
	body := append(leU32(0xfeedfacf), []byte("@@HOMEBREW_PREFIX@@ do not touch")...)
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
	opts := RelocateOptions{
		Prefix: "/opt/homebrew",
		Cellar: "/opt/homebrew/Cellar",
	}.withDefaults()
	repls := buildReplacements(opts)
	isMachO, err := patchFile(path, repls)
	if err != nil {
		t.Fatalf("patchFile: %v", err)
	}
	if !isMachO {
		t.Fatal("expected Mach-O flag on fake dylib")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("patchFile modified Mach-O bytes; expected no change")
	}
}

func TestRewriteMachOPath(t *testing.T) {
	// rewriteMachOPath feeds install_name_tool -change, which accepts
	// arbitrary-length absolute paths, so it isn't subject to any
	// length-preservation constraint.
	opts := RelocateOptions{
		Prefix: "/opt/homebrew",
		Cellar: "/opt/homebrew/Cellar",
	}.withDefaults()
	cases := []struct {
		in, want string
	}{
		{"@@HOMEBREW_CELLAR@@/zlib/1.3/lib/libz.1.dylib", "/opt/homebrew/Cellar/zlib/1.3/lib/libz.1.dylib"},
		{"@@HOMEBREW_OPT@@/pcre2/lib/libpcre2-8.0.dylib", "/opt/homebrew/opt/pcre2/lib/libpcre2-8.0.dylib"},
		{"@@HOMEBREW_PREFIX@@/lib/libfoo.dylib", "/opt/homebrew/lib/libfoo.dylib"},
		{"/usr/lib/libSystem.B.dylib", "/usr/lib/libSystem.B.dylib"},
		{"@rpath/libfoo.dylib", "@rpath/libfoo.dylib"},
	}
	for _, c := range cases {
		if got := rewriteMachOPath(c.in, opts); got != c.want {
			t.Errorf("rewriteMachOPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDylibIDFor(t *testing.T) {
	opts := RelocateOptions{
		Prefix: "/opt/homebrew",
		Cellar: "/opt/homebrew/Cellar",
	}.withDefaults()
	// The pre-relocation path lives somewhere under a Cellar — the
	// helper locates the /Cellar/ segment and re-anchors it at
	// opts.Cellar.
	in := "/private/tmp/bodega/Cellar/zlib/1.3/lib/libz.1.dylib"
	want := "/opt/homebrew/Cellar/zlib/1.3/lib/libz.1.dylib"
	if got := dylibIDFor(in, opts); got != want {
		t.Fatalf("dylibIDFor: got %q want %q", got, want)
	}
	// No /Cellar/ segment → empty return, caller skips -id.
	if got := dylibIDFor("/opt/other/foo.dylib", opts); got != "" {
		t.Fatalf("expected empty ID, got %q", got)
	}
}

func TestRelocateEndToEnd(t *testing.T) {
	// Extract a synthetic bottle, relocate it, and verify the
	// placeholder bytes in the two text files got rewritten to full
	// paths — including the 20-byte Apple Silicon /opt/homebrew/Cellar
	// that doesn't fit inside the 19-byte @@HOMEBREW_CELLAR@@
	// placeholder. Mach-O fixups are skipped automatically on Linux CI
	// and, even on macOS, our synthetic text files don't carry a
	// Mach-O magic so fixMachO walks zero binaries.
	dest := t.TempDir()
	tarBytes := makeBottleTarGz(t, "foo", "1.2.3", map[string]string{
		"bin/foo.sh":   "#!/bin/sh\nexec @@HOMEBREW_PREFIX@@/bin/real-foo\n",
		"etc/foo.conf": "cellar=@@HOMEBREW_CELLAR@@/foo/1.2.3\n",
		"clean.txt":    "no placeholders here\n",
	})
	archive := filepath.Join(dest, "foo.tar.gz")
	if err := os.WriteFile(archive, tarBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	cellarRoot := filepath.Join(dest, "Cellar")
	if err := os.MkdirAll(cellarRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	pkgRoot, err := Extract(context.Background(), archive, cellarRoot)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	err = Relocate(context.Background(), pkgRoot, RelocateOptions{
		Prefix: "/opt/homebrew",
		Cellar: "/opt/homebrew/Cellar",
	})
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}

	shBody, err := os.ReadFile(filepath.Join(pkgRoot, "bin", "foo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(shBody, []byte("/opt/homebrew")) {
		t.Fatalf("prefix missing after relocate: %q", shBody)
	}
	if bytes.Contains(shBody, []byte("@@HOMEBREW_PREFIX@@")) {
		t.Fatalf("placeholder still present after relocate: %q", shBody)
	}

	cfgBody, err := os.ReadFile(filepath.Join(pkgRoot, "etc", "foo.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(cfgBody, []byte("/opt/homebrew/Cellar/foo/1.2.3")) {
		t.Fatalf("cellar missing: %q", cfgBody)
	}

	// The clean file should be untouched, byte for byte.
	cleanBody, err := os.ReadFile(filepath.Join(pkgRoot, "clean.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(cleanBody) != "no placeholders here\n" {
		t.Fatalf("clean file mutated: %q", cleanBody)
	}
}

func TestMachOFixupsAvailableIsCached(t *testing.T) {
	// We can't meaningfully assert the boolean value — it's host-
	// dependent — but we can verify two back-to-back calls return the
	// same answer and don't panic. The sync.Once guard is what we're
	// really exercising here.
	a := MachOFixupsAvailable()
	b := MachOFixupsAvailable()
	if a != b {
		t.Fatalf("MachOFixupsAvailable inconsistent: %v vs %v", a, b)
	}
}
