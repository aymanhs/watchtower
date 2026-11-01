package server

import (
	"testing"
	"time"
)

func TestLoadServers_Valid(t *testing.T) {
	configs, err := LoadServers("testdata/valid")
	if err != nil {
		t.Fatalf("LoadServers: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("got %d configs, want 2", len(configs))
	}

	byName := make(map[string]*Config, len(configs))
	for _, c := range configs {
		byName[c.Name] = c
	}

	s1, ok := byName["server1"]
	if !ok {
		t.Fatal("server1 not found")
	}
	if s1.Connection.Host != "192.168.1.20" {
		t.Errorf("server1 host = %q, want 192.168.1.20", s1.Connection.Host)
	}
	if s1.Connection.Port != 22 {
		t.Errorf("server1 port = %d, want 22", s1.Connection.Port)
	}
	override, ok := s1.Metrics["memory"]
	if !ok {
		t.Fatal("server1 memory override missing")
	}
	if override.Interval == nil || override.Interval.Duration != 5*time.Second {
		t.Errorf("server1 memory interval override = %+v, want 5s", override.Interval)
	}

	s2, ok := byName["server2"]
	if !ok {
		t.Fatal("server2 not found")
	}
	if s2.Connection.Port != 22 {
		t.Errorf("server2 port defaulted = %d, want 22", s2.Connection.Port)
	}
}

func TestLoadServers_Invalid(t *testing.T) {
	_, err := LoadServers("testdata/invalid")
	if err == nil {
		t.Fatal("expected error loading server missing host, got nil")
	}
}

func TestLoadServers_RejectsDuplicateName(t *testing.T) {
	_, err := LoadServers("testdata/duplicate")
	if err == nil {
		t.Fatal("expected error for two server files defining the same name, got nil")
	}
}

func TestConfigValidate_RejectsUnsupportedConnectionType(t *testing.T) {
	c := &Config{
		Name: "x",
		Connection: Connection{
			Type: "telnet",
			Host: "1.2.3.4",
			User: "u",
			Key:  "k",
		},
		Groups: []string{"basic"},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for unsupported connection type, got nil")
	}
}
