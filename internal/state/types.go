// Package state is the application's data model for presentation: a
// thread-safe, continuously-updated view of every server's current
// metrics and overall health, independent of any particular GUI toolkit.
// This is the brief's "Application API / data model" layer — a GUI (or any
// other presentation) reads Store.Snapshot and nothing else from the
// collection engine.
package state

import (
	"time"
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

// Worst is worst exported — a presentation layer grouping servers (e.g.
// by Cluster) needs the same "a single critical member makes the whole
// group read critical" rule Store already applies across one server's
// metrics, without duplicating the rank table.
func Worst(a, b Status) Status { return worst(a, b) }

// Scale is a gauge or bars dashboard widget's own 0%-100% reference — set
// by whoever builds that widget (dashboard.Widget.Min/Max), not anything
// the collection engine resolves any more: there's no per-measurement
// Display/Scale left to derive it from (see internal/metric's own
// package doc). A widget with no Scale configured just doesn't draw a
// gauge dial at all (bars falls back to scaling relative to its own
// peer values instead — see cmd/watchtower-gui's barsTile).
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

// MeasurementView is one named value within a metric, as last observed —
// just a key and a number. Presentation is decided by the dashboard
// widget, not by the collection engine.
type MeasurementView struct {
	Key   string // e.g. "available", "used" — the raw collector output key
	Value float64
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
	Name string
	// Cluster is the free-form grouping label from server.Config.Cluster,
	// if any — not interpreted here, just carried through for a GUI to
	// group by. "" means this server isn't part of any named cluster.
	Cluster string
	Status  Status       // worst of all its Metrics
	Metrics []MetricView // sorted by Name
}
