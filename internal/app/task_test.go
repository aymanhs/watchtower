package app

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
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

// DialContext is never exercised by these shell-collector tests; it only
// exists so fakeRunner satisfies collect.Backend.
func (f fakeRunner) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return nil, errors.New("fakeRunner: DialContext not implemented")
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
				Display: &metric.Display{Widget: metric.WidgetGauge, Primary: true, Scale: &metric.Scale{Min: 0, Max: 100}, Label: "Disk Usage"},
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
	if pct.DisplayLabel() != "Disk Usage" {
		t.Errorf("used_percent.DisplayLabel() = %q, want %q (from display.label)", pct.DisplayLabel(), "Disk Usage")
	}
	if total.DisplayLabel() != "total" {
		t.Errorf("total.DisplayLabel() = %q, want %q (falls back to Key with no label)", total.DisplayLabel(), "total")
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

// TestMetricTask_PerInstanceMeasurements reproduces disk.yaml's per-volume
// shape live, through the real Run path: a collector that discovers its
// own instances at runtime and emits them instance-qualified
// ("root.total"/"root.used"/..., "data.total"/"data.used"/...) against
// one statically-declared "total"/"used"/"used_percent" schema.
func TestMetricTask_PerInstanceMeasurements(t *testing.T) {
	store := state.NewStore()
	task := &MetricTask{
		ServerName: "s1",
		Def:        diskDef(),
		Client: fakeRunner{out: "root.total=1000\nroot.used=500\nroot.used_percent=50\n" +
			"data.total=2000\ndata.used=1900\ndata.used_percent=95\n"},
		CollectInterval: 10 * time.Second,
		Deadline:        5 * time.Second,
		Events:          event.NewEngine(0),
		Store:           store,
	}

	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	snap := store.Snapshot()
	byKey := map[string]state.MeasurementView{}
	for _, m := range snap[0].Metrics[0].Measurements {
		byKey[m.Key] = m
	}

	rootUsed := byKey["root.used"]
	if rootUsed.Scale == nil || rootUsed.Scale.Max != 1000 {
		t.Fatalf("root.used.Scale = %+v, want max_ref 'total' resolved against root's own total (1000), not data's (2000)", rootUsed.Scale)
	}
	dataUsed := byKey["data.used"]
	if dataUsed.Scale == nil || dataUsed.Scale.Max != 2000 {
		t.Fatalf("data.used.Scale = %+v, want max_ref 'total' resolved against data's own total (2000)", dataUsed.Scale)
	}

	rootPct := byKey["root.used_percent"]
	if rootPct.DisplayLabel() != "Disk Usage (root)" {
		t.Errorf("root.used_percent.DisplayLabel() = %q, want %q (instance-annotated)", rootPct.DisplayLabel(), "Disk Usage (root)")
	}
	dataPct := byKey["data.used_percent"]
	if dataPct.DisplayLabel() != "Disk Usage (data)" {
		t.Errorf("data.used_percent.DisplayLabel() = %q, want %q", dataPct.DisplayLabel(), "Disk Usage (data)")
	}

	// root at 50% and data at 95% must alarm independently — data alone
	// crosses both thresholds, root crosses neither.
	if sev := task.Events.CurrentSeverity("s1", "disk.root.used_percent"); sev != "" {
		t.Errorf("root severity = %q, want \"\" (50%% breaches nothing)", sev)
	}
	if sev := task.Events.CurrentSeverity("s1", "disk.data.used_percent"); sev != metric.SeverityCritical {
		t.Errorf("data severity = %q, want critical (95%% breaches the shared used_percent rule)", sev)
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

func TestMetricTask_WritesHistory(t *testing.T) {
	hist, err := storage.Open(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer hist.Close()

	task := &MetricTask{
		ServerName:      "s1",
		Def:             diskDef(),
		Client:          fakeRunner{out: "total=1000\nused=500\nused_percent=50\n"},
		CollectInterval: 10 * time.Second,
		Deadline:        5 * time.Second,
		Events:          event.NewEngine(0),
		History:         hist,
	}
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	now := time.Now()
	points, err := hist.Query("s1/disk.used_percent", now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 1 || points[0].Value != 50 {
		t.Fatalf("got %+v, want one point valued 50", points)
	}
}

func TestMetricTask_HistoryNilIsFine(t *testing.T) {
	// History is optional (nil for the console binary today) — Run must
	// not panic or error just because there's nowhere to persist to.
	task := &MetricTask{
		ServerName:      "s1",
		Def:             diskDef(),
		Client:          fakeRunner{out: "total=1000\nused=500\nused_percent=50\n"},
		CollectInterval: 10 * time.Second,
		Deadline:        5 * time.Second,
		Events:          event.NewEngine(0),
		History:         nil,
	}
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("Run with nil History: %v", err)
	}
}
