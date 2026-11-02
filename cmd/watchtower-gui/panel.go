package main

import (
	"fmt"
	"io"
	"log"
	"sort"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"

	"aymanhs/watchtower/internal/app"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
)

// Master-detail state. Package-level like themeCycle/cycleTheme — this is
// a single-window app, there's nowhere else for "which server is open"
// to live between frames, and a GUI toolkit's view function doesn't carry
// its own persistent storage across renders.
var (
	selectedServer = ""
	sidebarWidth   = float32(220)
	bottomHeight   = float32(180)

	// bottomMode picks what the bottom split shows: "" (closed), "events"
	// or "terminal" — one slot, one thing at a time, not stacked.
	bottomMode = ""

	// The terminal session, if one's been opened, independent of
	// selectedServer so switching servers in the sidebar to check
	// something doesn't kill a live shell: it keeps running, just not
	// shown, until the terminal button targets a different server.
	termSession *terminal.Terminal
	termConn    io.Closer
	termServer  = ""
)

// chartWindow is how far back a chart looks. Fixed for this first pass —
// a picker (1h/24h/7d/...) is a natural follow-up, not built yet.
const chartWindow = time.Hour

// chartItem is one series a chart can plot: a measurement's current view
// (for its Unit/Type/Format/DisplayLabel) plus the exact history key to
// query it under (internal/storage's "server/metric.key"). Carrying
// historyKey explicitly, not re-deriving it from a naming convention, is
// what lets the same grouping/drawing code (renderSeriesChart) serve both
// the ad hoc single-metric chart (chartPanel, every series from the one
// clicked metric) and a dashboard chart widget (series can each name a
// different metric on the same server).
type chartItem struct {
	mv         state.MeasurementView
	historyKey string
}

// unitGroup is every chartItem that shares a unit — chartPanel gives each
// group its own y-axis (left for the first group, right for the second)
// rather than, say, plotting load averages and a percentage on the same
// scale.
type unitGroup struct {
	unit  string
	items []chartItem
}

// groupByUnit splits measurements into unitGroups, the group containing
// the Primary measurement (if any) first, the rest ordered by how many
// measurements they hold (most first, alphabetical-by-unit to break
// ties) — deterministic regardless of the measurement map's own
// iteration order. Capped at 2 groups: chart.Plot's Axes only has a Left
// and a Right, not arbitrarily many.
//
// Sorting the non-primary groups by size, not just alphabetically, matters
// in practice: cpu's measurements are load1/load5/load15 (unit "load", 3
// members) and usage (unit "percent", primary) and cores (unit "count", 1
// member, really just a scaling reference for the load bars, not
// something anyone wants charted on its own) — alphabetical-only ordering
// would pick "count" over "load" for the second slot and silently drop
// the very loads this feature exists to chart together.
func groupByUnit(items []chartItem) []unitGroup {
	byUnit := map[string][]chartItem{}
	var units []string
	primaryUnit := ""
	for _, it := range items {
		if _, ok := byUnit[it.mv.Unit]; !ok {
			units = append(units, it.mv.Unit)
		}
		byUnit[it.mv.Unit] = append(byUnit[it.mv.Unit], it)
		if it.mv.Primary {
			primaryUnit = it.mv.Unit
		}
	}
	sort.Slice(units, func(i, j int) bool {
		ni, nj := len(byUnit[units[i]]), len(byUnit[units[j]])
		if ni != nj {
			return ni > nj
		}
		return units[i] < units[j]
	})
	if primaryUnit != "" {
		for i, u := range units {
			if u == primaryUnit {
				units = append(units[:i:i], units[i+1:]...)
				units = append([]string{primaryUnit}, units...)
				break
			}
		}
	}
	if len(units) > 2 {
		units = units[:2]
	}
	groups := make([]unitGroup, len(units))
	for i, u := range units {
		groups[i] = unitGroup{unit: u, items: byUnit[u]}
	}
	return groups
}

// seriesColors is a small fixed categorical palette, cycled through for
// however many series a group has — good for the handful of measurements
// one metric realistically has (cpu's 3 loads, memory's 4 byte figures),
// not meant to stay distinguishable for dozens.
func seriesColors(t *ui.Theme) []ui.Color {
	return []ui.Color{t.Accent, t.Success, t.Warning, t.Danger, t.TextMuted}
}

// toolbar is the single row of global, always-visible actions at the top
// of the window — events and theme today, more to come, all in one place
// instead of scattered across per-tier headers. ui.Toolbar (not a plain
// Row) gives its buttons the flat, compact "toolbar button" look — no
// background or border until hovered, tighter padding than an ordinary
// Button — and an overflow menu for whenever more buttons stop fitting.
// Natural width, no Fill/SpaceBetween, keeps everything flush left.
func toolbar(c *ui.Context, shells app.Shells) {
	t := c.Theme()
	ui.Toolbar(c, func() {
		ui.Text(c, "Watchtower").FontSize(12).TextColor(t.TextMuted)

		eventsTooltip := "Show events"
		if bottomMode == "events" {
			eventsTooltip = "Hide events"
		}
		if ui.Button(c.Key("events-toggle"), "").Tooltip(eventsTooltip).
			Children(func() { ui.Icon(c, bellIcon).FontSize(14) }).
			Clicked() {
			if bottomMode == "events" {
				bottomMode = ""
			} else {
				bottomMode = "events"
			}
		}

		if ui.Button(c.Key("terminal"), "").
			Tooltip(terminalTooltip(shells)).
			Disabled(selectedServer == "" || shells[selectedServer] == nil).
			Children(func() { ui.Icon(c, terminalIcon).FontSize(14) }).
			Clicked() {
			if bottomMode == "terminal" && termServer == selectedServer {
				bottomMode = "" // same server's terminal already showing: just hide it, session stays alive
			} else {
				ensureTerminal(selectedServer, shells)
				bottomMode = "terminal"
			}
		}

		if ui.Button(c.Key("theme"), "").Tooltip(themeTooltip()).
			Children(func() { ui.Icon(c, themeIcon()).FontSize(14) }).
			Clicked() {
			cycleTheme()
		}
	}).Label("Main")
}

func terminalTooltip(shells app.Shells) string {
	if selectedServer == "" {
		return "Terminal: select a server first"
	}
	if shells[selectedServer] == nil {
		return "Terminal: " + selectedServer + " has no SSH connection (local server)"
	}
	if termServer == selectedServer && termSession != nil {
		if bottomMode == "terminal" {
			return "Hide terminal (" + selectedServer + " session stays open)"
		}
		return "Show terminal on " + selectedServer
	}
	return "Open a terminal on " + selectedServer
}

// ensureTerminal makes termSession the terminal for serverName, reusing
// the existing live session if one's already open for it (switching
// sidebar selection and back doesn't restart your shell), or opening a
// fresh one — over the server's existing persistent SSH connection
// (sshmgr.Client, via Shells), not a new authentication — closing
// whatever session was open for a different server first.
//
// The terminal engine (github.com/egoist/mygo/plugins/terminal) downloads
// a small native library from GitHub the first time any terminal opens on
// this machine, caching it after — a one-time cost, not per-session.
func ensureTerminal(serverName string, shells app.Shells) {
	if termSession != nil && termServer == serverName {
		return
	}
	if termConn != nil {
		termConn.Close()
		termSession, termConn, termServer = nil, nil, ""
	}

	client := shells[serverName]
	if client == nil {
		return
	}
	conn, err := client.OpenShell(80, 24)
	if err != nil {
		log.Printf("terminal: open shell on %s: %v", serverName, err)
		return
	}
	term, err := terminal.New(terminal.Options{Conn: conn})
	if err != nil {
		log.Printf("terminal: %v", err)
		conn.Close()
		return
	}
	termSession, termConn, termServer = term, conn, serverName
}

// renderMasterDetail is the "wide" tier: a compact, clickable server list
// on the left (every server and cluster, always visible, never scrolled
// out of a long stacked page) and one server's full detail on the right —
// what "detailed" was trying to be before several metrics per server made
// it a wall of gauges. Selecting a different server is just a click, not
// a scroll.
func renderMasterDetail(c *ui.Context, store *state.Store, events *event.Engine, history *storage.Store, servers app.Servers, ungrouped []state.ServerView, clusters []clusterGroup, paneWidth float32) {
	all := allServers(ungrouped, clusters)
	if selectedServer == "" || !hasServer(all, selectedServer) {
		if len(all) > 0 {
			selectedServer = all[0].Name
		}
	}

	ui.Split(c, &sidebarWidth, func() {
		sidebar(c, ungrouped, clusters)
	}, func() {
		detailPane(c, events, history, servers, all, paneWidth)
	}).Fill()
}

// allServers flattens ungrouped servers and every cluster's members into
// one list, sorted by name — the sidebar's selection needs to find "the
// currently selected one" regardless of whether it's clustered.
func allServers(ungrouped []state.ServerView, clusters []clusterGroup) []state.ServerView {
	all := append([]state.ServerView(nil), ungrouped...)
	for _, g := range clusters {
		all = append(all, g.members...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all
}

func hasServer(all []state.ServerView, name string) bool {
	for _, sv := range all {
		if sv.Name == name {
			return true
		}
	}
	return false
}

func findServer(all []state.ServerView, name string) (state.ServerView, bool) {
	for _, sv := range all {
		if sv.Name == name {
			return sv, true
		}
	}
	return state.ServerView{}, false
}

// sidebar is the left pane: every server, compact (dot + name only — this
// pane stays narrow by design), clickable to select it. Clusters still
// group their members under a header, same as the stacked tiers.
func sidebar(c *ui.Context, ungrouped []state.ServerView, clusters []clusterGroup) {
	t := c.Theme()
	ui.Scroll(c).Fill().Background(t.Surface).Padding(8).Gap(2).Children(func() {
		for i := range ungrouped {
			selectableRow(c, ungrouped[i])
		}
		for _, g := range clusters {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(4, 0, 2, 0).Children(func() {
				statusDot(c, g.status, 8, clusterTooltip(g))
				ui.Text(c, g.name).FontSize(11).Bold().TextColor(t.TextMuted)
			})
			ui.Column(c).Gap(2).Padding(0, 0, 0, 10).Children(func() {
				for _, m := range g.members {
					selectableRow(c, m)
				}
			})
		}
	})
}

// selectableRow is one sidebar entry: a dot, the name, highlighted when
// it's the one currently shown in the detail pane, clicking it selects it.
func selectableRow(c *ui.Context, sv state.ServerView) {
	t := c.Theme()
	row := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(4, 6, 4, 6).Radius(4)
	if sv.Name == selectedServer {
		row = row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		statusDot(c, sv.Status, 9, serverTooltip(sv))
		ui.Text(c, sv.Name).FontSize(12)
	})
	if row.Clicked() {
		selectedServer = sv.Name
	}
}

// detailPane is the right side: the selected server's own dashboard,
// optionally split with the events/terminal panel beneath it.
func detailPane(c *ui.Context, events *event.Engine, history *storage.Store, servers app.Servers, all []state.ServerView, paneWidth float32) {
	t := c.Theme()
	sv, ok := findServer(all, selectedServer)
	if !ok {
		ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Text(c, "select a server").FontSize(13).TextColor(t.TextMuted)
		})
		return
	}

	// The detail pane's top content is always the server's own dashboard
	// now — the per-metric list (metricLine by metricLine, what this used
	// to show, and still what the narrower stacked tiers in view.go show,
	// since they have no split pane to put a dashboard in) stepped back
	// in favor of the widget grid once dashboards existed at all: there's
	// no reason to keep two different "look at this server" views once
	// one of them is strictly more capable.
	top := func() {
		dashboardGrid(c, sv, servers[sv.Name], history, paneWidth)
	}

	switch bottomMode {
	case "events":
		ui.SplitVertical(c, &bottomHeight, top, func() {
			eventsPanel(c, events, sv.Name)
		}).Fill()
	case "terminal":
		ui.SplitVertical(c, &bottomHeight, top, func() {
			terminalPanel(c)
		}).Fill()
	default:
		top()
	}
}

// terminalPanel shows termSession — the live shell ensureTerminal opened
// — filling the bottom slot. terminal.View is a plain Element like any
// other; it never needed a window of its own.
func terminalPanel(c *ui.Context) {
	t := c.Theme()
	if termSession == nil {
		ui.Box(c).Fill().Background(t.Surface).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Text(c, "no terminal open").FontSize(11).TextColor(t.TextMuted)
		})
		return
	}
	terminal.View(c, termSession).Fill().AutoFocus()
}

// eventsPanel lists sv's events (both threshold and operational), most
// recent first — the one piece of "investigate this server further" the
// brief asks for that's genuinely buildable without Phase 3 storage:
// event.Engine already keeps this history, just not filtered per server
// until now.
func eventsPanel(c *ui.Context, events *event.Engine, serverName string) {
	t := c.Theme()
	all := events.History()

	var mine []event.Event
	for _, ev := range all {
		if ev.Server == serverName {
			mine = append(mine, ev)
		}
	}

	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		ui.Row(c).Padding(10, 10, 4, 10).Children(func() {
			ui.Text(c, fmt.Sprintf("Events — %s", serverName)).FontSize(11).Bold().TextColor(t.TextMuted)
		})
		ui.Scroll(c).Fill().Padding(0, 10, 10, 10).Gap(3).Children(func() {
			if len(mine) == 0 {
				ui.Text(c, "no events yet").FontSize(11).TextColor(t.TextMuted)
				return
			}
			for i := len(mine) - 1; i >= 0; i-- {
				eventRow(c, mine[i])
			}
		})
	})
}

func eventRow(c *ui.Context, ev event.Event) {
	t := c.Theme()
	sev := ev.Severity
	color := t.Success
	switch sev {
	case "warning":
		color = t.Warning
	case "critical":
		color = t.Danger
	}
	if sev == "" {
		sev = "normal"
	}
	kind := string(ev.Kind)
	msg := ev.Message
	if msg == "" {
		msg = fmt.Sprintf("value=%v threshold=%v", ev.Value, ev.Threshold)
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(7, 7).Radius(3.5).Background(color)
		ui.Text(c, ev.Timestamp.Format("15:04:05")).FontSize(10).Width(50).TextColor(t.TextMuted)
		ui.Text(c, kind).FontSize(10).Width(55).TextColor(t.TextMuted)
		ui.Text(c, ev.Metric).FontSize(10).Width(90)
		ui.Text(c, msg).FontSize(10).TextColor(t.TextMuted)
	})
}

// rateOf converts cumulative counter points (bytes sent since boot, say)
// into a per-second rate series, one point shorter than the input: each
// output point is (value[i]-value[i-1]) / seconds-between-them. A
// negative delta — the counter reset, e.g. the process or the remote
// host restarted — clamps to 0 rather than plotting a bogus negative
// spike; it's not a real negative rate, it's a discontinuity.
func rateOf(points []storage.Point) []storage.Point {
	if len(points) < 2 {
		return nil
	}
	out := make([]storage.Point, 0, len(points)-1)
	for i := 1; i < len(points); i++ {
		dt := points[i].Timestamp.Sub(points[i-1].Timestamp).Seconds()
		if dt <= 0 {
			continue
		}
		dv := points[i].Value - points[i-1].Value
		if dv < 0 {
			dv = 0
		}
		out = append(out, storage.Point{Timestamp: points[i].Timestamp, Value: dv / dt})
	}
	return out
}

// chartSeries is one measurement's resolved, ready-to-draw data within a
// chart tile: its history (rate-converted already, if it's a counter),
// scaled into [0,1]-normalized x/y coordinates against a shared x scale and
// its unitGroup's own y scale, plus the color and label to draw/legend it
// with.
type chartSeries struct {
	label string
	color ui.Color
	xs    []float32
	ys    []float32
}

// axisUnitScale picks a divisor and a short suffix for one unit group's
// y-axis, so chart.Linear.Ticks's own adaptive per-tick-step decimal
// formatter (see scale.go's doc comment: "nil [Format] labels them with
// as many decimals as their step needs") runs on sensibly-scaled numbers
// instead of raw bytes or millions. hi is the group's own largest value
// this window — unlike formatValue's live single-value formatting (fixed
// by unit alone), an axis divisor has to match the group's actual
// magnitude, not just its unit, so a %.1fGiB-style fixed-decimal collision
// across several close ticks never happens regardless of how wide or
// narrow that group's real range is.
func axisUnitScale(unit string, hi float64) (divisor float64, suffix string) {
	if unit == "bytes" {
		return 1 << 30, "GiB"
	}
	switch {
	case hi >= 1e9:
		return 1e9, "B"
	case hi >= 1e6:
		return 1e6, "M"
	case hi >= 1e3:
		return 1e3, "K"
	default:
		return 1, ""
	}
}

// renderSeriesChart draws a multi-series, dual-axis line chart over
// items' history — the shared rendering chartPanel (the ad hoc
// single-metric chart opened from a metric line's chart icon) and a
// dashboard chart widget (internal/dashboard-configured, arbitrary
// series) both build on, so there's one place this logic lives instead of
// two copies slowly drifting apart.
func renderSeriesChart(c *ui.Context, history *storage.Store, title string, items []chartItem) ui.Element {
	t := c.Theme()
	groups := groupByUnit(items)

	to := time.Now()
	from := to.Add(-chartWindow)
	xScale := chart.NewTime(from, to)
	colors := seriesColors(t)

	type plotGroup struct {
		unit   string
		suffix string
		scale  chart.Linear
		series []chartSeries
	}
	var plotGroups []plotGroup
	colorIdx := 0
	var queryErr error
	anyData := false

	for _, g := range groups {
		var groupValues []float64
		type rawSeries struct {
			label  string
			isRate bool
			points []storage.Point
		}
		var raws []rawSeries
		for _, it := range g.items {
			points, err := history.Query(it.historyKey, from, to)
			if err != nil {
				queryErr = err
				continue
			}
			isRate := it.mv.Type == metric.TypeCounter
			if isRate {
				points = rateOf(points)
			}
			if len(points) == 0 {
				continue
			}
			anyData = true
			for _, p := range points {
				groupValues = append(groupValues, p.Value)
			}
			raws = append(raws, rawSeries{label: it.mv.DisplayLabel(), isRate: isRate, points: points})
		}
		if len(raws) == 0 {
			continue
		}
		lo, hi := chart.Extent(groupValues...)
		// Scale the domain itself down by a unit-appropriate divisor
		// (bytes -> GiB; otherwise whatever K/M/B magnitude the group's
		// own values are actually at) *before* building the scale, then
		// leave yScale.Format nil so chart.Linear.Ticks' own adaptive
		// decimal-from-tick-step formatter runs on the divided numbers —
		// a fixed decimal count (the earlier approach: always "%.1fGiB")
		// is fine for one single reading but silently collides across
		// several close ticks drawn from a narrow range: 22.7-22.8 GiB
		// of free memory all rounded to the same "22.8GiB" at 1 decimal,
		// a real bug caught live, not in review. Only the suffix is
		// appended ourselves, in the axis Label callback below.
		divisor, suffix := axisUnitScale(g.unit, hi)
		yScale := chart.NewLinear(lo/divisor, hi/divisor).Nice(4)

		var series []chartSeries
		for _, r := range raws {
			label := r.label
			if r.isRate {
				label += "/sec"
			}
			xs := make([]float32, len(r.points))
			ys := make([]float32, len(r.points))
			for i, p := range r.points {
				xs[i] = xScale.At(p.Timestamp)
				ys[i] = yScale.At(p.Value / divisor)
			}
			series = append(series, chartSeries{
				label: label,
				color: colors[colorIdx%len(colors)],
				xs:    xs,
				ys:    ys,
			})
			colorIdx++
		}
		plotGroups = append(plotGroups, plotGroup{unit: g.unit, suffix: suffix, scale: yScale, series: series})
	}

	return ui.Column(c).Background(t.Surface).Children(func() {
		// title is "" from a dashboard chart tile — widgetCard already
		// shows a title in its own header, so this row would otherwise
		// just be dead padding above the legend. Only the ad hoc
		// single-metric chart (chartPanel) passes a real title, needing
		// its own row since it has no other header to put one in.
		if title != "" {
			ui.Row(c).Padding(8, 8, 2, 8).Children(func() {
				ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted)
			})
		}
		if queryErr != nil {
			ui.Text(c, "chart: "+queryErr.Error()).FontSize(11).TextColor(t.Danger).Padding(10, 10, 10, 10)
			return
		}
		if !anyData {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "no data yet for this window").FontSize(11).TextColor(t.TextMuted)
			})
			return
		}

		// axisColor is the one color a group's axis ticks and legend
		// heading both use — its first series' color, so "which line
		// reads off which axis" is answerable by eye (match the color),
		// not just by position (left ticks happen to belong to whichever
		// group came first).
		axisColor := func(i int) ui.Color {
			if i >= len(plotGroups) || len(plotGroups[i].series) == 0 {
				return t.TextMuted
			}
			return plotGroups[i].series[0].color
		}

		// Legend: grouped by axis side, not a flat list — a bold "Left
		// axis (unit)" / "Right axis (unit)" heading in that axis's own
		// color, its series listed under it. Answers "which line reads
		// which axis" directly instead of leaving it to be inferred.
		ui.Row(c).Wrap().Gap(12).Padding(8, 8, 4, 8).Children(func() {
			for i, g := range plotGroups {
				side := "Left axis"
				if i == 1 {
					side = "Right axis"
				}
				color := axisColor(i)
				unitLabel := g.unit
				if g.suffix != "" {
					unitLabel = g.suffix
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, side+" ("+unitLabel+")").FontSize(9).Bold().TextColor(color).NoWrap()
					for _, s := range g.series {
						ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
							ui.Box(c).Size(7, 7).Radius(3.5).Background(s.color)
							ui.Text(c, s.label).FontSize(9).TextColor(t.TextMuted).NoWrap()
						})
					}
				})
			}
		})

		axes := chart.Axes{
			Bottom: xScale.Ticks(4),
			// Tint each side's tick labels to match that axis's color
			// (the same one the legend heading above uses) — Bottom/Top
			// (there's no Top here) fall back to the plain muted text
			// every other tick in this app already uses.
			Label: func(side chart.Side, tk chart.Tick) {
				color := t.TextMuted
				label := tk.Label
				switch side {
				case chart.Left:
					color = axisColor(0)
					if len(plotGroups) > 0 {
						label += plotGroups[0].suffix
					}
				case chart.Right:
					color = axisColor(1)
					if len(plotGroups) > 1 {
						label += plotGroups[1].suffix
					}
				}
				ui.Text(c, label).FontSize(9).NoWrap().TextColor(color)
			},
		}
		if len(plotGroups) > 0 {
			axes.Left = plotGroups[0].scale.Ticks(4)
		}
		if len(plotGroups) > 1 {
			axes.Right = plotGroups[1].scale.Ticks(4)
		}

		chart.Plot(c, axes, func(area ui.Element) {
			area.Draw(func(p *ui.Painter, r ui.Rect) {
				// Grid lines first, faint, behind the data — the left
				// axis's own ticks (every plot has at least one axis) for
				// horizontal lines, the bottom's (time) ticks for
				// vertical ones.
				grid := t.Border.Alpha(0.5)
				if len(plotGroups) > 0 {
					for _, tk := range plotGroups[0].scale.Ticks(4) {
						y := chart.Y(r, tk.At)
						p.Line(r.X, y, r.X+r.W, y, 1, grid)
					}
				}
				for _, tk := range xScale.Ticks(4) {
					x := chart.X(r, tk.At)
					p.Line(x, r.Y, x, r.Y+r.H, 1, grid)
				}
				for _, g := range plotGroups {
					for _, s := range g.series {
						path := chart.Line(r, s.xs, s.ys, chart.CurveMonotone)
						p.StrokePath(path, 2, s.color)
					}
				}
			})
		}).Fill().Gap(3).Padding(0, 8, 6, 4)
	})
}
