package group

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadGroups walks dir recursively and parses every .yaml/.yml file into a
// Config. Each file is one group.
func LoadGroups(dir string) ([]*Config, error) {
	var configs []*Config
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
