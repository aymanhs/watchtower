package app

import (
	"testing"
	"time"

	"aymanhs/watchtower/internal/group"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/server"
)

func TestEffective_ResolvesConfigFromServerOverride(t *testing.T) {
	def := &metric.Definition{
		Name:     "nats",
		Interval: Duration(15 * time.Second),
		Timeout:  Duration(5 * time.Second),
	}
	cfg := &server.Config{
		Name:   "nats1",
		Groups: []string{"messaging"},
		Metrics: map[string]server.MetricOverride{
			"nats": {Config: map[string]any{"endpoint": "localhost:8222"}},
		},
	}
	emptyGroups, err := group.NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	interval, timeout, config := effective(emptyGroups, cfg, def)
	if interval != 15*time.Second || timeout != 5*time.Second {
		t.Fatalf("got interval=%v timeout=%v, want unchanged definition defaults", interval, timeout)
	}
	if config["endpoint"] != "localhost:8222" {
		t.Fatalf("config = %+v, want endpoint=localhost:8222 from the server override", config)
	}
}

func TestEffective_GroupConfigLosesToServerOverride(t *testing.T) {
	def := &metric.Definition{Name: "nats", Interval: Duration(15 * time.Second), Timeout: Duration(5 * time.Second)}
	cfg := &server.Config{
		Name:   "nats1",
		Groups: []string{"messaging"},
		Metrics: map[string]server.MetricOverride{
			"nats": {Config: map[string]any{"endpoint": "localhost:9999"}},
		},
	}
	configs, err := group.LoadGroups("testdata/groups")
	if err != nil {
		t.Fatalf("LoadGroups: %v", err)
	}
	groupReg, err := group.NewRegistry(configs)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	_, _, config := effective(groupReg, cfg, def)
	if config["endpoint"] != "localhost:9999" {
		t.Fatalf("config = %+v, want the server's own override (9999) to beat the group's (8222)", config)
	}
}
