package backend

import (
	"context"
	"sort"
)

// Registry holds multiple backends and fans queries out.
type Registry struct {
	Backends []Backend
}

func (r *Registry) Primary() Backend {
	if len(r.Backends) == 0 {
		return nil
	}
	return r.Backends[0]
}

// Search queries every backend and merges results (dedup by source+name).
func (r *Registry) Search(ctx context.Context, q string) ([]Package, error) {
	var all []Package
	for _, b := range r.Backends {
		ps, err := b.Search(ctx, q)
		if err != nil {
			continue
		}
		all = append(all, ps...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Source != all[j].Source {
			return all[i].Source < all[j].Source
		}
		return all[i].Name < all[j].Name
	})
	return all, nil
}

// Resolve finds which backend owns a package. Asks each backend's Info.
func (r *Registry) Resolve(ctx context.Context, name string) (Backend, *Package, error) {
	for _, b := range r.Backends {
		if p, err := b.Info(ctx, name); err == nil && p != nil {
			return b, p, nil
		}
	}
	return nil, nil, &notFoundErr{name: name}
}

type notFoundErr struct{ name string }

func (e *notFoundErr) Error() string { return "package not found: " + e.name }
