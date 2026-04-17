package brew

import (
	"context"
	"os"
	"testing"

	"github.com/hunchom/bodega/internal/runner"
)

func TestInfoParsesFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/info-ripgrep.json")
	if err != nil {
		t.Fatal(err)
	}
	fake := &runner.Fake{Stdout: map[string]string{
		"brew info --json=v2 ripgrep": string(data),
	}}
	b := &Brew{R: fake}
	p, err := b.Info(context.Background(), "ripgrep")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "ripgrep" {
		t.Fatalf("name=%q", p.Name)
	}
	if p.Version == "" {
		t.Fatal("empty version")
	}
	if p.Homepage == "" {
		t.Fatal("empty homepage")
	}
}

func TestListInstalled(t *testing.T) {
	fake := &runner.Fake{Stdout: map[string]string{
		"brew list --formula --versions": "ripgrep 14.1.0\njq 1.7.1\n",
	}}
	b := &Brew{R: fake}
	pkgs, err := b.List(context.Background(), "installed")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("got %d", len(pkgs))
	}
	if pkgs[0].Name != "ripgrep" || pkgs[0].Version != "14.1.0" {
		t.Fatalf("parsed wrong: %+v", pkgs[0])
	}
}
