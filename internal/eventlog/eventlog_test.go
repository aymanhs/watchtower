package eventlog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"aymanhs/watchtower/internal/event"
)

func ev(id int64, ts time.Time, kind event.Kind) event.Event {
	return event.Event{ID: id, Category: event.CategoryOperational, Kind: kind, Server: "s", Metric: "cpu", Message: "boom", Timestamp: ts}
}

func TestAppendLoadAcrossDaysAndReopen(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	d1 := time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC)
	d2 := d1.Add(2 * time.Hour)
	for _, e := range []event.Event{ev(1, d1, event.KindRaised), ev(2, d2, event.KindRecovered)} {
		if err := l.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()

	l2, _ := Open(dir, 0)
	got, err := l2.Load(d1.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 || got[0].Message != "boom" || !got[1].Timestamp.Equal(d2) {
		t.Fatalf("got %+v", got)
	}
	if got, _ := l2.Load(d2); len(got) != 1 || got[0].ID != 2 {
		t.Errorf("since filter: %+v", got)
	}
}

func TestLoadSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir, 0)
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.Append(ev(1, ts, event.KindRaised))
	l.Close()
	f, _ := os.OpenFile(filepath.Join(dir, "events-2026-01-01.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{torn\n")
	f.Close()
	got, err := l.Load(ts.Add(-time.Hour))
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestApplyRetention(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir, 48*time.Hour)
	old := time.Now().UTC().AddDate(0, 0, -10)
	l.Append(ev(1, old, event.KindRaised))
	l.Append(ev(2, time.Now(), event.KindRaised))
	l.Close()
	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644)
	if err := l.ApplyRetention(); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Load(old.Add(-time.Hour))
	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("got %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Error("unrelated file removed")
	}
}
