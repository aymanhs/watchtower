package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"

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
		// Compact: just a dot and the name.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			statusDot(c, sv.Status, 10)
			ui.Text(c, sv.Name).FontSize(13)
		})

	case width < breakpointDetailed:
		// Medium: name, plus one small dot per metric.
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			statusDot(c, sv.Status, 10)
			ui.Text(c, sv.Name).FontSize(13).Width(90)
			ui.Row(c).Gap(6).Children(func() {
				for i := range sv.Metrics {
					statusDot(c, sv.Metrics[i].Status, 8)
				}
			})
		})

	default:
		// Detailed: name + a line per metric with its measurements.
		ui.Column(c).Gap(4).Padding(0, 0, 8, 0).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				statusDot(c, sv.Status, 12)
				ui.Text(c, sv.Name).FontSize(15).Bold()
			})
			for i := range sv.Metrics {
				metricLine(c, t, sv.Metrics[i])
			}
		})
	}
}

func metricLine(c *ui.Context, t *ui.Theme, mv state.MetricView) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(0, 0, 0, 22).Children(func() {
		statusDot(c, mv.Status, 8)
		ui.Text(c, mv.Name).FontSize(12).Width(60).TextColor(t.TextMuted)
		if mv.Stale {
			ui.Text(c, "stale: "+mv.LastError).FontSize(12).TextColor(t.Danger)
			return
		}
		color := statusColor(t, mv.Status)

		// total scales "bytes" measurements into bars proportional to it
		// (used/free/cache/available as a share of total) — no separate
		// magic number needed, the metric already carries its own 100%
		// reference.
		var totalBytes float64
		for _, m := range mv.Measurements {
			if m.Unit == "bytes" && m.Key == "total" {
				totalBytes = m.Value
			}
		}

		// One primary percent measurement becomes the big pressure gauge;
		// "load" measurements and bytes-with-a-total each get their own
		// small bar; everything else (including total itself) stays text.
		var primaryPercent *state.MeasurementView
		var bars []barEntry
		var plain []state.MeasurementView
		for i := range mv.Measurements {
			m := mv.Measurements[i]
			switch {
			case m.Unit == "percent" && primaryPercent == nil:
				primaryPercent = &m
			case m.Unit == "load":
				bars = append(bars, barEntry{m.Key, barFraction("load", m.Value), formatValue(m.Unit, m.Value)})
			case m.Unit == "bytes" && m.Key != "total" && totalBytes > 0:
				bars = append(bars, barEntry{m.Key, clamp01(float32(m.Value / totalBytes)), formatValue(m.Unit, m.Value)})
			default:
				plain = append(plain, m)
			}
		}

		if primaryPercent != nil {
			pressureGauge(c, barFraction(primaryPercent.Unit, primaryPercent.Value), color, 64, percentLabel(primaryPercent.Value))
		}

		if len(bars) > 0 {
			ui.Column(c).Gap(4).Children(func() {
				for _, be := range bars {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, be.label).FontSize(11).Width(40).TextColor(t.TextMuted)
						bar(c, be.frac, color, 90, 6)
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

func statusDot(c *ui.Context, status state.Status, size float32) {
	t := c.Theme()
	ui.Box(c).Size(size, size).Radius(size / 2).Background(statusColor(t, status))
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
