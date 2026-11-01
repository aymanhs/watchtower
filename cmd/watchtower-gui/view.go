package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/state"
)

// Breakpoints, in DIPs, for the three tiers the user asked for:
//   - compact:  server name + overall status only
//   - medium:   + one indicator per metric
//   - detailed: + each metric's measurements
const (
	breakpointMedium   = 260
	breakpointDetailed = 480
)

func render(c *ui.Context, store *state.Store) {
	t := c.Theme()
	width, _ := c.Size()
	ungrouped, clusters := groupByCluster(store.Snapshot())

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Padding(8, 10, 0, 10).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Watchtower").FontSize(12).TextColor(t.TextMuted)
			if ui.Button(c.Key("theme"), themeLabel()).FontSize(11).Clicked() {
				cycleTheme()
			}
		})
		ui.Scroll(c).Fill().Padding(10).Gap(8).Children(func() {
			if len(ungrouped) == 0 && len(clusters) == 0 {
				ui.Text(c, "waiting for data…").FontSize(13).TextColor(t.TextMuted)
				return
			}
			for i := range ungrouped {
				serverRow(c, width, ungrouped[i])
			}
			for _, g := range clusters {
				clusterRow(c, width, g)
			}
		})
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

// clusterRow renders a cluster's header, then each member indented
// beneath it via serverRow — the same per-server rendering (and the same
// three width tiers) a standalone server gets, so drilling from "the
// cluster" to "this one member" is just reading further down, not a
// different view.
func clusterRow(c *ui.Context, width float32, g clusterGroup) {
	t := c.Theme()
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			statusDot(c, g.status, 10, clusterTooltip(g))
			ui.Text(c, g.name).FontSize(13).Bold()
			ui.Text(c, fmt.Sprintf("(%d)", len(g.members))).FontSize(11).TextColor(t.TextMuted)
		})
		ui.Column(c).Gap(6).Padding(0, 0, 0, 18).Children(func() {
			for _, m := range g.members {
				serverRow(c, width, m)
			}
		})
	})
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

func serverRow(c *ui.Context, width float32, sv state.ServerView) {
	t := c.Theme()

	switch {
	case width < breakpointMedium:
		// Compact: just a dot and the name. The name's already visible, so
		// the dot's tooltip adds the one thing that isn't: why it's that
		// color, when it isn't simply ok.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			statusDot(c, sv.Status, 10, serverTooltip(sv))
			ui.Text(c, sv.Name).FontSize(13)
		})

	case width < breakpointDetailed:
		// Medium: name, plus one small dot per metric — these have no
		// visible label at all, so the tooltip is the only way to know
		// which is which without widening the window.
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			statusDot(c, sv.Status, 10, serverTooltip(sv))
			ui.Text(c, sv.Name).FontSize(13).Width(90)
			ui.Row(c).Gap(6).Children(func() {
				for i := range sv.Metrics {
					statusDot(c, sv.Metrics[i].Status, 8, metricTooltip(sv.Metrics[i]))
				}
			})
		})

	default:
		// Detailed: name + a line per metric with its measurements.
		ui.Column(c).Gap(4).Padding(0, 0, 8, 0).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				statusDot(c, sv.Status, 12, serverTooltip(sv))
				ui.Text(c, sv.Name).FontSize(15).Bold()
			})
			for i := range sv.Metrics {
				metricLine(c, t, sv.Metrics[i])
			}
		})
	}
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

// metricTooltip is the abbreviation, plus the stale reason when there is
// one — the tooltip is where the "why is this grey" detail lives, since a
// stale metric still shows its (greyed) last-known value, not a dead end.
func metricTooltip(mv state.MetricView) string {
	abbr := shortAbbrev(mv.Name)
	if mv.Stale {
		return fmt.Sprintf("%s: stale — %s", abbr, mv.LastError)
	}
	return abbr
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

// metricLine renders one metric's measurements entirely by what each one's
// own display config says — widget choice, scale, and primary — never by
// guessing from a unit string. A measurement with no display block (the
// common case) renders as plain text, exactly as a brand-new metric
// dropped into metrics/ with no GUI changes at all should.
func metricLine(c *ui.Context, t *ui.Theme, mv state.MetricView) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(0, 0, 0, 22).Children(func() {
		statusDot(c, mv.Status, 8, metricTooltip(mv))
		ui.Text(c, mv.Name).FontSize(12).Width(60).TextColor(t.TextMuted)
		// Stale does NOT stop here: the last-known gauges/bars/values below
		// still render, just in grey (mv.Status is StatusUnknown while
		// stale, and every color in this function comes from it) — a
		// failed collection must show the last good reading, not a blank.
		// The reason why is in the dot's tooltip (metricTooltip above).
		color := statusColor(t, mv.Status)

		var primary *state.MeasurementView
		var bars []barEntry
		var dots []state.MeasurementView
		var plain []state.MeasurementView
		for i := range mv.Measurements {
			m := mv.Measurements[i]
			switch {
			case m.Primary && m.Scale != nil:
				primary = &m
			case m.Widget == metric.WidgetBar && m.Scale != nil:
				bars = append(bars, barEntry{m.DisplayLabel(), m.Value, *m.Scale, m.Zones, formatValue(m.Unit, m.Value)})
			case m.Widget == metric.WidgetDot:
				dots = append(dots, m)
			default:
				// Any bar/gauge whose Scale failed to resolve this cycle
				// also lands here (internal/app degrades those to plain
				// text on purpose), alongside anything with no display
				// block at all.
				plain = append(plain, m)
			}
		}

		if primary != nil {
			pressureGauge(c, *primary.Scale, primary.Value, primary.Zones, color, 64, percentLabel(primary.Value))
		}

		if len(bars) > 0 {
			ui.Column(c).Gap(4).Children(func() {
				for _, be := range bars {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, be.label).FontSize(11).Width(40).TextColor(t.TextMuted)
						bar(c, be, color, 90, 6)
						ui.Text(c, be.text).FontSize(11)
					})
				}
			})
		}

		if len(dots) > 0 {
			ui.Row(c).Gap(10).Children(func() {
				for _, m := range dots {
					// A dot measurement is a state, not a value — "active"
					// reads better as a colored dot + its label than as
					// "active=1". Uses the metric's own (possibly stale,
					// possibly aggregate-across-measurements) color, same
					// as every other widget on this line.
					ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
						statusDot(c, mv.Status, 8, m.DisplayLabel())
						ui.Text(c, m.DisplayLabel()).FontSize(11).TextColor(t.TextMuted)
					})
				}
			})
		}

		if len(plain) > 0 {
			ui.Row(c).Gap(10).Children(func() {
				for _, m := range plain {
					ui.Text(c, m.DisplayLabel()+"="+formatValue(m.Unit, m.Value)).FontSize(12)
				}
			})
		}
	})
}

func statusDot(c *ui.Context, status state.Status, size float32, tooltip string) {
	t := c.Theme()
	e := ui.Box(c).Size(size, size).Radius(size / 2).Background(statusColor(t, status))
	if tooltip != "" {
		e.Tooltip(tooltip)
	}
}

// formatValue is a minimal, deliberately non-exhaustive humanizer — bytes
// get GiB-scaled, everything else prints as-is. Good enough for a first
// PoC; a real unit-aware formatter belongs with the rest of the metadata
// (display hints) work, not hand-rolled in the GUI.
func formatValue(unit string, v float64) string {
	if unit == "bytes" && v >= 1<<20 {
		return fmt.Sprintf("%.1fGiB", v/(1<<30))
	}
	return fmt.Sprintf("%.2f", v)
}
