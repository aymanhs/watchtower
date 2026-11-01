package main

import (
	"fmt"
	"math"

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

// clamp01 keeps a draw fraction within [0,1] so an out-of-range reading
// never overflows or underflows a bar or gauge.
func clamp01(f float32) float32 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}

// barFraction maps a raw measurement to a 0-1 draw fraction, by unit.
// Percent is the natural 0-100 scale. Load averages have no fixed max —
// 4.0 is a reasonable "fully loaded" reference for a small box (and
// happens to match this library's own cpu.load1.warning threshold); a
// nicer version would size this per-server from core count, not hardcode
// it here.
func barFraction(unit string, value float64) float32 {
	switch unit {
	case "percent":
		return clamp01(float32(value / 100))
	case "load":
		return clamp01(float32(value / 4))
	default:
		return 0
	}
}

// barEntry is one already-resolved small bar: whatever scale its
// measurement used (percent, load, or a share of some total), by the time
// it's a barEntry it's just a fraction and a label.
type barEntry struct {
	label string
	frac  float32
	text  string
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

// (circularGauge, the earlier full-ring design, was replaced by
// pressureGauge below — a dial reads more like a gauge than a donut.)

// Pressure-gauge sweep: starts bottom-left, goes clockwise over the top,
// ends bottom-right — the classic speedometer shape, with a gap at the
// bottom. Angles are degrees in mygo's screen-space convention (0° = East,
// increasing clockwise since Y grows downward).
const (
	gaugeStartDeg = 135.0
	gaugeSweepDeg = 270.0
)

// pressureGauge draws a dial-style arc gauge: a full-sweep track in the
// theme's muted border color, and a progress arc of frac's share of the
// sweep in color, round-capped, with centerLabel (typically "42%") in the
// middle. mygo's Path only offers a full circle as a primitive, so the arc
// itself is hand-built from cubic Beziers (buildArc) rather than trimmed
// from one.
func pressureGauge(c *ui.Context, frac float32, color ui.Color, diameter float32, centerLabel string) ui.Element {
	t := c.Theme()
	stroke := diameter * 0.15
	e := ui.Box(c).Size(diameter, diameter).AlignItems(ui.Center).Justify(ui.Center)
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		radius := r.W/2 - stroke/2

		track := buildArc(cx, cy, radius, gaugeStartDeg, gaugeSweepDeg)
		track.Cap(ui.RoundCap)
		p.StrokePath(track, stroke, t.Border)

		if frac > 0 {
			progress := buildArc(cx, cy, radius, gaugeStartDeg, gaugeSweepDeg*float64(frac))
			progress.Cap(ui.RoundCap)
			p.StrokePath(progress, stroke, color)
		}
	})
	e.Children(func() {
		ui.Text(c, centerLabel).FontSize(diameter * 0.22).Bold().TextColor(t.Text)
	})
	return e
}

// buildArc approximates a circular arc from startDeg, sweeping sweepDeg
// clockwise, as cubic Bezier segments no wider than 45° each (the same
// per-segment accuracy mygo's own Circle uses for a full 360° circle,
// via the standard kappa = 4/3*tan(segment/4) construction).
func buildArc(cx, cy, r float32, startDeg, sweepDeg float64) *ui.Path {
	path := new(ui.Path)
	if sweepDeg == 0 {
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
	path.MoveTo(x0, y0)

	for i := 0; i < segments; i++ {
		theta2 := theta + segRad
		x3 := cx + r*float32(math.Cos(theta2))
		y3 := cy + r*float32(math.Sin(theta2))
		t1x, t1y := -float32(math.Sin(theta)), float32(math.Cos(theta))
		t2x, t2y := -float32(math.Sin(theta2)), float32(math.Cos(theta2))
		path.CubeTo(x0+alpha*r*t1x, y0+alpha*r*t1y, x3-alpha*r*t2x, y3-alpha*r*t2y, x3, y3)
		x0, y0, theta = x3, y3, theta2
	}
	return path
}

// percentLabel formats a percent-unit value for a gauge's center label.
func percentLabel(v float64) string {
	return fmt.Sprintf("%.0f%%", v)
}
