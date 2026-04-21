package journal

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPackageLogOrderAndFields(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	ctx := context.Background()

	txs := []struct {
		verb    string
		cmdline string
		exit    int
		pkg     TxPackage
	}{
		{"install", "yum install hello", 0, TxPackage{Name: "hello", ToVersion: "1.0.0", Source: "formula", Action: "installed"}},
		{"upgrade", "yum upgrade hello", 0, TxPackage{Name: "hello", FromVersion: "1.0.0", ToVersion: "1.1.0", Source: "formula", Action: "upgraded"}},
		{"upgrade", "yum upgrade hello", 0, TxPackage{Name: "hello", FromVersion: "1.1.0", ToVersion: "1.2.0", Source: "formula", Action: "upgraded"}},
	}

	ids := make([]int64, 0, len(txs))
	for _, row := range txs {
		id, err := j.Begin(ctx, row.verb, row.cmdline, "1.0", "5.1.6")
		if err != nil {
			t.Fatal(err)
		}
		if err := j.End(ctx, id, row.exit, []TxPackage{row.pkg}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	events, err := j.PackageLog(ctx, "hello", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	if events[0].TxID != ids[2] || events[1].TxID != ids[1] || events[2].TxID != ids[0] {
		t.Fatalf("expected most-recent-first ordering, got tx ids %d %d %d",
			events[0].TxID, events[1].TxID, events[2].TxID)
	}

	if events[0].Action != "upgraded" || events[0].FromVersion != "1.1.0" || events[0].ToVersion != "1.2.0" {
		t.Fatalf("first event fields wrong: %+v", events[0])
	}
	if events[2].Action != "installed" || events[2].FromVersion != "" || events[2].ToVersion != "1.0.0" {
		t.Fatalf("oldest event fields wrong: %+v", events[2])
	}
	if events[0].Verb != "upgrade" || events[0].Cmdline != "yum upgrade hello" {
		t.Fatalf("verb/cmdline not populated: %+v", events[0])
	}
	if events[0].Source != "formula" {
		t.Fatalf("source not populated: %+v", events[0])
	}
	if events[0].StartedAt.IsZero() {
		t.Fatalf("started_at not populated")
	}
}

func TestPackageLogFiltersByName(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	ctx := context.Background()

	id1, err := j.Begin(ctx, "install", "yum install hello", "1.0", "5.1.6")
	if err != nil {
		t.Fatal(err)
	}
	if err := j.End(ctx, id1, 0, []TxPackage{
		{Name: "hello", ToVersion: "1.0.0", Source: "formula", Action: "installed"},
	}); err != nil {
		t.Fatal(err)
	}

	id2, err := j.Begin(ctx, "install", "yum install ripgrep", "1.0", "5.1.6")
	if err != nil {
		t.Fatal(err)
	}
	if err := j.End(ctx, id2, 0, []TxPackage{
		{Name: "ripgrep", ToVersion: "14.1.0", Source: "formula", Action: "installed"},
	}); err != nil {
		t.Fatal(err)
	}

	id3, err := j.Begin(ctx, "remove", "yum remove hello", "1.0", "5.1.6")
	if err != nil {
		t.Fatal(err)
	}
	if err := j.End(ctx, id3, 0, []TxPackage{
		{Name: "hello", FromVersion: "1.0.0", Source: "formula", Action: "removed"},
	}); err != nil {
		t.Fatal(err)
	}

	events, err := j.PackageLog(ctx, "hello", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 hello events, got %d", len(events))
	}
	for _, e := range events {
		if e.TxID == id2 {
			t.Fatalf("ripgrep tx leaked into hello log: %+v", e)
		}
	}

	empty, err := j.PackageLog(ctx, "nonexistent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected 0 events for nonexistent pkg, got %d", len(empty))
	}
}
