package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/app"
	"aymanhs/watchtower/internal/dashboard"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
)

// render is always the master-detail layout now — a compact sidebar list
// plus one server's full detail (its own dashboard) at a time — not just
// above some width breakpoint. There used to be three narrower stacked
// tiers (compact/medium/detailed) falling back to a per-metric list
// rendering (metricLine) below that breakpoint; removed once the
// dashboard existed, rather than keeping two different "look at this
// server" views where only one of them could show a dashboard at all —
// resizing the window narrower no longer drops back to the old pane.
func render(c *ui.Context, store *state.Store, events *event.Engine, shells app.Shells, history *storage.Store, dashStore *dashboard.Store) {
	t := c.Theme()
	width, _ := c.Size()
	ungrouped, clusters := groupByCluster(store.Snapshot())

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		toolbar(c, shells)

		if len(ungrouped) == 0 && len(clusters) == 0 {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "waiting for data…").FontSize(13).TextColor(t.TextMuted)
			})
			return
		}

		renderMasterDetail(c, store, events, history, dashStore, ungrouped, clusters, width-sidebarWidth)
	})
}

// clusterGroup is servers sharing a server.Config.Cluster label, with a
// rolled-up status (the worst of its members', same rule Store already
// applies across one server's own metrics) so "look at a cluster" means
// something at a glance before drilling into any one member.
type clusterGroup struct {
	name    string
	status  state.Status
	members []state.ServerView
}

// groupByCluster splits a snapshot into servers with no cluster label
// (rendered exactly as before) and clusters (rendered as a header plus
// their members), clusters sorted by name for a stable display order.
func groupByCluster(servers []state.ServerView) (ungrouped []state.ServerView, clusters []clusterGroup) {
	byName := map[string][]state.ServerView{}
	var order []string
	for _, sv := range servers {
		if sv.Cluster == "" {
			ungrouped = append(ungrouped, sv)
			continue
		}
		if _, ok := byName[sv.Cluster]; !ok {
			order = append(order, sv.Cluster)
		}
		byName[sv.Cluster] = append(byName[sv.Cluster], sv)
	}
	sort.Strings(order)
	for _, name := range order {
		members := byName[name]
		status := state.StatusOK
		for _, m := range members {
			status = state.Worst(status, m.Status)
		}
		clusters = append(clusters, clusterGroup{name: name, status: status, members: members})
	}
	return ungrouped, clusters
}

// clusterTooltip names any member not currently ok, same spirit as
// serverTooltip for one server's metrics.
func clusterTooltip(g clusterGroup) string {
	if g.status == state.StatusOK {
		return g.name + ": all " + fmt.Sprint(len(g.members)) + " ok"
	}
	var worst []string
	for _, m := range g.members {
		if m.Status != state.StatusOK {
			worst = append(worst, fmt.Sprintf("%s (%s)", m.Name, m.Status))
		}
	}
	return g.name + ": " + strings.Join(worst, ", ")
}

// shortAbbrev generically abbreviates a metric name for a tooltip or a
// tight label — the first three letters, upper-cased ("cpu" -> "CPU",
// "memory" -> "MEM"). Deliberately not a hand-maintained per-metric name
// table: the brief is explicit that the GUI must not need special-case
// code for any particular metric.
func shortAbbrev(name string) string {
	r := []rune(strings.ToUpper(name))
	if len(r) > 3 {
		r = r[:3]
	}
	return string(r)
}

// serverTooltip summarizes a server's status for its dot, naming any
// metric currently at warning/critical/unknown so the detail a small dot
// can't show text for is still a hover away.
func serverTooltip(sv state.ServerView) string {
	if sv.Status == state.StatusOK {
		return sv.Name + ": ok"
	}
	var worst []string
	for _, mv := range sv.Metrics {
		if mv.Status != state.StatusOK {
			worst = append(worst, fmt.Sprintf("%s (%s)", shortAbbrev(mv.Name), mv.Status))
		}
	}
	return sv.Name + ": " + strings.Join(worst, ", ")
}

func statusDot(c *ui.Context, status state.Status, size float32, tooltip string) {
	t := c.Theme()
	e := ui.Box(c).Size(size, size).Radius(size / 2).Background(statusColor(t, status))
	if tooltip != "" {
		e.Tooltip(tooltip)
	}
}

// formatValue prints one value the way its measurement declared it should
// look: bytes always get GiB-scaled (that's the unit itself saying so, not
// a guess), then format picks the rest — FormatHuman's K/M/B scaling,
// FormatInt's no-decimals, or FormatDefault's plain two decimals. The GUI
// never infers this from a value's magnitude; a metric that wants "always
// humanized" or "always a whole number" says so in its own display.format,
// same principle as Widget.
func formatValue(unit string, format metric.Format, v float64) string {
	if unit == "bytes" && v >= 1<<20 {
		return fmt.Sprintf("%.1fGiB", v/(1<<30))
	}
	switch format {
	case metric.FormatInt:
		return fmt.Sprintf("%.0f", v)
	case metric.FormatHuman:
		switch {
		case v >= 1e9:
			return fmt.Sprintf("%.2fB", v/1e9)
		case v >= 1e6:
			return fmt.Sprintf("%.2fM", v/1e6)
		case v >= 1e3:
			return fmt.Sprintf("%.2fK", v/1e3)
		}
	}
	return fmt.Sprintf("%.2f", v)
}
