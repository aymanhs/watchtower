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
		Events: []metric.EventRule{
			{Name: "disk.used_percent.warning", Measurement: "used_percent", Condition: metric.ConditionAbove, Threshold: 80, Severity: metric.SeverityWarning},
			{Name: "disk.used_percent.critical", Measurement: "used_percent", Condition: metric.ConditionAbove, Threshold: 90, Severity: metric.SeverityCritical},
		},
	}
}

// Duration is a tiny local helper so tests can build metric.Duration
// without importing time.Duration-wrapping boilerplate at every call site.
func Duration(d time.Duration) metric.Duration { return metric.Duration{Duration: d} }

// TestMetricTask_StoresRawMeasurements confirms Run populates the store
// with exactly what the collector produced — key and value, nothing
// else: no declared Unit/Type/Display/Scale exists any more to resolve
// into a MeasurementView.
func TestMetricTask_StoresRawMeasurements(t *testing.T) {
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
	byKey := map[string]state.MeasurementView{}
	for _, m := range snap[0].Metrics[0].Measurements {
		byKey[m.Key] = m
	}
	for key, want := range map[string]float64{"total": 1000, "used": 500, "used_percent": 50} {
		got, ok := byKey[key]
		if !ok || got.Value != want {
			t.Errorf("%s = %+v, want value %v", key, got, want)
		}
	}

	// used_percent at 50% breaches nothing yet.
	if sev := task.Events.CurrentSeverity("s1", "disk.used_percent"); sev != "" {
		t.Errorf("severity = %q, want \"\" (50%% breaches nothing)", sev)
	}
}

// TestMetricTask_PerInstanceMeasurements reproduces disk.yaml's per-volume
// shape live, through the real Run path: a collector that discovers its
// own instances at runtime and emits them instance-qualified
// ("root.total"/"root.used"/..., "data.total"/"data.used"/...) — one
// declared event rule ("used_percent") still alarms each instance
// independently via metric.SplitInstance's own baseKey/instance split.
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
	if byKey["root.used"].Value != 500 || byKey["data.used"].Value != 1900 {
		t.Fatalf("got root.used=%v data.used=%v, want 500/1900", byKey["root.used"].Value, byKey["data.used"].Value)
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
