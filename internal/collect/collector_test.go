package collect

import (
	"context"
	"errors"
	"net"
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

// DialContext is never exercised by these shell-collector tests; it only
// exists so fakeRunner satisfies Backend.
func (f fakeRunner) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return nil, errors.New("fakeRunner: DialContext not implemented")
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
	}
}

// TestExecute_EmitsEveryKey: there's no declared measurement list any
// more to filter collector output against — every key the collector's
// own output actually produces becomes an Observation, full stop.
func TestExecute_EmitsEveryKey(t *testing.T) {
	runner := fakeRunner{out: "used=100\nfree=200\nextra=999\n"}
	obs, err := Execute(context.Background(), runner, "server1", testDef(), 5*time.Second, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(obs) != 3 {
		t.Fatalf("got %d observations, want 3 (used, free, extra — nothing filtered)", len(obs))
	}
	byName := map[string]float64{}
	for _, o := range obs {
		byName[o.FullName()] = o.Value
	}
	for name, want := range map[string]float64{
		"server1/memory.used":  100,
		"server1/memory.free":  200,
		"server1/memory.extra": 999,
	} {
		if byName[name] != want {
			t.Errorf("%s = %v, want %v", name, byName[name], want)
		}
	}
}

func TestExecute_RunnerErrorYieldsNoObservations(t *testing.T) {
	runner := fakeRunner{err: errors.New("ssh: command timed out")}
	obs, err := Execute(context.Background(), runner, "server1", testDef(), 5*time.Second, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if obs != nil {
		t.Fatalf("expected nil observations on error, got %v", obs)
	}
}

func TestExecute_MalformedOutputIsError(t *testing.T) {
	runner := fakeRunner{out: "used=not-a-number\n"}
	obs, err := Execute(context.Background(), runner, "server1", testDef(), 5*time.Second, nil)
	if err == nil {
		t.Fatal("expected error for malformed output, got nil")
	}
	if obs != nil {
		t.Fatalf("expected nil observations on error, got %v", obs)
	}
}

func TestExecute_MissingMeasurementSkippedNotZero(t *testing.T) {
	runner := fakeRunner{out: "used=100\n"} // "free" absent
	obs, err := Execute(context.Background(), runner, "server1", testDef(), 5*time.Second, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1 (nothing in output but 'used')", len(obs))
	}
	if obs[0].FullName() != "server1/memory.used" {
		t.Errorf("got %q, want server1/memory.used", obs[0].FullName())
	}
}

// TestExecute_PerInstanceKeysPassThrough reproduces disk.yaml's
// per-volume shape: a collector that discovers its own instances at
// runtime (df over however many real mounts exist) emits them
// instance-qualified ("root.used", "data.used") — Execute itself doesn't
// decompose or care about this at all (that's metric.SplitInstance's
// job, used by the event engine and the dashboard selector, not here);
// every key, instance-qualified or not, just becomes an Observation.
func TestExecute_PerInstanceKeysPassThrough(t *testing.T) {
	def := testDef()
	runner := fakeRunner{out: "root.used=100\nroot.free=200\ndata.used=300\n"}
	obs, err := Execute(context.Background(), runner, "server1", def, 5*time.Second, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(obs) != 3 {
		t.Fatalf("got %d observations, want 3 (root.used, root.free, data.used)", len(obs))
	}
	byName := map[string]float64{}
	for _, o := range obs {
		byName[o.FullName()] = o.Value
	}
	for name, want := range map[string]float64{
		"server1/memory.root.used": 100,
		"server1/memory.root.free": 200,
		"server1/memory.data.used": 300,
	} {
		if byName[name] != want {
			t.Errorf("%s = %v, want %v", name, byName[name], want)
		}
	}
}
