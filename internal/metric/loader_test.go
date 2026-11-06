package metric

import (
	"testing"
	"time"
)

func TestLoadDefinitions_Valid(t *testing.T) {
	defs, err := LoadDefinitions("testdata/valid")
	if err != nil {
		t.Fatalf("LoadDefinitions: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("got %d definitions, want 2", len(defs))
	}

	reg, err := NewRegistry(defs)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	mem, ok := reg.Get("memory")
	if !ok {
		t.Fatal("memory definition not found")
	}
	if mem.Interval.Duration != 10*time.Second {
		t.Errorf("memory interval = %v, want 10s", mem.Interval.Duration)
	}
	if mem.Timeout.Duration != 5*time.Second {
		t.Errorf("memory timeout = %v, want 5s", mem.Timeout.Duration)
	}
	if mem.Collector.Type != CollectorShell {
		t.Errorf("memory collector type = %v, want shell", mem.Collector.Type)
	}
	if len(mem.Events) != 4 { // available + used_percent, each warning/critical
		t.Errorf("memory events = %d, want 4", len(mem.Events))
	}

	cpu, ok := reg.Get("cpu")
	if !ok {
		t.Fatal("cpu definition not found")
	}
	if cpu.Interval.Duration != 5*time.Second {
		t.Errorf("cpu interval = %v, want 5s", cpu.Interval.Duration)
	}

	resolved := reg.ResolveGroups([]string{"linux"})
	if len(resolved) != 2 {
		t.Fatalf("ResolveGroups(linux) = %d defs, want 2", len(resolved))
	}
	if resolved[0].Name != "cpu" || resolved[1].Name != "memory" {
		t.Errorf("ResolveGroups order = [%s %s], want [cpu memory]", resolved[0].Name, resolved[1].Name)
	}

	if none := reg.ResolveGroups([]string{"nonexistent"}); len(none) != 0 {
		t.Errorf("ResolveGroups(nonexistent) = %d defs, want 0", len(none))
	}
}

func TestLoadDefinitions_Invalid(t *testing.T) {
	_, err := LoadDefinitions("testdata/invalid")
	if err == nil {
		t.Fatal("expected error loading invalid duration, got nil")
	}
}

func TestDefinitionValidate_RejectsTimeoutGEInterval(t *testing.T) {
	d := &Definition{
		Name:     "bad",
		Groups:   []string{"basic"},
		Interval: Duration{5 * time.Second},
		Timeout:  Duration{5 * time.Second},
		Collector: Collector{
			Type:    CollectorShell,
			Command: "echo x=1",
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatal("expected error when timeout >= interval, got nil")
	}
}

func TestDuplicateMetricNameRejected(t *testing.T) {
	a := &Definition{Name: "dup", SourceFile: "a.yaml"}
	b := &Definition{Name: "dup", SourceFile: "b.yaml"}
	if _, err := NewRegistry([]*Definition{a, b}); err == nil {
		t.Fatal("expected error for duplicate definition name, got nil")
	}
}
