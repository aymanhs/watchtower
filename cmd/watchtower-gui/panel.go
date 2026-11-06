package main

import (
	"fmt"
	"io"
	"log"
	"sort"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"

	"aymanhs/watchtower/internal/app"
	"aymanhs/watchtower/internal/dashboard"
	"aymanhs/watchtower/internal/event"
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

// chartItem is one series a chart can plot: its label plus the exact
// history key to query it under (internal/storage's "server/metric.key").
// There's no per-measurement Unit/Type any more (see internal/metric's
// package doc), so every chartItem shares one y-axis now — no more
// unit-grouped dual-axis layout, and no more rate-conversion for counters
// (both accepted losses of that simplification).
type chartItem struct {
	label      string
	historyKey string
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
func toolbar(c *ui.Context, win *mygo.Window, shells app.Shells) {
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

		// Minimize-to-tray and exit, as their own buttons right in the
		// toolbar — not just the OS window's own minimize/close (both
		// already wired to the same hide-to-tray behavior, see tray.go),
		// since those two don't actually look any different from a
		// normal window's, and "close" there doesn't quit at all. An
		// explicit pair here makes both actions discoverable without
		// needing the tray icon's own right-click menu.
		if ui.Button(c.Key("minimize-to-tray"), "").Tooltip("Minimize to tray").
			Children(func() { ui.Icon(c, minimizeToTrayIcon).FontSize(14) }).
			Clicked() {
			win.Hide()
		}
		if ui.Button(c.Key("exit"), "").Tooltip("Exit Watchtower").
			Children(func() { ui.Icon(c, exitIcon).FontSize(14) }).
			Clicked() {
			mygo.App.Quit()
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
func renderMasterDetail(c *ui.Context, store *state.Store, events *event.Engine, history *storage.Store, dashStore *dashboard.Store, ungrouped []state.ServerView, clusters []clusterGroup, paneWidth float32) {
	all := allServers(ungrouped, clusters)
	if selectedServer == "" || !hasServer(all, selectedServer) {
		if len(all) > 0 {
			selectedServer = all[0].Name
		}
	}

	ui.Split(c, &sidebarWidth, func() {
		sidebar(c, ungrouped, clusters)
	}, func() {
		detailPane(c, events, history, dashStore, all, paneWidth)
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

// detailPane is the right side: a dashboard (any of them — dashboardPane
// has its own picker, independent of the sidebar), optionally split with
// the events/terminal panel beneath it. Those two bottom panels are
// still per-server, so they need selectedServer to actually resolve to
// one; the dashboard above them never does.
func detailPane(c *ui.Context, events *event.Engine, history *storage.Store, dashStore *dashboard.Store, all []state.ServerView, paneWidth float32) {
	// The detail pane's top content is always a dashboard now — the
	// per-metric list (metricLine by metricLine, what this used to show,
	// and still what the narrower stacked tiers in view.go show, since
	// they have no split pane to put a dashboard in) stepped back in
	// favor of the widget grid once dashboards existed at all: there's
	// no reason to keep two different "look at this server" views once
	// one of them is strictly more capable.
	top := func() {
		dashboardPane(c, all, dashStore, history, paneWidth)
	}

	sv, ok := findServer(all, selectedServer)
	if !ok {
		// No server selected: events/terminal have nothing to show, so
		// the dashboard just takes the whole pane rather than splitting
		// against an empty bottom half.
		top()
		return
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

// chartSeries is one item's resolved, ready-to-draw data within a chart
// tile: its history scaled into [0,1]-normalized x/y coordinates against
// the chart's shared x and y scales, plus the color and label to
// draw/legend it with.
type chartSeries struct {
	label string
	color ui.Color
	xs    []float32
	ys    []float32
}

// renderSeriesChart draws a multi-series line chart over items' history,
// all sharing one y-axis — the shared rendering a dashboard chart widget
// (internal/dashboard-configured, arbitrary series) builds on. There's no
// per-measurement Unit any more (see internal/metric's package doc), so
// there's nothing left to group series by for a second axis; every
// series just shares the one scale, whatever its own magnitude.
func renderSeriesChart(c *ui.Context, history *storage.Store, title string, items []chartItem) ui.Element {
	t := c.Theme()

	to := time.Now()
	from := to.Add(-chartWindow)
	xScale := chart.NewTime(from, to)
	colors := seriesColors(t)

	var allValues []float64
	type rawSeries struct {
		label  string
		points []storage.Point
	}
	var raws []rawSeries
	var queryErr error
	for _, it := range items {
		points, err := history.Query(it.historyKey, from, to)
		if err != nil {
			queryErr = err
			continue
		}
		if len(points) == 0 {
			continue
		}
		for _, p := range points {
			allValues = append(allValues, p.Value)
		}
		raws = append(raws, rawSeries{label: it.label, points: points})
	}

	var series []chartSeries
	var yScale chart.Linear
	if len(raws) > 0 {
		lo, hi := chart.Extent(allValues...)
		yScale = chart.NewLinear(lo, hi).Nice(4)
		for i, r := range raws {
			xs := make([]float32, len(r.points))
			ys := make([]float32, len(r.points))
			for j, p := range r.points {
				xs[j] = xScale.At(p.Timestamp)
				ys[j] = yScale.At(p.Value)
			}
			series = append(series, chartSeries{
				label: r.label,
				color: colors[i%len(colors)],
				xs:    xs,
				ys:    ys,
			})
		}
	}

	return ui.Column(c).Background(t.Surface).Children(func() {
		// title is "" from a dashboard chart tile — widgetCard already
		// shows a title in its own header, so this row would otherwise
		// just be dead padding above the legend.
		if title != "" {
			ui.Row(c).Padding(8, 8, 2, 8).Children(func() {
				ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted)
			})
		}
		if queryErr != nil {
			ui.Text(c, "chart: "+queryErr.Error()).FontSize(11).TextColor(t.Danger).Padding(10, 10, 10, 10)
			return
		}
		if len(series) == 0 {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "no data yet for this window").FontSize(11).TextColor(t.TextMuted)
			})
			return
		}

		ui.Row(c).Wrap().Gap(12).Padding(8, 8, 4, 8).Children(func() {
			for _, s := range series {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(7, 7).Radius(3.5).Background(s.color)
					ui.Text(c, s.label).FontSize(9).TextColor(t.TextMuted).NoWrap()
				})
			}
		})

		axes := chart.Axes{
			Bottom: xScale.Ticks(4),
			Left:   yScale.Ticks(4),
			Label: func(side chart.Side, tk chart.Tick) {
				ui.Text(c, tk.Label).FontSize(9).NoWrap().TextColor(t.TextMuted)
			},
		}

		chart.Plot(c, axes, func(area ui.Element) {
			area.Draw(func(p *ui.Painter, r ui.Rect) {
				// Grid lines first, faint, behind the data.
				grid := t.Border.Alpha(0.5)
				for _, tk := range yScale.Ticks(4) {
					y := chart.Y(r, tk.At)
					p.Line(r.X, y, r.X+r.W, y, 1, grid)
				}
				for _, tk := range xScale.Ticks(4) {
					x := chart.X(r, tk.At)
					p.Line(x, r.Y, x, r.Y+r.H, 1, grid)
				}
				for _, s := range series {
					path := chart.Line(r, s.xs, s.ys, chart.CurveMonotone)
					p.StrokePath(path, 2, s.color)
				}
			})
		}).Fill().Gap(3).Padding(0, 8, 6, 4)
	})
}
