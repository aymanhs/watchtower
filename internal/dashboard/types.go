// Package dashboard is a user-composed widget grid — the GUI's "what do I
// want to look at" layer, deliberately decoupled from metric.Display's
// "how does this one measurement render inline" auto-display. A metric
// definition's Display block is about a sensible default appearing with
// zero GUI config; a dashboard widget is someone choosing, after the
// fact, to put a specific selection of measurements on a chart, gauge,
// number or pie tile.
//
// A Dashboard is global, not scoped to any one server: each of a widget's
// Series names a selector (see internal/selector) — a pattern like
// "jumpbox/mem.free_pct" (one fixed metric on one server),
// "*/mem.free_pct" (that metric on every server) or
// "cluster:connetic-nats/nats.connections" (every server in a cluster) —
// rather than a bare metric+measurement pair implicitly scoped to
// whichever server's config it used to live inside. How many concrete
// (server, metric, measurement) tuples a selector actually resolves to
// is live data, not something this package knows or validates; a widget
// renderer decides how to lay out however many it gets back (one gauge,
// or a small grid of them; one line on a chart, or several).
//
// This package only defines and validates a Dashboard's shape — that its
// widgets make sense as widgets, and that each Series' selector text at
// least parses. internal/server used to own loading a dashboard as part
// of a server's own Config and persisting edits back to that server's
// YAML file; that 1:1 coupling is gone (see Store in this package).
package dashboard

import (
	"fmt"

	"aymanhs/watchtower/internal/selector"
)

// WidgetType is the tile's visual shape. Purely declarative, like
// metric.Widget — the GUI decides how to draw it, this package only
// validates that the shape makes sense.
type WidgetType string

const (
	// WidgetChart is a multi-series line chart over history — the same
	// unit-grouped, dual-axis rendering the ad hoc per-metric chart
	// already does, just fed an explicit, user-picked list of series
	// instead of "every measurement of whichever metric was clicked".
	WidgetChart WidgetType = "chart"
	// WidgetGauge is a dial-style gauge — needs each measurement it
	// shows to already resolve a Scale from its own metric.Display (a
	// dashboard widget doesn't invent one). A selector matching several
	// servers draws several gauges, not one.
	WidgetGauge WidgetType = "gauge"
	// WidgetNumber is a plain value tile — several matches draw a small
	// grid of values rather than one.
	WidgetNumber WidgetType = "number"
	// WidgetPie is a proportion-of-whole tile across 2+ measurements
	// (e.g. docker's running/paused/stopped container counts).
	WidgetPie WidgetType = "pie"
	// WidgetBars is a horizontal bar per resolved match — built for a
	// selector that returns several measurements at once and needs each
	// one told apart by something more concrete than a repeated metric
	// name: a selector's own resolved Match.Instance (e.g. each mount
	// point for "jumpbox/disk.*.used_percent"), falling back to the
	// measurement's own label, or the server name when matches span
	// several servers — see cmd/watchtower-gui's matchLabel/barLabel.
	WidgetBars WidgetType = "bars"
)

func (w WidgetType) valid() bool {
	switch w {
	case WidgetChart, WidgetGauge, WidgetNumber, WidgetPie, WidgetBars:
		return true
	default:
		return false
	}
}

// SeriesRef is one line in a widget's Series list: a selector pattern
// (see internal/selector.Parse) naming whichever measurements this
// widget wants, on whichever server(s) match it — fixed to one server,
// a glob across several, or a whole cluster. How many concrete matches
// that resolves to at any given moment is live data: a resolver (outside
// this package, against a state snapshot) is what turns one SeriesRef
// into zero or more rendered series.
type SeriesRef struct {
	Selector string `yaml:"selector"`

	// Label overrides the measurement's own DisplayLabel for this
	// series — unset means use whatever the metric definition already
	// calls it. When a selector resolves to several matches, this is
	// used as a prefix/template rather than a single fixed name; the
	// renderer still needs to tell them apart (e.g. by server name).
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

// Parsed parses s.Selector, surfacing any error a resolver or editor UI
// needs — Validate below already rejects an unparseable selector at save
// time, so a caller past that point can usually ignore the error.
func (s SeriesRef) Parsed() (selector.Selector, error) {
	return selector.Parse(s.Selector)
}

func (s SeriesRef) valid() bool {
	_, err := s.Parsed()
	return err == nil
}

// Widget is one tile in a dashboard grid.
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
	if t == WidgetChart || t == WidgetBars {
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

// Validate checks one widget's shape in isolation — that its type and
// span are sane, and that it has at least one Series whose selector text
// at least parses. It deliberately does not check how many concrete
// measurements each selector actually resolves to, or enforce a
// type-specific count (a gauge used to require exactly one Series; a
// pie, at least two) — a single selector can now expand to any number of
// matches at render time (a glob across every server, say), so "does
// this widget have enough to draw" is live data a resolver answers
// against a current snapshot, the same spirit as a Series naming a
// metric since removed from metrics/ degrading to a "not reporting"
// placeholder instead of failing to load at all.
func (w Widget) Validate() error {
	if !w.Type.valid() {
		return fmt.Errorf("dashboard widget: unknown type %q", w.Type)
	}
	if w.Span < 0 || w.Span > maxSpan {
		return fmt.Errorf("dashboard widget %q (%s): span %d out of range (1-%d, or 0 for the type's default)", w.Title, w.Type, w.Span, maxSpan)
	}
	if len(w.Series) == 0 {
		return fmt.Errorf("dashboard widget %q (%s): needs at least one series", w.Title, w.Type)
	}
	for i, s := range w.Series {
		if !s.valid() {
			return fmt.Errorf("dashboard widget %q (%s): series %d: invalid selector %q", w.Title, w.Type, i, s.Selector)
		}
	}
	return nil
}

// Dashboard is a named, global widget grid — independent of any one
// server, saved and loaded as its own unit (see Store).
type Dashboard struct {
	Name    string   `yaml:"name"`
	Widgets []Widget `yaml:"widgets"`

	// SourceFile is set by Store (LoadStore/Save), never read from YAML —
	// which file this Dashboard round-trips through, for diagnostics and
	// so Save knows where to write it back.
	SourceFile string `yaml:"-"`
}

// Validate checks Dashboard's own shape and every Widget's.
func (d Dashboard) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("dashboard: missing name")
	}
	for i, w := range d.Widgets {
		if err := w.Validate(); err != nil {
			return fmt.Errorf("dashboard %q: widget %d: %w", d.Name, i, err)
		}
	}
	return nil
}
