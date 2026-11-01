// Package server defines per-server configuration: how to connect, which
// metric groups are enabled, and any server-specific overrides.
//
// This package does not open connections or run collectors; it only parses
// and validates configuration.
package server

import (
	"fmt"

	"aymanhs/watchtower/internal/metric"
)

// ConnectionType identifies the transport used to reach a server. SSH is the
// only one implemented initially; the field exists so others can be added
// without reshaping Config.
type ConnectionType string

const (
	ConnectionSSH ConnectionType = "ssh"
)

// Connection describes how to reach a server. Key is a path to a private
// key file; it is never a literal key or password.
type Connection struct {
	Type ConnectionType `yaml:"type"`
	Host string         `yaml:"host"`
	Port int            `yaml:"port"`
	User string         `yaml:"user"`
	Key  string         `yaml:"key"`
}

// MetricOverride lets one server tweak a metric definition's collection
// behavior without modifying the shared YAML definition. Nil fields mean
// "use the definition's default".
type MetricOverride struct {
	Interval *metric.Duration `yaml:"interval,omitempty"`
	Timeout  *metric.Duration `yaml:"timeout,omitempty"`
	Config   map[string]any   `yaml:"config,omitempty"`
}

// Config is one server's configuration: how to reach it, which metric
// groups it collects, and any per-metric overrides.
type Config struct {
	Name       string                    `yaml:"name"`
	Connection Connection                `yaml:"connection"`
	Groups     []string                  `yaml:"groups,omitempty"`
	Metrics    map[string]MetricOverride `yaml:"metrics,omitempty"`

	// SourceFile is set by the loader, not read from YAML, for diagnostics.
	SourceFile string `yaml:"-"`
}

// Validate checks structural invariants the YAML loader cannot enforce by
// itself.
func (c *Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("server config missing name")
	}
	if c.Connection.Type != ConnectionSSH {
		return fmt.Errorf("server %q: unsupported connection type %q", c.Name, c.Connection.Type)
	}
	if c.Connection.Host == "" {
		return fmt.Errorf("server %q: connection.host is required", c.Name)
	}
	if c.Connection.Port == 0 {
		c.Connection.Port = 22
	}
	if c.Connection.User == "" {
		return fmt.Errorf("server %q: connection.user is required", c.Name)
	}
	if c.Connection.Key == "" {
		return fmt.Errorf("server %q: connection.key is required (path to private key)", c.Name)
	}
	if len(c.Groups) == 0 {
		return fmt.Errorf("server %q: must select at least one group", c.Name)
	}
	return nil
}
