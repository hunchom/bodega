package brew

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// cacheDir returns the directory used for `brew info` response caching. We
// prefer XDG_CACHE_HOME when set, otherwise ~/.cache. On first use the
// directory is created with 0o755. An empty string is returned if neither
// XDG_CACHE_HOME nor HOME is discoverable — callers should treat that as a
// soft miss and shell out as usual.
var (
	cacheDirOnce sync.Once
	cacheDirPath string
)

func cacheDir() string {
	cacheDirOnce.Do(func() {
		base := os.Getenv("XDG_CACHE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				return
			}
			base = filepath.Join(home, ".cache")
		}
		dir := filepath.Join(base, "yum", "info")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		cacheDirPath = dir
	})
	return cacheDirPath
}

// cachePath maps a formula/cask name to the on-disk location of its cached
// `brew info --json=v2` response. The filename is the sha256 hex of the name
// so we don't have to worry about odd characters colliding with filesystem
// rules.
func cachePath(name string) string {
	dir := cacheDir()
	if dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(dir, hex.EncodeToString(sum[:]))
}

// readCache returns the cached payload for `name` when it exists and its
// mtime is newer than ttl ago. On any error (missing file, unreadable,
// stale) it returns (nil, false).
func readCache(name string, ttl time.Duration) ([]byte, bool) {
	p := cachePath(name)
	if p == "" {
		return nil, false
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, false
	}
	if time.Since(st.ModTime()) > ttl {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return data, true
}

// writeCache atomically writes `data` as the cached payload for `name`. It
// writes to a temp file in the same directory then renames into place so a
// concurrent reader never sees a half-written file. Errors are surfaced but
// callers typically ignore them — a failed cache write is never fatal.
func writeCache(name string, data []byte) error {
	p := cachePath(name)
	if p == "" {
		return nil
	}
	dir := filepath.Dir(p)
	tmp, err := os.CreateTemp(dir, "info-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, p); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// invalidateCache removes cached `brew info` payloads for the given names.
// Called from mutation paths (install/remove/reinstall/upgrade) so the next
// `Info` call goes through fresh. Missing files are ignored.
func invalidateCache(names []string) {
	for _, n := range names {
		p := cachePath(n)
		if p == "" {
			continue
		}
		_ = os.Remove(p)
	}
}

// resetCacheDirForTest forces the next cacheDir() call to re-resolve the
// directory. Tests use it after t.Setenv("XDG_CACHE_HOME", ...) to pick up
// the override instead of a previously memoized path.
func resetCacheDirForTest() {
	cacheDirOnce = sync.Once{}
	cacheDirPath = ""
}
