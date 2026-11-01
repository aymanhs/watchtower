package group

import (
	"testing"
	"time"
)

func TestLoadGroups_Valid(t *testing.T) {
	configs, err := LoadGroups("testdata/valid")
	if err != nil {
		t.Fatalf("LoadGroups: %v", err)
	}
	reg, err := NewRegistry(configs)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	basic, ok := reg.Get("basic")
	if !ok {
		t.Fatal("basic group not found")
	}
	mem, ok := basic.Metrics["memory"]
	if !ok || mem.Interval == nil || mem.Interval.Duration != 15*time.Second {
		t.Fatalf("basic.memory override = %+v, want interval=15s", mem)
	}

	if _, ok := reg.Get("nonexistent"); ok {
		t.Fatal("Get(nonexistent) = true, want false")
	}
}

func TestLoadGroups_Invalid(t *testing.T) {
	if _, err := LoadGroups("testdata/invalid"); err == nil {
		t.Fatal("expected error loading unnamed group, got nil")
	}
}

// TestOverrides_LaterGroupInListWins proves the deterministic precedence
// rule: when two groups both configure the same metric, the one listed
// later in the server's own group order wins — regardless of file load
// order on disk.
func TestOverrides_LaterGroupInListWins(t *testing.T) {
	configs, err := LoadGroups("testdata/valid")
	if err != nil {
		t.Fatalf("LoadGroups: %v", err)
	}
	reg, err := NewRegistry(configs)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	// basic sets memory.interval=15s, linux sets memory.interval=20s.
	// Server lists "basic" before "linux" -> linux should win.
	overrides := reg.Overrides([]string{"basic", "linux"}, "memory")
	if len(overrides) != 2 {
		t.Fatalf("got %d overrides, want 2", len(overrides))
	}
	if overrides[0].Interval.Duration != 15*time.Second {
		t.Errorf("overrides[0] (basic) interval = %v, want 15s", overrides[0].Interval.Duration)
	}
	if overrides[1].Interval.Duration != 20*time.Second {
		t.Errorf("overrides[1] (linux) interval = %v, want 20s", overrides[1].Interval.Duration)
	}

	// Reversed server order flips the winner.
	reversed := reg.Overrides([]string{"linux", "basic"}, "memory")
	if reversed[1].Interval.Duration != 15*time.Second {
		t.Errorf("reversed order: overrides[1] (basic) interval = %v, want 15s", reversed[1].Interval.Duration)
	}

	// A group not configuring this metric at all contributes nothing.
	none := reg.Overrides([]string{"linux"}, "disk")
	if len(none) != 0 {
		t.Fatalf("got %d overrides for unconfigured metric, want 0", len(none))
	}
}
