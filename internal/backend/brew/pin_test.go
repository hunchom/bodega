package brew

import (
	"context"
	"os"
	"testing"

	"github.com/hunchom/bodega/internal/backend"
)

func TestPinUnpinNative(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	prefix := t.TempDir()
	installWithStubbedPrefix(t, prefix)

	// Fake an installed keg.
	if err := os.MkdirAll(prefix+"/Cellar/foo/1.0", 0o755); err != nil {
		t.Fatal(err)
	}
	b := &Brew{}
	ctx := context.Background()

	// Pin → symlink under var/homebrew/pinned pointing at the keg.
	if err := b.Pin(ctx, "foo", true); err != nil {
		t.Fatalf("pin: %v", err)
	}
	link := prefix + "/var/homebrew/pinned/foo"
	tgt, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if tgt != prefix+"/Cellar/foo/1.0" {
		t.Fatalf("pin target = %q", tgt)
	}

	// listPinned must see it.
	pinned, ok := listPinned()
	if !ok {
		t.Fatal("listPinned failed")
	}
	if !hasPkg(pinned, "foo") {
		t.Fatalf("foo missing from pinned: %+v", pinned)
	}

	// Pinning a non-installed formula errors.
	if err := b.Pin(ctx, "ghost", true); err == nil {
		t.Fatal("expected not-installed error")
	}

	// Re-pin is idempotent.
	if err := b.Pin(ctx, "foo", true); err != nil {
		t.Fatalf("re-pin: %v", err)
	}

	// Unpin removes the link; unpin again is a no-op.
	if err := b.Pin(ctx, "foo", false); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("pin link survived unpin")
	}
	if err := b.Pin(ctx, "foo", false); err != nil {
		t.Fatalf("idempotent unpin: %v", err)
	}
}

func hasPkg(pkgs []backend.Package, name string) bool {
	for _, p := range pkgs {
		if p.Name == name {
			return true
		}
	}
	return false
}
