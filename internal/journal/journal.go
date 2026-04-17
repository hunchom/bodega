package journal

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type Journal struct {
	db *sql.DB
}

func Open(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Journal{db: db}, nil
}

func (j *Journal) Close() error { return j.db.Close() }

type Transaction struct {
	ID          int64
	StartedAt   time.Time
	EndedAt     time.Time
	Verb        string
	ExitCode    int
	Cmdline     string
	YumVersion  string
	BrewVersion string
	Packages    []TxPackage
}

type TxPackage struct {
	Name        string
	FromVersion string
	ToVersion   string
	Source      string
	Action      string // installed|removed|upgraded|reinstalled|pinned|unpinned
}

func (j *Journal) Begin(ctx context.Context, verb, cmdline, yv, bv string) (int64, error) {
	r, err := j.db.ExecContext(ctx,
		`INSERT INTO transactions(started_at, verb, cmdline, yum_version, brew_version) VALUES (?, ?, ?, ?, ?)`,
		time.Now().Unix(), verb, cmdline, yv, bv)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (j *Journal) End(ctx context.Context, id int64, exit int, pkgs []TxPackage) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE transactions SET ended_at=?, exit_code=? WHERE id=?`,
		time.Now().Unix(), exit, id); err != nil {
		return err
	}
	for _, p := range pkgs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO transaction_packages(transaction_id,name,from_version,to_version,source,action) VALUES (?,?,?,?,?,?)`,
			id, p.Name, p.FromVersion, p.ToVersion, p.Source, p.Action); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (j *Journal) Recent(ctx context.Context, limit int) ([]Transaction, error) {
	rows, err := j.db.QueryContext(ctx,
		`SELECT id, started_at, COALESCE(ended_at,0), verb, COALESCE(exit_code,0), cmdline FROM transactions ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transaction
	for rows.Next() {
		var t Transaction
		var s, e int64
		if err := rows.Scan(&t.ID, &s, &e, &t.Verb, &t.ExitCode, &t.Cmdline); err != nil {
			return nil, err
		}
		t.StartedAt = time.Unix(s, 0)
		if e > 0 {
			t.EndedAt = time.Unix(e, 0)
		}
		out = append(out, t)
	}
	return out, nil
}

func (j *Journal) Get(ctx context.Context, id int64) (*Transaction, error) {
	row := j.db.QueryRowContext(ctx,
		`SELECT id, started_at, COALESCE(ended_at,0), verb, COALESCE(exit_code,0), cmdline, yum_version, brew_version FROM transactions WHERE id=?`, id)
	var t Transaction
	var s, e int64
	if err := row.Scan(&t.ID, &s, &e, &t.Verb, &t.ExitCode, &t.Cmdline, &t.YumVersion, &t.BrewVersion); err != nil {
		return nil, err
	}
	t.StartedAt = time.Unix(s, 0)
	if e > 0 {
		t.EndedAt = time.Unix(e, 0)
	}
	rows, err := j.db.QueryContext(ctx,
		`SELECT name, COALESCE(from_version,''), COALESCE(to_version,''), source, action FROM transaction_packages WHERE transaction_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p TxPackage
		if err := rows.Scan(&p.Name, &p.FromVersion, &p.ToVersion, &p.Source, &p.Action); err != nil {
			return nil, err
		}
		t.Packages = append(t.Packages, p)
	}
	return &t, nil
}

// DefaultPath is ~/.local/share/yum/history.db
func DefaultPath() string {
	if p := os.Getenv("XDG_DATA_HOME"); p != "" {
		return filepath.Join(p, "yum", "history.db")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local/share/yum/history.db")
}

// Cmdline rebuilds the invocation string for storage.
func Cmdline(args []string) string { return strings.Join(args, " ") }
