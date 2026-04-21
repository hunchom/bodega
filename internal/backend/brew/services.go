package brew

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hunchom/bodega/internal/backend"
)

// ServiceStatus is the normalized lifecycle state for a brew-managed
// launchd service. The wire vocabulary from `brew services list --json`
// is passed through directly for the four known values; anything else
// collapses to SvcUnknown so callers (especially the JSON consumers)
// never see a stray brew-internal label.
type ServiceStatus string

const (
	SvcStarted   ServiceStatus = "started"
	SvcStopped   ServiceStatus = "stopped"
	SvcScheduled ServiceStatus = "scheduled"
	SvcError     ServiceStatus = "error"
	SvcUnknown   ServiceStatus = "unknown"
)

// Service is a single row from `brew services list`. Empty User/File
// is normal for services that have never been run (brew emits null).
type Service struct {
	Name        string        `json:"name"`
	Status      ServiceStatus `json:"status"`
	User        string        `json:"user,omitempty"`
	File        string        `json:"file,omitempty"`
	ExitCode    int           `json:"exit_code,omitempty"`
	LastStarted *time.Time    `json:"last_started,omitempty"`
}

// rawService mirrors the JSON brew emits. user/file are nullable which
// is why we go through *string — json.Unmarshal will leave them nil on
// null and we flatten to "" in normalize().
type rawService struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	User     *string `json:"user"`
	File     *string `json:"file"`
	ExitCode *int    `json:"exit_code"`
}

// ListServices parses `brew services list --json` into a normalized slice.
// Returns nil (not an error) for an empty JSON array.
func (b *Brew) ListServices(ctx context.Context) ([]Service, error) {
	out, err := b.R.Run(ctx, "brew", "services", "list", "--json")
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, brewErr("services list", "", out)
	}
	var raws []rawService
	if err := json.Unmarshal(out.Stdout, &raws); err != nil {
		return nil, fmt.Errorf("parse brew services json: %w", err)
	}
	if len(raws) == 0 {
		return nil, nil
	}
	svcs := make([]Service, 0, len(raws))
	for _, r := range raws {
		svc := Service{
			Name:   r.Name,
			Status: normalizeStatus(r.Status),
			User:   deref(r.User),
			File:   deref(r.File),
		}
		if r.ExitCode != nil {
			svc.ExitCode = *r.ExitCode
		}
		svcs = append(svcs, svc)
	}
	return svcs, nil
}

// ServiceAction streams a mutation through brew. action is one of
// start/stop/restart/run/cleanup; name is "" for cleanup.
func (b *Brew) ServiceAction(ctx context.Context, action, name string, w backend.ProgressWriter) error {
	args := []string{"services", action}
	if name != "" {
		args = append(args, name)
	}
	return b.stream(ctx, w, args...)
}

func normalizeStatus(s string) ServiceStatus {
	switch ServiceStatus(s) {
	case SvcStarted, SvcStopped, SvcScheduled, SvcError:
		return ServiceStatus(s)
	}
	return SvcUnknown
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
