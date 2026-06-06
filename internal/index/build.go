package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// BuildResult summarizes a rebuild.
type BuildResult struct {
	Formulae int
	Casks    int
}

// Rebuild replaces the entire index from freshly-fetched (and already-verified)
// formula/cask payloads, in a single transaction so readers never observe a
// half-written index. The whole index is disposable, so we drop-and-reinsert
// rather than diff.
func (s *Store) Rebuild(ctx context.Context, formulaPayload, caskPayload []byte, formulaETag, caskETag string, now time.Time) (*BuildResult, error) {
	formulae, formulaRaw, err := parseFormulae(formulaPayload)
	if err != nil {
		return nil, fmt.Errorf("parse formulae: %w", err)
	}
	casks, caskRaw, err := parseCasks(caskPayload)
	if err != nil {
		return nil, fmt.Errorf("parse casks: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	for _, t := range []string{"formulae", "formula_deps", "formula_bottles", "casks", "search_fts"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return nil, err
		}
	}

	if err := insertFormulae(ctx, tx, formulae, formulaRaw); err != nil {
		return nil, err
	}
	if err := insertCasks(ctx, tx, casks, caskRaw); err != nil {
		return nil, err
	}

	meta := map[string]string{
		"schema_version": schemaVersion,
		"built_at":       strconv.FormatInt(now.Unix(), 10),
		"formula_etag":   formulaETag,
		"cask_etag":      caskETag,
	}
	for k, v := range meta {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &BuildResult{Formulae: len(formulae), Casks: len(casks)}, nil
}

func parseFormulae(payload []byte) ([]payloadFormula, []json.RawMessage, error) {
	if len(payload) == 0 {
		return nil, nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, nil, err
	}
	out := make([]payloadFormula, 0, len(raw))
	for _, r := range raw {
		var f payloadFormula
		if err := json.Unmarshal(r, &f); err != nil {
			return nil, nil, err
		}
		out = append(out, f)
	}
	return out, raw, nil
}

func parseCasks(payload []byte) ([]payloadCask, []json.RawMessage, error) {
	if len(payload) == 0 {
		return nil, nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, nil, err
	}
	out := make([]payloadCask, 0, len(raw))
	for _, r := range raw {
		var c payloadCask
		if err := json.Unmarshal(r, &c); err != nil {
			return nil, nil, err
		}
		out = append(out, c)
	}
	return out, raw, nil
}

func insertFormulae(ctx context.Context, tx *sql.Tx, fs []payloadFormula, raw []json.RawMessage) error {
	insF, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO formulae(name,full_name,tap,desc,license,homepage,stable_version,revision,raw)
		 VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insF.Close()
	insD, err := tx.PrepareContext(ctx, `INSERT INTO formula_deps(formula,dep,kind) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer insD.Close()
	insB, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO formula_bottles(formula,tag,url,sha256,cellar) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insB.Close()
	insS, err := tx.PrepareContext(ctx, `INSERT INTO search_fts(name,desc,source) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer insS.Close()

	for i, f := range fs {
		if f.Name == "" {
			continue
		}
		if _, err := insF.ExecContext(ctx, f.Name, f.FullName, f.Tap, f.Desc, f.License, f.Homepage, f.Versions.Stable, f.Revision, string(raw[i])); err != nil {
			return err
		}
		for _, d := range f.Dependencies {
			if _, err := insD.ExecContext(ctx, f.Name, d, "runtime"); err != nil {
				return err
			}
		}
		for _, d := range f.BuildDependencies {
			if _, err := insD.ExecContext(ctx, f.Name, d, "build"); err != nil {
				return err
			}
		}
		for tag, file := range f.Bottle.Stable.Files {
			if _, err := insB.ExecContext(ctx, f.Name, tag, file.URL, file.SHA256, file.Cellar); err != nil {
				return err
			}
		}
		if _, err := insS.ExecContext(ctx, f.Name, f.Desc, "formula"); err != nil {
			return err
		}
	}
	return nil
}

func insertCasks(ctx context.Context, tx *sql.Tx, cs []payloadCask, raw []json.RawMessage) error {
	insC, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO casks(token,name,desc,homepage,version,tap,raw) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insC.Close()
	insS, err := tx.PrepareContext(ctx, `INSERT INTO search_fts(name,desc,source) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer insS.Close()

	for i, c := range cs {
		if c.Token == "" {
			continue
		}
		if _, err := insC.ExecContext(ctx, c.Token, strings.Join(c.Name, "\n"), c.Desc, c.Homepage, c.Version, c.Tap, string(raw[i])); err != nil {
			return err
		}
		if _, err := insS.ExecContext(ctx, c.Token, c.Desc, "cask"); err != nil {
			return err
		}
	}
	return nil
}
