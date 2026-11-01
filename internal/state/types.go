// Package state is the application's data model for presentation: a
// thread-safe, continuously-updated view of every server's current
// metrics and overall health, independent of any particular GUI toolkit.
// This is the brief's "Application API / data model" layer — a GUI (or any
// other presentation) reads Store.Snapshot and nothing else from the
// collection engine.
package state

import "time"

// Status is the four-color state the brief calls for: green, amber, red,
// grey. It generalizes metric.Severity with an explicit "unreachable/stale"
// state that severity alone doesn't carry.
type Status string

const (
	StatusOK       Status = "ok"
	StatusWarning  Status = "warning"
	StatusCritical Status = "critical"
	StatusUnknown  Status = "unknown" // stale or unreachable — last value may be old
)

// rank orders statuses worst-first for aggregation: a single critical
// metric makes the whole server read critical even if others are unknown.
func (s Status) rank() int {
	switch s {
	case StatusCritical:
		return 3
	case StatusWarning:
		return 2
	case StatusUnknown:
		return 1
	default:
		return 0
	}
}

// worst returns whichever of a, b ranks worse.
func worst(a, b Status) Status {
	if b.rank() > a.rank() {
		return b
	}
	return a
}

// MeasurementView is one named value within a metric, as last observed.
type MeasurementView struct {
	Key   string // e.g. "available", "used"
	Value float64
	Unit  string
}

// MetricView is one metric definition's current state on one server: its
// last-known measurements, plus whether the collector is currently
// succeeding. Measurements are never cleared on failure — they stay at
// their last-known value, with Stale marking that they may be out of date.
type MetricView struct {
	Name         string
	Status       Status
	Stale        bool
	LastError    string
	Updated      time.Time
	Measurements []MeasurementView // sorted by Key
}

// ServerView is one server's full current state, as the GUI consumes it.
type ServerView struct {
	Name    string
	Status  Status       // worst of all its Metrics
	Metrics []MetricView // sorted by Name
}
