package main

import (
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

// Pressure-gauge sweep: starts bottom-left, goes clockwise over the top,
// ends bottom-right — the classic speedometer shape, with a gap at the
// bottom. Angles are degrees in mygo's screen-space convention (0° = East,
// increasing clockwise since Y grows downward).
const (
	gaugeStartDeg = 135.0
	gaugeSweepDeg = 270.0
)

// pressureGauge draws a dial-style arc gauge, round-capped: a flat muted
// track with a progress arc filled in fallbackColor (typically the
// measurement's current event status color — see statusColor), and
// centerLabel (typically "42%") in the middle. There's no per-measurement
// Scale/event-rule-derived zone banding any more — internal/metric
// declares neither any more (see its package doc) — so this is the only
// style a gauge ever draws now; it used to be the "no zones" fallback
// for a measurement with no alerting rules, now it's simply what a gauge
// looks like.
func pressureGauge(c *ui.Context, scale state.Scale, value float64, fallbackColor ui.Color, diameter float32, centerLabel string) ui.Element {
	t := c.Theme()
	stroke := diameter * 0.15
	frac := scale.Fraction(value)

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
			p.StrokePath(progress, stroke, fallbackColor)
		}
	})
	e.Children(func() {
		ui.Text(c, centerLabel).FontSize(centerLabelFontSize(diameter, centerLabel)).Bold().TextColor(t.Text)
	})
	return e
}

// centerLabelFontSize picks a size that fits label inside the gauge's
// own inner circle, not just diameter*0.22 — that constant reads fine
// for the "42%"-shaped labels pressureGauge was first built around, but
// a longer one (a dashboard gauge widget's own formatted value, e.g.
// "23.6GiB", or a custom display.format string) badly overflows and
// overlaps the ring at the same constant. Caught live twice: the first
// fix (an 0.8-of-diameter, 0.55em/char estimate) still wasn't aggressive
// enough — it measured against the full diameter, not the ring's own
// inner diameter (diameter minus the stroke on both sides, where the
// text actually has to fit), and underestimated how wide a bold digit
// actually renders. This version fits against the inner circle, with a
// tighter per-character estimate tuned for Bold().
func centerLabelFontSize(diameter float32, label string) float32 {
	n := float32(len([]rune(label)))
	if n <= 3 {
		return diameter * 0.22
	}
	inner := diameter * 0.7 // stroke is diameter*0.15 on each side (pressureGauge)
	size := (inner * 0.75) / (n * 0.62)
	return max(size, 9)
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
