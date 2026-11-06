package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/server"
	"aymanhs/watchtower/internal/state"
)

func TestRunner_ReloadAddsAndRemovesServers(t *testing.T) {
	defs, err := metric.LoadDefinitions("../../metrics")
	if err != nil || len(defs) == 0 || len(defs[0].Groups) == 0 {
		t.Skipf("no metric definitions to run: %v", err)
	}
	dir := t.TempDir()
	store := state.NewStore()
	r := &Runner{
		Paths:         Paths{Metrics: "../../metrics", Groups: filepath.Join(dir, "nogroups"), Servers: filepath.Join(dir, "servers")},
		Events:        event.NewEngine(0),
		Store:         store,
		MaxConcurrent: 4,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); r.Wait() }()

	if err := r.Start(ctx); err != nil {
		t.Fatalf("start with no servers must succeed: %v", err)
	}
	cfg := &server.Config{Name: "here", Connection: server.Connection{Type: server.ConnectionLocal}, Groups: defs[0].Groups[:1]}
	if err := server.Save(r.Paths.Servers, cfg); err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	waitFor(t, func() bool { return len(store.Snapshot()) == 1 })

	if err := server.Delete(r.Paths.Servers, "here"); err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("reload after delete: %v", err)
	}
	if n := len(store.Snapshot()); n != 0 {
		t.Fatalf("removed server still shown: %d entries", n)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("condition not met in time")
}
