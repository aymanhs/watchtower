package main

import (
	"strings"
	"testing"
	"time"

	"aymanhs/watchtower/internal/event"
)

func TestBuildEventRows_PairsAndOngoing(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return now.Add(time.Duration(-m) * time.Minute) }
	hist := []event.Event{
		{Category: event.CategoryOperational, Kind: event.KindRaised, Server: "a", Metric: "cpu", Message: "ssh: refused", Timestamp: at(60)},
		{Category: event.CategoryOperational, Kind: event.KindRecovered, Server: "a", Metric: "cpu", Message: "collector recovered", Timestamp: at(38)},
		{Category: event.CategoryThreshold, Kind: event.KindRaised, Server: "a", Metric: "disk.root", Severity: "warning", Timestamp: at(30)},
		{Category: event.CategoryThreshold, Kind: event.KindRaised, Server: "a", Metric: "disk.root", Severity: "critical", Timestamp: at(20)},
		{Category: event.CategoryThreshold, Kind: event.KindRecovered, Server: "a", Metric: "disk.root", Timestamp: at(10)},
		{Category: event.CategoryOperational, Kind: event.KindRaised, Server: "b", Metric: "cpu", Message: "timeout", Timestamp: at(7)},
	}
	rows := buildEventRows(hist, now)
	if len(rows) != 6 || !rows[0].Ev.Timestamp.Equal(at(7)) {
		t.Fatalf("not newest first: %+v", rows[0])
	}
	bySpan := map[string]eventRow{}
	for _, r := range rows {
		bySpan[r.Span] = r
	}
	if _, ok := bySpan["until "+at(38).Format("15:04:05")+" (22m)"]; !ok {
		t.Errorf("missing closed outage span: %v", bySpan)
	}
	if r, ok := bySpan["after 22m"]; !ok || r.Ev.Kind != event.KindRecovered {
		t.Errorf("missing recovery span")
	}
	if r, ok := bySpan["ongoing 7m"]; !ok || !r.Ongoing {
		t.Errorf("missing ongoing span")
	}
	// The escalation (critical) stays inside the warning episode: 30m -> 10m.
	if _, ok := bySpan["until "+at(10).Format("15:04:05")+" (20m)"]; !ok {
		t.Errorf("threshold episode span wrong: %v", bySpan)
	}
}

func TestFilterEventRowsAndExport(t *testing.T) {
	now := time.Now()
	hist := []event.Event{
		{Category: event.CategoryThreshold, Server: "a", Timestamp: now.Add(-48 * time.Hour), Message: "old"},
		{Category: event.CategoryOperational, Kind: event.KindRaised, Server: "b", Metric: "cpu", Timestamp: now.Add(-3 * time.Hour), Message: "mid"},
		{Category: event.CategoryThreshold, Server: "c", Timestamp: now.Add(-time.Minute), Message: "new, \"q\""},
	}
	rows := buildEventRows(hist, now)
	for idx, want := range map[int]int{0: 1, 1: 2, rangeAll: 3} {
		if got := len(filterEventRows(rows, rangeCutoff(idx, now), 0)); got != want {
			t.Errorf("range %d = %d, want %d", idx, got, want)
		}
	}
	if got := len(filterEventRows(rows, time.Time{}, 1)); got != 2 {
		t.Errorf("thresholds = %d, want 2", got)
	}
	if got := filterEventRows(rows, time.Time{}, 2); len(got) != 1 || got[0].Ev.Server != "b" {
		t.Errorf("collector = %+v", got)
	}
	out, err := eventsCSV(rows[:1])
	if err != nil || !strings.HasPrefix(out, "server,time,type,kind,severity,metric,duration,message\n") || !strings.Contains(out, `"new, ""q"""`) {
		t.Errorf("csv = %q err=%v", out, err)
	}
	if !strings.HasPrefix(eventsTSV(rows[:1]), "c\t") {
		t.Error("tsv should start with server")
	}
}

func TestEventText_Collector(t *testing.T) {
	d := event.Event{Category: event.CategoryOperational, Kind: event.KindRaised, Metric: "cpu", Message: "boom"}
	if got := eventText(d); got != "DOWN cpu: boom" {
		t.Errorf("got %q", got)
	}
	d.Kind = event.KindRecovered
	if got := eventText(d); !strings.HasPrefix(got, "UP cpu") {
		t.Errorf("got %q", got)
	}
}
