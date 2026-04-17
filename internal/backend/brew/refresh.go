package brew

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hunchom/bodega/internal/backend"
)

// DefaultStaleAge is how long we're willing to coast on cached tap metadata
// before considering it stale. Most users don't upgrade more than once a day;
// 24h keeps us fresh without forcing a git fetch on every install.
const DefaultStaleAge = 24 * time.Hour

// fetchHeadCandidates lists paths we'll consult to decide whether taps are
// stale. homebrew-core is the big one; we stop at the first one that exists.
func fetchHeadCandidates() []string {
	return []string{
		"/opt/homebrew/Library/Taps/homebrew/homebrew-core/.git/FETCH_HEAD",
		"/usr/local/Homebrew/Library/Taps/homebrew/homebrew-core/.git/FETCH_HEAD",
	}
}

// Stale returns true when the homebrew-core tap's FETCH_HEAD is older than
// maxAge, or if we can't find it at all (fail-open: refresh if unsure).
// Pure os.Stat — no subprocess, no network.
func Stale(maxAge time.Duration) bool {
	now := time.Now()
	for _, p := range fetchHeadCandidates() {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		return now.Sub(st.ModTime()) > maxAge
	}
	// No FETCH_HEAD found — treat as stale so a first-run caller gets fresh
	// metadata. This is the fail-open choice; the caller can always pass
	// --no-refresh to skip.
	return true
}

// RefreshTaps runs `brew update`. Caller decides whether it's needed;
// Stale(DefaultStaleAge) is the typical gate. Progress is streamed to pw
// if non-nil, otherwise discarded.
func (b *Brew) RefreshTaps(ctx context.Context, pw backend.ProgressWriter) error {
	var sink io.Writer = io.Discard
	if pw != nil {
		sink = pw
	}
	r, err := b.R.Stream(ctx, sink, sink, "brew", "update", "--quiet")
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("brew update: exit %d", r.ExitCode)
	}
	return nil
}
