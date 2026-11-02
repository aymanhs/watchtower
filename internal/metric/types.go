// Package metric defines metric metadata: the declarative YAML shape that
// describes how a measurement is collected, interpreted and alerted on.
//
// This package knows nothing about SSH, storage, events state or the GUI.
// It only parses and validates definitions.
package metric

import (
	"fmt"
	"strings"
	"text/template"
)

// Type classifies how a measurement behaves over time.
type Type string

const (
	TypeGauge   Type = "gauge"   // current value, e.g. memory used.
	TypeCounter Type = "counter" // monotonically increasing, e.g. bytes received.
	TypeRate    Type = "rate"    // change per unit time, e.g. requests/sec.
)

func (t Type) valid() bool {
	switch t {
	case TypeGauge, TypeCounter, TypeRate:
		return true
	default:
		return false
	}
}

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

// Measurement describes one named value returned by a single collector execution.
type Measurement struct {
	Unit      string `yaml:"unit"`
	Type      Type   `yaml:"type"`
	Precision int    `yaml:"precision,omitempty"`

	// Display is this measurement's GUI presentation hint. Nil (no
	// "display:" block in YAML) means plain text, no code change needed.
	Display *Display `yaml:"display,omitempty"`
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

// EventRule is a generic threshold rule attached to one measurement.
// The event engine evaluates these without knowing what the metric means.
type EventRule struct {
	Name        string         `yaml:"name"`
	Measurement string         `yaml:"measurement"` // which key in Definition.Metrics this applies to; required, no name-based guessing
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

// Definition is one metric YAML file: identity, collection, measurements and
// their event rules. One execution of Collector may produce several
// Measurements, one per key in Metrics.
type Definition struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Groups      []string `yaml:"groups,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`

	Interval Duration `yaml:"interval"`
	Timeout  Duration `yaml:"timeout"`

	Collector Collector              `yaml:"collector"`
	Metrics   map[string]Measurement `yaml:"metrics"`
	Events    []EventRule            `yaml:"events,omitempty"`

	// SourceFile is set by the loader, not read from YAML, for diagnostics.
	SourceFile string `yaml:"-"`
}

// ResolveMeasurement looks up name against d.Metrics. The common case is
// a direct hit — name matches a declared key exactly, baseKey == name,
// instance == "". The other case is a dynamically per-instance name a
// collector can legitimately emit without it being individually declared
// (disk.yaml's per-volume stats: one declared "used_percent" measurement,
// a collector that discovers real volumes at runtime and emits
// "root_used_percent", "data_used_percent", ... — baseKey is "used_percent"
// (what Display/Scale/events were actually declared against), instance is
// "root"/"data" (everything before the matched "_<baseKey>" suffix). ok
// is false only when neither resolves.
func (d *Definition) ResolveMeasurement(name string) (m Measurement, baseKey, instance string, ok bool) {
	if m, ok := d.Metrics[name]; ok {
		return m, name, "", true
	}
	for key, m := range d.Metrics {
		if suffix := "_" + key; strings.HasSuffix(name, suffix) {
			return m, key, strings.TrimSuffix(name, suffix), true
		}
	}
	return Measurement{}, "", "", false
}

// Validate checks structural invariants the YAML loader cannot enforce by
// itself (required fields, sane durations, referenced measurement keys).
func (d *Definition) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("metric definition missing name")
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
	if len(d.Metrics) == 0 {
		return fmt.Errorf("metric %q: must define at least one measurement", d.Name)
	}
	primaryCount := 0
	for key, m := range d.Metrics {
		if m.Unit == "" {
			return fmt.Errorf("metric %q: measurement %q missing unit", d.Name, key)
		}
		if !m.Type.valid() {
			return fmt.Errorf("metric %q: measurement %q has invalid type %q", d.Name, key, m.Type)
		}
		if err := d.validateDisplay(key, m); err != nil {
			return err
		}
		if m.Display != nil && m.Display.Primary {
			primaryCount++
		}
	}
	if primaryCount > 1 {
		return fmt.Errorf("metric %q: more than one measurement marked display.primary", d.Name)
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
		if _, ok := d.Metrics[ev.Measurement]; !ok {
			return fmt.Errorf("metric %q: event %q references unknown measurement %q", d.Name, ev.Name, ev.Measurement)
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
