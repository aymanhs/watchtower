package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"aymanhs/watchtower/internal/metric"
)

type fakeRunner struct {
	out string
	err error
}

func (f fakeRunner) Run(ctx context.Context, command string, timeout time.Duration) (string, error) {
	return f.out, f.err
}

func testDef() *metric.Definition {
	return &metric.Definition{
		Name:     "memory",
		Groups:   []string{"basic"},
		Interval: metric.Duration{Duration: 10 * time.Second},
		Timeout:  metric.Duration{Duration: 5 * time.Second},
		Collector: metric.Collector{
			Type:    metric.CollectorShell,
			Command: "irrelevant for this test",
		},
		Metrics: map[string]metric.Measurement{
			"used": {Unit: "bytes", Type: metric.TypeGauge},
			"free": {Unit: "bytes", Type: metric.TypeGauge},
		},
	}
}

func TestExecute_ParsesKnownMeasurements(t *testing.T) {
	runner := fakeRunner{out: "used=100\nfree=200\nextra=999\n"}
	obs, err := Execute(context.Background(), runner, "server1", testDef(), 5*time.Second)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(obs) != 2 {
		t.Fatalf("got %d observations, want 2 (unknown 'extra' key must be ignored)", len(obs))
	}
	byName := map[string]float64{}
	for _, o := range obs {
		byName[o.FullName()] = o.Value
	}
	if byName["server1/memory.used"] != 100 {
		t.Errorf("server1/memory.used = %v, want 100", byName["server1/memory.used"])
	}
	if byName["server1/memory.free"] != 200 {
		t.Errorf("server1/memory.free = %v, want 200", byName["server1/memory.free"])
	}
}

func TestExecute_RunnerErrorYieldsNoObservations(t *testing.T) {
	runner := fakeRunner{err: errors.New("ssh: command timed out")}
	obs, err := Execute(context.Background(), runner, "server1", testDef(), 5*time.Second)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if obs != nil {
		t.Fatalf("expected nil observations on error, got %v", obs)
	}
}

func TestExecute_MalformedOutputIsError(t *testing.T) {
	runner := fakeRunner{out: "used=not-a-number\n"}
	obs, err := Execute(context.Background(), runner, "server1", testDef(), 5*time.Second)
	if err == nil {
		t.Fatal("expected error for malformed output, got nil")
	}
	if obs != nil {
		t.Fatalf("expected nil observations on error, got %v", obs)
	}
}

func TestExecute_MissingMeasurementSkippedNotZero(t *testing.T) {
	runner := fakeRunner{out: "used=100\n"} // "free" absent
	obs, err := Execute(context.Background(), runner, "server1", testDef(), 5*time.Second)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1 (missing 'free' must be skipped, not zeroed)", len(obs))
	}
	if obs[0].FullName() != "server1/memory.used" {
		t.Errorf("got %q, want server1/memory.used", obs[0].FullName())
	}
}
