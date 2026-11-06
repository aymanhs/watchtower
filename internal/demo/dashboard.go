package demo

import "aymanhs/watchtower/internal/dashboard"

func ptr(v float64) *float64 { return &v }

// SeedDashboard saves a sample dashboard covering every widget type
// against the fake fleet, unless the store already has dashboards.
func SeedDashboard(store *dashboard.Store) error {
	if len(store.All()) > 0 {
		return nil
	}
	d := store.GetOrNew("Demo overview")
	d.Widgets = []dashboard.Widget{
		{Type: dashboard.WidgetChart, Title: "CPU usage", SpanX: 3, Series: []dashboard.SeriesRef{
			{Selector: "web-*/cpu.usage", Format: "%.0f%%"},
			{Selector: "db-*/cpu.usage", Format: "%.0f%%"},
		}},
		{Type: dashboard.WidgetGauge, Title: "web CPU", Min: ptr(0), Max: ptr(100), Series: []dashboard.SeriesRef{
			{Selector: "web-*/cpu.usage", Format: "%.0f%%"},
		}},
		{Type: dashboard.WidgetChart, Title: "Memory used % (7d)", Range: "7d", SpanX: 3, Series: []dashboard.SeriesRef{
			{Selector: "*/memory.used_percent", Format: "%.0f%%"},
		}},
		{Type: dashboard.WidgetBars, Title: "Disk used %", Min: ptr(0), Max: ptr(100), Series: []dashboard.SeriesRef{
			{Selector: "*/disk.*.used_percent", Format: "%.0f%%"},
		}},
		{Type: dashboard.WidgetNumber, Title: "NATS connections", Series: []dashboard.SeriesRef{
			{Selector: "cluster:nats/nats.connections", Format: "%.0f"},
		}},
		{Type: dashboard.WidgetChart, Title: "Load (30d)", Range: "30d", SpanX: 3, Series: []dashboard.SeriesRef{
			{Selector: "*/cpu.load1"},
		}},
		{Type: dashboard.WidgetPie, Title: "web-1 containers", Series: []dashboard.SeriesRef{
			{Selector: "web-1/docker.containers_running"},
			{Selector: "web-1/docker.containers_paused"},
			{Selector: "web-1/docker.containers_stopped"},
		}},
	}
	return store.Save(d)
}
