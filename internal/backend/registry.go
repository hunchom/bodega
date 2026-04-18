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

// NativeSearcher is an optional fast-path search hook. Backends that can
// answer `search` without shelling out implement this; Registry.Search
// prefers it when available and falls back to Backend.Search on error.
type NativeSearcher interface {
	SearchNative(ctx context.Context, q string) ([]Package, error)
}

// Search queries every backend and merges results (dedup by source+name).
// When a backend implements NativeSearcher we try its fast path first and
// only fall back to the generic Search when the native call errors (e.g.
// the JSON cache isn't available).
func (r *Registry) Search(ctx context.Context, q string) ([]Package, error) {
	var all []Package
	for _, b := range r.Backends {
		var (
			ps     []Package
			err    error
			native bool
		)
		if ns, ok := b.(NativeSearcher); ok {
			ps, err = ns.SearchNative(ctx, q)
			native = err == nil
		}
		if !native {
			ps, err = b.Search(ctx, q)
			if err != nil {
				continue
			}
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
