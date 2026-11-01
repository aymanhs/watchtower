package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/state"
)

// statusColor maps our own event-engine-driven status to a theme color.
// Deliberately not delegated to ui.Meter's built-in level coloring: that
// re-derives color from a static threshold with no hysteresis memory,
// while mv.Status already reflects the event engine's (correct, debounced)
// current severity.
func statusColor(t *ui.Theme, status state.Status) ui.Color {
	switch status {
	case state.StatusOK:
		return t.Success
	case state.StatusWarning:
		return t.Warning
	case state.StatusCritical:
		return t.Danger
	default: // StatusUnknown: stale/unreachable
		return t.TextMuted
	}
}

// clampFraction maps a 0-100 percent value to a 0-1 draw fraction, clamped
// so an out-of-range reading never overflows or underflows the widget.
func clampFraction(percent float64) float32 {
	f := float32(percent / 100)
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}

// bar draws a horizontal progress bar: a track in the theme's muted
// border color, filled from the left by frac in color. Same geometry as
// ui.Meter, just driven by our own status color instead of Meter's static
// threshold levels.
func bar(c *ui.Context, frac float32, color ui.Color, w, h float32) ui.Element {
	t := c.Theme()
	rad := h / 2
	e := ui.Box(c).Width(w).Height(h).Radius(rad).Background(t.Border).Clip()
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * frac, H: r.H}, color, rad)
	})
	return e
}

// circularGauge draws a ring progress indicator: a full muted-color
// circle as the track, with an arc of frac's length drawn over it in
// color, round-capped. centerLabel prints in the middle (typically the
// percentage, e.g. "42%").
func circularGauge(c *ui.Context, frac float32, color ui.Color, diameter float32, centerLabel string) ui.Element {
	t := c.Theme()
	stroke := diameter * 0.14
	e := ui.Box(c).Size(diameter, diameter).AlignItems(ui.Center).Justify(ui.Center)
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		radius := r.W/2 - stroke/2

		track := new(ui.Path)
		track.Circle(cx, cy, radius)
		p.StrokePath(track, stroke, t.Border)

		if frac > 0 {
			progress := new(ui.Path)
			progress.Circle(cx, cy, radius).Trim(0, frac).Cap(ui.RoundCap)
			p.StrokePath(progress, stroke, color)
		}
	})
	e.Children(func() {
		ui.Text(c, centerLabel).FontSize(diameter * 0.26).Bold().TextColor(t.Text)
	})
	return e
}

// percentLabel formats a percent-unit value for a gauge's center label.
func percentLabel(v float64) string {
	return fmt.Sprintf("%.0f%%", v)
}
