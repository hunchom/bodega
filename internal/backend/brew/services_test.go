package brew

import (
	"context"
	"strings"
	"testing"

	"github.com/hunchom/bodega/internal/runner"
)

func TestListServicesParsesJSON(t *testing.T) {
	payload := `[
	  {"name":"postgresql@16","status":"started","user":"roger","file":"/Users/roger/Library/LaunchAgents/homebrew.mxcl.postgresql@16.plist"},
	  {"name":"redis","status":"stopped","user":null,"file":null}
	]`
	fake := &runner.Fake{Stdout: map[string]string{
		"brew services list --json": payload,
	}}
	b := &Brew{R: fake}
	svcs, err := b.ListServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 2 {
		t.Fatalf("want 2 services, got %d", len(svcs))
	}
	if svcs[0].Name != "postgresql@16" || svcs[0].Status != SvcStarted {
		t.Fatalf("bad first service: %+v", svcs[0])
	}
	if svcs[0].User != "roger" || !strings.HasSuffix(svcs[0].File, "postgresql@16.plist") {
		t.Fatalf("bad first metadata: %+v", svcs[0])
	}
	if svcs[1].Name != "redis" || svcs[1].Status != SvcStopped {
		t.Fatalf("bad second service: %+v", svcs[1])
	}
	if svcs[1].User != "" || svcs[1].File != "" {
		t.Fatalf("nulls should flatten to empty: %+v", svcs[1])
	}
}

func TestListServicesEmpty(t *testing.T) {
	fake := &runner.Fake{Stdout: map[string]string{
		"brew services list --json": "[]",
	}}
	b := &Brew{R: fake}
	svcs, err := b.ListServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 0 {
		t.Fatalf("want empty, got %d", len(svcs))
	}
}

func TestListServicesMalformed(t *testing.T) {
	fake := &runner.Fake{Stdout: map[string]string{
		"brew services list --json": "{not json",
	}}
	b := &Brew{R: fake}
	if _, err := b.ListServices(context.Background()); err == nil {
		t.Fatal("expected error on malformed json")
	}
}

func TestListServicesUnknownStatusMapsToUnknown(t *testing.T) {
	payload := `[{"name":"weird","status":"fubar","user":"x","file":"/tmp/x.plist"}]`
	fake := &runner.Fake{Stdout: map[string]string{
		"brew services list --json": payload,
	}}
	b := &Brew{R: fake}
	svcs, err := b.ListServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 1 {
		t.Fatalf("want 1, got %d", len(svcs))
	}
	if svcs[0].Status != SvcUnknown {
		t.Fatalf("want unknown, got %q", svcs[0].Status)
	}
}

func TestListServicesStatusPassthrough(t *testing.T) {
	payload := `[
	  {"name":"a","status":"started"},
	  {"name":"b","status":"stopped"},
	  {"name":"c","status":"scheduled"},
	  {"name":"d","status":"error"}
	]`
	fake := &runner.Fake{Stdout: map[string]string{
		"brew services list --json": payload,
	}}
	b := &Brew{R: fake}
	svcs, err := b.ListServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []ServiceStatus{SvcStarted, SvcStopped, SvcScheduled, SvcError}
	if len(svcs) != len(want) {
		t.Fatalf("want %d, got %d", len(want), len(svcs))
	}
	for i, s := range svcs {
		if s.Status != want[i] {
			t.Fatalf("idx %d: want %q, got %q", i, want[i], s.Status)
		}
	}
}
