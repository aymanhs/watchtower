// This file is the per-server dashboard: a user-composed widget grid,
// decoupled from metric.Display's auto-rendering (metricLine in view.go)
// — see internal/dashboard's package doc for why the two are
// deliberately separate. Config lives in the server's own YAML
// (server.Config.Dashboard); add/remove here write straight back to it
// via server.SaveDashboard, so hand-editing the file and using the GUI
// are both just "the dashboard", never two sources of truth.
package main

import (
	"fmt"
	"log"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/dashboard"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/server"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
)

// formatVerb matches a single printf placeholder's verb letter — the
// first one, flags/width/precision and all (%[-+ 0#]*[0-9]*(.[0-9]+)?X).
// applyFormat uses it to decide whether the caller's format string wants
// an integer argument (d/b/o/x/X) or a float one (everything else);
// Go's fmt itself doesn't make that distinction for you, it just panics
// -- no, it prints "%!d(float64=...)" -- when given the wrong type.
var formatVerb = regexp.MustCompile(`%[-+ 0#]*[0-9]*(?:\.[0-9]+)?([a-zA-Z])`)

// applyFormat renders value with a user-supplied printf-style format
// string — SeriesRef.Format, e.g. "%.0f", "%5.2fGb", "%6.2f%%". Integer
// verbs get value rounded to an int64 first (fmt.Sprintf("%d", 1.0)
// prints the ugly "%!d(float64=1)", not "1"); every other verb (f, e, g,
// ...) gets the float64 as-is. A malformed format string still can't
// panic — Go's fmt never does — it just prints its own "%!verb(...)"
// text, visibly wrong rather than silently blank.
func applyFormat(format string, value float64) string {
	if m := formatVerb.FindStringSubmatch(format); m != nil {
		switch m[1] {
		case "d", "b", "o", "O", "x", "X", "c":
			return fmt.Sprintf(format, int64(value))
		}
	}
	return fmt.Sprintf(format, value)
}

// seriesValue is one series' number, formatted the way it was asked to
// be: ref.Format if the widget set one (dashboard-only, printf-style,
// always wins), else the measurement's own formatValue (unit +
// display.format) exactly as every non-dashboard presentation renders it.
func seriesValue(ref dashboard.SeriesRef, mv state.MeasurementView) string {
	if ref.Format != "" {
		return applyFormat(ref.Format, mv.Value)
	}
	return formatValue(mv.Unit, mv.Format, mv.Value)
}

// tileWidth/tileHeight are one base cell size every widget is sized in
// multiples of — "will be simple" was the explicit brief, so v1 has no
// freeform resize, just whole-cell spans (dashboard.Widget.Span, 0
// meaning the type's own default — 2x2 for a chart, 1x1 otherwise — and
// otherwise an explicit per-widget override, user-settable from the add
// dialog). A chart defaults bigger because a line chart genuinely needs
// more room than a gauge/number/pie to be readable (axis labels, a
// legend, several series) — confirmed live, a chart squeezed into one
// cell had its axis labels and legend crowding the plot itself.
const (
	tileWidth  = float32(260)
	tileHeight = float32(200)
	gridGap    = float32(12)
)

// tileSize returns the pixel size a tile spanning span cells each side
// takes, cells plus the gaps between them.
func tileSize(span int) (w, h float32) {
	s := float32(span)
	return tileWidth*s + gridGap*(s-1), tileHeight*s + gridGap*(s-1)
}

// Add-widget modal draft state — package-level for the same reason
// bottomMode/selectedServer are: one window, nowhere else for "what's
// being typed into the open dialog right now" to live between frames.
var (
	addWidgetOpen        = false
	addWidgetErr         = ""
	addWidgetType        = dashboard.WidgetChart
	addWidgetTitle       = ""
	addWidgetSinglePick  = "" // gauge/number: the Select itself is the one series
	addWidgetNewPick     = "" // chart/pie: the "available measurements" Select, before "Add" appends it
	addWidgetSeriesPicks []string
	// addWidgetFormat is the single series' optional SeriesRef.Format —
	// only exposed for gauge/number, which have exactly one series to
	// apply it to; a pie/chart's several series would each want their
	// own, which the "pick, then Add" multi-series flow has no per-item
	// editing for yet (hand-edit the YAML's series[].format for those).
	addWidgetFormat = ""
	// addWidgetSpan is the picked dashboard.Widget.Span, 0 meaning "auto"
	// — the type's own default, same as an unset Span in YAML.
	addWidgetSpan = 0
	// editWidgetIdx is -1 while the modal is adding a brand new widget,
	// or the index (into the selected server's own Dashboard slice)
	// being edited in place — confirmAddWidget checks this to decide
	// "append" vs "replace at this index".
	editWidgetIdx = -1

	// maximizedServer/maximizedIdx say which one widget, if any, is
	// temporarily filling the whole pane instead of sharing the grid —
	// index into that server's own Dashboard slice. Keyed by server too
	// (not just an index) so switching the sidebar's selection to a
	// different server never shows a stray maximized tile that happened
	// to share the same index on this one.
	maximizedServer = ""
	maximizedIdx    = -1
)

// dashboardGrid renders sv's own widget grid (cfg.Dashboard, cfg may be
// nil for a server the loader somehow has no Config for — defensive, not
// expected) plus the "+ Add widget" tile every dashboard ends with. A
// real ui.Grid, not a wrapping row: mygo's Grid supports ColumnSpan/
// RowSpan (confirmed by reading ui/grid.go before relying on it), which
// is what lets a chart tile span 2x2 cells instead of being stuck at the
// same size as a gauge/number/pie tile.
//
// If one widget is maximized (maximizedServer/maximizedIdx), it alone
// fills the pane — the rest of the grid, and the "+ Add widget" tile,
// don't render at all until it's restored.
func dashboardGrid(c *ui.Context, sv state.ServerView, cfg *server.Config, history *storage.Store, paneWidth float32) {
	var widgets []dashboard.Widget
	if cfg != nil {
		widgets = cfg.Dashboard
	}

	if maximizedServer == sv.Name && maximizedIdx >= 0 && maximizedIdx < len(widgets) {
		ui.Box(c).Fill().Padding(12).Children(func() {
			widgetTile(c, sv, cfg, widgets, maximizedIdx, history, true)
		})
		return
	}

	cols := int((paneWidth - 24) / (tileWidth + gridGap))
	if cols < 1 {
		cols = 1
	}
	tracks := make([]ui.Track, cols)
	for i := range tracks {
		tracks[i] = ui.Fixed(tileWidth)
	}
	ui.Scroll(c).Fill().Padding(12).Children(func() {
		ui.Grid(c).ColumnTracks(tracks...).Gap(gridGap).Children(func() {
			for i := range widgets {
				widgetTile(c, sv, cfg, widgets, i, history, false)
			}
			addWidgetTile(c)
		})
	})
	addWidgetModal(c, sv, cfg)
}

// widgetTile renders one configured widget by its type, with a small ×
// in its header that removes it (splices widgets and calls
// server.SaveDashboard) and a maximize/restore toggle — the only edits a
// tile itself offers; everything else about it is set once, at add time.
func widgetTile(c *ui.Context, sv state.ServerView, cfg *server.Config, widgets []dashboard.Widget, idx int, history *storage.Store, maximized bool) {
	w := widgets[idx]
	onRemove := func() {
		if cfg == nil {
			return
		}
		next := append(append([]dashboard.Widget(nil), widgets[:idx]...), widgets[idx+1:]...)
		if err := server.SaveDashboard(cfg, next); err != nil {
			log.Printf("dashboard: remove widget: %v", err)
		}
	}
	onToggleMaximize := func() {
		if maximizedServer == sv.Name && maximizedIdx == idx {
			maximizedServer, maximizedIdx = "", -1
		} else {
			maximizedServer, maximizedIdx = sv.Name, idx
		}
	}
	onEdit := func() { openWidgetEditor(idx, w) }
	onReorder := func(fromIdx int) { reorderWidget(cfg, widgets, fromIdx, idx) }

	switch w.Type {
	case dashboard.WidgetChart:
		chartTile(c, w, sv, history, idx, maximized, onRemove, onToggleMaximize, onEdit, onReorder)
	case dashboard.WidgetGauge:
		gaugeTile(c, w, sv, idx, maximized, onRemove, onToggleMaximize, onEdit, onReorder)
	case dashboard.WidgetPie:
		pieTile(c, w, sv, idx, maximized, onRemove, onToggleMaximize, onEdit, onReorder)
	default: // WidgetNumber, and any future type default to a plain value
		numberTile(c, w, sv, idx, maximized, onRemove, onToggleMaximize, onEdit, onReorder)
	}
}

// reorderWidget moves the widget at fromIdx to sit where toIdx currently
// is (the rest shift to make room, same as dragging an item in any
// reorderable list), then persists it. A no-op for any index out of
// range or a drop on itself — defensive, since by the time this runs the
// drag/drop library has already confirmed a real drop happened, but idx
// values come from a UI gesture, not a trusted source.
func reorderWidget(cfg *server.Config, widgets []dashboard.Widget, fromIdx, toIdx int) {
	if cfg == nil || fromIdx == toIdx ||
		fromIdx < 0 || fromIdx >= len(widgets) || toIdx < 0 || toIdx >= len(widgets) {
		return
	}
	next := append([]dashboard.Widget(nil), widgets...)
	moved := next[fromIdx]
	next = append(next[:fromIdx], next[fromIdx+1:]...)
	// toIdx is where moved ends up in the final (still N-long) slice, not
	// a position to insert-before in the now-(N-1)-long one — no
	// direction-dependent shift needed: inserting at toIdx into the
	// shorter slice already lands moved at index toIdx of the result,
	// whichever direction it moved.
	next = append(next[:toIdx:toIdx], append([]dashboard.Widget{moved}, next[toIdx:]...)...)
	if err := server.SaveDashboard(cfg, next); err != nil {
		log.Printf("dashboard: reorder widget: %v", err)
	}
}

// headerButtonSize is how big a widget card's own header buttons
// (edit/maximize/remove) are — deliberately smaller than this app's
// ordinary buttons: three of them now share one header row, and at the
// original size (tuned back when there were only two) they'd crowd out
// the title on anything but the widest tile.
const headerButtonSize = 9.0

// dragWidgetIdx is a dashboard widget's index while it's being dragged —
// a named type, not a bare int, so this drag/drop pairing can't
// accidentally match some unrelated int-carrying drag elsewhere in the
// app (mygo's Drag/DragOver/Drop are generic over the payload's type).
type dragWidgetIdx int

// widgetCard is the shared tile chrome every widget type uses: a header
// row (title, edit, maximize/restore, remove) and body below it, plus
// drag-to-reorder — one place that look lives, not four slightly
// different copies. Sized from span cells normally (and given that span
// in the grid, via ColumnSpan/RowSpan); Fill()s the whole pane instead
// when maximized, with no span chained — a maximized tile isn't a child
// of ui.Grid at all (dashboardGrid renders it alone, in a plain Box), so
// ColumnSpan/RowSpan wouldn't mean anything there, and dragging a
// maximized tile (there's nothing else visible to drop it on) is
// disabled the same way.
func widgetCard(c *ui.Context, key string, idx, span int, title string, maximized bool, onEdit, onRemove, onToggleMaximize func(), onReorder func(fromIdx int), body func()) ui.Element {
	t := c.Theme()
	card := ui.Column(c).Background(t.Surface).Radius(8).Padding(10).Gap(6)
	if maximized {
		card = card.Fill()
	} else {
		w, h := tileSize(span)
		card = card.Size(w, h)
	}
	card = card.Children(func() {
		ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted).Grow(1).NoWrap()
			if ui.Button(c.Key("widget-edit-"+key), "").FontSize(headerButtonSize).Tooltip("Edit widget").
				Children(func() { ui.Icon(c, editIcon).FontSize(headerButtonSize) }).
				Clicked() {
				onEdit()
			}
			maxIcon, maxTooltip := maximizeIcon, "Maximize"
			if maximized {
				maxIcon, maxTooltip = restoreIcon, "Restore"
			}
			if ui.Button(c.Key("widget-maximize-"+key), "").FontSize(headerButtonSize).Tooltip(maxTooltip).
				Children(func() { ui.Icon(c, maxIcon).FontSize(headerButtonSize) }).
				Clicked() {
				onToggleMaximize()
			}
			if ui.Button(c.Key("widget-remove-"+key), "×").FontSize(headerButtonSize).Tooltip("Remove widget").Clicked() {
				onRemove()
			}
		})
		body()
	})
	if maximized {
		return card
	}
	if span > 1 {
		card = card.ColumnSpan(span).RowSpan(span)
	}
	// Drag to reorder: this tile is both a source (carries its own
	// index) and a target (accepts another tile's index) — a highlight
	// while a drag is over it, and the actual move on drop, handled by
	// the caller (onReorder already knows which index it's at).
	card = card.Drag(dragWidgetIdx(idx))
	if _, over := ui.DragOver[dragWidgetIdx](card); over {
		card = card.Border(2, t.Accent)
	}
	if from, dropped := ui.Drop[dragWidgetIdx](card); dropped {
		onReorder(int(from))
	}
	return card
}

// widgetTitle is what a tile's header shows: the widget's own Title if
// it set one, else the one series' own label (a dashboard-only override,
// then the measurement's own DisplayLabel), else its type.
func widgetTitle(w dashboard.Widget) string {
	if w.Title != "" {
		return w.Title
	}
	if len(w.Series) == 1 {
		if w.Series[0].Label != "" {
			return w.Series[0].Label
		}
		return w.Series[0].Metric + "." + w.Series[0].Measurement
	}
	return string(w.Type)
}

// findMeasurement looks up one (metric, measurement) pair in sv's current
// snapshot — a dashboard widget's series are just names, resolved fresh
// against live data every render, same as the ad hoc chart already does;
// a series naming a metric/measurement that doesn't currently exist
// (removed from metrics/, or just not reporting yet) degrades to "not
// reporting" rather than a crash or a stale guess.
func findMeasurement(sv state.ServerView, metricName, key string) (mv state.MeasurementView, metricStatus state.Status, found bool) {
	for _, m := range sv.Metrics {
		if m.Name != metricName {
			continue
		}
		for _, meas := range m.Measurements {
			if meas.Key == key {
				return meas, m.Status, true
			}
		}
	}
	return state.MeasurementView{}, state.StatusUnknown, false
}

func numberTile(c *ui.Context, w dashboard.Widget, sv state.ServerView, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	key := fmt.Sprintf("number-%d", idx)
	span := w.EffectiveSpan()
	widgetCard(c, key, idx, span, widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		if len(w.Series) == 0 {
			ui.Text(c, "no series configured").FontSize(11).TextColor(t.TextMuted)
			return
		}
		ref := w.Series[0]
		mv, _, found := findMeasurement(sv, ref.Metric, ref.Measurement)
		fontSize := float32(40)
		if maximized {
			fontSize = 90
		} else if span > 1 {
			fontSize = 40 * float32(span)
		}
		ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			if !found {
				ui.Text(c, "not reporting").FontSize(11).TextColor(t.TextMuted)
				return
			}
			ui.Text(c, seriesValue(ref, mv)).FontSize(fontSize).Bold()
		})
	})
}

func gaugeTile(c *ui.Context, w dashboard.Widget, sv state.ServerView, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	key := fmt.Sprintf("gauge-%d", idx)
	span := w.EffectiveSpan()
	widgetCard(c, key, idx, span, widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		if len(w.Series) == 0 {
			ui.Text(c, "no series configured").FontSize(11).TextColor(t.TextMuted)
			return
		}
		ref := w.Series[0]
		mv, metricStatus, found := findMeasurement(sv, ref.Metric, ref.Measurement)
		ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			switch {
			case !found:
				ui.Text(c, "not reporting").FontSize(11).TextColor(t.TextMuted)
			case mv.Scale == nil:
				// A dashboard gauge widget never invents a scale — it
				// only shows one if the measurement's own metric.yaml
				// display block already resolved one (same rule
				// metricLine's own gauge/bar branch follows).
				ui.Text(c, "no scale configured for this measurement").FontSize(11).TextColor(t.TextMuted)
			default:
				// Base diameter fills most of a 1x1 tile's own body
				// (tileHeight minus the header and padding) instead of
				// leaving a ring of empty space around a small dial —
				// stroke width and center-label size are already
				// proportional to diameter (pressureGauge, gauge.go), so
				// scaling this up alone can't make the label collide with
				// the ring.
				diameter := float32(140)
				switch {
				case maximized:
					diameter = 300
				case span > 1:
					diameter = 140 * float32(span)
				}
				pressureGauge(c, *mv.Scale, mv.Value, mv.Zones, statusColor(t, metricStatus), diameter, seriesValue(ref, mv))
			}
		})
	})
}

func pieTile(c *ui.Context, w dashboard.Widget, sv state.ServerView, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	key := fmt.Sprintf("pie-%d", idx)
	span := w.EffectiveSpan()
	widgetCard(c, key, idx, span, widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		colors := seriesColors(t)
		type slice struct {
			label     string
			formatted string
			value     float64
			color     ui.Color
		}
		var slices []slice
		var total float64
		for i, ref := range w.Series {
			mv, _, found := findMeasurement(sv, ref.Metric, ref.Measurement)
			if !found || mv.Value <= 0 {
				continue
			}
			label := ref.Label
			if label == "" {
				label = mv.DisplayLabel()
			}
			formatted := seriesValue(ref, mv)
			if ref.Format == "" {
				// Default pie formatting (no ref.Format set) is plain
				// counts, not whatever unit the measurement happens to
				// declare — a pie slice is a share of a whole, not a
				// standalone reading.
				formatted = formatValue("count", metric.FormatDefault, mv.Value)
			}
			slices = append(slices, slice{label: label, formatted: formatted, value: mv.Value, color: colors[i%len(colors)]})
			total += mv.Value
		}
		if total <= 0 {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "no data").FontSize(11).TextColor(t.TextMuted)
			})
			return
		}
		ui.Row(c).Fill().Gap(24).AlignItems(ui.Center).Children(func() {
			// Base diameter fills most of a 1x1 tile's own height,
			// same reasoning as the gauge's — a small pie with a ring
			// of empty space around it was the actual complaint.
			diameter := float32(130)
			switch {
			case maximized:
				diameter = 260
			case span > 1:
				diameter = 130 * float32(span)
			}
			ui.Box(c).Size(diameter, diameter).Draw(func(p *ui.Painter, r ui.Rect) {
				cx, cy := r.X+r.W/2, r.Y+r.H/2
				radius := r.W / 2
				startDeg := -90.0 // 12 o'clock, clockwise — a clock face, not the gauge's speedometer gap
				for _, s := range slices {
					sweep := 360 * (s.value / total)
					p.FillPath(buildPieSlice(cx, cy, radius, startDeg, sweep), s.color)
					startDeg += sweep
				}
			})
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				for _, s := range slices {
					ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(8, 8).Radius(4).Background(s.color)
						ui.Text(c, fmt.Sprintf("%s: %s", s.label, s.formatted)).FontSize(10).TextColor(t.TextMuted).NoWrap()
					})
				}
			})
		})
	})
}

// buildPieSlice builds one closed wedge of a pie: center, out to the
// arc's start, along the arc, back to center — the filled shape a pie
// slice is, unlike buildArc (gauge.go) which builds an open curve meant
// to be stroked as a band, not filled as a sector. Same per-segment
// Bezier construction as buildArc, just closed into a sector instead.
func buildPieSlice(cx, cy, r float32, startDeg, sweepDeg float64) *ui.Path {
	path := new(ui.Path)
	path.MoveTo(cx, cy)
	if sweepDeg == 0 {
		path.Close()
		return path
	}
	const maxSegDeg = 45.0
	segments := int(math.Ceil(math.Abs(sweepDeg) / maxSegDeg))
	if segments < 1 {
		segments = 1
	}
	segRad := (sweepDeg / float64(segments)) * math.Pi / 180
	alpha := float32(4.0 / 3.0 * math.Tan(segRad/4))

	theta := startDeg * math.Pi / 180
	x0 := cx + r*float32(math.Cos(theta))
	y0 := cy + r*float32(math.Sin(theta))
	path.LineTo(x0, y0)

	for i := 0; i < segments; i++ {
		theta2 := theta + segRad
		x3 := cx + r*float32(math.Cos(theta2))
		y3 := cy + r*float32(math.Sin(theta2))
		t1x, t1y := -float32(math.Sin(theta)), float32(math.Cos(theta))
		t2x, t2y := -float32(math.Sin(theta2)), float32(math.Cos(theta2))
		path.CubeTo(x0+alpha*r*t1x, y0+alpha*r*t1y, x3-alpha*r*t2x, y3-alpha*r*t2y, x3, y3)
		x0, y0, theta = x3, y3, theta2
	}
	path.Close()
	return path
}

func chartTile(c *ui.Context, w dashboard.Widget, sv state.ServerView, history *storage.Store, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	key := fmt.Sprintf("chart-%d", idx)
	widgetCard(c, key, idx, w.EffectiveSpan(), widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		if history == nil {
			ui.Text(c, "no history store").FontSize(11).TextColor(t.TextMuted)
			return
		}
		var items []chartItem
		for _, ref := range w.Series {
			mv, _, found := findMeasurement(sv, ref.Metric, ref.Measurement)
			if !found {
				continue
			}
			if ref.Label != "" {
				mv.Label = ref.Label
			}
			items = append(items, chartItem{mv: mv, historyKey: sv.Name + "/" + ref.Metric + "." + ref.Measurement})
		}
		if len(items) == 0 {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "none of this widget's series are reporting").FontSize(11).TextColor(t.TextMuted)
			})
			return
		}
		renderSeriesChart(c, history, "", items).Fill()
	})
}

// addWidgetTile is the grid's last cell: a dashed-feeling plain button
// the same size as every other tile, opening the add-widget modal with a
// fresh draft.
func addWidgetTile(c *ui.Context) {
	t := c.Theme()
	if ui.Button(c.Key("add-widget"), "").Size(tileWidth, tileHeight).
		Background(t.Surface).Radius(8).BorderColor(t.Border).Border(1, t.Border).
		Tooltip("Add a widget").
		Children(func() {
			ui.Column(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Gap(4).Children(func() {
				ui.Text(c, "+").FontSize(24).TextColor(t.TextMuted)
				ui.Text(c, "Add widget").FontSize(11).TextColor(t.TextMuted)
			})
		}).Clicked() {
		resetWidgetDraft()
		addWidgetOpen = true
	}
}

// resetWidgetDraft clears the add/edit modal's draft state back to "add
// a brand new widget" — editWidgetIdx = -1, everything else at its zero
// value/default type.
func resetWidgetDraft() {
	addWidgetErr = ""
	addWidgetType = dashboard.WidgetChart
	addWidgetTitle = ""
	addWidgetSinglePick = ""
	addWidgetNewPick = ""
	addWidgetSeriesPicks = nil
	addWidgetFormat = ""
	addWidgetSpan = 0
	editWidgetIdx = -1
}

// openWidgetEditor opens the same modal addWidgetTile does, but
// pre-filled from w and in "edit this one in place" mode
// (confirmAddWidget replaces index idx instead of appending) — reusing
// one modal/one draft-state/one confirm path for both add and edit, not
// a second editor built separately.
func openWidgetEditor(idx int, w dashboard.Widget) {
	resetWidgetDraft()
	editWidgetIdx = idx
	addWidgetType = w.Type
	addWidgetTitle = w.Title
	addWidgetSpan = w.Span
	single := w.Type == dashboard.WidgetGauge || w.Type == dashboard.WidgetNumber
	if single && len(w.Series) > 0 {
		addWidgetSinglePick = w.Series[0].Metric + "." + w.Series[0].Measurement
		addWidgetFormat = w.Series[0].Format
	} else {
		for _, s := range w.Series {
			addWidgetSeriesPicks = append(addWidgetSeriesPicks, s.Metric+"."+s.Measurement)
		}
	}
	addWidgetOpen = true
}

// availableSeries lists every "metric.measurement" pair sv is currently
// reporting, sorted — the picker's options. Resolved from the live
// snapshot, same as the rest of this file: a widget can only be built
// from a measurement that actually exists right now, not guessed from
// static YAML the GUI would otherwise need a Registry reference to read.
func availableSeries(sv state.ServerView) []string {
	var out []string
	for _, mv := range sv.Metrics {
		for _, m := range mv.Measurements {
			out = append(out, mv.Name+"."+m.Key)
		}
	}
	sort.Strings(out)
	return out
}

func addWidgetModal(c *ui.Context, sv state.ServerView, cfg *server.Config) {
	t := c.Theme()
	ui.Modal(c, &addWidgetOpen, func() {
		options := availableSeries(sv)

		ui.Column(c).Width(360).Background(t.Surface).Radius(8).Padding(16).Gap(10).Children(func() {
			modalTitle := "Add widget — " + sv.Name
			if editWidgetIdx >= 0 {
				modalTitle = "Edit widget — " + sv.Name
			}
			ui.Text(c, modalTitle).FontSize(13).Bold()

			ui.Text(c, "Type").FontSize(11).TextColor(t.TextMuted)
			typeOptions := []string{string(dashboard.WidgetChart), string(dashboard.WidgetGauge), string(dashboard.WidgetNumber), string(dashboard.WidgetPie)}
			typeSel := string(addWidgetType)
			ui.Select(c, &typeSel, typeOptions)
			addWidgetType = dashboard.WidgetType(typeSel)

			ui.Text(c, "Size").FontSize(11).TextColor(t.TextMuted)
			sizeOptions := []string{"Auto", "1x1", "2x2", "3x3"}
			sizeSel := sizeOptions[addWidgetSpan] // addWidgetSpan is 0-3, same index as sizeOptions
			ui.Select(c, &sizeSel, sizeOptions)
			for i, opt := range sizeOptions {
				if opt == sizeSel {
					addWidgetSpan = i
				}
			}

			ui.Text(c, "Title (optional)").FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &addWidgetTitle)

			single := addWidgetType == dashboard.WidgetGauge || addWidgetType == dashboard.WidgetNumber
			ui.Text(c, "Series").FontSize(11).TextColor(t.TextMuted)
			if len(options) == 0 {
				ui.Text(c, "this server isn't reporting any measurements yet").FontSize(11).TextColor(t.TextMuted)
			} else if single {
				if addWidgetSinglePick == "" {
					addWidgetSinglePick = options[0]
				}
				ui.Select(c, &addWidgetSinglePick, options)

				ui.Text(c, "Format (optional, e.g. %.0f, %5.2fGB, %6.2f%%)").FontSize(11).TextColor(t.TextMuted)
				ui.TextInput(c, &addWidgetFormat)
			} else {
				if addWidgetNewPick == "" {
					addWidgetNewPick = options[0]
				}
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Select(c, &addWidgetNewPick, options).Grow(1)
					if ui.Button(c.Key("add-series-pick"), "Add").Clicked() && addWidgetNewPick != "" {
						addWidgetSeriesPicks = append(addWidgetSeriesPicks, addWidgetNewPick)
					}
				})
				for i, p := range addWidgetSeriesPicks {
					i := i
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, p).FontSize(11).Grow(1)
						if ui.Button(c.Key(fmt.Sprintf("series-pick-remove-%d", i)), "×").Clicked() {
							addWidgetSeriesPicks = append(addWidgetSeriesPicks[:i:i], addWidgetSeriesPicks[i+1:]...)
						}
					})
				}
			}

			if addWidgetErr != "" {
				ui.Text(c, addWidgetErr).FontSize(11).TextColor(t.Danger)
			}

			confirmLabel := "Add"
			if editWidgetIdx >= 0 {
				confirmLabel = "Save"
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("add-widget-cancel"), "Cancel").Clicked() {
					addWidgetOpen = false
				}
				if ui.Button(c.Key("add-widget-confirm"), confirmLabel).Clicked() {
					confirmAddWidget(cfg)
				}
			})
		})
	})
}

// confirmAddWidget builds a dashboard.Widget from the modal's draft
// state, validates it, and persists it via server.SaveDashboard — errors
// (an empty picker, a rejected save) show inline in the modal instead of
// silently closing it, so a failed add is visibly a failed add.
func confirmAddWidget(cfg *server.Config) {
	if cfg == nil {
		addWidgetErr = "no config for this server"
		return
	}
	var series []dashboard.SeriesRef
	single := addWidgetType == dashboard.WidgetGauge || addWidgetType == dashboard.WidgetNumber
	if single {
		if addWidgetSinglePick != "" {
			ref := seriesRefFromPick(addWidgetSinglePick)
			ref.Format = addWidgetFormat
			series = append(series, ref)
		}
	} else {
		for _, p := range addWidgetSeriesPicks {
			series = append(series, seriesRefFromPick(p))
		}
	}
	w := dashboard.Widget{Type: addWidgetType, Title: addWidgetTitle, Series: series, Span: addWidgetSpan}
	if err := w.Validate(); err != nil {
		addWidgetErr = err.Error()
		return
	}
	var next []dashboard.Widget
	if editWidgetIdx >= 0 && editWidgetIdx < len(cfg.Dashboard) {
		next = append([]dashboard.Widget(nil), cfg.Dashboard...)
		next[editWidgetIdx] = w
	} else {
		next = append(append([]dashboard.Widget(nil), cfg.Dashboard...), w)
	}
	if err := server.SaveDashboard(cfg, next); err != nil {
		addWidgetErr = err.Error()
		return
	}
	addWidgetOpen = false
}

// seriesRefFromPick splits one "metric.measurement" picker option back
// into a SeriesRef — the exact inverse of availableSeries' own
// mv.Name+"."+m.Key join.
func seriesRefFromPick(pick string) dashboard.SeriesRef {
	metricName, measurement, _ := strings.Cut(pick, ".")
	return dashboard.SeriesRef{Metric: metricName, Measurement: measurement}
}
