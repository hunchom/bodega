package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.UI.Theme != "amber" {
		t.Fatalf("default theme=%q", c.UI.Theme)
	}
}

func TestLoadFromFile(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "yum.toml")
	os.WriteFile(p, []byte(`[ui]`+"\n"+`theme = "teal"`+"\n"), 0644)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.UI.Theme != "teal" {
		t.Fatalf("got %q", c.UI.Theme)
	}
}
