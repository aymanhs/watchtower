package collect

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"aymanhs/watchtower/internal/metric"
)

// recordingRunner remembers the command it was actually asked to run, so
// tests can confirm Execute rendered the template before sending it.
type recordingRunner struct {
	gotCommand string
	out        string
}

func (r *recordingRunner) Run(ctx context.Context, command string, timeout time.Duration) (string, error) {
	r.gotCommand = command
	return r.out, nil
}

func (r *recordingRunner) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return nil, errors.New("recordingRunner: DialContext not implemented")
}

func natsDef() *metric.Definition {
	return &metric.Definition{
		Name:     "nats",
		Groups:   []string{"messaging"},
		Interval: metric.Duration{Duration: 15 * time.Second},
		Timeout:  metric.Duration{Duration: 5 * time.Second},
		Collector: metric.Collector{
			Type:    metric.CollectorShell,
			Command: "curl -s http://{{.endpoint}}/varz",
		},
	}
}

func TestExecute_RendersConfigIntoCommand(t *testing.T) {
	runner := &recordingRunner{out: "connections=3\n"}
	_, err := Execute(context.Background(), runner, "nats1", natsDef(), 5*time.Second, map[string]any{"endpoint": "localhost:8222"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "curl -s http://localhost:8222/varz"
	if runner.gotCommand != want {
		t.Fatalf("command sent to runner = %q, want %q", runner.gotCommand, want)
	}
}

func TestExecute_MissingConfigFailsWithoutRunning(t *testing.T) {
	runner := &recordingRunner{out: "connections=3\n"}
	_, err := Execute(context.Background(), runner, "nats1", natsDef(), 5*time.Second, nil)
	if err == nil {
		t.Fatal("expected error when config doesn't supply 'endpoint', got nil")
	}
	if runner.gotCommand != "" {
		t.Fatalf("runner.Run should never have been called; got command %q", runner.gotCommand)
	}
}
