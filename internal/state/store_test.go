package state

import (
	"testing"
	"time"

	"aymanhs/watchtower/internal/metric"
)

func TestStore_SetCluster(t *testing.T) {
	s := NewStore()
	now := time.Now()

	// Set before any metric update: must not be lost, and must not create
	// a phantom metric.
	s.SetCluster("nats1", "connetic-nats")
	s.UpdateSuccess("nats1", "nats", nil, map[string]metric.Severity{}, now)

	snap := s.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("got %d servers, want 1", len(snap))
	}
	if snap[0].Cluster != "connetic-nats" {
		t.Fatalf("Cluster = %q, want connetic-nats", snap[0].Cluster)
	}

	// A server nobody ever called SetCluster for has an empty Cluster,
	// not an error.
	s.UpdateSuccess("plain-server", "cpu", nil, map[string]metric.Severity{}, now)
	snap = s.Snapshot()
	for _, sv := range snap {
		if sv.Name == "plain-server" && sv.Cluster != "" {
			t.Errorf("plain-server.Cluster = %q, want empty", sv.Cluster)
		}
	}
}

func TestWorst(t *testing.T) {
	if Worst(StatusOK, StatusCritical) != StatusCritical {
		t.Error("Worst(ok, critical) should be critical")
	}
	if Worst(StatusWarning, StatusUnknown) != StatusWarning {
		t.Error("Worst(warning, unknown) should be warning — critical/warning outrank unknown")
	}
}

func TestStore_EmptySnapshot(t *testing.T) {
	s := NewStore()
	if got := s.Snapshot(); len(got) != 0 {
		t.Fatalf("got %d servers, want 0", len(got))
	}
}

func TestStore_UpdateSuccess_BuildsServerAndMetricView(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.UpdateSuccess("s1", "memory",
		[]MeasurementView{{Key: "available", Value: 3e10, Unit: "bytes"}, {Key: "used", Value: 2e9, Unit: "bytes"}},
		map[string]metric.Severity{"available": ""}, now)

	snap := s.Snapshot()
	if len(snap) != 1 || snap[0].Name != "s1" {
		t.Fatalf("got %+v, want one server named s1", snap)
	}
	if snap[0].Status != StatusOK {
		t.Fatalf("server status = %v, want ok", snap[0].Status)
	}
	if len(snap[0].Metrics) != 1 || snap[0].Metrics[0].Name != "memory" {
		t.Fatalf("got %+v, want one metric named memory", snap[0].Metrics)
	}
	mv := snap[0].Metrics[0]
	if mv.Stale {
		t.Fatal("fresh update should not be stale")
	}
	if len(mv.Measurements) != 2 || mv.Measurements[0].Key != "available" {
		t.Fatalf("measurements = %+v, want sorted [available used]", mv.Measurements)
	}
}

func TestStore_SeverityDrivesStatus(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.UpdateSuccess("s1", "memory", nil, map[string]metric.Severity{"available": metric.SeverityCritical}, now)

	snap := s.Snapshot()
	if snap[0].Metrics[0].Status != StatusCritical {
		t.Fatalf("metric status = %v, want critical", snap[0].Metrics[0].Status)
	}
	if snap[0].Status != StatusCritical {
		t.Fatalf("server status = %v, want critical (worst of its metrics)", snap[0].Status)
	}
}

func TestStore_MarkStale_PreservesLastKnownMeasurements(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.UpdateSuccess("s1", "cpu",
		[]MeasurementView{{Key: "load1", Value: 0.5, Unit: "load"}},
		map[string]metric.Severity{"load1": ""}, now)

	s.MarkStale("s1", "cpu", "ssh: timed out", now.Add(time.Second))

	snap := s.Snapshot()
	mv := snap[0].Metrics[0]
	if !mv.Stale {
		t.Fatal("expected Stale=true after MarkStale")
	}
	if mv.Status != StatusUnknown {
		t.Fatalf("status = %v, want unknown", mv.Status)
	}
	if mv.LastError != "ssh: timed out" {
		t.Fatalf("LastError = %q, want the failure message", mv.LastError)
	}
	// The crucial guarantee: last-known value is NOT wiped or zeroed.
	if len(mv.Measurements) != 1 || mv.Measurements[0].Value != 0.5 {
		t.Fatalf("measurements = %+v, want last-known load1=0.5 preserved", mv.Measurements)
	}
}

func TestStore_ServerStatusIsWorstAcrossMetrics(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.UpdateSuccess("s1", "memory", nil, map[string]metric.Severity{"available": metric.SeverityWarning}, now)
	s.UpdateSuccess("s1", "cpu", nil, map[string]metric.Severity{"load1": ""}, now)

	snap := s.Snapshot()
	if snap[0].Status != StatusWarning {
		t.Fatalf("server status = %v, want warning (worst of warning+ok)", snap[0].Status)
	}

	// Now cpu's collector starts failing: unknown ranks below critical but
	// above ok, and above warning per our rank order — but here nothing is
	// critical, so unknown (from cpu) should not demote the existing
	// warning from memory. Worst-of applies both ways: still warning
	// shows unless cpu's unknown outranks it.
	s.MarkStale("s1", "cpu", "boom", now)
	snap = s.Snapshot()
	if snap[0].Status != StatusWarning {
		t.Fatalf("server status after one metric goes stale = %v, want still warning (critical/warning outrank unknown)", snap[0].Status)
	}
}

func TestStore_IndependentServersAndMetrics(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.UpdateSuccess("s1", "memory", nil, map[string]metric.Severity{"available": metric.SeverityCritical}, now)
	s.UpdateSuccess("s2", "memory", nil, map[string]metric.Severity{"available": ""}, now)

	snap := s.Snapshot()
	byName := map[string]ServerView{}
	for _, sv := range snap {
		byName[sv.Name] = sv
	}
	if byName["s1"].Status != StatusCritical {
		t.Errorf("s1 status = %v, want critical", byName["s1"].Status)
	}
	if byName["s2"].Status != StatusOK {
		t.Errorf("s2 status = %v, want ok (independent of s1)", byName["s2"].Status)
	}
}
