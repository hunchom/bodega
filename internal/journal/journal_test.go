package journal

import (
	"context"
	"path/filepath"
	"testing"
)

func TestJournalRoundtrip(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	ctx := context.Background()
	id, err := j.Begin(ctx, "install", "yum install ripgrep", "1.0", "5.1.6")
	if err != nil {
		t.Fatal(err)
	}

	err = j.End(ctx, id, 0, []TxPackage{
		{Name: "ripgrep", ToVersion: "14.1.0", Source: "formula", Action: "installed"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := j.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Packages) != 1 || got.Packages[0].Name != "ripgrep" {
		t.Fatalf("bad roundtrip: %+v", got)
	}

	rec, err := j.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec) != 1 {
		t.Fatalf("expected 1 tx, got %d", len(rec))
	}
}
