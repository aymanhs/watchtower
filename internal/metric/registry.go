package metric

import (
	"fmt"
	"sort"
)

// Registry indexes loaded definitions by name and resolves server groups
// into the definitions that apply.
type Registry struct {
	byName map[string]*Definition
}

// NewRegistry builds a Registry from a flat list of definitions, rejecting
// duplicate names so two YAML files can never silently collide.
func NewRegistry(defs []*Definition) (*Registry, error) {
	r := &Registry{byName: make(map[string]*Definition, len(defs))}
	for _, d := range defs {
		if existing, exists := r.byName[d.Name]; exists {
			return nil, fmt.Errorf("duplicate metric definition %q in %s and %s", d.Name, existing.SourceFile, d.SourceFile)
		}
		r.byName[d.Name] = d
	}
	return r, nil
}

// Get looks up a single definition by name.
func (r *Registry) Get(name string) (*Definition, bool) {
	d, ok := r.byName[name]
	return d, ok
}

// All returns every known definition, sorted by name.
func (r *Registry) All() []*Definition {
	out := make([]*Definition, 0, len(r.byName))
	for _, d := range r.byName {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ResolveGroups returns every definition belonging to at least one of the
// given groups, sorted by name for deterministic scheduling order.
func (r *Registry) ResolveGroups(groups []string) []*Definition {
	want := make(map[string]bool, len(groups))
	for _, g := range groups {
		want[g] = true
	}
	var out []*Definition
	for _, d := range r.byName {
		for _, g := range d.Groups {
			if want[g] {
				out = append(out, d)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
