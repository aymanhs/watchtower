package metric

import (
	"testing"
	"time"
)

func baseDefForDisplay() *Definition {
	return &Definition{
		Name:     "disk",
		Groups:   []string{"basic"},
		Interval: Duration{5 * time.Second},
		Timeout:  Duration{2 * time.Second},
		Collector: Collector{
			Type:    CollectorShell,
			Command: "echo used_percent=1",
		},
		Metrics: map[string]Measurement{
			"total":        {Unit: "bytes", Type: TypeGauge},
			"used":         {Unit: "bytes", Type: TypeGauge},
			"used_percent": {Unit: "percent", Type: TypeGauge},
		},
	}
}

func TestDisplay_NilIsAlwaysValid(t *testing.T) {
	d := baseDefForDisplay()
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate with no display blocks: %v", err)
	}
}

func TestDisplay_TextAndDotNeedNoScale(t *testing.T) {
	for _, w := range []Widget{WidgetText, WidgetDot} {
		d := baseDefForDisplay()
		m := d.Metrics["total"]
		m.Display = &Display{Widget: w}
		d.Metrics["total"] = m
		if err := d.Validate(); err != nil {
			t.Errorf("widget %q should need no scale: %v", w, err)
		}
	}
}

func TestDisplay_BarAndGaugeRequireScale(t *testing.T) {
	for _, w := range []Widget{WidgetBar, WidgetGauge} {
		d := baseDefForDisplay()
		m := d.Metrics["used_percent"]
		m.Display = &Display{Widget: w} // no Scale
		d.Metrics["used_percent"] = m
		if err := d.Validate(); err == nil {
			t.Errorf("widget %q with no scale should be rejected", w)
		}
	}
}

func TestDisplay_ScaleWithLiteralMax(t *testing.T) {
	d := baseDefForDisplay()
	m := d.Metrics["used_percent"]
	m.Display = &Display{Widget: WidgetGauge, Scale: &Scale{Min: 0, Max: 100}}
	d.Metrics["used_percent"] = m
	if err := d.Validate(); err != nil {
		t.Fatalf("literal min/max scale should be valid: %v", err)
	}
}

func TestDisplay_ScaleMaxRefMustExist(t *testing.T) {
	d := baseDefForDisplay()
	m := d.Metrics["used"]
	m.Display = &Display{Widget: WidgetBar, Scale: &Scale{MaxRef: "nonexistent"}}
	d.Metrics["used"] = m
	if err := d.Validate(); err == nil {
		t.Fatal("max_ref referencing an unknown measurement should be rejected")
	}
}

func TestDisplay_ScaleMaxRefToRealSibling(t *testing.T) {
	d := baseDefForDisplay()
	m := d.Metrics["used"]
	m.Display = &Display{Widget: WidgetBar, Scale: &Scale{MaxRef: "total"}}
	d.Metrics["used"] = m
	if err := d.Validate(); err != nil {
		t.Fatalf("max_ref to a real sibling should be valid: %v", err)
	}
}

func TestDisplay_ScaleMinEqualsMaxRejected(t *testing.T) {
	d := baseDefForDisplay()
	m := d.Metrics["used_percent"]
	m.Display = &Display{Widget: WidgetGauge, Scale: &Scale{Min: 5, Max: 5}}
	d.Metrics["used_percent"] = m
	if err := d.Validate(); err == nil {
		t.Fatal("scale.max == scale.min should be rejected")
	}
}

func TestDisplay_UnknownWidgetRejected(t *testing.T) {
	d := baseDefForDisplay()
	m := d.Metrics["total"]
	m.Display = &Display{Widget: "sparkline"}
	d.Metrics["total"] = m
	if err := d.Validate(); err == nil {
		t.Fatal("unknown widget should be rejected")
	}
}

func TestDisplay_AtMostOnePrimary(t *testing.T) {
	d := baseDefForDisplay()
	used := d.Metrics["used"]
	used.Display = &Display{Widget: WidgetBar, Primary: true, Scale: &Scale{MaxRef: "total"}}
	d.Metrics["used"] = used
	usedPct := d.Metrics["used_percent"]
	usedPct.Display = &Display{Widget: WidgetGauge, Primary: true, Scale: &Scale{Min: 0, Max: 100}}
	d.Metrics["used_percent"] = usedPct

	if err := d.Validate(); err == nil {
		t.Fatal("two measurements marked primary should be rejected")
	}
}
