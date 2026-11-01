// Package group defines group-level metric configuration: tier 2 of the
// precedence chain in internal/server's package doc (definition default ->
// group -> server override). A group only carries overrides; it does not
// decide which servers belong to it — that's the server's own Groups list.
package group

import (
	"fmt"

	"aymanhs/watchtower/internal/metric"
)

// Config is one group's metric overrides, keyed by metric definition name.
type Config struct {
	Name    string                     `yaml:"name"`
	Metrics map[string]metric.Override `yaml:"metrics,omitempty"`

	// SourceFile is set by the loader, not read from YAML, for diagnostics.
	SourceFile string `yaml:"-"`
}

func (c *Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("group config missing name")
	}
	return nil
}
