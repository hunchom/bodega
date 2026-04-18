package brew

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeJWS drops a synthetic {"payload":"..."} file at path. If b64 is true
// the payload is base64 of data, otherwise the raw JSON string.
func writeJWS(t *testing.T, path string, payload []byte, b64 bool) {
	t.Helper()
	var envPayload string
	if b64 {
		envPayload = base64.StdEncoding.EncodeToString(payload)
	} else {
		envPayload = string(payload)
	}
	body, err := json.Marshal(map[string]string{"payload": envPayload})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAPICacheLoadFormulae(t *testing.T) {
	dir := t.TempDir()
	payload := []byte(`[{"name":"ripgrep","full_name":"ripgrep","tap":"homebrew/core","desc":"Search tool","license":"Unlicense","homepage":"https://github.com/BurntSushi/ripgrep","versions":{"stable":"15.1.0"},"dependencies":["pcre2"],"build_dependencies":["rust"]}]`)
	writeJWS(t, filepath.Join(dir, "formula.jws.json"), payload, false)

	c := &APICache{root: dir}
	m, err := c.LoadFormulae()
	if err != nil {
		t.Fatalf("LoadFormulae: %v", err)
	}
	f := m["ripgrep"]
	if f == nil {
		t.Fatal("ripgrep missing from map")
	}
	if f.Versions.Stable != "15.1.0" {
		t.Fatalf("stable=%q", f.Versions.Stable)
	}
	if len(f.Dependencies) != 1 || f.Dependencies[0] != "pcre2" {
		t.Fatalf("deps=%v", f.Dependencies)
	}
}

func TestAPICacheLoadCasksBase64(t *testing.T) {
	dir := t.TempDir()
	payload := []byte(`[{"token":"firefox","name":["Firefox"],"desc":"Web browser","homepage":"https://mozilla.org","version":"1.0","tap":"homebrew/cask"}]`)
	writeJWS(t, filepath.Join(dir, "cask.jws.json"), payload, true)

	c := &APICache{root: dir}
	m, err := c.LoadCasks()
	if err != nil {
		t.Fatalf("LoadCasks: %v", err)
	}
	ck := m["firefox"]
	if ck == nil {
		t.Fatal("firefox missing from map")
	}
	if ck.Version != "1.0" {
		t.Fatalf("version=%q", ck.Version)
	}
}

func TestAPICacheLookup(t *testing.T) {
	dir := t.TempDir()
	writeJWS(t, filepath.Join(dir, "formula.jws.json"),
		[]byte(`[{"name":"jq","full_name":"jq","tap":"homebrew/core","desc":"JSON processor","versions":{"stable":"1.7.1"}}]`), false)
	writeJWS(t, filepath.Join(dir, "cask.jws.json"),
		[]byte(`[{"token":"firefox","name":["Firefox"],"desc":"Browser","version":"120"}]`), false)

	c := &APICache{root: dir}

	p, err := c.Lookup("jq")
	if err != nil {
		t.Fatalf("Lookup jq: %v", err)
	}
	if p == nil || p.Name != "jq" || p.Version != "1.7.1" {
		t.Fatalf("jq result: %+v", p)
	}

	p, err = c.Lookup("firefox")
	if err != nil {
		t.Fatalf("Lookup firefox: %v", err)
	}
	if p == nil || p.Name != "firefox" || p.Source != "cask" {
		t.Fatalf("firefox result: %+v", p)
	}

	p, err = c.Lookup("does-not-exist")
	if err != nil {
		t.Fatalf("miss error: %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil, got %+v", p)
	}
}

func TestAPICacheSearchNames(t *testing.T) {
	dir := t.TempDir()
	writeJWS(t, filepath.Join(dir, "formula.jws.json"),
		[]byte(`[{"name":"ripgrep","full_name":"ripgrep","desc":"Search"},{"name":"grep","full_name":"grep","desc":"GNU grep"}]`), false)
	writeJWS(t, filepath.Join(dir, "cask.jws.json"),
		[]byte(`[{"token":"gripe","name":["Gripe"]}]`), false)

	c := &APICache{root: dir}
	pkgs, err := c.SearchNames("rip")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("got %d pkgs: %+v", len(pkgs), pkgs)
	}
}

func TestAPICacheMtimeReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "formula.jws.json")
	writeJWS(t, path, []byte(`[{"name":"a","full_name":"a"}]`), false)

	c := &APICache{root: dir}
	m1, err := c.LoadFormulae()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m1["a"]; !ok {
		t.Fatal("no a")
	}

	// Rewrite with different content; bump mtime explicitly so the test is
	// resilient to high-resolution filesystem timestamps that might otherwise
	// tie the two writes.
	writeJWS(t, path, []byte(`[{"name":"b","full_name":"b"}]`), false)
	later := time.Now().Add(1 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	m2, err := c.LoadFormulae()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m2["b"]; !ok {
		t.Fatalf("reload didn't pick up new data: %v", m2)
	}
}
