package dashboard

import (
	"testing"

	"aymanhs/watchtower/internal/state"
)

func serversFixture() []state.ServerView {
	return []state.ServerView{
		{
			Name: "jumpbox",
			Metrics: []state.MetricView{
				{Name: "cpu", Status: state.StatusOK, Measurements: []state.MeasurementView{
					{Key: "usage", Value: 10},
				}},
				{Name: "memory", Status: state.StatusWarning, Measurements: []state.MeasurementView{
					{Key: "free_pct", Value: 20},
				}},
			},
		},
		{
			Name: "sbnats-nats1", Cluster: "connetic-nats",
			Metrics: []state.MetricView{
				{Name: "cpu", Status: state.StatusOK, Measurements: []state.MeasurementView{
					{Key: "usage", Value: 30},
				}},
				{Name: "memory", Status: state.StatusOK, Measurements: []state.MeasurementView{
					{Key: "free_pct", Value: 40},
				}},
			},
		},
		{
			Name: "sbnats-nats2", Cluster: "connetic-nats",
			Metrics: []state.MetricView{
				{Name: "memory", Status: state.StatusOK, Measurements: []state.MeasurementView{
					{Key: "free_pct", Value: 50},
				}},
			},
		},
		{
			Name: "disk-server",
			Metrics: []state.MetricView{
				{Name: "disk", Status: state.StatusOK, Measurements: []state.MeasurementView{
					{Key: "root_used_percent", Value: 60},
					{Key: "data_used_percent", Value: 70},
				}},
			},
		},
	}
}

func TestResolve_FixedServer(t *testing.T) {
	ref := SeriesRef{Selector: "jumpbox/cpu.usage"}
	got := ref.Resolve(serversFixture())
	if len(got) != 1 || got[0].Server != "jumpbox" || got[0].Measurement.Value != 10 {
		t.Fatalf("got %+v, want exactly jumpbox/cpu.usage=10", got)
	}
	if got[0].HistoryKey != "jumpbox/cpu.usage" {
		t.Errorf("HistoryKey = %q, want jumpbox/cpu.usage", got[0].HistoryKey)
	}
}

func TestResolve_ServerWildcard(t *testing.T) {
	ref := SeriesRef{Selector: "*/memory.free_pct"}
	got := ref.Resolve(serversFixture())
	if len(got) != 3 {
		t.Fatalf("got %d matches, want 3 (one per server reporting memory.free_pct)", len(got))
	}
	// Sorted by server name.
	wantServers := []string{"jumpbox", "sbnats-nats1", "sbnats-nats2"}
	for i, w := range wantServers {
		if got[i].Server != w {
			t.Errorf("got[%d].Server = %q, want %q", i, got[i].Server, w)
		}
	}
}

func TestResolve_Cluster(t *testing.T) {
	ref := SeriesRef{Selector: "cluster:connetic-nats/cpu.usage"}
	got := ref.Resolve(serversFixture())
	if len(got) != 1 || got[0].Server != "sbnats-nats1" {
		t.Fatalf("got %+v, want exactly sbnats-nats1/cpu.usage (the only cluster member reporting it)", got)
	}
}

func TestResolve_InstanceWildcardAgainstSuffixConvention(t *testing.T) {
	ref := SeriesRef{Selector: "disk-server/disk.*.used_percent"}
	got := ref.Resolve(serversFixture())
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2 (root_used_percent, data_used_percent)", len(got))
	}
	wantKeys := []string{"data_used_percent", "root_used_percent"} // sorted by Measurement.Key
	for i, k := range wantKeys {
		if got[i].Measurement.Key != k {
			t.Errorf("got[%d].Measurement.Key = %q, want %q", i, got[i].Measurement.Key, k)
		}
	}
}

func TestResolve_InstanceExact(t *testing.T) {
	ref := SeriesRef{Selector: "disk-server/disk.root.used_percent"}
	got := ref.Resolve(serversFixture())
	if len(got) != 1 || got[0].Measurement.Key != "root_used_percent" {
		t.Fatalf("got %+v, want exactly root_used_percent", got)
	}
}

func TestResolve_NoMatches(t *testing.T) {
	ref := SeriesRef{Selector: "jumpbox/cpu.nonexistent"}
	if got := ref.Resolve(serversFixture()); len(got) != 0 {
		t.Fatalf("got %d matches, want 0", len(got))
	}
}

func TestResolve_UnparseableSelectorNeverMatches(t *testing.T) {
	ref := SeriesRef{Selector: "not a selector"}
	if got := ref.Resolve(serversFixture()); len(got) != 0 {
		t.Fatalf("got %d matches, want 0 (unparseable selector)", len(got))
	}
}

func TestResolveWidget_FlattensEverySeries(t *testing.T) {
	w := Widget{Type: WidgetChart, Series: []SeriesRef{
		{Selector: "jumpbox/cpu.usage"},
		{Selector: "jumpbox/memory.free_pct"},
	}}
	got := ResolveWidget(w, serversFixture())
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2 (one per series)", len(got))
	}
}
