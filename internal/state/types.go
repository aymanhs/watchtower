// Package state is the application's data model for presentation: a
// thread-safe, continuously-updated view of every server's current
// metrics and overall health, independent of any particular GUI toolkit.
// This is the brief's "Application API / data model" layer — a GUI (or any
// other presentation) reads Store.Snapshot and nothing else from the
// collection engine.
package state

import (
	"time"

	"aymanhs/watchtower/internal/metric"
)

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

// Scale is a resolved 0%-100% reference for a Bar or Gauge widget — by the
// time it reaches a MeasurementView, metric.Scale's Min/Max/MaxRef has
// already been resolved to concrete numbers (see ResolveScale).
type Scale struct {
	Min, Max float64
}

// Fraction maps value onto this scale, clamped to [0,1] so an
// out-of-range reading never overflows or underflows a bar or gauge.
func (s Scale) Fraction(value float64) float32 {
	if s.Max == s.Min {
		return 0
	}
	f := float32((value - s.Min) / (s.Max - s.Min))
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}

// Zone is one colored band on a Gauge's dial, in the measurement's own
// value units (not a 0-1 fraction — the GUI maps it through Scale when
// drawing). Severity "" means the normal/success band.
type Zone struct {
	From, To float64
	Severity metric.Severity
}

// MeasurementView is one named value within a metric, as last observed,
// plus its GUI presentation — resolved once per collection cycle from the
// metric definition's Display block (see internal/app), not decided by
// the GUI from the measurement's name or unit.
type MeasurementView struct {
	Key   string // e.g. "available", "used"
	Value float64
	Unit  string

	Widget  metric.Widget
	Primary bool
	Scale   *Scale // nil unless Widget needs one and it resolved successfully
	Zones   []Zone // only ever set for a Gauge; nil if the measurement has no event rules to derive bands from
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
