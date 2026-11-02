// Package dashboard is a server's own, user-composed widget grid — the
// GUI's "what do I want to look at" layer, deliberately decoupled from
// metric.Display's "how does this one measurement render inline"
// auto-display. A metric definition's Display block is about a sensible
// default appearing with zero GUI config; a dashboard widget is someone
// choosing, after the fact, to put a specific measurement (or several)
// on a chart, gauge, number or pie tile.
//
// A dashboard belongs to one server — its widgets' Series name a metric
// and measurement, never a server, because the server is already implied
// by whichever server.Config.Dashboard they're in. This package only
// defines and validates the shape; internal/server owns loading it as
// part of Config and persisting edits back to that server's YAML file.
package dashboard

import "fmt"

// WidgetType is the tile's visual shape. Purely declarative, like
// metric.Widget — the GUI decides how to draw it, this package only
// validates that the shape and its Series count make sense together.
type WidgetType string

const (
	// WidgetChart is a multi-series line chart over history — the same
	// unit-grouped, dual-axis rendering the ad hoc per-metric chart
	// already does, just fed an explicit, user-picked list of series
	// instead of "every measurement of whichever metric was clicked".
	WidgetChart WidgetType = "chart"
	// WidgetGauge is a single dial-style gauge — needs the one
	// measurement it shows to already resolve a Scale from its own
	// metric.Display (a dashboard widget doesn't invent one).
	WidgetGauge WidgetType = "gauge"
	// WidgetNumber is a single plain value tile.
	WidgetNumber WidgetType = "number"
	// WidgetPie is a proportion-of-whole tile across 2+ measurements
	// (e.g. docker's running/paused/stopped container counts).
	WidgetPie WidgetType = "pie"
)

func (w WidgetType) valid() bool {
	switch w {
	case WidgetChart, WidgetGauge, WidgetNumber, WidgetPie:
		return true
	default:
		return false
	}
}

// SeriesRef points at one measurement within one of this widget's
// server's own metrics. Metric+Measurement together are the same pair
// internal/storage already keys history by (server.FullName() is
// "server/metric.measurement"); a dashboard widget only ever needs the
// metric/measurement half, the server half comes from context.
type SeriesRef struct {
	Metric      string `yaml:"metric"`
	Measurement string `yaml:"measurement"`
	// Label overrides the measurement's own DisplayLabel for this
	// widget only — unset means use whatever the metric definition
	// already calls it.
	Label string `yaml:"label,omitempty"`

	// Format is a user-supplied printf-style format string for this
	// series' number — e.g. "%.0f", "%5.2fGb", "%6.2f%%" — for a
	// dashboard widget wanting to show a value exactly how it chooses
	// to, not how metric.Display.Format's int/human auto-scaling would.
	// Unset means fall back to the measurement's own formatValue
	// (unit + display.format) exactly as before this field existed.
	// Never validated here (a bad verb isn't this package's business,
	// and Go's fmt never panics on one — it prints "%!verb(...)",
	// visibly wrong rather than silently blank, same as any other
	// best-effort fallback in this app).
	Format string `yaml:"format,omitempty"`
}

func (s SeriesRef) valid() bool {
	return s.Metric != "" && s.Measurement != ""
}

// Widget is one tile in a server's dashboard grid.
type Widget struct {
	Type   WidgetType  `yaml:"type"`
	Title  string      `yaml:"title,omitempty"`
	Series []SeriesRef `yaml:"series"`

	// Span is how many grid cells this tile takes, each side (a 2 means
	// 2x2, not 2x1) — 0 (unset) means "this type's own default": 2 for a
	// chart (axis labels, a legend and several series genuinely need more
	// room than a single number does), 1 for everything else. Explicit
	// Span always overrides the default either direction — a one-series
	// gauge can be made bigger, a chart with only one line can be made
	// smaller.
	Span int `yaml:"span,omitempty"`
}

// defaultSpan is Span's unset (0) value resolved per type.
func (t WidgetType) defaultSpan() int {
	if t == WidgetChart {
		return 2
	}
	return 1
}

// maxSpan is the largest Span this package accepts — generous enough for
// "let me see this one clearly" without a single tile swallowing an
// entire dashboard.
const maxSpan = 3

// EffectiveSpan is Span if set, else Type's own default.
func (w Widget) EffectiveSpan() int {
	if w.Span > 0 {
		return w.Span
	}
	return w.Type.defaultSpan()
}

// minSeries is how many Series a WidgetType requires: a gauge or number
// shows exactly one measurement, a chart wants at least one line, a pie
// needs at least two slices to be a proportion of anything.
func (t WidgetType) minSeries() int {
	switch t {
	case WidgetGauge, WidgetNumber:
		return 1
	case WidgetPie:
		return 2
	default: // WidgetChart
		return 1
	}
}

// maxSeries is 0 for "no limit" (chart, pie), 1 for widgets that only
// ever show a single measurement (gauge, number).
func (t WidgetType) maxSeries() int {
	switch t {
	case WidgetGauge, WidgetNumber:
		return 1
	default:
		return 0
	}
}

// Validate checks one widget's shape in isolation — it does not check
// that its Series actually name a real metric/measurement; that's
// resolved live against the server's current snapshot when rendered
// (a widget referencing a metric since removed from metrics/ degrades to
// a "not reporting" placeholder, the same spirit as a stale metric
// keeping its last-known value instead of vanishing).
func (w Widget) Validate() error {
	if !w.Type.valid() {
		return fmt.Errorf("dashboard widget: unknown type %q", w.Type)
	}
	if w.Span < 0 || w.Span > maxSpan {
		return fmt.Errorf("dashboard widget %q (%s): span %d out of range (1-%d, or 0 for the type's default)", w.Title, w.Type, w.Span, maxSpan)
	}
	if len(w.Series) < w.Type.minSeries() {
		return fmt.Errorf("dashboard widget %q (%s): needs at least %d series, has %d", w.Title, w.Type, w.Type.minSeries(), len(w.Series))
	}
	if max := w.Type.maxSeries(); max > 0 && len(w.Series) > max {
		return fmt.Errorf("dashboard widget %q (%s): takes at most %d series, has %d", w.Title, w.Type, max, len(w.Series))
	}
	for i, s := range w.Series {
		if !s.valid() {
			return fmt.Errorf("dashboard widget %q (%s): series %d missing metric/measurement", w.Title, w.Type, i)
		}
	}
	return nil
}
