package journal

import (
	"context"
	"fmt"

	"github.com/hunchom/yum/internal/backend"
)

// Plan returns the steps required to invert a past transaction.
// For installed → remove. For removed → install. For upgraded → install @from version.
type Step struct {
	Verb string // install|remove|pin|unpin
	Pkg  backend.Package
}

func (j *Journal) PlanRollback(ctx context.Context, id int64) ([]Step, error) {
	tx, err := j.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, fmt.Errorf("transaction %d not found", id)
	}

	var steps []Step
	for _, p := range tx.Packages {
		pkg := backend.Package{Name: p.Name, Source: backend.Source(p.Source)}
		switch p.Action {
		case "installed":
			steps = append(steps, Step{Verb: "remove", Pkg: pkg})
		case "removed":
			pkg.Version = p.FromVersion
			steps = append(steps, Step{Verb: "install", Pkg: pkg})
		case "upgraded":
			// brew can't easily downgrade; surface limitation
			pkg.Version = p.FromVersion
			steps = append(steps, Step{Verb: "downgrade", Pkg: pkg})
		case "pinned":
			steps = append(steps, Step{Verb: "unpin", Pkg: pkg})
		case "unpinned":
			steps = append(steps, Step{Verb: "pin", Pkg: pkg})
		}
	}
	return steps, nil
}
