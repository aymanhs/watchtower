// Package event is the generic event/alert engine: it evaluates observed
// values against their metric definition's EventRules and maintains
// lifecycle state (raised/escalated/recovered, deduplicated) entirely in
// memory. It also tracks collector failures/recoveries as a separate
// "operational" category, per the brief's requirement that those be kept
// distinct from ordinary metric threshold events.
//
// The engine knows nothing about SSH, storage or the GUI, and nothing
// about what any particular metric means — only conditions, thresholds and
// severities attached to named measurements.
package event

import (
	"time"

	"aymanhs/watchtower/internal/metric"
)

// Kind distinguishes a transition that makes things worse from one that
// makes them better. There is deliberately no "unchanged" Kind: the engine
// only ever produces an Event on an actual state transition.
type Kind string

const (
	KindRaised    Kind = "raised"    // entered, or escalated to, a worse state
	KindRecovered Kind = "recovered" // dropped to a better state, possibly all the way to normal
)

// Category separates ordinary metric threshold events from operational
// events about the collection process itself.
type Category string

const (
	CategoryThreshold   Category = "threshold"
	CategoryOperational Category = "operational"
)

// Event is one state-transition notification, never a per-cycle one.
type Event struct {
	ID       int64
	Category Category
	Kind     Kind

	Server string
	// Metric is "name.measurement" (e.g. "memory.available") for threshold
	// events, or just the metric definition name for operational events.
	Metric string

	RuleName string          // the EventRule.Name that fired; empty for operational events and for full-recovery-to-normal events
	Severity metric.Severity // empty means "back to normal" / resolved

	Value     float64 // the measurement value that caused this transition; zero for operational events
	Threshold float64 // the rule's threshold at the time; zero for operational events

	Message      string
	Timestamp    time.Time
	Acknowledged bool
}
