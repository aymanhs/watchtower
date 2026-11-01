// Package metric defines metric metadata: the declarative YAML shape that
// describes how a measurement is collected, interpreted and alerted on.
//
// This package knows nothing about SSH, storage, events state or the GUI.
// It only parses and validates definitions.
package metric

import "fmt"

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
	// CollectorShell runs Command as a shell command over the SSH session.
	CollectorShell CollectorType = "shell"
)

// Collector describes how to produce raw measurement output.
type Collector struct {
	Type    CollectorType `yaml:"type"`
	Command string        `yaml:"command"`
}

// Measurement describes one named value returned by a single collector execution.
type Measurement struct {
	Unit      string `yaml:"unit"`
	Type      Type   `yaml:"type"`
	Precision int    `yaml:"precision,omitempty"`
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

// EventRule is a generic threshold rule attached to a measurement.
// The event engine evaluates these without knowing what the metric means.
type EventRule struct {
	Name         string         `yaml:"name"`
	Measurement  string         `yaml:"measurement,omitempty"` // which key in Definition.Metrics this applies to; defaults to the rule's own name prefix
	Condition    EventCondition `yaml:"condition"`
	Threshold    float64        `yaml:"threshold"`
	Severity     Severity       `yaml:"severity"`
	RecoverBelow *float64       `yaml:"recover_below,omitempty"`
	RecoverAbove *float64       `yaml:"recover_above,omitempty"`
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
	case "":
		return fmt.Errorf("metric %q: collector.type is required", d.Name)
	default:
		return fmt.Errorf("metric %q: unknown collector type %q", d.Name, d.Collector.Type)
	}
	if len(d.Metrics) == 0 {
		return fmt.Errorf("metric %q: must define at least one measurement", d.Name)
	}
	for key, m := range d.Metrics {
		if m.Unit == "" {
			return fmt.Errorf("metric %q: measurement %q missing unit", d.Name, key)
		}
		if !m.Type.valid() {
			return fmt.Errorf("metric %q: measurement %q has invalid type %q", d.Name, key, m.Type)
		}
	}
	for _, ev := range d.Events {
		if ev.Name == "" {
			return fmt.Errorf("metric %q: event rule missing name", d.Name)
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
