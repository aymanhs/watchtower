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

	m, baseKey, instance, ok = def.ResolveMeasurement("root.used")
	if !ok || baseKey != "used" || instance != "root" {
		t.Errorf("instance case: got baseKey=%q instance=%q ok=%v, want baseKey=used instance=root ok=true", baseKey, instance, ok)
	}
	if m.Display == nil || m.Display.Scale.MaxRef != "total" {
		t.Errorf("instance case didn't resolve the declared Measurement: %+v", m)
	}

	// used_percent's own key must not be confused with "used"'s — only
	// the *last* "." is the instance/baseKey split, so an instance name
	// containing a literal segment that looks like another declared key
	// (unlikely in practice, but this is what the split rule promises)
	// still resolves against the real last segment.
	_, baseKey, instance, ok = def.ResolveMeasurement("root.used_percent")
	if !ok || baseKey != "used_percent" || instance != "root" {
		t.Errorf("got baseKey=%q instance=%q ok=%v, want baseKey=used_percent instance=root ok=true", baseKey, instance, ok)
	}

	// An instance name itself containing a "." (e.g. a sanitizer letting
	// one through) still resolves correctly: the split is always at the
	// *last* dot, however many others come before it.
	_, baseKey, instance, ok = def.ResolveMeasurement("mnt.v1.used")
	if !ok || baseKey != "used" || instance != "mnt.v1" {
		t.Errorf("got baseKey=%q instance=%q ok=%v, want baseKey=used instance=mnt.v1 ok=true", baseKey, instance, ok)
	}

	if _, _, _, ok := def.ResolveMeasurement("nonexistent"); ok {
		t.Error("resolved a measurement that was never declared, directly or by instance suffix")
	}
	if _, _, _, ok := def.ResolveMeasurement("root.nonexistent"); ok {
		t.Error("resolved an instance-qualified name whose base key was never declared")
	}
}
