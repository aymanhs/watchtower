package state

import (
	"testing"

	"aymanhs/watchtower/internal/metric"
)

func TestResolveScale_Nil(t *testing.T) {
	s, err := ResolveScale(nil, nil)
	if err != nil || s != nil {
		t.Fatalf("ResolveScale(nil) = (%v, %v), want (nil, nil)", s, err)
	}
}

func TestResolveScale_LiteralMax(t *testing.T) {
	s, err := ResolveScale(&metric.Scale{Min: 0, Max: 4}, nil)
	if err != nil {
		t.Fatalf("ResolveScale: %v", err)
	}
	if s.Min != 0 || s.Max != 4 {
		t.Fatalf("got %+v, want {0 4}", s)
	}
}

func TestResolveScale_MaxRefResolvedFromSiblings(t *testing.T) {
	siblings := map[string]float64{"total": 1000}
	s, err := ResolveScale(&metric.Scale{MaxRef: "total"}, siblings)
	if err != nil {
		t.Fatalf("ResolveScale: %v", err)
	}
	if s.Max != 1000 {
		t.Fatalf("got max %v, want 1000 (from sibling)", s.Max)
	}
}

func TestResolveScale_MaxRefMissingFromCycle(t *testing.T) {
	_, err := ResolveScale(&metric.Scale{MaxRef: "total"}, map[string]float64{})
	if err == nil {
		t.Fatal("expected error when max_ref's sibling value is absent this cycle")
	}
}

func TestResolveScale_MaxEqualsMinRejected(t *testing.T) {
	_, err := ResolveScale(&metric.Scale{Min: 5, Max: 5}, nil)
	if err == nil {
		t.Fatal("expected error when resolved max equals min")
	}
}

func TestScale_Fraction(t *testing.T) {
	s := Scale{Min: 0, Max: 100}
	cases := map[float64]float32{-10: 0, 0: 0, 50: 0.5, 100: 1, 150: 1}
	for value, want := range cases {
		if got := s.Fraction(value); got != want {
			t.Errorf("Fraction(%v) = %v, want %v", value, got, want)
		}
	}
}

func float64p(v float64) *float64 { return &v }

func TestResolveZones_AboveDirection(t *testing.T) {
	// Mirrors the brief's own disk example: warning@80, critical@90.
	rules := []metric.EventRule{
		{Name: "warn", Measurement: "used_percent", Condition: metric.ConditionAbove, Threshold: 80, Severity: metric.SeverityWarning},
		{Name: "crit", Measurement: "used_percent", Condition: metric.ConditionAbove, Threshold: 90, Severity: metric.SeverityCritical},
	}
	zones := ResolveZones(rules, Scale{Min: 0, Max: 100})
	want := []Zone{
		{From: 0, To: 80, Severity: ""},
		{From: 80, To: 90, Severity: metric.SeverityWarning},
		{From: 90, To: 100, Severity: metric.SeverityCritical},
	}
	if len(zones) != len(want) {
		t.Fatalf("got %d zones, want %d: %+v", len(zones), len(want), zones)
	}
	for i := range want {
		if zones[i] != want[i] {
			t.Errorf("zone[%d] = %+v, want %+v", i, zones[i], want[i])
		}
	}
}

func TestResolveZones_BelowDirection(t *testing.T) {
	// Mirrors memory.available: warning below 512MB, critical below 128MB
	// — critical's threshold (128) is numerically smaller than warning's
	// (512), and is the more severe band.
	rules := []metric.EventRule{
		{Name: "warn", Measurement: "available", Condition: metric.ConditionBelow, Threshold: 512, Severity: metric.SeverityWarning},
		{Name: "crit", Measurement: "available", Condition: metric.ConditionBelow, Threshold: 128, Severity: metric.SeverityCritical, RecoverAbove: float64p(256)},
	}
	zones := ResolveZones(rules, Scale{Min: 0, Max: 1000})
	want := []Zone{
		{From: 0, To: 128, Severity: metric.SeverityCritical},
		{From: 128, To: 512, Severity: metric.SeverityWarning},
		{From: 512, To: 1000, Severity: ""},
	}
	if len(zones) != len(want) {
		t.Fatalf("got %d zones, want %d: %+v", len(zones), len(want), zones)
	}
	for i := range want {
		if zones[i] != want[i] {
			t.Errorf("zone[%d] = %+v, want %+v", i, zones[i], want[i])
		}
	}
}

func TestResolveZones_NoRulesYieldsNil(t *testing.T) {
	if zones := ResolveZones(nil, Scale{Min: 0, Max: 100}); zones != nil {
		t.Fatalf("got %+v, want nil", zones)
	}
}

func TestResolveZones_SingleRuleEachDirection(t *testing.T) {
	above := ResolveZones([]metric.EventRule{
		{Measurement: "x", Condition: metric.ConditionAbove, Threshold: 4, Severity: metric.SeverityWarning},
	}, Scale{Min: 0, Max: 8})
	wantAbove := []Zone{{0, 4, ""}, {4, 8, metric.SeverityWarning}}
	if len(above) != 2 || above[0] != wantAbove[0] || above[1] != wantAbove[1] {
		t.Errorf("above single-rule zones = %+v, want %+v", above, wantAbove)
	}

	below := ResolveZones([]metric.EventRule{
		{Measurement: "x", Condition: metric.ConditionBelow, Threshold: 4, Severity: metric.SeverityCritical},
	}, Scale{Min: 0, Max: 8})
	wantBelow := []Zone{{0, 4, metric.SeverityCritical}, {4, 8, ""}}
	if len(below) != 2 || below[0] != wantBelow[0] || below[1] != wantBelow[1] {
		t.Errorf("below single-rule zones = %+v, want %+v", below, wantBelow)
	}
}
