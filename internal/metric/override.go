package metric

import "time"

// Override lets a group or a server tweak a metric definition's collection
// behavior without modifying the shared YAML definition. Nil fields mean
// "inherit whatever came before this override in the resolution chain".
type Override struct {
	Interval *Duration      `yaml:"interval,omitempty"`
	Timeout  *Duration      `yaml:"timeout,omitempty"`
	Config   map[string]any `yaml:"config,omitempty"`
}

// Resolve computes the effective interval, timeout and collector config for
// this definition after applying overrides in order — each one takes
// precedence over everything before it. The intended call shape is: group
// overrides first, in the server's declared group order, then the
// server's own per-metric override last, giving the precedence chain
// definition-default -> group -> server required by the config model.
func (d *Definition) Resolve(overrides ...Override) (interval, timeout time.Duration, config map[string]any) {
	interval = d.Interval.Duration
	timeout = d.Timeout.Duration
	config = make(map[string]any, len(d.Collector.Config))
	// Seed from the collector's own defaults (e.g. disk's path: "/") before
	// any override — a definition can ship something sensible so a server
	// only needs to supply config at all when it wants something different,
	// the same precedence chain as interval/timeout, just with one more
	// rung underneath.
	for k, v := range d.Collector.Config {
		config[k] = v
	}
	for _, o := range overrides {
		if o.Interval != nil {
			interval = o.Interval.Duration
		}
		if o.Timeout != nil {
			timeout = o.Timeout.Duration
		}
		for k, v := range o.Config {
			config[k] = v
		}
	}
	return interval, timeout, config
}
