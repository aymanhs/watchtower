package main

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/metric"
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

// severityColor is statusColor's counterpart for metric.Severity (a Zone's
// severity), which is a different type from a different package — not
// worth a conversion just to share one three-case switch.
func severityColor(t *ui.Theme, sev metric.Severity) ui.Color {
	switch sev {
	case metric.SeverityWarning:
		return t.Warning
	case metric.SeverityCritical:
		return t.Danger
	default:
		return t.Success
	}
}

// barEntry is one already-resolved small bar: whatever its measurement's
// own display config said, by the time it's a barEntry it's a value, a
// scale and (maybe) zones to draw.
type barEntry struct {
	label string
	value float64
	scale state.Scale
	zones []state.Zone
	text  string
}

// bar draws a horizontal progress indicator.
//
// With zones (same source as pressureGauge's: state.ResolveZones from the
// measurement's own event rules), the bar's face is the colored threshold
// bands themselves, with a thin marker line at the current value — the
// bar's equivalent of the gauge's dial-plus-marker, not a different idea.
//
// Without zones, it's the plain flat-track-plus-fill bar this always was.
func bar(c *ui.Context, be barEntry, fallbackColor ui.Color, w, h float32) ui.Element {
	t := c.Theme()
	rad := h / 2
	e := ui.Box(c).Width(w).Height(h).Radius(rad).Clip()
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		if len(be.zones) > 0 {
			for _, z := range be.zones {
				from, to := be.scale.Fraction(z.From), be.scale.Fraction(z.To)
				p.Fill(ui.Rect{X: r.X + r.W*from, Y: r.Y, W: r.W * (to - from), H: r.H}, severityColor(t, z.Severity), 0)
			}
			mx := r.X + r.W*be.scale.Fraction(be.value)
			p.Fill(ui.Rect{X: mx - 1, Y: r.Y, W: 2, H: r.H}, t.Text, 0)
		} else {
			p.Fill(r, t.Border, 0)
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * be.scale.Fraction(be.value), H: r.H}, fallbackColor, rad)
		}
	})
	return e
}

// Pressure-gauge sweep: starts bottom-left, goes clockwise over the top,
// ends bottom-right — the classic speedometer shape, with a gap at the
// bottom. Angles are degrees in mygo's screen-space convention (0° = East,
// increasing clockwise since Y grows downward).
const (
	gaugeStartDeg = 135.0
	gaugeSweepDeg = 270.0
)

// pressureGauge draws a dial-style arc gauge, round-capped, with
// centerLabel (typically "42%") in the middle.
//
// With zones (resolved from the measurement's own event rules —
// state.ResolveZones), the dial's face is the colored threshold bands
// themselves, and a single marker dot shows where the current value sits
// — a real instrument face, not just a progress bar bent into a circle.
//
// Without zones (a gauge-widget measurement with no alerting rules), it
// falls back to a flat muted track with a progress arc filled in
// fallbackColor — still a gauge, just one with nothing to show bands for.
func pressureGauge(c *ui.Context, scale state.Scale, value float64, zones []state.Zone, fallbackColor ui.Color, diameter float32, centerLabel string) ui.Element {
	t := c.Theme()
	stroke := diameter * 0.15
	frac := scale.Fraction(value)

	e := ui.Box(c).Size(diameter, diameter).AlignItems(ui.Center).Justify(ui.Center)
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		radius := r.W/2 - stroke/2

		if len(zones) > 0 {
			for _, z := range zones {
				from, to := scale.Fraction(z.From), scale.Fraction(z.To)
				band := buildArc(cx, cy, radius, gaugeStartDeg+gaugeSweepDeg*float64(from), gaugeSweepDeg*float64(to-from))
				p.StrokePath(band, stroke, severityColor(t, z.Severity))
			}
			markerDeg := gaugeStartDeg + gaugeSweepDeg*float64(frac)
			mx := cx + radius*float32(math.Cos(markerDeg*math.Pi/180))
			my := cy + radius*float32(math.Sin(markerDeg*math.Pi/180))
			marker := new(ui.Path)
			marker.Circle(mx, my, stroke*0.5)
			p.FillPath(marker, t.Text)
		} else {
			track := buildArc(cx, cy, radius, gaugeStartDeg, gaugeSweepDeg)
			track.Cap(ui.RoundCap)
			p.StrokePath(track, stroke, t.Border)
			if frac > 0 {
				progress := buildArc(cx, cy, radius, gaugeStartDeg, gaugeSweepDeg*float64(frac))
				progress.Cap(ui.RoundCap)
				p.StrokePath(progress, stroke, fallbackColor)
			}
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
