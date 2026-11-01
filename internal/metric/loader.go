package metric

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadDefinitions walks dir recursively and parses every .yaml/.yml file
// into a Definition. Each file is independent; a directory layout like
// metrics/linux/memory.yaml is purely organizational.
func LoadDefinitions(dir string) ([]*Definition, error) {
	var defs []*Definition
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
		def, err := loadOne(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		defs = append(defs, def)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return defs, nil
}

func loadOne(path string) (*Definition, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var def Definition
	if err := yaml.Unmarshal(b, &def); err != nil {
		return nil, err
	}
	def.SourceFile = path
	if err := def.Validate(); err != nil {
		return nil, err
	}
	return &def, nil
}
