package main

import (
	"io"
	"log"
	"sort"
	"strconv"
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

	// rightTab picks what the right pane shows: tabDashboard, tabMetrics
	// (the selected server's metric details) or tabEvents (global).
	rightTab = tabDashboard

	// bottomMode is "" (closed) or "terminal": the terminal splits under
	// whichever tab is showing.
	bottomMode = ""

	// The terminal session, if one's been opened, independent of
	// selectedServer so switching servers in the sidebar to check
	// something doesn't kill a live shell: it keeps running, just not
	// shown, until the terminal button targets a different server.
	termSession *terminal.Terminal
	termConn    io.Closer
	termServer  = ""
)

// chartCache serves every chart's aggregated history; set once in main
// (it needs the history store and the window's repaint hook).
var chartCache *aggCache

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

		if ui.Button(c.Key("events-toggle"), "").Tooltip("Show events").
			Children(func() { ui.Icon(c, bellIcon).FontSize(14) }).
			Clicked() {
			rightTab = tabEvents
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

// Right pane tabs.
const (
	tabDashboard = iota
	tabMetrics
	tabEvents
)

// detailPane is the right side: a tab bar (dashboards, the selected
// server's metric details, global events) over the tab's content,
// optionally split with the terminal beneath it, which needs
// selectedServer to resolve to a server.
func detailPane(c *ui.Context, events *event.Engine, history *storage.Store, dashStore *dashboard.Store, all []state.ServerView, paneWidth float32) {
	top := func() {
		ui.Column(c).Fill().Children(func() {
			ui.Tabs(c, &rightTab, "Dashboard", "Metrics", "Events")
			switch rightTab {
			case tabMetrics:
				metricDetailsPane(c, all, history)
			case tabEvents:
				eventsPanel(c, events)
			default:
				dashboardPane(c, all, dashStore, history, paneWidth)
			}
		}).Fill()
	}

	_, hasServer := findServer(all, selectedServer)
	if bottomMode == "terminal" && hasServer {
		ui.SplitVertical(c, &bottomHeight, top, func() {
			terminalPanel(c)
		}).Fill()
		return
	}
	top()
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

// chartSegment is one unbroken run of a series, as [0,1]-normalized
// coordinates against the chart's shared scales.
type chartSegment struct{ xs, ys, lo, hi []float32 }

// chartSeries is one item's resolved, ready-to-draw data within a chart
// tile: the average per window split into segments wherever data is
// missing, plus the color and label to draw/legend it with.
type chartSeries struct {
	label string
	color ui.Color
	segs  []chartSegment
}

// splitAtGaps breaks buckets into runs, starting a new run wherever the
// space between one bucket and the next exceeds two windows — an outage
// then shows as a gap instead of a line drawn across it.
func splitAtGaps(buckets []storage.Bucket, window time.Duration) [][]storage.Bucket {
	var runs [][]storage.Bucket
	for i, b := range buckets {
		if i == 0 || b.Start.Sub(buckets[i-1].End) > 2*window {
			runs = append(runs, nil)
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], b)
	}
	return runs
}

// windowLabel renders a window compactly: 15s, 5m, 6h, 1d.
func windowLabel(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	}
	return strconv.Itoa(int(d/time.Second)) + "s"
}

// rangeLabel is windowLabel for a chart's whole range.
func rangeLabel(d time.Duration) string { return windowLabel(d) }

// bucketMid is where a bucket's average is plotted on the time axis.
func bucketMid(b storage.Bucket) time.Time {
	return b.Start.Add(b.End.Sub(b.Start) / 2)
}

// renderSeriesChart draws a multi-series line chart over items' aggregated
// history for the last rng, all sharing one y-axis — the shared rendering
// a dashboard chart widget builds on. Data comes from chartCache, never a
// direct query: the window repaints every second. There's no
// per-measurement Unit any more (see internal/metric's package doc), so
// every series just shares the one scale, whatever its own magnitude.
func renderSeriesChart(c *ui.Context, title string, items []chartItem, rng time.Duration) ui.Element {
	t := c.Theme()
	colors := seriesColors(t)

	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.historyKey
	}
	view := chartCache.Get(names, rng)
	xScale := chart.NewTime(view.From, view.To)

	var allValues []float64
	for _, it := range items {
		for _, b := range view.Data[it.historyKey] {
			allValues = append(allValues, b.Min, b.Max)
		}
	}

	var series []chartSeries
	var yScale chart.Linear
	if len(allValues) > 0 {
		lo, hi := chart.Extent(allValues...)
		yScale = chart.NewLinear(lo, hi).Nice(4)
		for i, it := range items {
			buckets := view.Data[it.historyKey]
			if len(buckets) == 0 {
				continue
			}
			s := chartSeries{label: it.label, color: colors[i%len(colors)]}
			for _, run := range splitAtGaps(buckets, view.Window) {
				n := len(run)
				seg := chartSegment{xs: make([]float32, n), ys: make([]float32, n), lo: make([]float32, n), hi: make([]float32, n)}
				for j, b := range run {
					seg.xs[j] = xScale.At(bucketMid(b))
					seg.ys[j] = yScale.At(b.Avg())
					seg.lo[j] = yScale.At(b.Min)
					seg.hi[j] = yScale.At(b.Max)
				}
				s.segs = append(s.segs, seg)
			}
			series = append(series, s)
		}
	}
	queryErr := view.Err
	if len(series) > 0 {
		queryErr = nil // a failed refresh keeps showing the last good data
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
			msg := "no data yet for this window"
			if !view.Loaded {
				msg = "loading…"
			}
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, msg).FontSize(11).TextColor(t.TextMuted)
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
			// Says what each plotted point is, since it is no longer a raw sample.
			ui.Text(c, "avg + min/max per "+windowLabel(view.Window)+" · last "+rangeLabel(rng)).FontSize(9).TextColor(t.TextMuted).NoWrap()
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
					for _, seg := range s.segs {
						if len(seg.xs) == 1 { // a lone point has no line to draw
							p.Fill(ui.Rect{X: chart.X(r, seg.xs[0]) - 2, Y: chart.Y(r, seg.ys[0]) - 2, W: 4, H: 4}, s.color, 2)
							continue
						}
						// Each point summarizes many samples, so the min–max
						// envelope is drawn behind the average line.
						p.FillPath(chart.Area(r, seg.xs, seg.lo, seg.hi, chart.CurveMonotone), s.color.Alpha(0.18))
						p.StrokePath(chart.Line(r, seg.xs, seg.ys, chart.CurveMonotone), 2, s.color)
					}
				}
			})
		}).Fill().Gap(3).Padding(0, 8, 6, 4)
	})
}
