// This file is the dashboard: a user-composed widget grid, decoupled
// from the sidebar's server selection. A dashboard lives in its
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
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/dashboard"
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
//
// Two extras beyond plain printf: the keyword "bytes" renders a binary
// human-readable size ("250.1 GiB"), and a "/N:" prefix divides the value
// by N first ("/1073741824:%.1f GB" — a scale factor).
func applyFormat(format string, value float64) string {
	if strings.TrimSpace(format) == "bytes" {
		return humanBytes(value)
	}
	if rest, ok := strings.CutPrefix(format, "/"); ok {
		if div, tail, ok := strings.Cut(rest, ":"); ok {
			if d, err := strconv.ParseFloat(strings.TrimSpace(div), 64); err == nil && d != 0 {
				format, value = tail, value/d
			}
		}
	}
	if m := formatVerb.FindStringSubmatch(format); m != nil {
		switch m[1] {
		case "d", "b", "o", "O", "x", "X", "c":
			return fmt.Sprintf(format, int64(value))
		}
	}
	return fmt.Sprintf(format, value)
}

// humanBytes renders n with the largest binary unit keeping it under 1024.
func humanBytes(n float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	neg := n < 0
	n = math.Abs(n)
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	out := fmt.Sprintf("%.0f %s", n, units[i])
	if i > 0 {
		out = fmt.Sprintf("%.1f %s", n, units[i])
	}
	if neg {
		out = "-" + out
	}
	return out
}

// seriesValue is one series' number, formatted the way it was asked to
// be: ref.Format if the series set one (printf-style, always wins), else
// the plain two-decimal default (formatValue) — there's no
// per-measurement Unit/Format left to fall back to instead (see
// internal/metric's package doc); a series wanting anything else now
// always says so itself.
func seriesValue(ref dashboard.SeriesRef, mv state.MeasurementView) string {
	if ref.Format != "" {
		return applyFormat(ref.Format, mv.Value)
	}
	return formatValue(mv.Value)
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

// tileSize returns the pixel size a tile spanning spanX by spanY cells
// takes, cells plus the gaps between them, each direction independent.
func tileSize(spanX, spanY int) (w, h float32) {
	x, y := float32(spanX), float32(spanY)
	return tileWidth*x + gridGap*(x-1), tileHeight*y + gridGap*(y-1)
}

// minSpan is the smaller of spanX/spanY — used wherever a tile's own
// content is inherently round or square (a gauge's dial, a pie, a plain
// number's font size) and so scales off whichever dimension is tighter,
// rather than overflowing a non-square tile in the larger direction.
func minSpan(spanX, spanY int) int {
	if spanX < spanY {
		return spanX
	}
	return spanY
}

// Add-widget modal draft state — package-level for the same reason
// bottomMode/selectedServer are: one window, nowhere else for "what's
// being typed into the open dialog right now" to live between frames.
var (
	addWidgetOpen  = false
	addWidgetErr   = ""
	addWidgetType  = dashboard.WidgetChart
	addWidgetTitle = ""
	// addWidgetSeriesPicks is every series this widget will have — an
	// editable ui.Table, one row per entry, each cell bound straight to
	// that entry's own Selector/Label/Format field (&addWidgetSeriesPicks[row].X),
	// so there's no separate staging row or "click edit to load it"
	// step the way an Add/Update/Cancel flow needed: every row is
	// always live-editable, every field always visible, the same way
	// for every widget type (gauge/number included — there used to be a
	// single-pick shortcut just for them; unified since a gauge with
	// 2+ series, already supported at render time (resolveTileSeries,
	// any widget type adapts to N matches), was actually buildable from
	// the GUI too, not just by hand-editing YAML).
	addWidgetSeriesPicks []dashboard.SeriesRef
	// addWidgetSpanX/addWidgetSpanY are the picked dashboard.Widget.SpanX/
	// SpanY, each 0 meaning "auto" — the type's own default, same as an
	// unset SpanX/SpanY in YAML. float64 because ui.NumberInput edits a
	// *float64; always a whole number in [0, maxSpan] in practice.
	addWidgetSpanX = float64(0)
	addWidgetSpanY = float64(0)
	// addWidgetMin/addWidgetMax are a gauge/bars widget's scale as typed;
	// both blank means no scale (Widget.Min/Max nil).
	addWidgetMin = ""
	addWidgetMax = ""
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
				ui.Text(c, "no dashboards yet — use the new dashboard button above to create one").FontSize(13).TextColor(t.TextMuted)
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
		ui.Box(c).Width(1).Height(20).Background(t.Border)
		if squareIconButton(c, "new-dashboard", newDashboardIcon, "New dashboard").Clicked() {
			newDashboardName = ""
			newDashboardErr = ""
			newDashboardOpen = true
		}
		if squareIconButton(c, "add-widget", addWidgetIcon, "Add widget to this dashboard").Clicked() && selectedDashboard != "" {
			resetWidgetDraft()
			addWidgetOpen = true
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
				if squareIconButton(c, "new-dashboard-cancel", cancelIcon, "Cancel").Clicked() {
					newDashboardOpen = false
				}
				if squareIconButton(c, "new-dashboard-create", confirmIcon, "Create dashboard").Clicked() {
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

// dashboardGrid renders d's own widget grid. A real ui.Grid, not a wrapping row: mygo's
// Grid supports ColumnSpan/RowSpan (confirmed by reading ui/grid.go
// before relying on it), which is what lets a chart tile span 2x2 cells
// instead of being stuck at the same size as a gauge/number/pie tile.
//
// If one widget is maximized (maximizedDashboard/maximizedIdx), it alone
// fills the pane — the rest of the grid
// doesn't render until it's restored.
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
		addWidgetModal(c, d, all, dashStore)
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

// squareButtonSize/squareIconSize are every small per-row action
// button's fixed size throughout this file — a widget card's own
// header (edit/maximize/remove) and a series-picker row's own
// (edit/remove) all use squareIconButton below instead of each sizing
// itself from its own label/icon, which is what made some of them
// visibly bigger than others, and not even a consistent size next to
// each other, before this existed.
const (
	squareButtonSize = float32(20)
	squareIconSize   = float32(11)
)

// squareIconButton is one small, fixed-size square button showing icon
// — every per-row action in this file (a widget card's edit/maximize/
// remove, a series-picker row's edit/remove) is one of these, so they
// always render as one same-sized set of controls regardless of which
// icon or how long its tooltip is. Returns the Element so the caller
// chains .Clicked() same as any other button.
//
// Padding(2) overrides mygo's own default Button padding — asymmetric
// (more horizontal than vertical, styleButton's own t.Space(1.5)/
// t.Space(3.5), sized for a text label) and, combined with a button's
// own Shrink(0), wide enough on its own to push the rendered button past
// Size()'s square request on the horizontal axis alone. A small, equal
// Padding on every side is what actually makes the box — and the icon
// centered inside it — square.
func squareIconButton(c *ui.Context, key string, icon *ui.SVG, tooltip string) ui.Element {
	return ui.Button(c.Key(key), "").Padding(2).Size(squareButtonSize, squareButtonSize).Tooltip(tooltip).
		Children(func() { ui.Icon(c, icon).FontSize(squareIconSize) })
}

// spanControl is one axis of the add-widget modal's own Width/Height
// picker: a plain read-only number (or "Auto" at 0) next to a Stepper's
// +/- arrows, AppKit-style — not ui.NumberInput, which rendered only one
// shared-looking +/- pair for both axes when two sat side by side in the
// same row (a mygo quirk, not an identity bug on this file's side: each
// Stepper here is bound to its own distinct *float64, same as the two
// NumberInputs were). Two separate calls, each nested in its own Column,
// give each axis a genuinely different position in the render tree, so
// — same as the two label/format table cells elsewhere in this file —
// there's never any ambiguity about which pair of arrows adjusts which
// value, without needing an explicit Key.
func spanControl(c *ui.Context, t *ui.Theme, label string, value *float64) {
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, label).FontSize(9).TextColor(t.TextMuted)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			display := "Auto"
			if *value > 0 {
				display = fmt.Sprintf("%.0f", *value)
			}
			ui.Text(c, display).FontSize(12).Width(28)
			ui.Stepper(c, value, 0, float64(dashboard.MaxSpan), 1)
		})
	})
}

// dragWidgetIdx is a dashboard widget's index while it's being dragged —
// a named type, not a bare int, so this drag/drop pairing can't
// accidentally match some unrelated int-carrying drag elsewhere in the
// app (mygo's Drag/DragOver/Drop are generic over the payload's type).
type dragWidgetIdx int

// widgetCard is the shared tile chrome every widget type uses: a header
// row (title, edit, maximize/restore, remove) and body below it, plus
// drag-to-reorder — one place that look lives, not four slightly
// different copies. Sized from spanX/spanY cells normally (and given
// that span in the grid, via ColumnSpan/RowSpan); Fill()s the whole pane
// instead when maximized, with no span chained — a maximized tile isn't
// a child of ui.Grid at all (dashboardGrid renders it alone, in a plain
// Box), so ColumnSpan/RowSpan wouldn't mean anything there, and dragging
// a maximized tile (there's nothing else visible to drop it on) is
// disabled the same way.
func widgetCard(c *ui.Context, key string, idx, spanX, spanY int, title string, maximized bool, onEdit, onRemove, onToggleMaximize func(), onReorder func(fromIdx int), body func()) ui.Element {
	t := c.Theme()
	card := ui.Column(c).Background(t.Surface).Radius(8).Padding(10).Gap(6)
	if maximized {
		card = card.Fill()
	} else {
		w, h := tileSize(spanX, spanY)
		card = card.Size(w, h)
	}
	card = card.Children(func() {
		ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted).Grow(1).NoWrap()
			if squareIconButton(c, "widget-edit-"+key, editIcon, "Edit widget").Clicked() {
				onEdit()
			}
			maxIcon, maxTooltip := maximizeIcon, "Maximize"
			if maximized {
				maxIcon, maxTooltip = restoreIcon, "Restore"
			}
			if squareIconButton(c, "widget-maximize-"+key, maxIcon, maxTooltip).Clicked() {
				onToggleMaximize()
			}
			if squareIconButton(c, "widget-remove-"+key, removeIcon, "Remove widget").Clicked() {
				onRemove()
			}
		})
		body()
	})
	if maximized {
		return card
	}
	if spanX > 1 || spanY > 1 {
		card = card.ColumnSpan(spanX).RowSpan(spanY)
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
// then the measurement key), else its type.
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
// override if it set one, else the measurement's own key,
// prefixed with the server name when qualify says several servers are
// in play together.
func matchLabel(it resolvedSeries, qualify bool) string {
	label := it.ref.Label
	if label == "" {
		label = it.match.Measurement.Key
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
			label = it.match.Measurement.Key
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
	spanX, spanY := w.EffectiveSpanX(), w.EffectiveSpanY()
	widgetCard(c, key, idx, spanX, spanY, widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		items := resolveTileSeries(w, all)
		switch len(items) {
		case 0:
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "not reporting").FontSize(11).TextColor(t.TextMuted)
			})
		case 1:
			span := minSpan(spanX, spanY)
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
	spanX, spanY := w.EffectiveSpanX(), w.EffectiveSpanY()
	widgetCard(c, key, idx, spanX, spanY, widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		items := resolveTileSeries(w, all)
		// A gauge's scale is the widget's own Min/Max now — set once when
		// building it, not resolved per-measurement (internal/metric
		// declares no per-measurement Scale any more; see its package
		// doc) — so every match in this one widget shares the same
		// dial range regardless of which server/instance it came from.
		min, max, hasScale := w.Scale()
		switch {
		case len(items) == 0:
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "not reporting").FontSize(11).TextColor(t.TextMuted)
			})
		case !hasScale:
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "no scale configured — set min/max when editing this widget").FontSize(11).TextColor(t.TextMuted)
			})
		case len(items) == 1:
			mv := items[0].match.Measurement
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				// Base diameter fills most of a 1x1 tile's own body
				// (tileHeight minus the header and padding) instead of
				// leaving a ring of empty space around a small dial —
				// stroke width and center-label size are already
				// proportional to diameter (pressureGauge, gauge.go), so
				// scaling this up alone can't make the label collide with
				// the ring. Scales off minSpan, not either dimension
				// alone, so a non-square tile's dial still fits within
				// its tighter side instead of overflowing it.
				span := minSpan(spanX, spanY)
				diameter := float32(140)
				switch {
				case maximized:
					diameter = 300
				case span > 1:
					diameter = 140 * float32(span)
				}
				scale := state.Scale{Min: min, Max: max}
				pressureGauge(c, scale, mv.Value, statusColor(t, items[0].match.MetricStatus), diameter, seriesValue(items[0].ref, mv))
			})
		default:
			qualify := needsServerQualifier(items)
			scale := state.Scale{Min: min, Max: max}
			ui.Scroll(c).Fill().Children(func() {
				ui.Row(c).Wrap().Gap(16).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					for _, it := range items {
						mv := it.match.Measurement
						ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
							pressureGauge(c, scale, mv.Value, statusColor(t, it.match.MetricStatus), multiDiameter, seriesValue(it.ref, mv))
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
	spanX, spanY := w.EffectiveSpanX(), w.EffectiveSpanY()
	widgetCard(c, key, idx, spanX, spanY, widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
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
			// seriesValue's own default (no ref.Format set) is already
			// the plain two-decimal formatValue, with no unit-driven
			// auto-scaling left to override — exactly a pie slice's own
			// "share of a whole" formatting, no special-casing needed.
			formatted := seriesValue(it.ref, mv)
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
			span := minSpan(spanX, spanY)
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
// the widget's own Min/Max (w.Scale()) when set — the same scale every
// bar in the tile shares, there's no per-measurement scale any more (see
// internal/metric's package doc) — or, with no widget scale configured,
// relative to the largest value among this tile's own matches instead,
// so bars without an absolute reference still show meaningful relative
// proportions instead of all reading as either empty or full.
func barsTile(c *ui.Context, w dashboard.Widget, all []state.ServerView, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	key := fmt.Sprintf("bars-%d", idx)
	widgetCard(c, key, idx, w.EffectiveSpanX(), w.EffectiveSpanY(), widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		items := resolveTileSeries(w, all)
		if len(items) == 0 {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "not reporting").FontSize(11).TextColor(t.TextMuted)
			})
			return
		}
		qualify := needsServerQualifier(items)
		min, max, hasScale := w.Scale()
		var maxNoScale float64
		if !hasScale {
			for _, it := range items {
				if it.match.Measurement.Value > maxNoScale {
					maxNoScale = it.match.Measurement.Value
				}
			}
		}
		ui.Scroll(c).Fill().Children(func() {
			ui.Column(c).Gap(barGap).Padding(2).Children(func() {
				for _, it := range items {
					barRow(c, it, qualify, hasScale, min, max, maxNoScale)
				}
			})
		})
	})
}

// barRow draws one bar: label, track+fill, formatted value.
func barRow(c *ui.Context, it resolvedSeries, qualify bool, hasScale bool, min, max float64, maxNoScale float64) {
	t := c.Theme()
	mv := it.match.Measurement
	var frac float32
	switch {
	case hasScale:
		frac = state.Scale{Min: min, Max: max}.Fraction(mv.Value)
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
	widgetCard(c, key, idx, w.EffectiveSpanX(), w.EffectiveSpanY(), widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		if history == nil {
			ui.Text(c, "no history store").FontSize(11).TextColor(t.TextMuted)
			return
		}
		resolved := resolveTileSeries(w, all)
		qualify := needsServerQualifier(resolved)
		var items []chartItem
		for _, it := range resolved {
			// HistoryKey is this match's own internal/storage key
			// (Server+"/"+Metric+"."+Measurement.Key) — a selector
			// resolving to several servers draws one line per match,
			// each queried under its own server's history, not one
			// shared key the way a literal single-server selector's
			// always been.
			items = append(items, chartItem{label: matchLabel(it, qualify), historyKey: it.match.HistoryKey})
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

// resetWidgetDraft clears the add/edit modal's draft state back to "add
// a brand new widget" — editWidgetIdx = -1, everything else at its zero
// value/default type.
func resetWidgetDraft() {
	addWidgetErr = ""
	addWidgetType = dashboard.WidgetChart
	addWidgetTitle = ""
	addWidgetSeriesPicks = nil
	addWidgetSpanX = 0
	addWidgetSpanY = 0
	addWidgetMin = ""
	addWidgetMax = ""
	editWidgetIdx = -1
}

// openWidgetEditor opens the same modal the picker's add button does, but
// pre-filled from w and in "edit this one in place" mode
// (confirmAddWidget replaces index idx instead of appending) — reusing
// one modal/one draft-state/one confirm path for both add and edit, not
// a second editor built separately. w.Series copies straight into
// addWidgetSeriesPicks — every widget type edits its series list the
// same way now, no more gauge/number-only single-pick shortcut.
func openWidgetEditor(idx int, w dashboard.Widget) {
	resetWidgetDraft()
	editWidgetIdx = idx
	addWidgetType = w.Type
	addWidgetTitle = w.Title
	addWidgetSpanX = float64(w.RawSpanX())
	addWidgetSpanY = float64(w.RawSpanY())
	addWidgetSeriesPicks = append([]dashboard.SeriesRef(nil), w.Series...)
	if w.Min != nil && w.Max != nil {
		addWidgetMin = strconv.FormatFloat(*w.Min, 'f', -1, 64)
		addWidgetMax = strconv.FormatFloat(*w.Max, 'f', -1, 64)
	}
	addWidgetOpen = true
}

// availableSeries lists selector suggestions the picker offers, sorted —
// the full cartesian product of every position a selector pattern has,
// each varied independently, for every measurement actually observed:
//
//   - server: the literal server name, "*", and (if it's in one)
//     "cluster:itsCluster"
//   - metric: the literal metric name, and "*"
//   - instance (only for a per-instance measurement — disk's
//     "root.used_percent", decomposing at the last "." into instance
//     "root", baseKey "used_percent", the same split
//     metric.Definition.ResolveMeasurement uses): the literal instance,
//     "*", and omitted entirely (selector.Match's own instance-agnostic
//     2-segment form)
//
// Generating every combination, not just one wildcarded position at a
// time, is deliberate: ui.Autocomplete matches by substring, so typing
// "*/disk.*" toward "every volume on every server" needs a suggestion
// that already has "*" in both the server and instance slots at once —
// a suggestion set that only ever wildcards one position can't be typed
// toward from there, no matter how many single-wildcard variants it
// offers. These are still only suggestions, not the only selectors an
// Autocomplete field accepts — a pattern this function didn't think to
// generate (there's no cluster+instance-wildcard combination, say) still
// works fine typed by hand. Resolved from the live snapshot, same as the
// rest of this file: a widget can only be suggested a measurement that
// actually exists right now, not guessed from static YAML the GUI would
// otherwise need a Registry reference to read.
func availableSeries(all []state.ServerView) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(sel string) {
		if !seen[sel] {
			seen[sel] = true
			out = append(out, sel)
		}
	}

	for _, sv := range all {
		serverPatterns := []string{sv.Name, "*"}
		if sv.Cluster != "" {
			serverPatterns = append(serverPatterns, "cluster:"+sv.Cluster)
		}
		for _, mv := range sv.Metrics {
			metricPatterns := []string{mv.Name, "*"}
			for _, m := range mv.Measurements {
				// measurePatterns is the metric path's own remainder
				// after the metric name: just the raw key for an
				// ordinary measurement, or — when it decomposes into
				// (instance, baseKey) — all three ways of naming that:
				// the exact instance, any instance, or no instance
				// segment at all.
				measurePatterns := []string{m.Key}
				if i := strings.LastIndexByte(m.Key, '.'); i > 0 && i < len(m.Key)-1 {
					instance, baseKey := m.Key[:i], m.Key[i+1:]
					measurePatterns = []string{instance + "." + baseKey, "*." + baseKey, baseKey}
				}
				for _, sp := range serverPatterns {
					for _, mp := range metricPatterns {
						for _, ip := range measurePatterns {
							add(sp + "/" + mp + "." + ip)
						}
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// defaultSeriesPick is addWidgetModal's starting Autocomplete value: the
// first option in availableSeries' own sorted order. There's no
// per-measurement Unit any more (see internal/metric's package doc) to
// prefer a percent-scaled option by, so every widget type just gets
// whatever sorts first — an accepted loss of that simplification.
func defaultSeriesPick(options []string, all []state.ServerView, widgetType dashboard.WidgetType) string {
	if len(options) == 0 {
		return ""
	}
	return options[0]
}

func addWidgetModal(c *ui.Context, d *dashboard.Dashboard, all []state.ServerView, dashStore *dashboard.Store) {
	t := c.Theme()
	ui.Modal(c, &addWidgetOpen, func() {
		options := availableSeries(all)

		ui.Column(c).Width(520).Background(t.Surface).Radius(8).Padding(16).Gap(10).Children(func() {
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

			ui.Text(c, "Size (cells, 0 = auto)").FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(20).AlignItems(ui.Center).Children(func() {
				spanControl(c, t, "Width", &addWidgetSpanX)
				spanControl(c, t, "Height", &addWidgetSpanY)
			})

			ui.Text(c, "Title (optional)").FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &addWidgetTitle)

			if addWidgetType == dashboard.WidgetGauge || addWidgetType == dashboard.WidgetBars {
				ui.Text(c, "Scale (0% to 100% of the dial/bar — e.g. 0 and 100 for percentages)").FontSize(11).TextColor(t.TextMuted)
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Gap(2).Children(func() {
						ui.Text(c, "Min").FontSize(9).TextColor(t.TextMuted)
						ui.TextInput(c, &addWidgetMin).Placeholder("0").Width(90)
					})
					ui.Column(c).Gap(2).Children(func() {
						ui.Text(c, "Max").FontSize(9).TextColor(t.TextMuted)
						ui.TextInput(c, &addWidgetMax).Placeholder("100").Width(90)
					})
				})
			}

			ui.Text(c, "Series").FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, "type or pick a suggestion — * matches anything (*/cpu.usage, cluster:name/cpu.usage, jumpbox/disk.*.used_percent)").FontSize(9).TextColor(t.TextMuted)
			if len(options) == 0 {
				ui.Text(c, "nothing is reporting yet to suggest from — you can still type a selector").FontSize(10).TextColor(t.TextMuted)
			}
			// There's always exactly one blank row at the end, ready to
			// type a new series into — no separate "+" button. The very
			// first row of a brand new widget still gets the
			// percent-preferring smart default (same as "+" used to
			// seed it with); every row after that starts genuinely
			// blank, since by the second series a guessed default is
			// more often wrong than helpful.
			if len(addWidgetSeriesPicks) == 0 {
				addWidgetSeriesPicks = append(addWidgetSeriesPicks, dashboard.SeriesRef{
					Selector: defaultSeriesPick(options, all, addWidgetType),
				})
			} else if strings.TrimSpace(addWidgetSeriesPicks[len(addWidgetSeriesPicks)-1].Selector) != "" {
				addWidgetSeriesPicks = append(addWidgetSeriesPicks, dashboard.SeriesRef{})
			}
			// An editable ui.Table, one row per series — every field of
			// every row is always live-editable in place (bound straight
			// to &addWidgetSeriesPicks[row].X). flat strips each cell's
			// own input-box chrome (padding/background/border/radius) —
			// Table already pads its own cell wrapper
			// (t.Space(1.5)/t.Space(2.5), not ours to change), so a
			// plain TextInput/Autocomplete on top of that doubled the
			// padding and boxed every cell in its own little pill,
			// nothing like a spreadsheet. A blank Selector (the standing
			// trailing row, or one left blank mid-list) is filtered out
			// at Save, not here — this table shows the draft exactly as
			// typed, including not-yet-finished rows.
			flat := func(e ui.Element) ui.Element {
				return e.Padding(0).Background(ui.Color{}).Border(0, ui.Color{}).Radius(0).FontSize(11)
			}
			seriesColumns := []ui.TableColumn{
				{Title: "Selector"},
				{Title: "Label", Width: 110},
				{Title: "Format", Width: 70},
				{Title: "", Width: 28, Fixed: true},
			}
			ui.Table(c, nil, seriesColumns, len(addWidgetSeriesPicks), func(row, col int) {
				// A Remove click (col 3) earlier in this same pass may
				// already have shortened addWidgetSeriesPicks — Table
				// still calls cell() for every row up to the count it
				// was given at the top of this render, same hazard
				// dashboardGrid's own widget-removal crash came from
				// (mutating the slice mid-render, after a later index
				// was already committed to be built this frame).
				if row >= len(addWidgetSeriesPicks) {
					return
				}
				switch col {
				case 0:
					flat(ui.Autocomplete(c, &addWidgetSeriesPicks[row].Selector, options)).Grow(1).
						Placeholder("server/metric.measurement")
				case 1:
					flat(ui.TextInput(c, &addWidgetSeriesPicks[row].Label)).Placeholder("Label").
						Tooltip("Label (optional) — shown instead of the metric's own name")
				case 2:
					flat(ui.TextInput(c, &addWidgetSeriesPicks[row].Format)).Placeholder("Format").
						Tooltip("Format (optional): %.0f, %6.2f%%, bytes (human size), or /1073741824:%.1f GB (divide first)")
				case 3:
					if squareIconButton(c, fmt.Sprintf("series-remove-%d", row), removeIcon, "Remove").Clicked() {
						addWidgetSeriesPicks = append(addWidgetSeriesPicks[:row:row], addWidgetSeriesPicks[row+1:]...)
					}
				}
			}).Height(160)

			if addWidgetErr != "" {
				ui.Text(c, addWidgetErr).FontSize(11).TextColor(t.Danger)
			}

			confirmLabel := "Add widget"
			if editWidgetIdx >= 0 {
				confirmLabel = "Save widget"
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if squareIconButton(c, "add-widget-cancel", cancelIcon, "Cancel").Clicked() {
					addWidgetOpen = false
				}
				if squareIconButton(c, "add-widget-confirm", confirmIcon, confirmLabel).Clicked() {
					confirmAddWidget(d, dashStore)
				}
			})
		})
	})
}

// parseScale turns the editor's Min/Max text into Widget.Min/Max: both
// blank means no scale, one blank means the default 0 (min) or 100 (max).
func parseScale(minText, maxText string) (min, max *float64, err error) {
	minText, maxText = strings.TrimSpace(minText), strings.TrimSpace(maxText)
	if minText == "" && maxText == "" {
		return nil, nil, nil
	}
	lo, hi := 0.0, 100.0
	if minText != "" {
		if lo, err = strconv.ParseFloat(minText, 64); err != nil {
			return nil, nil, fmt.Errorf("scale min %q is not a number", minText)
		}
	}
	if maxText != "" {
		if hi, err = strconv.ParseFloat(maxText, 64); err != nil {
			return nil, nil, fmt.Errorf("scale max %q is not a number", maxText)
		}
	}
	return &lo, &hi, nil
}

// confirmAddWidget builds a dashboard.Widget from the modal's draft
// state, validates it, and persists it via dashStore.Save — errors (an
// empty picker, a rejected save) show inline in the modal instead of
// silently closing it, so a failed add is visibly a failed add.
func confirmAddWidget(d *dashboard.Dashboard, dashStore *dashboard.Store) {
	// addWidgetSeriesPicks always carries at least one blank trailing
	// row (the standing "type a new one here" row the table keeps
	// appending) — never a real series, so it (and any other row left
	// blank along the way) is dropped before Validate ever sees it: an
	// empty Selector fails selector.Parse, and "the trailing placeholder
	// row is an invalid series" isn't a real validation error to show
	// anyone.
	var series []dashboard.SeriesRef
	for _, p := range addWidgetSeriesPicks {
		if strings.TrimSpace(p.Selector) != "" {
			series = append(series, p)
		}
	}
	w := dashboard.Widget{Type: addWidgetType, Title: addWidgetTitle, Series: series, SpanX: int(addWidgetSpanX), SpanY: int(addWidgetSpanY)}
	if addWidgetType == dashboard.WidgetGauge || addWidgetType == dashboard.WidgetBars {
		min, max, err := parseScale(addWidgetMin, addWidgetMax)
		if err != nil {
			addWidgetErr = err.Error()
			return
		}
		w.Min, w.Max = min, max
	}
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
