package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	UI struct {
		Theme              string `toml:"theme"`
		ConfirmDestructive bool   `toml:"confirm_destructive"`
		CompactTables      bool   `toml:"compact_tables"`
	} `toml:"ui"`
	Defaults struct {
		AutoCleanup bool `toml:"auto_cleanup"`
		Parallel    bool `toml:"parallel"`
	} `toml:"defaults"`
	Aliases map[string]string `toml:"aliases"`
}

func defaultPath() string {
	if p := os.Getenv("XDG_CONFIG_HOME"); p != "" {
		return filepath.Join(p, "yum", "config.toml")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "yum", "config.toml")
}

func defaults() *Config {
	c := &Config{}
	c.UI.Theme = "amber"
	c.UI.ConfirmDestructive = true
	c.Defaults.Parallel = true
	return c
}

func Load(override string) (*Config, error) {
	path := override
	if path == "" {
		path = defaultPath()
	}
	c := defaults()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return nil, err
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, err
	}
	return c, nil
}
