package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/state"
)

func detailsFixture() state.ServerView {
	return state.ServerView{Name: "web-1", Metrics: []state.MetricView{
		{Name: "cpu", Status: state.StatusOK, Measurements: []state.MeasurementView{{Key: "used_percent", Value: 12.5}}},
		{Name: "disk", Status: state.StatusWarning, Measurements: []state.MeasurementView{
			{Key: "root.used_percent", Value: 91}, {Key: "data.used_percent", Value: 40},
		}},
		{Name: "svc", Status: state.StatusUnknown, Stale: true, LastError: "timeout"},
	}}
}

func TestMetricRows_Filters(t *testing.T) {
	sv := []state.ServerView{detailsFixture()}
	cases := []struct {
		text   string
		status int
		want   int
	}{
		{"", 0, 4},
		{"DISK", 0, 2},
		{"root", 0, 1},
		{"disk.*used*", 0, 2},
		{"cpu.*", 0, 1},
		{"", 1, 2},
		{"", 2, 1},
		{"nomatch", 0, 0},
	}
	for _, tc := range cases {
		if got := len(metricRows(sv, tc.text, tc.status)); got != tc.want {
			t.Errorf("metricRows(%q,%d) = %d rows, want %d", tc.text, tc.status, got, tc.want)
		}
	}
}

func TestMetricRows_InstanceAndError(t *testing.T) {
	rows := metricRows([]state.ServerView{detailsFixture()}, "root", 0)
	if rows[0].Instance != "root" || rows[0].Value != "91" {
		t.Errorf("row = %+v", rows[0])
	}
	rows = metricRows([]state.ServerView{detailsFixture()}, "svc", 0)
	if len(rows) != 1 || rows[0].Error != "timeout" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestMetricRows_AcrossServers(t *testing.T) {
	other := detailsFixture()
	other.Name = "db-1"
	all := []state.ServerView{detailsFixture(), other}
	for text, want := range map[string]int{"": 8, "db-1": 4, "db-*/cpu*": 1, "web-1/disk": 2, "cpu.*": 2} {
		if got := len(metricRows(all, text, 0)); got != want {
			t.Errorf("metricRows(%q) = %d rows, want %d", text, got, want)
		}
	}
}

func TestAgeText(t *testing.T) {
	now := time.Now()
	for in, want := range map[time.Duration]string{5 * time.Second: "5s ago", 3 * time.Minute: "3m ago", 2 * time.Hour: "2h ago"} {
		if got := ageText(now.Add(-in), now); got != want {
			t.Errorf("ageText(-%v) = %q, want %q", in, got, want)
		}
	}
	if ageText(time.Time{}, now) != "never" {
		t.Error("zero time should be never")
	}
}

func TestSortMetricRows(t *testing.T) {
	rows := metricRows([]state.ServerView{detailsFixture()}, "", 0)
	sortMetricRows(rows, ui.SortOrder{Column: "Value", Descending: true})
	if rows[0].Value != "91" {
		t.Errorf("highest first = %+v", rows[0])
	}
	sortMetricRows(rows, ui.SortOrder{Column: "Value"})
	if rows[0].Num != 0 || rows[len(rows)-1].Value != "91" {
		t.Errorf("ascending = %+v ... %+v", rows[0], rows[len(rows)-1])
	}
	sortMetricRows(rows, ui.SortOrder{Column: "Status", Descending: true})
	if rows[0].Status != state.StatusWarning {
		t.Errorf("status desc first = %v", rows[0].Status)
	}
}

func TestRowsCSVAndTSV(t *testing.T) {
	rows := metricRows([]state.ServerView{detailsFixture()}, "svc", 0)
	rows[0].Error = `bad, "quoted"`
	out, err := rowsCSV(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "server,metric,measurement,value,status,collected_at,error\n") ||
		!strings.Contains(out, `"bad, ""quoted"""`) {
		t.Errorf("csv = %q", out)
	}
	tsv := rowsTSV(metricRows([]state.ServerView{detailsFixture()}, "cpu", 0))
	if tsv != "web-1\tcpu\tused_percent\t12.500\tok\t\t\n" {
		t.Errorf("tsv = %q", tsv)
	}
}

func TestCopyRows(t *testing.T) {
	rows := metricRows([]state.ServerView{detailsFixture()}, "", 0)
	var chosen ui.Selection[string]
	if len(copyRows(rows, &chosen)) != len(rows) {
		t.Error("no selection should copy all")
	}
	chosen.Add(rows[1].key())
	got := copyRows(rows, &chosen)
	if len(got) != 1 || got[0].key() != rows[1].key() {
		t.Errorf("got %+v", got)
	}
}

func TestRangeCutoffAndFilters(t *testing.T) {
	now := time.Now()
	if !rangeCutoff(rangeAll, now).IsZero() {
		t.Error("all time should have no cutoff")
	}
	if got := now.Sub(rangeCutoff(0, now)); got != time.Hour {
		t.Errorf("hour cutoff = %v", got)
	}

	sv := detailsFixture()
	sv.Metrics[0].Updated = now.Add(-2 * time.Hour)
	sv.Metrics[1].Updated = now.Add(-time.Minute)
	sv.Metrics[2].Updated = now.Add(-48 * time.Hour)
	servers := []state.ServerView{sv}
	for idx, want := range map[int]int{0: 2, 1: 3, rangeAll: 4} {
		if got := len(metricRowsSince(servers, "", 0, rangeCutoff(idx, now))); got != want {
			t.Errorf("range %d = %d rows, want %d", idx, got, want)
		}
	}
}

func TestWithLastSuccess(t *testing.T) {
	lastSuccess, lastSuccessTry = map[string]time.Time{}, map[string]bool{}
	now := time.Now()
	ok := now.Add(-time.Minute)
	sv := state.ServerView{Name: "s", Metrics: []state.MetricView{{Name: "cpu", Updated: ok}}}

	got := withLastSuccess([]state.ServerView{sv}, nil, now)
	if !got[0].Metrics[0].Updated.Equal(ok) {
		t.Fatalf("healthy metric should report its collection time")
	}

	// The metric now fails: the store stamps Updated with the failure time,
	// but the last success is still what the table reports.
	sv.Metrics[0].Stale, sv.Metrics[0].Updated = true, now
	got = withLastSuccess([]state.ServerView{sv}, nil, now)
	if !got[0].Metrics[0].Updated.Equal(ok) {
		t.Errorf("stale metric Updated = %v, want last success %v", got[0].Metrics[0].Updated, ok)
	}
	if !sv.Metrics[0].Updated.Equal(now) {
		t.Error("input must not be modified")
	}

	// Stale from the start with nothing in history: unknown, not the failure time.
	fresh := state.ServerView{Name: "n", Metrics: []state.MetricView{{Name: "x", Stale: true, Updated: now}}}
	if got := withLastSuccess([]state.ServerView{fresh}, nil, now); !got[0].Metrics[0].Updated.IsZero() {
		t.Error("never-succeeded metric should have zero last success")
	}
}
