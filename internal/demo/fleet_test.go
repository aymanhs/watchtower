package demo

import (
	"testing"
	"time"

	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/storage"
)

func loadDefs(t *testing.T) []*metric.Definition {
	t.Helper()
	defs, err := metric.LoadDefinitions("../../metrics")
	if err != nil {
		t.Fatal(err)
	}
	return defs
}

func TestSamplesAreSaneAndDeterministic(t *testing.T) {
	now := time.Now()
	for _, srv := range Fleet() {
		for _, name := range srv.Metrics {
			for i := 0; i < 500; i++ {
				ts := now.Add(-time.Duration(i) * 7 * time.Minute)
				a, err := srv.Sample(name, ts)
				if err != nil {
					continue
				}
				b, _ := srv.Sample(name, ts)
				for k, v := range a {
					if v != b[k] {
						t.Fatalf("%s/%s.%s not deterministic", srv.Name, name, k)
					}
					if v < 0 {
						t.Fatalf("%s/%s.%s negative: %v", srv.Name, name, k, v)
					}
					if len(k) > 12 && k[len(k)-12:] == "used_percent" && v > 100 {
						t.Fatalf("%s/%s.%s above 100: %v", srv.Name, name, k, v)
					}
				}
			}
		}
	}
}

func TestCountersAreMonotonic(t *testing.T) {
	srv := Fleet()[0]
	now := time.Now()
	prev := -1.0
	for i := 0; i < 200; i++ {
		v, _ := srv.Sample("network", now.Add(time.Duration(i)*time.Minute))
		if v["rx_bytes"] < prev {
			t.Fatalf("rx_bytes decreased at step %d", i)
		}
		prev = v["rx_bytes"]
	}
}

func TestBackfillPopulatesHistory(t *testing.T) {
	defs := loadDefs(t)
	h, err := storage.Open(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	now := time.Now()
	if err := Backfill(h, defs, now, time.Hour, time.Minute); err != nil {
		t.Fatal(err)
	}
	pts, err := h.Query("web-1/cpu.usage", now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) < 55 {
		t.Fatalf("got %d points, want ~60", len(pts))
	}
	if disk, _ := h.Query("db-1/disk.data.used_percent", now.Add(-time.Hour), now); len(disk) == 0 {
		t.Fatal("expected per-instance disk history")
	}
}

func TestTasksCoverFleet(t *testing.T) {
	tasks := Tasks(loadDefs(t), nil, nil, nil)
	if len(tasks) < 30 {
		t.Fatalf("got %d tasks", len(tasks))
	}
}
