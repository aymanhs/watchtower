package objstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPutWritesOnlyOnChangeAndRestores(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	a, b := json.RawMessage(`[{"n":"web","state":"up"}]`), json.RawMessage(`[{"n":"web","state":"down"}]`)
	for i, c := range []struct {
		v       json.RawMessage
		changed bool
	}{{a, true}, {a, false}, {b, true}, {b, false}} {
		got, err := s.Put("srv/containers", now.Add(time.Duration(i)*time.Second), c.v)
		if err != nil || got != c.changed {
			t.Fatalf("put %d: changed=%v err=%v, want %v", i, got, err, c.changed)
		}
	}
	if e, _ := s.Latest("srv/containers"); string(e.Value) != string(b) {
		t.Fatalf("latest = %s", e.Value)
	}
	hist, _ := s.History("srv/containers", now.Add(-time.Hour), now.Add(time.Hour))
	if len(hist) != 2 {
		t.Fatalf("history has %d entries, want 2", len(hist))
	}

	// A new Store restores the latest and still treats it as unchanged.
	s2, err := Open(dir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := s2.Latest("srv/containers"); !ok || string(e.Value) != string(b) {
		t.Fatalf("restored latest = %v %s", ok, e.Value)
	}
	if changed, _ := s2.Put("srv/containers", now.Add(time.Minute), b); changed {
		t.Fatal("unchanged value written after restore")
	}
}

func TestApplyRetention(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "objects-2000-01-01.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := Open(dir, 24*time.Hour)
	if err := s.ApplyRetention(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old file still present: %v", err)
	}
}
