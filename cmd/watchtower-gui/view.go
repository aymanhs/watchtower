package main

import (
	"fmt"
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
	servers := store.Snapshot()

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Padding(8, 10, 0, 10).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Watchtower").FontSize(12).TextColor(t.TextMuted)
			if ui.Button(c.Key("theme"), themeLabel()).FontSize(11).Clicked() {
				cycleTheme()
			}
		})
		ui.Scroll(c).Fill().Padding(10).Gap(8).Children(func() {
			if len(servers) == 0 {
				ui.Text(c, "waiting for data…").FontSize(13).TextColor(t.TextMuted)
				return
			}
			for i := range servers {
				serverRow(c, width, servers[i])
			}
		})
	})
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
		var plain []state.MeasurementView
		for i := range mv.Measurements {
			m := mv.Measurements[i]
			switch {
			case m.Primary && m.Scale != nil:
				primary = &m
			case m.Widget == metric.WidgetBar && m.Scale != nil:
				bars = append(bars, barEntry{m.Key, m.Value, *m.Scale, m.Zones, formatValue(m.Unit, m.Value)})
			default:
				// Includes WidgetDot (not yet rendered inline here — a
				// metric line already has its own status dot) and any
				// bar/gauge whose Scale failed to resolve this cycle
				// (internal/app degrades those to plain text on purpose).
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

		if len(plain) > 0 {
			ui.Row(c).Gap(10).Children(func() {
				for _, m := range plain {
					ui.Text(c, m.Key+"="+formatValue(m.Unit, m.Value)).FontSize(12)
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
