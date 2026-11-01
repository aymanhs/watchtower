package event

import (
	"errors"
	"testing"
	"time"

	"aymanhs/watchtower/internal/metric"
)

func float64p(v float64) *float64 { return &v }

// diskDef mirrors the brief's own illustrative example: warning at 80,
// critical at 90, critical recovers only below 85 (hysteresis).
func diskDef() *metric.Definition {
	return &metric.Definition{
		Name: "disk",
		Events: []metric.EventRule{
			{Name: "disk.warning", Measurement: "used_percent", Condition: metric.ConditionAbove, Threshold: 80, Severity: metric.SeverityWarning},
			{Name: "disk.critical", Measurement: "used_percent", Condition: metric.ConditionAbove, Threshold: 90, Severity: metric.SeverityCritical, RecoverBelow: float64p(85)},
		},
	}
}

func TestEvaluateThreshold_RaiseEscalateRecover(t *testing.T) {
	e := NewEngine(0)
	def := diskDef()
	now := time.Now()

	// Normal -> normal: no event.
	if ev := e.EvaluateThreshold("s1", def, "used_percent", 50, now); ev != nil {
		t.Fatalf("got event %+v at 50%%, want nil", ev)
	}

	// Crosses warning threshold: raise warning.
	ev := e.EvaluateThreshold("s1", def, "used_percent", 82, now)
	if ev == nil || ev.Kind != KindRaised || ev.Severity != metric.SeverityWarning || ev.RuleName != "disk.warning" {
		t.Fatalf("got %+v, want raised warning via disk.warning", ev)
	}

	// Still above 80 but below 90: no new event (dedup).
	if ev := e.EvaluateThreshold("s1", def, "used_percent", 83, now); ev != nil {
		t.Fatalf("got %+v at 83%% while already warning, want nil (dedup)", ev)
	}

	// Crosses critical threshold: escalate.
	ev = e.EvaluateThreshold("s1", def, "used_percent", 95, now)
	if ev == nil || ev.Kind != KindRaised || ev.Severity != metric.SeverityCritical || ev.RuleName != "disk.critical" {
		t.Fatalf("got %+v, want escalated to critical via disk.critical", ev)
	}

	// Drops back below 90 (warning's threshold) but not below critical's
	// recover_below (85): must stay critical — hysteresis, no event.
	if ev := e.EvaluateThreshold("s1", def, "used_percent", 87, now); ev != nil {
		t.Fatalf("got %+v at 87%% (above recover_below=85), want nil (still critical)", ev)
	}

	// Drops below 85: recovers, but still above warning's 80, so steps
	// down to warning, not all the way to normal.
	ev = e.EvaluateThreshold("s1", def, "used_percent", 82, now)
	if ev == nil || ev.Kind != KindRecovered || ev.Severity != metric.SeverityWarning || ev.RuleName != "disk.warning" {
		t.Fatalf("got %+v, want recovered down to warning", ev)
	}

	// Drops below 80: fully recovers to normal.
	ev = e.EvaluateThreshold("s1", def, "used_percent", 50, now)
	if ev == nil || ev.Kind != KindRecovered || ev.Severity != "" {
		t.Fatalf("got %+v, want full recovery to normal", ev)
	}

	// Staying normal: no further event.
	if ev := e.EvaluateThreshold("s1", def, "used_percent", 10, now); ev != nil {
		t.Fatalf("got %+v while already normal, want nil", ev)
	}
}

func TestEvaluateThreshold_NoHysteresisDefaultsToThreshold(t *testing.T) {
	e := NewEngine(0)
	def := &metric.Definition{
		Name: "cpu",
		Events: []metric.EventRule{
			{Name: "cpu.load1.warning", Measurement: "load1", Condition: metric.ConditionAbove, Threshold: 4, Severity: metric.SeverityWarning},
		},
	}
	now := time.Now()

	if ev := e.EvaluateThreshold("s1", def, "load1", 4.5, now); ev == nil || ev.Kind != KindRaised {
		t.Fatalf("got %+v, want raised", ev)
	}
	// No recover_below set: recovery is immediate at <= threshold.
	if ev := e.EvaluateThreshold("s1", def, "load1", 4.0, now); ev == nil || ev.Kind != KindRecovered {
		t.Fatalf("got %+v at exactly threshold with no hysteresis, want recovered", ev)
	}
}

func TestEvaluateThreshold_IndependentPerServerAndMeasurement(t *testing.T) {
	e := NewEngine(0)
	def := diskDef()
	now := time.Now()

	if ev := e.EvaluateThreshold("s1", def, "used_percent", 95, now); ev == nil {
		t.Fatal("expected event for s1")
	}
	// s2 is a different entity entirely: starts normal regardless of s1's state.
	if ev := e.EvaluateThreshold("s2", def, "used_percent", 10, now); ev != nil {
		t.Fatalf("got %+v for unrelated server s2, want nil", ev)
	}
}

func TestEvaluateCollectorResult_DedupsRepeatedFailures(t *testing.T) {
	e := NewEngine(0)
	now := time.Now()
	errBoom := errors.New("ssh: timed out")

	ev := e.EvaluateCollectorResult("s1", "cpu", errBoom, now)
	if ev == nil || ev.Category != CategoryOperational || ev.Kind != KindRaised {
		t.Fatalf("got %+v, want raised operational event on first failure", ev)
	}

	// Same failure again: no duplicate event.
	if ev := e.EvaluateCollectorResult("s1", "cpu", errBoom, now); ev != nil {
		t.Fatalf("got %+v on repeated failure, want nil (dedup)", ev)
	}

	// Success: recovery event.
	ev = e.EvaluateCollectorResult("s1", "cpu", nil, now)
	if ev == nil || ev.Kind != KindRecovered {
		t.Fatalf("got %+v, want recovered operational event", ev)
	}

	// Repeated success: no duplicate event.
	if ev := e.EvaluateCollectorResult("s1", "cpu", nil, now); ev != nil {
		t.Fatalf("got %+v on repeated success, want nil (dedup)", ev)
	}
}

func TestHistoryAndAcknowledge(t *testing.T) {
	e := NewEngine(0)
	def := diskDef()
	now := time.Now()

	e.EvaluateThreshold("s1", def, "used_percent", 95, now)
	hist := e.History()
	if len(hist) != 1 {
		t.Fatalf("got %d history entries, want 1", len(hist))
	}
	if hist[0].Acknowledged {
		t.Fatal("new event should not start acknowledged")
	}

	if !e.Acknowledge(hist[0].ID) {
		t.Fatal("Acknowledge returned false for a real event ID")
	}
	if e.Acknowledge(99999) {
		t.Fatal("Acknowledge returned true for a nonexistent event ID")
	}
	if !e.History()[0].Acknowledged {
		t.Fatal("event not marked acknowledged in history after Acknowledge")
	}
}

func TestHistory_BoundedByMaxHistory(t *testing.T) {
	e := NewEngine(2)
	now := time.Now()
	def := &metric.Definition{
		Name: "cpu",
		Events: []metric.EventRule{
			{Name: "w", Measurement: "load1", Condition: metric.ConditionAbove, Threshold: 1, Severity: metric.SeverityWarning},
		},
	}
	// Flip the state three times (raise, recover, raise) to generate 3 events.
	e.EvaluateThreshold("s1", def, "load1", 2, now)
	e.EvaluateThreshold("s1", def, "load1", 0, now)
	e.EvaluateThreshold("s1", def, "load1", 2, now)

	hist := e.History()
	if len(hist) != 2 {
		t.Fatalf("got %d history entries, want 2 (bounded)", len(hist))
	}
}
