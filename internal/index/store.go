package index

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// schemaVersion bumps whenever schema.sql changes shape. A mismatch on Open
// triggers a wholesale rebuild (the index is disposable).
const schemaVersion = "1"

// Store is the queryable native package index, backed by SQLite.
type Store struct {
	db *sql.DB
}

// DefaultPath is $XDG_DATA_HOME/yum/index.db (or ~/.local/share/yum/index.db),
// alongside the journal but a separate, disposable database.
func DefaultPath() string {
	if p := os.Getenv("XDG_DATA_HOME"); p != "" {
		return filepath.Join(p, "yum", "index.db")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local/share/yum/index.db")
}

// Open opens (creating if needed) the index at path and applies the schema.
// WAL mode keeps reads live while `yum update` rebuilds in a transaction.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply index schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// SchemaStale reports whether the stored schema_version differs from the current
// build's — i.e. the on-disk index predates a schema change and must be rebuilt.
func (s *Store) SchemaStale() bool {
	v, _ := s.Meta("schema_version")
	return v != schemaVersion
}

// Meta reads a meta key. ok is false when the key is absent.
func (s *Store) Meta(key string) (value string, ok bool) {
	row := s.db.QueryRowContext(context.Background(), `SELECT value FROM meta WHERE key=?`, key)
	if err := row.Scan(&value); err != nil {
		return "", false
	}
	return value, true
}

// BuiltAt returns the wall-clock time the index was last rebuilt. ok is false
// when the index has never been built.
func (s *Store) BuiltAt() (time.Time, bool) {
	v, ok := s.Meta("built_at")
	if !ok {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, 0), true
}

// ETag returns the stored ETag for the given source key ("formula"|"cask"),
// used for conditional fetches.
func (s *Store) ETag(which string) string {
	v, _ := s.Meta(which + "_etag")
	return v
}

// FormulaCount reports how many formulae the index holds (0 when empty/unbuilt).
func (s *Store) FormulaCount() int {
	var n int
	_ = s.db.QueryRowContext(context.Background(), `SELECT count(*) FROM formulae`).Scan(&n)
	return n
}

// Lookup returns the formula by name (or full_name), with its dependencies
// populated. Returns (nil, nil) for a clean miss.
func (s *Store) Lookup(name string) (*Formula, error) {
	row := s.db.QueryRowContext(context.Background(),
		`SELECT name, full_name, tap, desc, license, homepage, stable_version, revision
		   FROM formulae WHERE name=? OR full_name=? LIMIT 1`, name, name)
	var f Formula
	if err := row.Scan(&f.Name, &f.FullName, &f.Tap, &f.Desc, &f.License, &f.Homepage, &f.StableVersion, &f.Revision); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	deps, build, err := s.deps(f.Name)
	if err != nil {
		return nil, err
	}
	f.Deps, f.BuildDeps = deps, build
	return &f, nil
}

func (s *Store) deps(formula string) (runtime, build []string, err error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT dep, kind FROM formula_deps WHERE formula=? ORDER BY rowid`, formula)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var dep, kind string
		if err := rows.Scan(&dep, &kind); err != nil {
			return nil, nil, err
		}
		if kind == "build" {
			build = append(build, dep)
		} else {
			runtime = append(runtime, dep)
		}
	}
	return runtime, build, rows.Err()
}

// Deps returns the runtime dependencies of name.
func (s *Store) Deps(name string) ([]string, error) {
	r, _, err := s.deps(name)
	return r, err
}

// ReverseDeps returns installed-agnostic consumers: every formula that declares
// name as a runtime dependency. SP2/SP3 intersect this with the Cellar.
func (s *Store) ReverseDeps(name string) ([]string, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT DISTINCT formula FROM formula_deps WHERE dep=? AND kind='runtime' ORDER BY formula`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Bottle returns the bottle for (name, tag), or (nil, nil) if none — e.g. a
// source-only formula or an unsupported macOS tag.
func (s *Store) Bottle(name, tag string) (*Bottle, error) {
	row := s.db.QueryRowContext(context.Background(),
		`SELECT tag, url, sha256, cellar FROM formula_bottles WHERE formula=? AND tag=?`, name, tag)
	var b Bottle
	if err := row.Scan(&b.Tag, &b.URL, &b.SHA256, &b.Cellar); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

// Bottles returns every per-tag bottle for a formula (empty for a source-only
// formula). The installer picks the host tag from this set.
func (s *Store) Bottles(name string) ([]Bottle, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT tag, url, sha256, cellar FROM formula_bottles WHERE formula=?`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bottle
	for rows.Next() {
		var b Bottle
		if err := rows.Scan(&b.Tag, &b.URL, &b.SHA256, &b.Cellar); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LookupCask returns the cask by token, or (nil, nil) for a miss.
func (s *Store) LookupCask(token string) (*Cask, error) {
	row := s.db.QueryRowContext(context.Background(),
		`SELECT token, name, desc, homepage, version, tap FROM casks WHERE token=?`, token)
	var c Cask
	var name string
	if err := row.Scan(&c.Token, &name, &c.Desc, &c.Homepage, &c.Version, &c.Tap); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if name != "" {
		c.Names = strings.Split(name, "\n")
	}
	return &c, nil
}

// Match is one search hit.
type Match struct {
	Name   string
	Desc   string
	Source string // "formula" | "cask"
}

// Search runs an FTS5 ranked query over names + descriptions, falling back to a
// LIKE scan if the FTS query is rejected (e.g. punctuation the tokenizer
// dislikes). Results are capped to limit.
func (s *Store) Search(q string, limit int) ([]Match, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if m, err := s.searchFTS(q, limit); err == nil {
		return m, nil
	}
	return s.searchLike(q, limit)
}

func (s *Store) searchFTS(q string, limit int) ([]Match, error) {
	// Prefix-match each term so "rip" finds "ripgrep". Quote terms to neutralize
	// FTS operators in user input.
	var terms []string
	for _, t := range strings.Fields(q) {
		terms = append(terms, `"`+strings.ReplaceAll(t, `"`, `""`)+`"*`)
	}
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT name, desc, source FROM search_fts WHERE search_fts MATCH ? ORDER BY rank LIMIT ?`,
		strings.Join(terms, " "), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMatches(rows)
}

func (s *Store) searchLike(q string, limit int) ([]Match, error) {
	like := "%" + strings.ToLower(q) + "%"
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT name, desc, source FROM search_fts
		   WHERE lower(name) LIKE ? OR lower(desc) LIKE ? LIMIT ?`, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMatches(rows)
}

func scanMatches(rows *sql.Rows) ([]Match, error) {
	var out []Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.Name, &m.Desc, &m.Source); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AllFormulaNames returns every formula name, sorted.
func (s *Store) AllFormulaNames() ([]string, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT name FROM formulae ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
