package brew

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hunchom/bodega/internal/index"
)

// Resolve must work over the native index, not just the legacy API cache. Build
// a tiny index (bottle tag "all", resolvable on any host) and resolve a→b.
func TestResolveViaIndex(t *testing.T) {
	st, err := index.Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	payload := `[
	  {"name":"a","versions":{"stable":"1.0"},"dependencies":["b"],
	   "bottle":{"stable":{"files":{"all":{"url":"https://ghcr/a","sha256":"AA"}}}}},
	  {"name":"b","versions":{"stable":"2.0"},
	   "bottle":{"stable":{"files":{"all":{"url":"https://ghcr/b","sha256":"BB"}}}}}
	]`
	if _, err := st.Rebuild(context.Background(), []byte(payload), []byte(`[]`), "", "", time.Now()); err != nil {
		t.Fatal(err)
	}

	plans, err := Resolve(context.Background(), indexFormulaSource{st: st}, []string{"a"})
	if err != nil {
		t.Fatalf("resolve via index: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("want 2 plans, got %d: %+v", len(plans), plans)
	}
	// dependency b must come before a (leaves-first).
	if plans[0].Name != "b" || plans[1].Name != "a" {
		t.Fatalf("bad order: %s, %s", plans[0].Name, plans[1].Name)
	}
	if !plans[1].IsRoot || plans[0].IsRoot {
		t.Fatalf("root flags wrong: %+v", plans)
	}
	if plans[1].BottleURL != "https://ghcr/a" || plans[1].SHA256 != "aa" {
		t.Fatalf("bad bottle for a: %+v", plans[1])
	}
}
