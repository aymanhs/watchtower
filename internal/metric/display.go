package metric

import "fmt"

// Widget is the GUI's preferred visual shape for a measurement when there's
// room to show it richly. It is purely declarative: the engine and this
// package never interpret it, only validate it — the GUI decides how
// (size, placement) based on its own window-size tiers, but never whether.
type Widget string

const (
	// WidgetText is the default: a plain "key=value" line. Always safe,
	// requires no Scale, and is what a measurement with no Display block
	// at all renders as — a new metric needs no GUI change to show up.
	WidgetText Widget = "text"
	// WidgetDot is a small colored status dot, no value text.
	WidgetDot Widget = "dot"
	// WidgetBar is a horizontal proportional bar; requires Scale.
	WidgetBar Widget = "bar"
	// WidgetGauge is a dial-style arc gauge; requires Scale. Only one
	// measurement per Definition may set Display.Primary with this (or
	// any) widget — Primary marks the metric's one headline widget.
	WidgetGauge Widget = "gauge"
)

func (w Widget) valid() bool {
	switch w {
	case "", WidgetText, WidgetDot, WidgetBar, WidgetGauge:
		return true
	default:
		return false
	}
}

// needsScale reports whether this widget requires a Scale to render (a
// bar or gauge has to know what "full" means; a dot or text doesn't).
func (w Widget) needsScale() bool {
	return w == WidgetBar || w == WidgetGauge
}

// Scale defines the 0%-to-100% range a Bar or Gauge widget draws against.
// Set Max directly for a fixed reference (e.g. a load average's "fully
// loaded" point), or MaxRef to use another measurement in the same
// Definition's current value instead (e.g. "used" scaled against
// "total") — resolved fresh each collection cycle, never a magic number.
type Scale struct {
	Min    float64 `yaml:"min,omitempty"`
	Max    float64 `yaml:"max,omitempty"`
	MaxRef string  `yaml:"max_ref,omitempty"`
}

// Display is one measurement's presentation hint. Nil (the common case)
// means WidgetText with no further configuration — the brief's "adding a
// metric shouldn't need GUI code changes" applies equally to not needing
// a Display block at all.
type Display struct {
	Widget  Widget `yaml:"widget,omitempty"`
	Primary bool   `yaml:"primary,omitempty"`
	Scale   *Scale `yaml:"scale,omitempty"`
}

// validateDisplay checks one measurement's Display block, if present,
// against the rest of the Definition it belongs to (for MaxRef lookups).
func (d *Definition) validateDisplay(key string, m Measurement) error {
	if m.Display == nil {
		return nil
	}
	disp := m.Display
	if !disp.Widget.valid() {
		return fmt.Errorf("metric %q: measurement %q has unknown display.widget %q", d.Name, key, disp.Widget)
	}
	if disp.Widget.needsScale() {
		if disp.Scale == nil {
			return fmt.Errorf("metric %q: measurement %q: display.widget %q requires a scale", d.Name, key, disp.Widget)
		}
		if disp.Scale.MaxRef != "" {
			if _, ok := d.Metrics[disp.Scale.MaxRef]; !ok {
				return fmt.Errorf("metric %q: measurement %q: display.scale.max_ref references unknown measurement %q", d.Name, key, disp.Scale.MaxRef)
			}
		} else if disp.Scale.Max == disp.Scale.Min {
			return fmt.Errorf("metric %q: measurement %q: display.scale.max must differ from min", d.Name, key)
		}
	}
	return nil
}
