package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/state"
)

// fakeRunner returns canned output for collect.Execute, same pattern as
// internal/collect's own tests — no real SSH needed.
type fakeRunner struct {
	out string
	err error
}

func (f fakeRunner) Run(ctx context.Context, command string, timeout time.Duration) (string, error) {
	return f.out, f.err
}

func diskDef() *metric.Definition {
	return &metric.Definition{
		Name:     "disk",
		Groups:   []string{"basic"},
		Interval: Duration(10 * time.Second),
		Timeout:  Duration(5 * time.Second),
		Collector: metric.Collector{
			Type:    metric.CollectorShell,
			Command: "irrelevant",
		},
		Metrics: map[string]metric.Measurement{
			"total": {
				Unit: "bytes", Type: metric.TypeGauge,
			},
			"used": {
				Unit: "bytes", Type: metric.TypeGauge,
				Display: &metric.Display{Widget: metric.WidgetBar, Scale: &metric.Scale{MaxRef: "total"}},
			},
			"used_percent": {
				Unit: "percent", Type: metric.TypeGauge,
				Display: &metric.Display{Widget: metric.WidgetGauge, Primary: true, Scale: &metric.Scale{Min: 0, Max: 100}},
			},
		},
		Events: []metric.EventRule{
			{Name: "disk.used_percent.warning", Measurement: "used_percent", Condition: metric.ConditionAbove, Threshold: 80, Severity: metric.SeverityWarning},
			{Name: "disk.used_percent.critical", Measurement: "used_percent", Condition: metric.ConditionAbove, Threshold: 90, Severity: metric.SeverityCritical},
			// A rule on "used" itself (a bar, not the primary gauge) so
			// TestMetricTask_ResolvesDisplayIntoStore can confirm bars get
			// zones too, not just gauges.
			{Name: "disk.used.warning", Measurement: "used", Condition: metric.ConditionAbove, Threshold: 800, Severity: metric.SeverityWarning},
		},
	}
}

// Duration is a tiny local helper so tests can build metric.Duration
// without importing time.Duration-wrapping boilerplate at every call site.
func Duration(d time.Duration) metric.Duration { return metric.Duration{Duration: d} }

func TestMetricTask_ResolvesDisplayIntoStore(t *testing.T) {
	store := state.NewStore()
	task := &MetricTask{
		ServerName:      "s1",
		Def:             diskDef(),
		Client:          fakeRunner{out: "total=1000\nused=500\nused_percent=50\n"},
		CollectInterval: 10 * time.Second,
		Deadline:        5 * time.Second,
		Events:          event.NewEngine(0),
		Store:           store,
	}

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	snap := store.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("got %d servers, want 1", len(snap))
	}
	mv := snap[0].Metrics[0]
	byKey := map[string]state.MeasurementView{}
	for _, m := range mv.Measurements {
		byKey[m.Key] = m
	}

	total := byKey["total"]
	if total.Widget != "" || total.Scale != nil {
		t.Errorf("total (no display block) = %+v, want zero-value widget/scale", total)
	}

	used := byKey["used"]
	if used.Widget != metric.WidgetBar {
		t.Errorf("used.Widget = %q, want bar", used.Widget)
	}
	if used.Scale == nil || used.Scale.Max != 1000 {
		t.Fatalf("used.Scale = %+v, want max_ref 'total' resolved to 1000", used.Scale)
	}
	if frac := used.Scale.Fraction(used.Value); frac != 0.5 {
		t.Errorf("used fraction = %v, want 0.5 (500/1000)", frac)
	}
	wantUsedZones := []state.Zone{
		{From: 0, To: 800, Severity: ""},
		{From: 800, To: 1000, Severity: metric.SeverityWarning},
	}
	if len(used.Zones) != len(wantUsedZones) {
		t.Fatalf("used.Zones = %+v, want %+v (bars get zones too, not just gauges)", used.Zones, wantUsedZones)
	}
	for i := range wantUsedZones {
		if used.Zones[i] != wantUsedZones[i] {
			t.Errorf("used.Zones[%d] = %+v, want %+v", i, used.Zones[i], wantUsedZones[i])
		}
	}

	pct := byKey["used_percent"]
	if !pct.Primary {
		t.Error("used_percent should be Primary")
	}
	if pct.Widget != metric.WidgetGauge {
		t.Errorf("used_percent.Widget = %q, want gauge", pct.Widget)
	}
	wantZones := []state.Zone{
		{From: 0, To: 80, Severity: ""},
		{From: 80, To: 90, Severity: metric.SeverityWarning},
		{From: 90, To: 100, Severity: metric.SeverityCritical},
	}
	if len(pct.Zones) != len(wantZones) {
		t.Fatalf("got %d zones, want %d: %+v", len(pct.Zones), len(wantZones), pct.Zones)
	}
	for i := range wantZones {
		if pct.Zones[i] != wantZones[i] {
			t.Errorf("zone[%d] = %+v, want %+v", i, pct.Zones[i], wantZones[i])
		}
	}
}

func TestMetricTask_MaxRefMissingDegradesToTextNotFailure(t *testing.T) {
	store := state.NewStore()
	def := diskDef()
	task := &MetricTask{
		ServerName:      "s1",
		Def:             def,
		Client:          fakeRunner{out: "used=500\nused_percent=50\n"}, // "total" missing this cycle
		CollectInterval: 10 * time.Second,
		Deadline:        5 * time.Second,
		Events:          event.NewEngine(0),
		Store:           store,
	}

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run should not fail just because one measurement's max_ref sibling is absent: %v", err)
	}

	snap := store.Snapshot()
	var used state.MeasurementView
	for _, m := range snap[0].Metrics[0].Measurements {
		if m.Key == "used" {
			used = m
		}
	}
	if used.Scale != nil {
		t.Errorf("used.Scale = %+v, want nil (max_ref sibling absent this cycle)", used.Scale)
	}
}

func TestMetricTask_CollectorErrorStillMarksStale(t *testing.T) {
	store := state.NewStore()
	task := &MetricTask{
		ServerName:      "s1",
		Def:             diskDef(),
		Client:          fakeRunner{err: errors.New("boom")},
		CollectInterval: 10 * time.Second,
		Deadline:        5 * time.Second,
		Events:          event.NewEngine(0),
		Store:           store,
	}

	if err := task.Run(context.Background()); err == nil {
		t.Fatal("expected Run to return the collector error")
	}

	snap := store.Snapshot()
	if snap[0].Metrics[0].Status != state.StatusUnknown {
		t.Fatalf("status = %v, want unknown (stale)", snap[0].Metrics[0].Status)
	}
}
