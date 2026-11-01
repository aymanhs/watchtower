package server

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadServers walks dir recursively and parses every .yaml/.yml file into a
// Config. Each file is one server; names must be unique across all of
// them — the SSH manager and the GUI's state.Store both key by name, so a
// collision would silently merge two servers into one.
func LoadServers(dir string) ([]*Config, error) {
	var configs []*Config
	seen := make(map[string]string) // name -> the file that first defined it
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		cfg, err := loadOne(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if first, dup := seen[cfg.Name]; dup {
			return fmt.Errorf("%s: duplicate server name %q, also defined in %s", path, cfg.Name, first)
		}
		seen[cfg.Name] = path
		configs = append(configs, cfg)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return configs, nil
}

func loadOne(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	cfg.SourceFile = path
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
