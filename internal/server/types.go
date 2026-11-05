// Package server defines per-server configuration: how to connect, which
// metric groups are enabled, and any server-specific overrides.
//
// Config precedence is: metric definition defaults, then group-level
// config (internal/group) in the order Groups lists them — a later group
// in the list wins over an earlier one on conflicting settings, which is
// deterministic and independent of how group YAML files happen to load
// from disk — then this server's own Metrics override last, which always
// wins over anything a group configured.
//
// This package does not open connections or run collectors; it only parses
// and validates configuration.
package server

import (
	"fmt"

	"aymanhs/watchtower/internal/metric"
)

// ConnectionType identifies the transport used to reach a server.
type ConnectionType string

const (
	ConnectionSSH ConnectionType = "ssh"
	// ConnectionLocal runs collector commands directly on the machine
	// Watchtower itself is running on — no SSH hop, Host/Port/User/Key
	// are unused. For endpoints reachable straight from wherever
	// Watchtower runs, where SSH-ing into some other host would be
	// pointless. See internal/localrun for exactly how it executes.
	ConnectionLocal ConnectionType = "local"
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

// MetricOverride is a server-specific metric override. It's the same shape
// used by groups (metric.Override); the alias exists so server-specific
// call sites can name it without importing metric directly for the type.
type MetricOverride = metric.Override

// Config is one server's configuration: how to reach it, which metric
// groups it collects (in the order given — see the package doc on group
// precedence), and any per-metric overrides, which always win over
// whatever the server's groups configured.
type Config struct {
	Name       string                    `yaml:"name"`
	Connection Connection                `yaml:"connection"`
	Groups     []string                  `yaml:"groups,omitempty"`
	Metrics    map[string]MetricOverride `yaml:"metrics,omitempty"`

	// Cluster is an optional, free-form grouping label a presentation
	// layer may use to show related servers together (e.g. a GUI grouping
	// a NATS cluster's members) — not interpreted by the engine at all,
	// and not specific to any one kind of server. Members of the same
	// cluster need not share a host: each Config still has its own
	// Connection, so a cluster can span machines freely.
	Cluster string `yaml:"cluster,omitempty"`

	// SourceFile is set by the loader, not read from YAML, for
	// diagnostics.
	SourceFile string `yaml:"-"`
}

// Validate checks structural invariants the YAML loader cannot enforce by
// itself.
func (c *Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("server config missing name")
	}
	switch c.Connection.Type {
	case ConnectionSSH:
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
	case ConnectionLocal:
		// Host/Port/User/Key are meaningless for a local server; nothing
		// to validate beyond the type itself being recognized.
	default:
		return fmt.Errorf("server %q: unsupported connection type %q", c.Name, c.Connection.Type)
	}
	if len(c.Groups) == 0 {
		return fmt.Errorf("server %q: must select at least one group", c.Name)
	}
	return nil
}
