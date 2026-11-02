package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aymanhs/watchtower/internal/dashboard"
)

func TestSaveDashboard_PreservesCommentsAndAddsWidget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.yaml")
	original := `name: example

connection:
  type: ssh
  host: 10.0.0.1
  port: 22
  user: ayman
  key: /home/ayman/.ssh/id_example

groups:
  - basic

metrics:
  nats:
    config:
      # Placeholder comment that must survive a dashboard save.
      endpoint: localhost:8223
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadOne(path)
	if err != nil {
		t.Fatalf("loadOne: %v", err)
	}

	widgets := []dashboard.Widget{
		{Type: dashboard.WidgetChart, Title: "CPU", Series: []dashboard.SeriesRef{
			{Metric: "cpu", Measurement: "load1"},
			{Metric: "cpu", Measurement: "load5"},
		}},
	}
	if err := SaveDashboard(cfg, widgets); err != nil {
		t.Fatalf("SaveDashboard: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gotStr := string(got)
	if !strings.Contains(gotStr, "Placeholder comment that must survive a dashboard save.") {
		t.Errorf("comment lost on save:\n%s", gotStr)
	}
	if !strings.Contains(gotStr, "dashboard:") {
		t.Errorf("dashboard key missing from saved file:\n%s", gotStr)
	}

	// Reload through the real loader and confirm the widget round-trips.
	reloaded, err := loadOne(path)
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	if len(reloaded.Dashboard) != 1 {
		t.Fatalf("got %d widgets after reload, want 1", len(reloaded.Dashboard))
	}
	if reloaded.Dashboard[0].Title != "CPU" || len(reloaded.Dashboard[0].Series) != 2 {
		t.Errorf("reloaded widget = %+v, want title CPU with 2 series", reloaded.Dashboard[0])
	}
	if cfg.Dashboard == nil || len(cfg.Dashboard) != 1 {
		t.Errorf("cfg.Dashboard not updated in memory: %+v", cfg.Dashboard)
	}
}

func TestSaveDashboard_EmptyWidgetsRemovesKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.yaml")
	original := `name: example

connection:
  type: local

groups:
  - basic

dashboard:
  - type: number
    series:
      - metric: cpu
        measurement: usage
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadOne(path)
	if err != nil {
		t.Fatalf("loadOne: %v", err)
	}
	if len(cfg.Dashboard) != 1 {
		t.Fatalf("precondition: got %d widgets, want 1", len(cfg.Dashboard))
	}

	if err := SaveDashboard(cfg, nil); err != nil {
		t.Fatalf("SaveDashboard: %v", err)
	}
	reloaded, err := loadOne(path)
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	if len(reloaded.Dashboard) != 0 {
		t.Errorf("got %d widgets after clearing, want 0", len(reloaded.Dashboard))
	}
}

func TestSaveDashboard_RejectsInvalidWidget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.yaml")
	if err := os.WriteFile(path, []byte("name: example\nconnection:\n  type: local\ngroups:\n  - basic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadOne(path)
	if err != nil {
		t.Fatal(err)
	}
	err = SaveDashboard(cfg, []dashboard.Widget{{Type: dashboard.WidgetGauge}}) // gauge needs exactly 1 series
	if err == nil {
		t.Fatal("expected error for an invalid widget, got nil")
	}
}
