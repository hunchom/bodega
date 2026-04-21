package journal

import (
	"context"
	"time"
)

type PackageEvent struct {
	TxID        int64     `json:"tx_id"`
	StartedAt   time.Time `json:"started_at"`
	Verb        string    `json:"verb"`
	Cmdline     string    `json:"cmdline"`
	ExitCode    int       `json:"exit_code"`
	Action      string    `json:"action"`
	FromVersion string    `json:"from_version,omitempty"`
	ToVersion   string    `json:"to_version,omitempty"`
	Source      string    `json:"source"`
}

func (j *Journal) PackageLog(ctx context.Context, name string, limit int) ([]PackageEvent, error) {
	rows, err := j.db.QueryContext(ctx,
		`SELECT t.id, t.started_at, t.verb, t.cmdline, COALESCE(t.exit_code, 0),
		        p.action, COALESCE(p.from_version, ''), COALESCE(p.to_version, ''), p.source
		 FROM transaction_packages p
		 JOIN transactions t ON t.id = p.transaction_id
		 WHERE p.name = ?
		 ORDER BY t.started_at DESC, p.rowid DESC
		 LIMIT ?`, name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PackageEvent
	for rows.Next() {
		var e PackageEvent
		var s int64
		if err := rows.Scan(&e.TxID, &s, &e.Verb, &e.Cmdline, &e.ExitCode,
			&e.Action, &e.FromVersion, &e.ToVersion, &e.Source); err != nil {
			return nil, err
		}
		e.StartedAt = time.Unix(s, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
