package group

import (
	"fmt"

	"aymanhs/watchtower/internal/metric"
)

// Registry indexes loaded group configs by name.
type Registry struct {
	byName map[string]*Config
}

// NewRegistry builds a Registry from a flat list of configs, rejecting
// duplicate group names.
func NewRegistry(configs []*Config) (*Registry, error) {
	r := &Registry{byName: make(map[string]*Config, len(configs))}
	for _, c := range configs {
		if existing, exists := r.byName[c.Name]; exists {
			return nil, fmt.Errorf("duplicate group %q in %s and %s", c.Name, existing.SourceFile, c.SourceFile)
		}
		r.byName[c.Name] = c
	}
	return r, nil
}

// Get looks up a single group config by name. A server may list a group
// that has no corresponding Config here — it still selects metric
// definitions tagged with that group (see metric.Registry.ResolveGroups),
// it just carries no group-level overrides.
func (r *Registry) Get(name string) (*Config, bool) {
	c, ok := r.byName[name]
	return c, ok
}

// Overrides returns, for one metric, the override from each named group
// that configures it, in the given order — typically a server's own
// Groups list, so a later group in that list takes precedence over an
// earlier one, deterministically.
func (r *Registry) Overrides(groups []string, metricName string) []metric.Override {
	var out []metric.Override
	for _, g := range groups {
		cfg, ok := r.byName[g]
		if !ok {
			continue
		}
		if ov, ok := cfg.Metrics[metricName]; ok {
			out = append(out, ov)
		}
	}
	return out
}
