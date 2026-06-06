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
	if rec[0].Incomplete {
		t.Fatal("completed tx flagged incomplete")
	}
}

// End must finalize even when the caller's ctx is already cancelled (Ctrl-C
// mid-transaction). Otherwise the terminal UPDATE never lands and history
// renders the aborted tx as ✓.
func TestEndFinalizesOnCancelledContext(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	ctx, cancel := context.WithCancel(context.Background())
	id, err := j.Begin(ctx, "upgrade", "yum upgrade", "1.0", "5.1.6")
	if err != nil {
		t.Fatal(err)
	}
	cancel() // simulate Ctrl-C before the operation finished

	if err := j.End(ctx, id, 1, nil); err != nil {
		t.Fatalf("End on cancelled ctx: %v", err)
	}

	got, err := j.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExitCode != 1 {
		t.Fatalf("exit code not recorded: %d", got.ExitCode)
	}
	if got.Incomplete {
		t.Fatal("tx still marked incomplete after End ran")
	}
}

// A transaction that was Begun but never Ended (hard kill) must report as
// incomplete, not as a clean exit-0 success.
func TestUnendedTransactionIsIncomplete(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	ctx := context.Background()
	id, err := j.Begin(ctx, "install", "yum install x", "1.0", "5.1.6")
	if err != nil {
		t.Fatal(err)
	}
	got, err := j.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Incomplete {
		t.Fatal("un-ended tx should be incomplete")
	}
}
