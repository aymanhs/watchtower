package server

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// fileNameFor turns a server name into a safe YAML file name.
func fileNameFor(name string) string {
	n := strings.Trim(unsafeFileChars.ReplaceAllString(name, "_"), "._")
	if n == "" {
		n = "server"
	}
	return n + ".yaml"
}

// Save validates cfg and writes it to dir. A config that came from the
// loader (SourceFile set) is rewritten in place, so a rename never leaves
// the old file behind; a new one gets <name>.yaml. The name must not be
// used by any other server. The write is atomic (temp file + rename), so a
// crash never leaves half a config for the next load to choke on.
func Save(dir string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	existing, err := LoadServers(dir)
	if err != nil {
		return err
	}
	for _, o := range existing {
		if o.Name == cfg.Name && o.SourceFile != cfg.SourceFile {
			return fmt.Errorf("a server named %q already exists", cfg.Name)
		}
	}
	path := cfg.SourceFile
	if path == "" {
		path = filepath.Join(dir, fileNameFor(cfg.Name))
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	cfg.SourceFile = path
	return nil
}

// Delete removes the file of the server called name.
func Delete(dir, name string) error {
	existing, err := LoadServers(dir)
	if err != nil {
		return err
	}
	for _, c := range existing {
		if c.Name == name {
			return os.Remove(c.SourceFile)
		}
	}
	return fmt.Errorf("no server named %q", name)
}
