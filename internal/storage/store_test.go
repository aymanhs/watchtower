package storage

import (
	"testing"
	"time"
)

func TestStore_AppendAndQuery(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		if err := s.Append("jumpbox/cpu.usage", ts, float64(i)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	points, err := s.Query("jumpbox/cpu.usage", base, base.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 5 {
		t.Fatalf("got %d points, want 5", len(points))
	}
	for i, p := range points {
		if p.Value != float64(i) {
			t.Errorf("points[%d].Value = %v, want %v", i, p.Value, i)
		}
		if !p.Timestamp.Equal(base.Add(time.Duration(i) * time.Minute)) {
			t.Errorf("points[%d].Timestamp = %v, want %v", i, p.Timestamp, base.Add(time.Duration(i)*time.Minute))
		}
	}
}

func TestStore_QueryUnknownMetricReturnsNilNotError(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	points, err := s.Query("never/seen.metric", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("Query on unknown metric should not error, got: %v", err)
	}
	if points != nil {
		t.Fatalf("got %v, want nil", points)
	}
}

func TestStore_TimeRangeFiltersCorrectly(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 24; i++ {
		s.Append("m", base.Add(time.Duration(i)*time.Hour), float64(i))
	}

	// Ask for just hours 10..14 inclusive.
	points, err := s.Query("m", base.Add(10*time.Hour), base.Add(14*time.Hour))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 5 {
		t.Fatalf("got %d points, want 5 (hours 10-14 inclusive)", len(points))
	}
	if points[0].Value != 10 || points[len(points)-1].Value != 14 {
		t.Fatalf("got first=%v last=%v, want first=10 last=14", points[0].Value, points[len(points)-1].Value)
	}
}

func TestStore_SpansMultiplePartitionFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	day1 := time.Date(2026, 6, 1, 23, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 6, 2, 1, 0, 0, 0, time.UTC)
	s.Append("m", day1, 1)
	s.Append("m", day2, 2)

	points, err := s.Query("m", day1.Add(-time.Hour), day2.Add(time.Hour))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2 (one from each day's partition file)", len(points))
	}
	if points[0].Value != 1 || points[1].Value != 2 {
		t.Fatalf("got %+v, want ordered [1, 2] across the day boundary", points)
	}
}

func TestStore_DifferentMetricsDontCollide(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	s.Append("jumpbox/cpu.usage", now, 11)
	s.Append("jumpbox/memory.used_percent", now, 22)
	s.Append("sbnats/cpu.usage", now, 33)

	cpu, _ := s.Query("jumpbox/cpu.usage", now.Add(-time.Minute), now.Add(time.Minute))
	if len(cpu) != 1 || cpu[0].Value != 11 {
		t.Fatalf("jumpbox/cpu.usage = %+v, want one point valued 11", cpu)
	}
	mem, _ := s.Query("jumpbox/memory.used_percent", now.Add(-time.Minute), now.Add(time.Minute))
	if len(mem) != 1 || mem[0].Value != 22 {
		t.Fatalf("jumpbox/memory.used_percent = %+v, want one point valued 22", mem)
	}
}

// TestStore_SyncsAfterEveryNWrites proves the count-based threshold: a
// write below syncEvery leaves pendingWrites outstanding (no fsync yet),
// the one that reaches it resets pendingWrites back to 0 (a sync just
// happened).
func TestStore_SyncsAfterEveryNWrites(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	s.syncEvery = 3
	s.syncInterval = time.Hour // effectively disabled for this test

	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if err := s.Append("m", now, float64(i)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if s.pendingWrites != 2 {
		t.Fatalf("pendingWrites = %d, want 2 (below syncEvery, no sync yet)", s.pendingWrites)
	}
	if err := s.Append("m", now, 2); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if s.pendingWrites != 0 {
		t.Fatalf("pendingWrites = %d, want 0 (3rd write hit syncEvery=3, must have synced and reset)", s.pendingWrites)
	}
}

// TestStore_SyncsAfterInterval proves the time-based threshold using the
// injectable clock: advancing it past syncInterval, with syncEvery
// effectively disabled, must still trigger a sync on the next write.
func TestStore_SyncsAfterInterval(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	s.syncEvery = 1000 // effectively disabled for this test
	s.syncInterval = time.Minute
	fakeNow := time.Now().UTC()
	s.now = func() time.Time { return fakeNow }
	s.lastSync = fakeNow

	ts := time.Now().UTC()
	if err := s.Append("m", ts, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if s.pendingWrites != 1 {
		t.Fatalf("pendingWrites = %d, want 1 (interval not elapsed yet)", s.pendingWrites)
	}

	fakeNow = fakeNow.Add(2 * time.Minute) // advance the fake clock past syncInterval
	if err := s.Append("m", ts, 2); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if s.pendingWrites != 0 {
		t.Fatalf("pendingWrites = %d, want 0 (interval elapsed, must have synced and reset)", s.pendingWrites)
	}
}

// TestStore_CloseSyncsEvenBelowThresholds is the real-world proof, not
// just internal counters: a write that never reached syncEvery or
// syncInterval still must not be lost on a graceful shutdown — Close
// syncs unconditionally.
func TestStore_CloseSyncsEvenBelowThresholds(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.syncEvery = 1000
	s.syncInterval = time.Hour

	now := time.Now().UTC()
	if err := s.Append("m", now, 7); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if s.pendingWrites == 0 {
		t.Fatal("test setup: expected an outstanding unsynced write before Close")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	points, err := s2.Query("m", now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Query after reopen: %v", err)
	}
	if len(points) != 1 || points[0].Value != 7 {
		t.Fatalf("got %+v after reopen, want the one point (value 7) Close should have synced despite being below both thresholds", points)
	}
}

func TestStore_RecoversAfterReopen(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	s1, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s1.Append("m", now, 42); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A fresh Store, as a restarted process would create, must see data
	// the previous instance wrote and synced — no WAL replay needed
	// because every Append was already durable when it returned.
	s2, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	points, err := s2.Query("m", now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Query after reopen: %v", err)
	}
	if len(points) != 1 || points[0].Value != 42 {
		t.Fatalf("got %+v after reopen, want one point valued 42", points)
	}
}

func TestStore_CatalogPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id1, err := s1.catalog.idFor("jumpbox/cpu.usage")
	if err != nil {
		t.Fatalf("idFor: %v", err)
	}
	s1.Close()

	s2, err := Open(dir, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	id2, ok := s2.catalog.lookupID("jumpbox/cpu.usage")
	if !ok {
		t.Fatal("catalog entry lost across reopen")
	}
	if id1 != id2 {
		t.Fatalf("id changed across reopen: %d -> %d", id1, id2)
	}

	// A brand new name after reopen must get a different ID, not reuse 1.
	id3, err := s2.catalog.idFor("jumpbox/memory.used_percent")
	if err != nil {
		t.Fatalf("idFor: %v", err)
	}
	if id3 == id2 {
		t.Fatalf("new metric got the same ID (%d) as an existing one", id3)
	}
}

func TestStore_ApplyRetention(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 24*time.Hour) // keep only the last day
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	old := time.Now().UTC().AddDate(0, 0, -10)
	recent := time.Now().UTC()
	s.Append("m", old, 1)
	s.Append("m", recent, 2)

	if err := s.ApplyRetention(); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}

	points, err := s.Query("m", old.Add(-time.Hour), recent.Add(time.Hour))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 1 || points[0].Value != 2 {
		t.Fatalf("got %+v after retention, want only the recent point (value 2)", points)
	}
}

func TestStore_RetentionDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 0) // <=0: no automatic deletion
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	old := time.Now().UTC().AddDate(-1, 0, 0)
	s.Append("m", old, 1)
	if err := s.ApplyRetention(); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	points, err := s.Query("m", old.Add(-time.Hour), old.Add(time.Hour))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want the year-old point still present (retention disabled)", len(points))
	}
}

func TestDatesBetween(t *testing.T) {
	from := time.Date(2026, 6, 1, 23, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 3, 1, 0, 0, 0, time.UTC)
	got := datesBetween(from, to)
	want := []string{"2026-06-01", "2026-06-02", "2026-06-03"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
