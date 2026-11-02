package metric

import "testing"

func TestDefinition_ResolveMeasurement(t *testing.T) {
	def := &Definition{
		Name: "disk",
		Metrics: map[string]Measurement{
			"total":        {Unit: "bytes", Type: TypeGauge},
			"used":         {Unit: "bytes", Type: TypeGauge, Display: &Display{Widget: WidgetBar, Scale: &Scale{MaxRef: "total"}}},
			"used_percent": {Unit: "percent", Type: TypeGauge},
		},
	}

	m, baseKey, instance, ok := def.ResolveMeasurement("used")
	if !ok || baseKey != "used" || instance != "" {
		t.Errorf("static case: got m=%+v baseKey=%q instance=%q ok=%v, want baseKey=used instance=\"\" ok=true", m, baseKey, instance, ok)
	}

	m, baseKey, instance, ok = def.ResolveMeasurement("root_used")
	if !ok || baseKey != "used" || instance != "root" {
		t.Errorf("instance case: got baseKey=%q instance=%q ok=%v, want baseKey=used instance=root ok=true", baseKey, instance, ok)
	}
	if m.Display == nil || m.Display.Scale.MaxRef != "total" {
		t.Errorf("instance case didn't resolve the declared Measurement: %+v", m)
	}

	// used_percent's own suffix must not be confused with "used"'s —
	// HasSuffix("root_used_percent", "_used") would be a false positive
	// if ResolveMeasurement checked suffixes naively.
	_, baseKey, instance, ok = def.ResolveMeasurement("root_used_percent")
	if !ok || baseKey != "used_percent" || instance != "root" {
		t.Errorf("got baseKey=%q instance=%q ok=%v, want baseKey=used_percent instance=root ok=true", baseKey, instance, ok)
	}

	if _, _, _, ok := def.ResolveMeasurement("nonexistent"); ok {
		t.Error("resolved a measurement that was never declared, directly or by instance suffix")
	}
}
