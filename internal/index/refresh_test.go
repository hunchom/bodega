package index

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// fakeSource returns canned payloads and records call count.
type fakeSource struct {
	res   *FetchResult
	err   error
	calls int
}

func (f *fakeSource) Fetch(ctx context.Context, _, _ string) (*FetchResult, error) {
	f.calls++
	return f.res, f.err
}

func freshStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestEnsureFreshBuildsWhenEmpty(t *testing.T) {
	s := freshStore(t)
	src := &fakeSource{res: &FetchResult{FormulaPayload: []byte(fxFormulae), CaskPayload: []byte(fxCasks), FormulaETag: "e1"}}
	refreshed, err := s.EnsureFresh(context.Background(), src, DefaultMaxAge)
	if err != nil || !refreshed {
		t.Fatalf("expected refresh, got %v %v", refreshed, err)
	}
	if s.FormulaCount() != 2 {
		t.Fatalf("count=%d", s.FormulaCount())
	}
}

func TestEnsureFreshSkipsWhenFresh(t *testing.T) {
	s := freshStore(t)
	// Seed a fresh build directly.
	if _, err := s.Rebuild(context.Background(), []byte(fxFormulae), []byte(fxCasks), "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{res: &FetchResult{NotModified: true}}
	refreshed, err := s.EnsureFresh(context.Background(), src, DefaultMaxAge)
	if err != nil || refreshed {
		t.Fatalf("expected skip, got %v %v", refreshed, err)
	}
	if src.calls != 0 {
		t.Fatalf("source fetched despite fresh index (calls=%d)", src.calls)
	}
}

func TestEnsureFreshRefreshesWhenStale(t *testing.T) {
	s := freshStore(t)
	// Build with a timestamp well in the past.
	if _, err := s.Rebuild(context.Background(), []byte(fxFormulae), []byte(fxCasks), "old", "old", time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{res: &FetchResult{NotModified: true}}
	if _, err := s.EnsureFresh(context.Background(), src, DefaultMaxAge); err != nil {
		t.Fatal(err)
	}
	if src.calls != 1 {
		t.Fatalf("stale index should have fetched once, calls=%d", src.calls)
	}
}

func TestRefreshNotModifiedBumpsBuiltAt(t *testing.T) {
	s := freshStore(t)
	if _, err := s.Rebuild(context.Background(), []byte(fxFormulae), []byte(fxCasks), "etag", "etag", time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{res: &FetchResult{NotModified: true, FormulaETag: "etag", CaskETag: "etag"}}
	if _, err := s.Refresh(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	// built_at must have moved to ~now so the stale-gate now skips.
	bt, _ := s.BuiltAt()
	if time.Since(bt) > time.Minute {
		t.Fatalf("built_at not bumped on NotModified: %v", bt)
	}
}

func TestRefreshFromErrorPropagates(t *testing.T) {
	s := freshStore(t)
	src := &fakeSource{err: errors.New("network down")}
	if _, err := s.EnsureFresh(context.Background(), src, DefaultMaxAge); err == nil {
		t.Fatal("expected fetch error to propagate")
	}
}

func TestChainSourceFallsBack(t *testing.T) {
	primary := &fakeSource{err: errors.New("offline")}
	backup := &fakeSource{res: &FetchResult{FormulaPayload: []byte(fxFormulae), CaskPayload: []byte(fxCasks)}}
	chain := ChainSource{Sources: []Source{primary, backup}}
	res, err := chain.Fetch(context.Background(), "", "")
	if err != nil || res == nil {
		t.Fatalf("chain should fall back: %v", err)
	}
	if primary.calls != 1 || backup.calls != 1 {
		t.Fatalf("calls primary=%d backup=%d", primary.calls, backup.calls)
	}
}

func TestSchemaStaleForcesRefresh(t *testing.T) {
	s := freshStore(t)
	if _, err := s.Rebuild(context.Background(), []byte(fxFormulae), []byte(fxCasks), "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Simulate an older schema_version on disk.
	s.setMeta("schema_version", "0")
	if !s.SchemaStale() {
		t.Fatal("expected schema stale")
	}
	src := &fakeSource{res: &FetchResult{FormulaPayload: []byte(fxFormulae), CaskPayload: []byte(fxCasks)}}
	refreshed, err := s.EnsureFresh(context.Background(), src, DefaultMaxAge)
	if err != nil || !refreshed {
		t.Fatalf("schema-stale index should rebuild even if recent: %v %v", refreshed, err)
	}
}
