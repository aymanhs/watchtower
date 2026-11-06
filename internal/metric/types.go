// Package metric defines metric metadata: the declarative YAML shape that
// describes how a measurement is collected and alerted on.
//
// This package knows nothing about SSH, storage, events state or the GUI,
// and — deliberately — nothing about how a measurement should look: no
// unit, no type, no display hint. A dashboard widget (internal/dashboard)
// picks its own visual shape and its own per-series label/format/scale at
// the point someone builds it; this package's only job is collection
// (how to run the collector) and alerting (what counts as a threshold
// breach), both things a dashboard can't reasonably infer on its own.
package metric

import (
	"fmt"
	"strings"
	"text/template"
)

// CollectorType identifies how a definition's command is executed.
type CollectorType string

const (
	// CollectorShell runs Command as a shell command — over SSH on the
	// target server, or locally for a "local" server. POSIX shell
	// (awk, curl, vmstat, ...), so only works where such a shell exists.
	CollectorShell CollectorType = "shell"
	// CollectorHTTP fetches URL and decodes it as JSON, natively in Go —
	// no shell, curl or jq anywhere, on any platform, remote or local.
	// For an SSH-based server the connection is tunneled through the
	// existing SSH session (so a loopback-bound endpoint like NATS's own
	// monitoring port is reachable exactly as if running on that host);
	// for a "local" server it dials directly. See internal/collect.
	CollectorHTTP CollectorType = "http"
)

// Kind says what a definition collects.
type Kind string

const (
	// KindMetric (the default) collects numbers.
	KindMetric Kind = "metric"
	// KindObject collects one JSON value, stored whole (see internal/objstore).
	KindObject Kind = "object"
)

// DefaultMaxObjectBytes caps an object's compact JSON size unless a
// definition's max_bytes says otherwise.
const DefaultMaxObjectBytes = 4096

// Collector describes how to produce raw measurement output.
type Collector struct {
	Type CollectorType `yaml:"type"`

	// Command is the shell command, for CollectorShell. A Go text
	// template rendered against the server's resolved config (Render).
	Command string `yaml:"command,omitempty"`

	// URL is the endpoint to GET and decode as JSON, for CollectorHTTP.
	// Also a template rendered against config (RenderURL).
	URL string `yaml:"url,omitempty"`
	// Fields maps a measurement key to the JSON field (dot-separated for
	// one level of nesting, e.g. "cluster.name") that holds its value.
	// A measurement with no entry here defaults to a JSON field of the
	// same name — the common case, since most APIs' field names already
	// match what you'd want to call the measurement — so Fields is only
	// needed to rename or reach into a nested one.
	Fields map[string]string `yaml:"fields,omitempty"`

	// Config is this collector's own default parameters — e.g. disk's
	// path: "/" — seeded into Definition.Resolve's config before any
	// group/server override applies. Lets a definition ship something
	// sensible so a server only needs to supply config at all when it
	// wants something different, exactly like a NATS server's endpoint
	// still must (no sensible universal default exists for that one).
	Config map[string]any `yaml:"config,omitempty"`
}

// EventCondition is the comparison a threshold rule evaluates.
type EventCondition string

const (
	ConditionAbove EventCondition = "above"
	ConditionBelow EventCondition = "below"
)

// Severity is the level of an event raised by a rule.
type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// EventRule is a generic threshold rule attached to one measurement key.
// The event engine evaluates these without knowing what the metric means —
// no declared Unit/Type/Display anywhere in this package, just a name and
// a numeric threshold. Measurement names a raw collector output key
// directly; for a per-instance one (disk's "root.used_percent"), name the
// base key ("used_percent") — SplitInstance's own instance/baseKey split
// is what lets one rule apply across however many instances a collector
// discovers at runtime, the same way it always has.
type EventRule struct {
	Name        string         `yaml:"name"`
	Measurement string         `yaml:"measurement"` // a raw collector output key (or, for a per-instance one, its base key) this applies to
	Condition   EventCondition `yaml:"condition"`
	Threshold   float64        `yaml:"threshold"`
	Severity    Severity       `yaml:"severity"`

	// RecoverBelow/RecoverAbove give the rule hysteresis: once breached, it
	// stays active until the value crosses back past this point, not just
	// past Threshold again. Only one is meaningful depending on Condition
	// (RecoverBelow for "above" rules, RecoverAbove for "below" rules). If
	// unset, recovery happens as soon as the value no longer breaches
	// Threshold.
	RecoverBelow *float64 `yaml:"recover_below,omitempty"`
	RecoverAbove *float64 `yaml:"recover_above,omitempty"`
}

// Breached reports whether value currently breaches this rule.
func (r EventRule) Breached(value float64) bool {
	switch r.Condition {
	case ConditionAbove:
		return value > r.Threshold
	case ConditionBelow:
		return value < r.Threshold
	default:
		return false
	}
}

// Recovered reports whether value has crossed back past this rule's
// recovery point. With no explicit recover_below/recover_above, recovery
// is the plain inverse of Breached (no hysteresis).
func (r EventRule) Recovered(value float64) bool {
	switch r.Condition {
	case ConditionAbove:
		if r.RecoverBelow != nil {
			return value < *r.RecoverBelow
		}
		return value <= r.Threshold
	case ConditionBelow:
		if r.RecoverAbove != nil {
			return value > *r.RecoverAbove
		}
		return value >= r.Threshold
	default:
		return true
	}
}

// Definition is one metric YAML file: identity, collection and event
// rules. No declared measurement schema — whatever keys Collector's
// command/URL actually produces at runtime are the measurements, full
// stop; see SplitInstance for the one piece of structure still imposed
// on a raw key (its optional instance qualifier).
type Definition struct {
	Name   string   `yaml:"name"`
	Groups []string `yaml:"groups,omitempty"`

	// Kind is "metric" (default) or "object". An object definition's shell
	// command prints one JSON value instead of key=value numbers.
	Kind Kind `yaml:"kind,omitempty"`
	// MaxBytes caps an object's size; 0 means DefaultMaxObjectBytes.
	MaxBytes int `yaml:"max_bytes,omitempty"`
	// Emit derives numbers from an object: key -> expression (see
	// ParseEmit). Each becomes the metric "<name>.<key>", with history,
	// charts and event rules like any other.
	Emit map[string]string `yaml:"emit,omitempty"`

	Interval Duration `yaml:"interval"`
	Timeout  Duration `yaml:"timeout"`

	Collector Collector   `yaml:"collector"`
	Events    []EventRule `yaml:"events,omitempty"`

	// SourceFile is set by the loader, not read from YAML, for diagnostics.
	SourceFile string `yaml:"-"`
}

// IsObject reports whether d collects an object rather than metrics.
func (d *Definition) IsObject() bool { return d.Kind == KindObject }

// ObjectLimit is the size cap in bytes for an object definition.
func (d *Definition) ObjectLimit() int {
	if d.MaxBytes > 0 {
		return d.MaxBytes
	}
	return DefaultMaxObjectBytes
}

// SplitInstance decomposes a raw collector output key into its base key
// and, if it has one, its instance qualifier — disk.yaml's per-volume
// stats: a collector that discovers real volumes at runtime and emits
// "root.used_percent", "data.used_percent", ... decomposes to baseKey
// "used_percent" (what an EventRule is actually declared against),
// instance "root"/"data" (everything before the last "."). No declared
// schema is consulted — unlike the old per-Definition ResolveMeasurement,
// this is unconditional: any key containing a "." decomposes this way,
// full stop. A name with no "." has no instance: baseKey is the name
// itself, instance "".
func SplitInstance(name string) (baseKey, instance string) {
	if i := strings.LastIndexByte(name, '.'); i > 0 && i < len(name)-1 {
		return name[i+1:], name[:i]
	}
	return name, ""
}

// Validate checks structural invariants the YAML loader cannot enforce by
// itself (required fields, sane durations, the collector's own shape, and
// each event rule's own shape — there's no declared measurement set left
// to check a rule's Measurement against; a rule naming a key the
// collector never actually produces just never fires, the same
// "degrades to nothing happening" spirit as a dashboard selector matching
// nothing live).
func (d *Definition) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("metric definition missing name")
	}
	switch d.Kind {
	case "", KindMetric:
	case KindObject:
		if d.Collector.Type != CollectorShell {
			return fmt.Errorf("metric %q: object definitions need a shell collector", d.Name)
		}
		for key, expr := range d.Emit {
			if _, err := ParseEmit(expr); err != nil {
				return fmt.Errorf("metric %q: emit %q: %w", d.Name, key, err)
			}
		}
	default:
		return fmt.Errorf("metric %q: unknown kind %q", d.Name, d.Kind)
	}
	if d.Kind != KindObject && len(d.Emit) > 0 {
		return fmt.Errorf("metric %q: emit is only for object definitions", d.Name)
	}
	if len(d.Groups) == 0 {
		return fmt.Errorf("metric %q: must belong to at least one group", d.Name)
	}
	if d.Interval.Duration <= 0 {
		return fmt.Errorf("metric %q: interval must be > 0", d.Name)
	}
	if d.Timeout.Duration <= 0 {
		return fmt.Errorf("metric %q: timeout must be > 0", d.Name)
	}
	if d.Timeout.Duration >= d.Interval.Duration {
		return fmt.Errorf("metric %q: timeout (%s) must be less than interval (%s)", d.Name, d.Timeout.Duration, d.Interval.Duration)
	}
	switch d.Collector.Type {
	case CollectorShell:
		if d.Collector.Command == "" {
			return fmt.Errorf("metric %q: shell collector requires a command", d.Name)
		}
		// Parse-only: config values aren't known until a server supplies
		// them at run time, but a malformed template ({{ .foo ) is a typo
		// every server would hit, so catch it once, here, at load time.
		if _, err := template.New("collector").Parse(d.Collector.Command); err != nil {
			return fmt.Errorf("metric %q: collector.command is not a valid template: %w", d.Name, err)
		}
	case CollectorHTTP:
		if d.Collector.URL == "" {
			return fmt.Errorf("metric %q: http collector requires a url", d.Name)
		}
		if _, err := template.New("collector").Parse(d.Collector.URL); err != nil {
			return fmt.Errorf("metric %q: collector.url is not a valid template: %w", d.Name, err)
		}
		for key, field := range d.Collector.Fields {
			if field == "" {
				return fmt.Errorf("metric %q: collector.fields[%q] is empty", d.Name, key)
			}
		}
	case "":
		return fmt.Errorf("metric %q: collector.type is required", d.Name)
	default:
		return fmt.Errorf("metric %q: unknown collector type %q", d.Name, d.Collector.Type)
	}
	seenEventNames := make(map[string]bool, len(d.Events))
	for _, ev := range d.Events {
		if ev.Name == "" {
			return fmt.Errorf("metric %q: event rule missing name", d.Name)
		}
		if seenEventNames[ev.Name] {
			return fmt.Errorf("metric %q: duplicate event rule name %q", d.Name, ev.Name)
		}
		seenEventNames[ev.Name] = true
		if ev.Measurement == "" {
			return fmt.Errorf("metric %q: event %q missing measurement", d.Name, ev.Name)
		}
		if ev.Condition != ConditionAbove && ev.Condition != ConditionBelow {
			return fmt.Errorf("metric %q: event %q has invalid condition %q", d.Name, ev.Name, ev.Condition)
		}
		if ev.Severity != SeverityWarning && ev.Severity != SeverityCritical {
			return fmt.Errorf("metric %q: event %q has invalid severity %q", d.Name, ev.Name, ev.Severity)
		}
	}
	return nil
}
