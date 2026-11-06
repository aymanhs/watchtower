package collect

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aymanhs/watchtower/internal/metric"
)

// directDialer dials straight out from this process — the "local" case.
// Testing the SSH-tunneled case the same way would need a real SSH
// server; that path is proven live against the real NATS servers instead.
type directDialer struct{}

func (directDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

func (directDialer) Run(ctx context.Context, command string, timeout time.Duration) (string, error) {
	panic("not used by these tests")
}

func httpDef(url string) *metric.Definition {
	return &metric.Definition{
		Name:     "nats",
		Groups:   []string{"messaging"},
		Interval: metric.Duration{Duration: 15 * time.Second},
		Timeout:  metric.Duration{Duration: 5 * time.Second},
		Collector: metric.Collector{
			Type: metric.CollectorHTTP,
			URL:  url,
		},
	}
}

func TestExecuteHTTP_ParsesJSONFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"connections": 3, "cpu": 1.5, "server_name": "nats1"}`))
	}))
	defer srv.Close()

	obs, err := Execute(context.Background(), directDialer{}, "nats1", httpDef(srv.URL), 2*time.Second, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	byName := map[string]float64{}
	for _, o := range obs {
		byName[o.FullName()] = o.Value
	}
	if byName["nats1/nats.connections"] != 3 {
		t.Errorf("connections = %v, want 3", byName["nats1/nats.connections"])
	}
	if byName["nats1/nats.cpu"] != 1.5 {
		t.Errorf("cpu = %v, want 1.5", byName["nats1/nats.cpu"])
	}
	if _, ok := byName["nats1/nats.missing"]; ok {
		t.Error("missing field should be skipped, not emitted as zero")
	}
}

// TestExecuteHTTP_FieldOverride: "conn_count" is both auto-discovered
// under its own raw JSON key and, via Fields, additionally exposed under
// the friendlier "connections" — both point at the same value. Fields
// adds a rename/alias on top of auto-discovery, it doesn't replace what
// auto-discovery already found.
func TestExecuteHTTP_FieldOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"conn_count": 7}`))
	}))
	defer srv.Close()

	def := httpDef(srv.URL)
	def.Collector.Fields = map[string]string{"connections": "conn_count"}

	obs, err := Execute(context.Background(), directDialer{}, "nats1", def, 2*time.Second, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	byName := map[string]float64{}
	for _, o := range obs {
		byName[o.FullName()] = o.Value
	}
	if byName["nats1/nats.conn_count"] != 7 {
		t.Errorf("conn_count = %v, want 7 (auto-discovered under its own raw name)", byName["nats1/nats.conn_count"])
	}
	if byName["nats1/nats.connections"] != 7 {
		t.Errorf("connections = %v, want 7 (aliased via fields override)", byName["nats1/nats.connections"])
	}
}

func TestExecuteHTTP_NestedFieldPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"cluster": {"pool_size": 3}}`))
	}))
	defer srv.Close()

	def := &metric.Definition{
		Name: "nats", Groups: []string{"messaging"},
		Interval: metric.Duration{Duration: 15 * time.Second}, Timeout: metric.Duration{Duration: 5 * time.Second},
		Collector: metric.Collector{Type: metric.CollectorHTTP, URL: srv.URL, Fields: map[string]string{"pool_size": "cluster.pool_size"}},
	}

	obs, err := Execute(context.Background(), directDialer{}, "nats1", def, 2*time.Second, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(obs) != 1 || obs[0].Value != 3 {
		t.Fatalf("got %+v, want one observation with value 3 (via dot-path)", obs)
	}
}

func TestExecuteHTTP_NonOKStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer srv.Close()

	_, err := Execute(context.Background(), directDialer{}, "nats1", httpDef(srv.URL), 2*time.Second, nil)
	if err == nil {
		t.Fatal("expected error for HTTP 404, got nil")
	}
}

func TestExecuteHTTP_MalformedJSONIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := Execute(context.Background(), directDialer{}, "nats1", httpDef(srv.URL), 2*time.Second, nil)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestExecuteHTTP_ConnectionRefusedIsError(t *testing.T) {
	// A closed listener's address: guaranteed nothing is listening.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	_, err = Execute(context.Background(), directDialer{}, "nats1", httpDef("http://"+addr), 2*time.Second, nil)
	if err == nil {
		t.Fatal("expected error for connection refused, got nil")
	}
}

func TestExecuteHTTP_RendersURLFromConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"connections": 9}`))
	}))
	defer srv.Close()

	def := httpDef("http://{{.endpoint}}")
	host := srv.Listener.Addr().String()

	obs, err := Execute(context.Background(), directDialer{}, "nats1", def, 2*time.Second, map[string]any{"endpoint": host})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(obs) != 1 || obs[0].Value != 9 {
		t.Fatalf("got %+v, want one observation with value 9 (endpoint from config)", obs)
	}
}
