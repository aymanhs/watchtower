package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadStore_MissingDirIsNotError(t *testing.T) {
	s, err := LoadStore(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	if len(s.All()) != 0 {
		t.Errorf("got %d dashboards, want 0", len(s.All()))
	}
}

func TestLoadStore_ParsesEveryFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "overview.yaml", `
name: overview
widgets:
  - type: gauge
    series:
      - selector: jumpbox/cpu.usage
`)
	write(t, dir, "other.yaml", `
name: other
widgets:
  - type: number
    series:
      - selector: "*/mem.free_pct"
`)
	s, err := LoadStore(dir)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	if len(s.All()) != 2 {
		t.Fatalf("got %d dashboards, want 2", len(s.All()))
	}
	d, ok := s.Get("overview")
	if !ok {
		t.Fatal("overview not found")
	}
	if len(d.Widgets) != 1 || d.Widgets[0].Series[0].Selector != "jumpbox/cpu.usage" {
		t.Errorf("overview = %+v, unexpected shape", d)
	}
	if d.SourceFile != filepath.Join(dir, "overview.yaml") {
		t.Errorf("SourceFile = %q, want %s", d.SourceFile, filepath.Join(dir, "overview.yaml"))
	}
}

func TestLoadStore_DuplicateNameErrors(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.yaml", "name: same\nwidgets: []\n")
	write(t, dir, "b.yaml", "name: same\nwidgets: []\n")
	if _, err := LoadStore(dir); err == nil {
		t.Fatal("expected error for duplicate dashboard name")
	}
}

func TestLoadStore_InvalidDashboardErrors(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "bad.yaml", "widgets: []\n") // missing name
	if _, err := LoadStore(dir); err == nil {
		t.Fatal("expected error for a dashboard missing its name")
	}
}

func TestStore_GetOrNew(t *testing.T) {
	s, err := LoadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := s.GetOrNew("jumpbox")
	if d.Name != "jumpbox" || len(d.Widgets) != 0 || d.SourceFile != "" {
		t.Errorf("GetOrNew on an empty store = %+v, want a fresh unsaved Dashboard", d)
	}
}

func TestStore_SaveCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := &Dashboard{Name: "My Dashboard!", Widgets: []Widget{
		{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}},
	}}
	if err := s.Save(d); err != nil {
		t.Fatalf("Save: %v", err)
	}
	wantPath := filepath.Join(dir, "my-dashboard.yaml")
	if d.SourceFile != wantPath {
		t.Errorf("SourceFile = %q, want %q", d.SourceFile, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected file at %s: %v", wantPath, err)
	}

	// Round-trips through a fresh LoadStore.
	s2, err := LoadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get("My Dashboard!")
	if !ok || len(got.Widgets) != 1 || got.Widgets[0].Series[0].Selector != "jumpbox/cpu.usage" {
		t.Errorf("reloaded = %+v, want 1 widget with jumpbox/cpu.usage", got)
	}
}

// Save only promises to leave content outside the "name"/"widgets" keys
// it rewrites untouched — a comment sitting inside the widgets list
// itself doesn't survive a fresh re-encode of that value, same as
// server.SaveDashboard's own "dashboard" key never did either.
func TestStore_SavePreservesCommentsOnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overview.yaml")
	write(t, dir, "overview.yaml", `name: overview
# A hand-written note that must survive a Save.
widgets:
  - type: gauge
    series:
      - selector: jumpbox/cpu.usage
`)
	s, err := LoadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := s.Get("overview")
	d.Widgets = append(d.Widgets, Widget{Type: WidgetNumber, Series: []SeriesRef{{Selector: "jumpbox/memory.free"}}})
	if err := s.Save(d); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "A hand-written note that must survive a Save.") {
		t.Errorf("comment lost on save:\n%s", got)
	}
	if !strings.Contains(string(got), "memory.free") {
		t.Errorf("new widget missing from saved file:\n%s", got)
	}
}

func TestStore_SaveRejectsInvalidDashboard(t *testing.T) {
	s, err := LoadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&Dashboard{Widgets: []Widget{{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}}}}); err == nil {
		t.Fatal("expected error: dashboard has no name")
	}
}

func TestStore_SaveRejectsNameCollisionWithAnotherDashboard(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := &Dashboard{Name: "overview", Widgets: []Widget{{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}}}}
	if err := s.Save(first); err != nil {
		t.Fatal(err)
	}
	second := &Dashboard{Name: "overview", Widgets: []Widget{{Type: WidgetNumber, Series: []SeriesRef{{Selector: "jumpbox/memory.free"}}}}}
	if err := s.Save(second); err == nil {
		t.Fatal("expected error: another *Dashboard already owns this name")
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
