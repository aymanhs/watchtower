// This file is the dashboard: a user-composed widget grid, decoupled
// both from metric.Display's auto-rendering (metricLine in view.go) and
// — now — from the sidebar's server selection. A dashboard lives in its
// own YAML file (data/dashboards/, see internal/dashboard.Store), named
// and picked independently of any one server (dashboardPicker); add/
// remove here write straight back to it via dashStore.Save, so
// hand-editing the file and using the GUI are both just "the dashboard",
// never two sources of truth. A widget's Series are selector patterns
// (internal/dashboard.SeriesRef.Resolve), so one widget can show one
// server's one measurement, or glob across every server or a whole
// cluster — this file only draws however many matches come back
// (resolveTileSeries), it doesn't know or care which dashboard any of
// them "belongs to".
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

	// maximizedDashboard/maximizedIdx say which one widget, if any, is
	// temporarily filling the whole pane instead of sharing the grid —
	// index into that dashboard's own Widgets slice. Keyed by dashboard
	// name too (not just an index) so switching dashboards never shows a
	// stray maximized tile that happened to share the same index on
	// this one.
	maximizedDashboard = ""
	maximizedIdx       = -1

	// selectedDashboard is which dashboard's grid is currently shown —
	// global GUI state, same reasoning as selectedServer: one window,
	// nowhere else for "which one is open right now" to live between
	// frames. Independent of selectedServer entirely: picking a
	// dashboard here no longer has anything to do with the sidebar's own
	// selection, which is the whole point of a dashboard no longer being
	// "the one belonging to whichever server you clicked".
	selectedDashboard = ""

	// New-dashboard modal draft state.
	newDashboardOpen = false
	newDashboardName = ""
	newDashboardErr  = ""
)

// dashboardNames is every dashboard currently in dashStore, sorted — the
// picker's options.
func dashboardNames(dashStore *dashboard.Store) []string {
	var out []string
	for _, d := range dashStore.All() {
		out = append(out, d.Name)
	}
	sort.Strings(out)
	return out
}

// dashboardPane is the detail pane's top content: a picker for which
// dashboard to show, independent of any server selected in the sidebar,
// plus that dashboard's own grid. If selectedDashboard names one that no
// longer exists (deleted from disk out from under the GUI, say) or
// hasn't been set yet, it falls back to the first one alphabetically —
// never silently keeps pointing at nothing when something is available.
func dashboardPane(c *ui.Context, all []state.ServerView, dashStore *dashboard.Store, history *storage.Store, paneWidth float32) {
	t := c.Theme()
	names := dashboardNames(dashStore)
	have := false
	for _, n := range names {
		if n == selectedDashboard {
			have = true
			break
		}
	}
	if !have && len(names) > 0 {
		selectedDashboard = names[0]
	}

	ui.Column(c).Fill().Children(func() {
		dashboardPicker(c, names, dashStore)
		if selectedDashboard == "" {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "no dashboards yet — click + New to create one").FontSize(13).TextColor(t.TextMuted)
			})
			return
		}
		d := dashStore.GetOrNew(selectedDashboard)
		dashboardGrid(c, d, all, dashStore, history, paneWidth)
	}).Fill()
}

// dashboardPicker is the row above the grid: pick any existing
// dashboard by name, or create a new one. Always visible, even with no
// servers selected in the sidebar at all — a dashboard is global now,
// not something that only makes sense once you've picked a server.
func dashboardPicker(c *ui.Context, names []string, dashStore *dashboard.Store) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8, 12, 8, 12).Background(t.Surface).Children(func() {
		ui.Text(c, "Dashboard").FontSize(11).TextColor(t.TextMuted)
		if len(names) > 0 {
			ui.Select(c, &selectedDashboard, names).Width(220)
		} else {
			ui.Text(c, "none yet").FontSize(11).TextColor(t.TextMuted).Grow(1)
		}
		if ui.Button(c.Key("new-dashboard"), "+ New").Clicked() {
			newDashboardName = ""
			newDashboardErr = ""
			newDashboardOpen = true
		}
	})
	newDashboardModal(c, dashStore)
}

func newDashboardModal(c *ui.Context, dashStore *dashboard.Store) {
	t := c.Theme()
	ui.Modal(c, &newDashboardOpen, func() {
		ui.Column(c).Width(320).Background(t.Surface).Radius(8).Padding(16).Gap(10).Children(func() {
			ui.Text(c, "New dashboard").FontSize(13).Bold()
			ui.Text(c, "Name").FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &newDashboardName)
			if newDashboardErr != "" {
				ui.Text(c, newDashboardErr).FontSize(11).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("new-dashboard-cancel"), "Cancel").Clicked() {
					newDashboardOpen = false
				}
				if ui.Button(c.Key("new-dashboard-create"), "Create").Clicked() {
					confirmNewDashboard(dashStore)
				}
			})
		})
	})
}

// confirmNewDashboard creates an empty dashboard named newDashboardName
// and selects it — errors (an empty name, a name already taken, a
// rejected save) show inline in the modal instead of silently closing
// it, same as confirmAddWidget's own errors do.
func confirmNewDashboard(dashStore *dashboard.Store) {
	name := strings.TrimSpace(newDashboardName)
	if name == "" {
		newDashboardErr = "name required"
		return
	}
	if _, exists := dashStore.Get(name); exists {
		newDashboardErr = fmt.Sprintf("a dashboard named %q already exists", name)
		return
	}
	if err := dashStore.Save(&dashboard.Dashboard{Name: name}); err != nil {
		newDashboardErr = err.Error()
		return
	}
	selectedDashboard = name
	newDashboardOpen = false
}

// dashboardGrid renders d's own widget grid plus the "+ Add widget" tile
// every dashboard ends with. A real ui.Grid, not a wrapping row: mygo's
// Grid supports ColumnSpan/RowSpan (confirmed by reading ui/grid.go
// before relying on it), which is what lets a chart tile span 2x2 cells
// instead of being stuck at the same size as a gauge/number/pie tile.
//
// If one widget is maximized (maximizedDashboard/maximizedIdx), it alone
// fills the pane — the rest of the grid, and the "+ Add widget" tile,
// don't render at all until it's restored.
func dashboardGrid(c *ui.Context, d *dashboard.Dashboard, all []state.ServerView, dashStore *dashboard.Store, history *storage.Store, paneWidth float32) {
	// Snapshot once per frame: a tile's own Clicked()/Drop() handling
	// (onRemove/onReorder below) runs synchronously, in the middle of
	// this same loop — a remove shortens d.Widgets right then, not on
	// the next frame. Without this snapshot, every tile here would
	// re-read the live, now-shorter d.Widgets by index as the loop kept
	// going, running off the end of it and panicking on any removal
	// that wasn't the very last widget. Rendering from a fixed-length
	// copy instead means the rest of this frame finishes against the
	// widgets as they were when it started; the removal still takes
	// effect, just visibly on the next frame instead of mid-draw.
	widgets := d.Widgets

	if maximizedDashboard == d.Name && maximizedIdx >= 0 && maximizedIdx < len(widgets) {
		ui.Box(c).Fill().Padding(12).Children(func() {
			widgetTile(c, d, widgets[maximizedIdx], all, dashStore, maximizedIdx, history, true)
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
			for i, w := range widgets {
				widgetTile(c, d, w, all, dashStore, i, history, false)
			}
			addWidgetTile(c)
		})
	})
	addWidgetModal(c, d, all, dashStore)
}

// widgetTile renders one configured widget by its type, with a small ×
// in its header that removes it (splices d.Widgets and calls
// dashStore.Save) and a maximize/restore toggle — the only edits a tile
// itself offers; everything else about it is set once, at add time. w
// is dashboardGrid's own frame-start snapshot of this widget, not a
// live re-read of d.Widgets[idx] — see dashboardGrid's own comment on
// why that distinction is the whole fix for a remove-mid-render crash.
// all is every server's current snapshot — a widget's Series are
// selectors (internal/dashboard.SeriesRef.Resolve), and a selector
// naming a glob or a cluster can match measurements on any number of
// servers, none of them tied to this dashboard's own name.
func widgetTile(c *ui.Context, d *dashboard.Dashboard, w dashboard.Widget, all []state.ServerView, dashStore *dashboard.Store, idx int, history *storage.Store, maximized bool) {
	onRemove := func() {
		if idx < 0 || idx >= len(d.Widgets) {
			return // already removed this frame by another handler — nothing to do
		}
		d.Widgets = append(append([]dashboard.Widget(nil), d.Widgets[:idx]...), d.Widgets[idx+1:]...)
		if err := dashStore.Save(d); err != nil {
			log.Printf("dashboard: remove widget: %v", err)
		}
	}
	onToggleMaximize := func() {
		if maximizedDashboard == d.Name && maximizedIdx == idx {
			maximizedDashboard, maximizedIdx = "", -1
		} else {
			maximizedDashboard, maximizedIdx = d.Name, idx
		}
	}
	onEdit := func() { openWidgetEditor(idx, w) }
	onReorder := func(fromIdx int) { reorderWidget(dashStore, d, fromIdx, idx) }

	switch w.Type {
	case dashboard.WidgetChart:
		chartTile(c, w, all, history, idx, maximized, onRemove, onToggleMaximize, onEdit, onReorder)
	case dashboard.WidgetGauge:
		gaugeTile(c, w, all, idx, maximized, onRemove, onToggleMaximize, onEdit, onReorder)
	case dashboard.WidgetPie:
		pieTile(c, w, all, idx, maximized, onRemove, onToggleMaximize, onEdit, onReorder)
	case dashboard.WidgetBars:
		barsTile(c, w, all, idx, maximized, onRemove, onToggleMaximize, onEdit, onReorder)
	default: // WidgetNumber, and any future type default to a plain value
		numberTile(c, w, all, idx, maximized, onRemove, onToggleMaximize, onEdit, onReorder)
	}
}

// reorderWidget moves the widget at fromIdx to sit where toIdx currently
// is (the rest shift to make room, same as dragging an item in any
// reorderable list), then persists it. A no-op for any index out of
// range or a drop on itself — defensive, since by the time this runs the
// drag/drop library has already confirmed a real drop happened, but idx
// values come from a UI gesture, not a trusted source.
func reorderWidget(dashStore *dashboard.Store, d *dashboard.Dashboard, fromIdx, toIdx int) {
	if fromIdx == toIdx ||
		fromIdx < 0 || fromIdx >= len(d.Widgets) || toIdx < 0 || toIdx >= len(d.Widgets) {
		return
	}
	next := append([]dashboard.Widget(nil), d.Widgets...)
	moved := next[fromIdx]
	next = append(next[:fromIdx], next[fromIdx+1:]...)
	// toIdx is where moved ends up in the final (still N-long) slice, not
	// a position to insert-before in the now-(N-1)-long one — no
	// direction-dependent shift needed: inserting at toIdx into the
	// shorter slice already lands moved at index toIdx of the result,
	// whichever direction it moved.
	next = append(next[:toIdx:toIdx], append([]dashboard.Widget{moved}, next[toIdx:]...)...)
	d.Widgets = next
	if err := dashStore.Save(d); err != nil {
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
		return w.Series[0].Selector
	}
	return string(w.Type)
}

// resolvedSeries pairs one live dashboard.Match with the SeriesRef that
// produced it — a tile needs both: the match for its value/scale/status,
// the ref for its own Label/Format override, which internal/dashboard's
// resolver (necessarily) doesn't carry since several matches can share
// one ref.
type resolvedSeries struct {
	ref   dashboard.SeriesRef
	match dashboard.Match
}

// resolveTileSeries resolves every one of w's Series against all's
// current snapshot, flattened and paired back with whichever ref
// produced each match — a dashboard widget's series are just selector
// patterns, resolved fresh against live data every render, same as the
// ad hoc chart already does. A ref matching nothing live (removed from
// metrics/, not reporting yet, or just a selector too narrow to match
// anything right now) simply contributes nothing, rather than a crash
// or a stale guess — same spirit as "not reporting" always was, just
// per-match instead of per-widget now that one ref can resolve to any
// number of matches.
func resolveTileSeries(w dashboard.Widget, all []state.ServerView) []resolvedSeries {
	var out []resolvedSeries
	for _, ref := range w.Series {
		for _, m := range ref.Resolve(all) {
			out = append(out, resolvedSeries{ref: ref, match: m})
		}
	}
	return out
}

// needsServerQualifier reports whether items span more than one server —
// a selector fixed to one server never needs its matches' labels
// prefixed with that server's own name (it'd be redundant on every one
// of them); a wildcard or cluster selector spanning several does, so
// the several gauges/numbers/slices/lines it draws stay tellable apart.
func needsServerQualifier(items []resolvedSeries) bool {
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		seen[it.match.Server] = true
		if len(seen) > 1 {
			return true
		}
	}
	return false
}

// matchLabel is one resolvedSeries' own display label: its ref's Label
// override if it set one, else the measurement's own DisplayLabel,
// prefixed with the server name when qualify says several servers are
// in play together.
func matchLabel(it resolvedSeries, qualify bool) string {
	label := it.ref.Label
	if label == "" {
		label = it.match.Measurement.DisplayLabel()
	}
	if qualify {
		return it.match.Server + ": " + label
	}
	return label
}

// barLabel is matchLabel's counterpart for a bars widget: it prefers
// whatever the resolver pulled out of the metric itself
// (dashboard.Match.Instance — a disk selector's mount point, say) over
// the measurement's own generic label, since the whole point of several
// bars in one widget is usually "one per instance", not "one per
// differently-labeled measurement". ref.Label still wins outright when
// set (an explicit override is still an override), and server
// qualification still applies on top, same as every other widget.
func barLabel(it resolvedSeries, qualify bool) string {
	label := it.ref.Label
	if label == "" {
		if it.match.Instance != "" {
			label = it.match.Instance
		} else {
			label = it.match.Measurement.DisplayLabel()
		}
	}
	if qualify {
		return it.match.Server + ": " + label
	}
	return label
}

// singleFontSize/singleDiameter scale a tile's one visual up when it's
// maximized or spans several cells — same numbers this file always
// used for the N==1 case. multiFontSize/multiDiameter are deliberately
// smaller fixed sizes for the N>1 case: a wrapping row of several
// gauges/numbers needs each one modest enough that a handful fit
// without a wall of dials, where a single one gets to fill the tile.
const (
	multiFontSize = 18.0
	multiDiameter = 90.0
)

func numberTile(c *ui.Context, w dashboard.Widget, all []state.ServerView, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	key := fmt.Sprintf("number-%d", idx)
	span := w.EffectiveSpan()
	widgetCard(c, key, idx, span, widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		items := resolveTileSeries(w, all)
		switch len(items) {
		case 0:
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "not reporting").FontSize(11).TextColor(t.TextMuted)
			})
		case 1:
			fontSize := float32(40)
			if maximized {
				fontSize = 90
			} else if span > 1 {
				fontSize = 40 * float32(span)
			}
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, seriesValue(items[0].ref, items[0].match.Measurement)).FontSize(fontSize).Bold()
			})
		default:
			// Several matches (a wildcard/cluster selector): a small
			// labeled grid instead of one big number, so "how many
			// metrics did this selector return" draws appropriately —
			// each value plus whatever tells it apart from the rest
			// (needsServerQualifier: the server name, when several are
			// in play; the measurement's own label otherwise).
			qualify := needsServerQualifier(items)
			ui.Scroll(c).Fill().Children(func() {
				ui.Row(c).Wrap().Gap(10).Children(func() {
					for _, it := range items {
						ui.Column(c).Gap(2).AlignItems(ui.Center).Children(func() {
							ui.Text(c, seriesValue(it.ref, it.match.Measurement)).FontSize(multiFontSize).Bold()
							ui.Text(c, matchLabel(it, qualify)).FontSize(9).TextColor(t.TextMuted).NoWrap()
						})
					}
				})
			})
		}
	})
}

func gaugeTile(c *ui.Context, w dashboard.Widget, all []state.ServerView, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	key := fmt.Sprintf("gauge-%d", idx)
	span := w.EffectiveSpan()
	widgetCard(c, key, idx, span, widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		items := resolveTileSeries(w, all)
		switch len(items) {
		case 0:
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "not reporting").FontSize(11).TextColor(t.TextMuted)
			})
		case 1:
			mv := items[0].match.Measurement
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				if mv.Scale == nil {
					// A dashboard gauge widget never invents a scale — it
					// only shows one if the measurement's own metric.yaml
					// display block already resolved one (same rule
					// metricLine's own gauge/bar branch follows).
					ui.Text(c, "no scale configured for this measurement").FontSize(11).TextColor(t.TextMuted)
					return
				}
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
				pressureGauge(c, *mv.Scale, mv.Value, mv.Zones, statusColor(t, items[0].match.MetricStatus), diameter, seriesValue(items[0].ref, mv))
			})
		default:
			qualify := needsServerQualifier(items)
			ui.Scroll(c).Fill().Children(func() {
				ui.Row(c).Wrap().Gap(16).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					for _, it := range items {
						mv := it.match.Measurement
						ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
							if mv.Scale == nil {
								ui.Box(c).Size(multiDiameter, multiDiameter).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
									ui.Text(c, "no scale").FontSize(9).TextColor(t.TextMuted)
								})
							} else {
								pressureGauge(c, *mv.Scale, mv.Value, mv.Zones, statusColor(t, it.match.MetricStatus), multiDiameter, seriesValue(it.ref, mv))
							}
							ui.Text(c, matchLabel(it, qualify)).FontSize(9).TextColor(t.TextMuted).NoWrap()
						})
					}
				})
			})
		}
	})
}

func pieTile(c *ui.Context, w dashboard.Widget, all []state.ServerView, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	key := fmt.Sprintf("pie-%d", idx)
	span := w.EffectiveSpan()
	widgetCard(c, key, idx, span, widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		items := resolveTileSeries(w, all)
		qualify := needsServerQualifier(items)
		colors := seriesColors(t)
		type slice struct {
			label     string
			formatted string
			value     float64
			color     ui.Color
		}
		var slices []slice
		var total float64
		for i, it := range items {
			mv := it.match.Measurement
			if mv.Value <= 0 {
				continue
			}
			label := matchLabel(it, qualify)
			formatted := seriesValue(it.ref, mv)
			if it.ref.Format == "" {
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

// barHeight/barGap/barLabelWidth/barValueWidth size a bars widget's rows
// — fixed, not scaled by span/maximized the way a single gauge/number
// is, since a bars widget's whole point is fitting several rows in the
// same tile; Span/maximizing it just gives more rows room, via Scroll,
// not bigger ones.
const (
	barHeight      = float32(14)
	barGap         = float32(8)
	barLabelWidth  = float32(90)
	barValueWidth  = float32(60)
	barTrackRadius = float32(3)
)

// barsTile draws one horizontal bar per resolved match — built for a
// selector returning several measurements at once (every mount point's
// disk.used_percent on one server, or one metric across every server a
// wildcard/cluster selector matches), each bar labeled by barLabel
// (preferring the resolver's own Match.Instance — a mount point, say —
// over the measurement's generic one) and sized proportionally: against
// the measurement's own Scale when it has one (the common case — most
// per-instance measurements worth bar-charting, like used_percent,
// already declare a 0-100 scale), or relative to the largest value among
// this tile's own scale-less matches otherwise, so bars without an
// absolute reference still show meaningful relative proportions instead
// of all reading as either empty or full.
func barsTile(c *ui.Context, w dashboard.Widget, all []state.ServerView, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	key := fmt.Sprintf("bars-%d", idx)
	widgetCard(c, key, idx, w.EffectiveSpan(), widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		items := resolveTileSeries(w, all)
		if len(items) == 0 {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "not reporting").FontSize(11).TextColor(t.TextMuted)
			})
			return
		}
		qualify := needsServerQualifier(items)
		var maxNoScale float64
		for _, it := range items {
			if it.match.Measurement.Scale == nil && it.match.Measurement.Value > maxNoScale {
				maxNoScale = it.match.Measurement.Value
			}
		}
		ui.Scroll(c).Fill().Children(func() {
			ui.Column(c).Gap(barGap).Padding(2).Children(func() {
				for _, it := range items {
					barRow(c, it, qualify, maxNoScale)
				}
			})
		})
	})
}

// barRow draws one bar: label, track+fill, formatted value.
func barRow(c *ui.Context, it resolvedSeries, qualify bool, maxNoScale float64) {
	t := c.Theme()
	mv := it.match.Measurement
	var frac float32
	switch {
	case mv.Scale != nil:
		frac = mv.Scale.Fraction(mv.Value)
	case maxNoScale > 0:
		frac = float32(mv.Value / maxNoScale)
	}
	switch {
	case frac < 0:
		frac = 0
	case frac > 1:
		frac = 1
	}
	fillColor := statusColor(t, it.match.MetricStatus)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, barLabel(it, qualify)).FontSize(10).Width(barLabelWidth).NoWrap().TextColor(t.TextMuted)
		ui.Box(c).Grow(1).Height(barHeight).Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, t.Border, barTrackRadius)
			if frac > 0 {
				fill := r
				fill.W = r.W * frac
				p.Fill(fill, fillColor, barTrackRadius)
			}
		})
		ui.Text(c, seriesValue(it.ref, mv)).FontSize(10).Width(barValueWidth).TextColor(t.Text)
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

func chartTile(c *ui.Context, w dashboard.Widget, all []state.ServerView, history *storage.Store, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	key := fmt.Sprintf("chart-%d", idx)
	widgetCard(c, key, idx, w.EffectiveSpan(), widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		if history == nil {
			ui.Text(c, "no history store").FontSize(11).TextColor(t.TextMuted)
			return
		}
		resolved := resolveTileSeries(w, all)
		qualify := needsServerQualifier(resolved)
		var items []chartItem
		for _, it := range resolved {
			mv := it.match.Measurement
			mv.Label = matchLabel(it, qualify)
			// HistoryKey is this match's own internal/storage key
			// (Server+"/"+Metric+"."+Measurement.Key) — a selector
			// resolving to several servers draws one line per match,
			// each queried under its own server's history, not one
			// shared key the way a literal single-server selector's
			// always been.
			items = append(items, chartItem{mv: mv, historyKey: it.match.HistoryKey})
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
// a second editor built separately. A SeriesRef's Selector is already
// exactly the picker's own option text (seriesRefFromPick's inverse is
// just itself now that a pick isn't qualified against "whichever
// server's pane is open" — there is no such thing anymore), so no
// stripping/requalifying is needed the way there was before dashboards
// stopped being per-server.
func openWidgetEditor(idx int, w dashboard.Widget) {
	resetWidgetDraft()
	editWidgetIdx = idx
	addWidgetType = w.Type
	addWidgetTitle = w.Title
	addWidgetSpan = w.Span
	single := w.Type == dashboard.WidgetGauge || w.Type == dashboard.WidgetNumber
	if single && len(w.Series) > 0 {
		addWidgetSinglePick = w.Series[0].Selector
		addWidgetFormat = w.Series[0].Format
	} else {
		for _, s := range w.Series {
			addWidgetSeriesPicks = append(addWidgetSeriesPicks, s.Selector)
		}
	}
	addWidgetOpen = true
}

// availableSeries lists selector suggestions the picker offers, sorted:
// a literal "server/metric.measurement" for every measurement every
// server in all is currently reporting; a wildcard "*/metric.measurement"
// for any (metric, measurement) pair that more than one server reports;
// and a "cluster:name/metric.measurement" for any pair reported by at
// least one member of a named cluster. These are suggestions for an
// Autocomplete field, not the only selectors it'll accept — the field
// itself takes any text, so a more specific glob (a partial server name,
// an instance segment) or a cluster this function didn't think to
// suggest still works, just typed by hand instead of clicked. Resolved
// from the live snapshot, same as the rest of this file: a widget can
// only be suggested a measurement that actually exists right now, not
// guessed from static YAML the GUI would otherwise need a Registry
// reference to read.
func availableSeries(all []state.ServerView) []string {
	seen := make(map[string]bool)
	serversFor := make(map[string]map[string]bool)  // "metric.measurement" -> set of servers reporting it
	clustersFor := make(map[string]map[string]bool) // "metric.measurement" -> set of clusters reporting it
	var out []string
	add := func(sel string) {
		if !seen[sel] {
			seen[sel] = true
			out = append(out, sel)
		}
	}
	for _, sv := range all {
		for _, mv := range sv.Metrics {
			for _, m := range mv.Measurements {
				pair := mv.Name + "." + m.Key
				if serversFor[pair] == nil {
					serversFor[pair] = make(map[string]bool)
				}
				serversFor[pair][sv.Name] = true
				if sv.Cluster != "" {
					if clustersFor[pair] == nil {
						clustersFor[pair] = make(map[string]bool)
					}
					clustersFor[pair][sv.Cluster] = true
				}
				add(sv.Name + "/" + pair)
			}
		}
	}
	for pair, servers := range serversFor {
		if len(servers) > 1 {
			add("*/" + pair)
		}
	}
	for pair, clusters := range clustersFor {
		for cluster := range clusters {
			add("cluster:" + cluster + "/" + pair)
		}
	}
	sort.Strings(out)
	return out
}

// defaultSeriesPick is addWidgetModal's starting Autocomplete value: the
// first option (in availableSeries' own sorted order) that actually
// resolves to a percent-scaled measurement, for a widget type where
// several matches get compared side by side in one tile (bars, several
// gauges) — a raw byte count or a plain total doesn't compare across
// differently-sized things the way a 0-100 percentage does, so a bars
// widget built from whatever sorted first (often "total", alphabetically
// ahead of "used_percent") would default to something that looks wrong
// out of the gate. Everything else, and a widget type with no
// percent-scaled option available at all, just gets the first option —
// exactly the old, simpler default.
func defaultSeriesPick(options []string, all []state.ServerView, widgetType dashboard.WidgetType) string {
	if len(options) == 0 {
		return ""
	}
	if widgetType == dashboard.WidgetBars || widgetType == dashboard.WidgetGauge {
		for _, opt := range options {
			for _, m := range (dashboard.SeriesRef{Selector: opt}).Resolve(all) {
				if m.Measurement.Unit == "percent" {
					return opt
				}
			}
		}
	}
	return options[0]
}

func addWidgetModal(c *ui.Context, d *dashboard.Dashboard, all []state.ServerView, dashStore *dashboard.Store) {
	t := c.Theme()
	ui.Modal(c, &addWidgetOpen, func() {
		options := availableSeries(all)

		ui.Column(c).Width(360).Background(t.Surface).Radius(8).Padding(16).Gap(10).Children(func() {
			modalTitle := "Add widget — " + d.Name
			if editWidgetIdx >= 0 {
				modalTitle = "Edit widget — " + d.Name
			}
			ui.Text(c, modalTitle).FontSize(13).Bold()

			ui.Text(c, "Type").FontSize(11).TextColor(t.TextMuted)
			typeOptions := []string{string(dashboard.WidgetChart), string(dashboard.WidgetGauge), string(dashboard.WidgetNumber), string(dashboard.WidgetPie), string(dashboard.WidgetBars)}
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
			ui.Text(c, "type or pick a suggestion — * matches anything (*/cpu.usage, cluster:name/cpu.usage)").FontSize(9).TextColor(t.TextMuted)
			if len(options) == 0 {
				ui.Text(c, "nothing is reporting yet to suggest from — you can still type a selector").FontSize(10).TextColor(t.TextMuted)
			}
			if single {
				if addWidgetSinglePick == "" {
					addWidgetSinglePick = defaultSeriesPick(options, all, addWidgetType)
				}
				ui.Autocomplete(c, &addWidgetSinglePick, options)

				ui.Text(c, "Format (optional, e.g. %.0f, %5.2fGB, %6.2f%%)").FontSize(11).TextColor(t.TextMuted)
				ui.TextInput(c, &addWidgetFormat)
			} else {
				if addWidgetNewPick == "" {
					addWidgetNewPick = defaultSeriesPick(options, all, addWidgetType)
				}
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Autocomplete(c, &addWidgetNewPick, options).Grow(1)
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
					confirmAddWidget(d, dashStore)
				}
			})
		})
	})
}

// confirmAddWidget builds a dashboard.Widget from the modal's draft
// state, validates it, and persists it via dashStore.Save — errors (an
// empty picker, a rejected save) show inline in the modal instead of
// silently closing it, so a failed add is visibly a failed add.
func confirmAddWidget(d *dashboard.Dashboard, dashStore *dashboard.Store) {
	var series []dashboard.SeriesRef
	single := addWidgetType == dashboard.WidgetGauge || addWidgetType == dashboard.WidgetNumber
	if single {
		if addWidgetSinglePick != "" {
			series = append(series, dashboard.SeriesRef{Selector: addWidgetSinglePick, Format: addWidgetFormat})
		}
	} else {
		for _, p := range addWidgetSeriesPicks {
			series = append(series, dashboard.SeriesRef{Selector: p})
		}
	}
	w := dashboard.Widget{Type: addWidgetType, Title: addWidgetTitle, Series: series, Span: addWidgetSpan}
	if err := w.Validate(); err != nil {
		addWidgetErr = err.Error()
		return
	}
	if editWidgetIdx >= 0 && editWidgetIdx < len(d.Widgets) {
		d.Widgets = append([]dashboard.Widget(nil), d.Widgets...)
		d.Widgets[editWidgetIdx] = w
	} else {
		d.Widgets = append(append([]dashboard.Widget(nil), d.Widgets...), w)
	}
	if err := dashStore.Save(d); err != nil {
		addWidgetErr = err.Error()
		return
	}
	addWidgetOpen = false
}
