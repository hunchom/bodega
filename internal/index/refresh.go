package index

import (
	"context"
	"strconv"
	"time"
)

// DefaultMaxAge is how long a built index is considered fresh before a read-path
// EnsureFresh re-fetches. Matches the prior brew-tap staleness window.
const DefaultMaxAge = 24 * time.Hour

// nowFunc is overridable in tests; production uses the wall clock.
var nowFunc = time.Now

// Refresh forces a fetch + verify + rebuild (this is `yum update`). A
// NotModified result (ETag match) is a near-instant no-op that just bumps the
// freshness clock.
func (s *Store) Refresh(ctx context.Context, src Source) (*BuildResult, error) {
	return s.refreshFrom(ctx, src)
}

// EnsureFresh refreshes only when needed: the index is empty, its schema is
// stale, or it's older than maxAge. Returns refreshed=true when a rebuild ran.
// A fetch error is returned to the caller, which may choose to serve a stale-
// but-present index (FormulaCount() > 0) with a warning.
func (s *Store) EnsureFresh(ctx context.Context, src Source, maxAge time.Duration) (refreshed bool, err error) {
	if !s.NeedsRefresh(maxAge) {
		return false, nil
	}
	res, err := s.refreshFrom(ctx, src)
	if err != nil {
		return false, err
	}
	return res != nil, nil
}

// NeedsRefresh reports whether the index is empty, schema-stale, or older than
// maxAge. Callers use it to decide whether to print a "refreshing" line.
func (s *Store) NeedsRefresh(maxAge time.Duration) bool {
	if s.SchemaStale() || s.FormulaCount() == 0 {
		return true
	}
	bt, ok := s.BuiltAt()
	if !ok {
		return true
	}
	return nowFunc().Sub(bt) >= maxAge
}

// refreshFrom fetches from src and rebuilds. On NotModified it bumps built_at so
// the stale-gate resets without a rebuild. Returns (nil, nil) for the
// NotModified no-op.
func (s *Store) refreshFrom(ctx context.Context, src Source) (*BuildResult, error) {
	prevF, prevC := s.ETag("formula"), s.ETag("cask")
	res, err := src.Fetch(ctx, prevF, prevC)
	if err != nil {
		return nil, err
	}
	now := nowFunc()
	if res.NotModified {
		s.setMeta("built_at", strconv.FormatInt(now.Unix(), 10))
		return nil, nil
	}
	return s.Rebuild(ctx, res.FormulaPayload, res.CaskPayload, res.FormulaETag, res.CaskETag, now)
}

func (s *Store) setMeta(key, value string) {
	_, _ = s.db.ExecContext(context.Background(),
		`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
}
