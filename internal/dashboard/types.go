// Package dashboard is a user-composed widget grid — the GUI's "what do I
// want to look at" layer. There's no auto-display derived from a metric
// definition any more (internal/metric declares no Unit/Type/Display at
// all — see its own package doc); a dashboard widget is someone
// explicitly choosing to put a specific selection of measurements on a
// chart, gauge, number or pie tile, with its own Min/Max for a gauge/bars'
// scale.
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
	"time"

	"aymanhs/watchtower/internal/selector"
)

// WidgetType is the tile's visual shape. Purely declarative, like
// metric.Widget — the GUI decides how to draw it, this package only
// validates that the shape makes sense.
type WidgetType string

const (
	// WidgetChart is a multi-series line chart over history, all series
	// sharing one y-axis, fed an explicit, user-picked list of series.
	WidgetChart WidgetType = "chart"
	// WidgetGauge is a dial-style gauge — needs the widget's own Min/Max
	// (see Widget.Scale) set to draw a dial at all; with neither set it
	// shows a "no scale configured" placeholder instead. A selector
	// matching several servers draws several gauges, not one.
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
	// WidgetObject shows the latest value of every object a series
	// selector matches (e.g. "jumpbox/processes.*"), pretty-printed.
	WidgetObject WidgetType = "object"
)

func (w WidgetType) valid() bool {
	switch w {
	case WidgetChart, WidgetGauge, WidgetNumber, WidgetPie, WidgetBars, WidgetObject:
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

	// Label overrides the measurement key for this series — unset means
	// use the raw key. When a selector resolves to several matches, this is
	// used as a prefix/template rather than a single fixed name; the
	// renderer still needs to tell them apart (e.g. by server name).
	Label string `yaml:"label,omitempty"`

	// Format is a user-supplied printf-style format string for this
	// series' number — e.g. "%.0f", "%5.2fGb", "%6.2f%%" — for a
	// dashboard widget wanting to show a value exactly how it chooses
	// to. Unset means fall back to the plain default formatValue (two
	// decimal places, no unit-driven scaling — there's no per-measurement
	// Unit any more, see internal/metric's package doc). Never validated
	// here (a bad verb isn't this package's business,
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

	// Span is how many grid cells this tile took, both sides equally (a
	// 2 always meant 2x2, never 2x1) — superseded by SpanX/SpanY below,
	// which size each side independently. Still read (never written) for
	// backward compatibility with a dashboard file saved before SpanX/Y
	// existed; RawSpanX/RawSpanY fall back to it when SpanX/SpanY are
	// unset. A fresh Save always writes SpanX/SpanY only.
	Span int `yaml:"span,omitempty"`

	// SpanX/SpanY are how many grid cells wide/tall this tile takes —
	// independent of each other, so a tile can be wide-and-short,
	// tall-and-narrow, or square, not just square. 0 (unset) means "this
	// type's own default" in that direction: 2 for a chart or bars
	// (axis labels, a legend, several bars/lines genuinely need more
	// room than a single number does), 1 for everything else. An
	// explicit value always overrides the default in that direction
	// only — a one-series gauge can be made bigger, a chart with only
	// one line can be made narrower without also being made shorter.
	SpanX int `yaml:"span_x,omitempty"`
	SpanY int `yaml:"span_y,omitempty"`

	// Min/Max are a gauge or bars widget's own absolute scale — the
	// 0%/100% reference its dial or bar fills against. There's no
	// per-measurement scale to resolve this from any more
	// (internal/metric declares no Unit/Type/Display/Scale at all —
	// see its package doc); whoever builds the widget sets it directly,
	// once, here, same as every other per-widget choice. Both nil (the
	// common case: a new gauge/bars widget defaults to 0-100, covering
	// any *_percent measurement) means no scale at all: a gauge shows
	// "no scale configured" instead of a dial, bars fall back to
	// scaling relative to their own peer values instead (see
	// cmd/watchtower-gui's barsTile) — a chart/pie/number widget never
	// reads these at all.
	Min *float64 `yaml:"min,omitempty"`
	Max *float64 `yaml:"max,omitempty"`

	// Range is how far back a chart looks: one of Ranges ("1h", "6h",
	// "1d", "7d", "30d"). Empty means DefaultRange, so dashboards saved
	// before this field existed keep their one-hour charts. Only chart
	// widgets read it.
	Range string `yaml:"range,omitempty"`
}

// Ranges are the chart ranges a widget may choose, shortest first.
var Ranges = []string{"1h", "6h", "1d", "7d", "30d"}

// DefaultRange is a chart's range when Widget.Range is unset.
const DefaultRange = "1h"

var rangeDurations = map[string]time.Duration{
	"1h": time.Hour, "6h": 6 * time.Hour, "1d": 24 * time.Hour,
	"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour,
}

// RangeDuration resolves a Range name to its duration; ok is false for a
// name that isn't in Ranges.
func RangeDuration(name string) (time.Duration, bool) {
	d, ok := rangeDurations[name]
	return d, ok
}

// EffectiveRange is w's chart range with the default applied.
func (w Widget) EffectiveRange() string {
	if w.Range == "" {
		return DefaultRange
	}
	return w.Range
}

// Scale reports w's own Min/Max as a plain (min, max, ok) triple — ok is
// false if either is unset, or if they're equal (a degenerate range
// nothing can fill a fraction of).
func (w Widget) Scale() (min, max float64, ok bool) {
	if w.Min == nil || w.Max == nil || *w.Min == *w.Max {
		return 0, 0, false
	}
	return *w.Min, *w.Max, true
}

// defaultSpan is SpanX/SpanY's unset (0) value resolved per type — both
// directions share one default (a chart's "wants more room" is the same
// story wide or tall), only an explicit SpanX/SpanY ever pulls them
// apart.
func (t WidgetType) defaultSpan() int {
	if t == WidgetChart || t == WidgetBars || t == WidgetObject {
		return 2
	}
	return 1
}

// MaxSpan is the largest SpanX/SpanY this package accepts, each
// direction independently — generous enough for "let me see this one
// clearly" without a single tile swallowing an entire dashboard.
const MaxSpan = 6

// RawSpanX/RawSpanY are SpanX/SpanY's (or, failing that, the legacy
// square Span's) actual stored value, 0 meaning "unset / auto" — unlike
// EffectiveSpanX/EffectiveSpanY, these don't resolve to the type's own
// default, so an editor can tell "never explicitly set" apart from
// "explicitly set to what the default happens to be".
func (w Widget) RawSpanX() int {
	if w.SpanX > 0 {
		return w.SpanX
	}
	return w.Span
}

func (w Widget) RawSpanY() int {
	if w.SpanY > 0 {
		return w.SpanY
	}
	return w.Span
}

// EffectiveSpanX/EffectiveSpanY are RawSpanX/RawSpanY if set, else Type's
// own default.
func (w Widget) EffectiveSpanX() int {
	if x := w.RawSpanX(); x > 0 {
		return x
	}
	return w.Type.defaultSpan()
}

func (w Widget) EffectiveSpanY() int {
	if y := w.RawSpanY(); y > 0 {
		return y
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
	if w.Span < 0 || w.Span > MaxSpan {
		return fmt.Errorf("dashboard widget %q (%s): span %d out of range (1-%d, or 0 for the type's default)", w.Title, w.Type, w.Span, MaxSpan)
	}
	if w.SpanX < 0 || w.SpanX > MaxSpan {
		return fmt.Errorf("dashboard widget %q (%s): span_x %d out of range (1-%d, or 0 for the type's default)", w.Title, w.Type, w.SpanX, MaxSpan)
	}
	if w.SpanY < 0 || w.SpanY > MaxSpan {
		return fmt.Errorf("dashboard widget %q (%s): span_y %d out of range (1-%d, or 0 for the type's default)", w.Title, w.Type, w.SpanY, MaxSpan)
	}
	if w.Range != "" {
		if _, ok := RangeDuration(w.Range); !ok {
			return fmt.Errorf("dashboard widget %q (%s): range %q must be one of %v", w.Title, w.Type, w.Range, Ranges)
		}
	}
	if (w.Min == nil) != (w.Max == nil) {
		return fmt.Errorf("dashboard widget %q (%s): min and max must both be set, or neither", w.Title, w.Type)
	}
	if w.Min != nil && w.Max != nil && *w.Min == *w.Max {
		return fmt.Errorf("dashboard widget %q (%s): max (%v) must differ from min", w.Title, w.Type, *w.Max)
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
