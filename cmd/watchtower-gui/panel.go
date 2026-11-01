package main

import (
	"fmt"
	"sort"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/state"
)

// Master-detail state. Package-level like themeCycle/cycleTheme — this is
// a single-window app, there's nowhere else for "which server is open"
// to live between frames, and a GUI toolkit's view function doesn't carry
// its own persistent storage across renders.
var (
	selectedServer = ""
	sidebarWidth   = float32(220)
	bottomOpen     = false
	bottomHeight   = float32(180)
)

// toolbar is the single row of global, always-visible actions at the top
// of the window — events and theme today, more to come, all in one place
// instead of scattered across per-tier headers. ui.Toolbar (not a plain
// Row) gives its buttons the flat, compact "toolbar button" look — no
// background or border until hovered, tighter padding than an ordinary
// Button — and an overflow menu for whenever more buttons stop fitting.
// Natural width, no Fill/SpaceBetween, keeps everything flush left.
func toolbar(c *ui.Context) {
	t := c.Theme()
	ui.Toolbar(c, func() {
		ui.Text(c, "Watchtower").FontSize(12).TextColor(t.TextMuted)

		eventsLabel := "Events ▸"
		if bottomOpen {
			eventsLabel = "Events ▾"
		}
		if ui.Button(c.Key("events-toggle"), eventsLabel).FontSize(11).Clicked() {
			bottomOpen = !bottomOpen
		}
		if ui.Button(c.Key("theme"), themeLabel()).FontSize(11).Clicked() {
			cycleTheme()
		}
	}).Label("Main")
}

// renderMasterDetail is the "wide" tier: a compact, clickable server list
// on the left (every server and cluster, always visible, never scrolled
// out of a long stacked page) and one server's full detail on the right —
// what "detailed" was trying to be before several metrics per server made
// it a wall of gauges. Selecting a different server is just a click, not
// a scroll.
func renderMasterDetail(c *ui.Context, store *state.Store, events *event.Engine, ungrouped []state.ServerView, clusters []clusterGroup) {
	all := allServers(ungrouped, clusters)
	if selectedServer == "" || !hasServer(all, selectedServer) {
		if len(all) > 0 {
			selectedServer = all[0].Name
		}
	}

	ui.Split(c, &sidebarWidth, func() {
		sidebar(c, ungrouped, clusters)
	}, func() {
		detailPane(c, events, all)
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

// detailPane is the right side: the selected server's full detail (every
// metric, exactly the "detailed" tier's own per-metric rendering — no
// second implementation to keep in sync), optionally split with the
// events/alerts panel beneath it.
func detailPane(c *ui.Context, events *event.Engine, all []state.ServerView) {
	t := c.Theme()
	sv, ok := findServer(all, selectedServer)
	if !ok {
		ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Text(c, "select a server").FontSize(13).TextColor(t.TextMuted)
		})
		return
	}

	top := func() {
		ui.Scroll(c).Fill().Padding(12).Gap(6).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(0, 0, 8, 0).Children(func() {
				statusDot(c, sv.Status, 14, serverTooltip(sv))
				ui.Text(c, sv.Name).FontSize(17).Bold()
			})
			for i := range sv.Metrics {
				metricLine(c, t, sv.Metrics[i])
			}
		})
	}

	if !bottomOpen {
		top()
		return
	}
	ui.SplitVertical(c, &bottomHeight, top, func() {
		eventsPanel(c, events, sv.Name)
	}).Fill()
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
