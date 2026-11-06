package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aymanhs/watchtower/internal/storage"
)

func TestPickWindow(t *testing.T) {
	for rng, want := range map[time.Duration]time.Duration{
		time.Hour:           15 * time.Second,
		6 * time.Hour:       2 * time.Minute,
		24 * time.Hour:      10 * time.Minute,
		7 * 24 * time.Hour:  time.Hour,
		30 * 24 * time.Hour: 6 * time.Hour,
	} {
		if got := pickWindow(rng); got != want {
			t.Errorf("pickWindow(%v) = %v, want %v", rng, got, want)
		}
	}
}

func TestRefreshAfterClamps(t *testing.T) {
	if refreshAfter(time.Second) != minRefresh || refreshAfter(24*time.Hour) != maxRefresh || refreshAfter(time.Minute) != time.Minute {
		t.Fatal("refreshAfter must clamp to [10s, 5m]")
	}
}

func TestAggCache_FetchesOnceInBackgroundAndRefreshesWhenDue(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{}, 8)
	var mu sync.Mutex
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cache := newAggCache(func(names []string, from, to time.Time, w time.Duration) (map[string][]storage.Bucket, error) {
		calls.Add(1)
		return map[string][]storage.Bucket{"a": {{Start: from, End: to, Count: 1}}}, nil
	}, func() { done <- struct{}{} })
	cache.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }

	v := cache.Get([]string{"b", "a"}, time.Hour)
	if v.Loaded {
		t.Fatal("first Get must not block on the fetch")
	}
	<-done
	// Repeated renders within the TTL must not fetch again, and key order is irrelevant.
	for i := 0; i < 5; i++ {
		v = cache.Get([]string{"a", "b"}, time.Hour)
	}
	if !v.Loaded || len(v.Data["a"]) != 1 || calls.Load() != 1 {
		t.Fatalf("loaded=%v calls=%d", v.Loaded, calls.Load())
	}

	mu.Lock()
	clock = clock.Add(20 * time.Second) // past the 15s window
	mu.Unlock()
	old := cache.Get([]string{"a", "b"}, time.Hour)
	if !old.Loaded {
		t.Fatal("previous data must stay visible while refreshing")
	}
	<-done
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want 2", calls.Load())
	}

	cache.Invalidate()
	cache.Get([]string{"a", "b"}, time.Hour)
	<-done
	if calls.Load() != 3 {
		t.Fatalf("Invalidate must force a refetch, calls=%d", calls.Load())
	}
}

func TestSplitAtGaps(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := time.Minute
	mk := func(min int) storage.Bucket {
		s := base.Add(time.Duration(min) * time.Minute)
		return storage.Bucket{Start: s, End: s.Add(w), Count: 1}
	}
	// Minutes 0,1,2 are contiguous; 3 and 4 are missing (a 2-window gap is
	// tolerated), 5 follows after a 3-minute hole and so starts a new run.
	runs := splitAtGaps([]storage.Bucket{mk(0), mk(1), mk(2), mk(6)}, w)
	if len(runs) != 2 || len(runs[0]) != 3 || len(runs[1]) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	if got := splitAtGaps([]storage.Bucket{mk(0), mk(3)}, w); len(got) != 1 {
		t.Fatalf("a gap of exactly two windows must not split: %+v", got)
	}
}

func TestWindowLabel(t *testing.T) {
	for d, want := range map[time.Duration]string{15 * time.Second: "15s", 5 * time.Minute: "5m", 6 * time.Hour: "6h", 30 * 24 * time.Hour: "30d"} {
		if got := windowLabel(d); got != want {
			t.Errorf("windowLabel(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestPageBounds(t *testing.T) {
	for _, tc := range []struct{ n, page, size, lo, hi, pages, clamped int }{
		{0, 0, 100, 0, 0, 1, 0},
		{250, 0, 100, 0, 100, 3, 0},
		{250, 2, 100, 200, 250, 3, 2},
		{250, 9, 100, 200, 250, 3, 2},
		{250, -1, 100, 0, 100, 3, 0},
	} {
		lo, hi, pages, clamped := pageBounds(tc.n, tc.page, tc.size)
		if lo != tc.lo || hi != tc.hi || pages != tc.pages || clamped != tc.clamped {
			t.Errorf("pageBounds(%d,%d,%d) = %d,%d,%d,%d", tc.n, tc.page, tc.size, lo, hi, pages, clamped)
		}
	}
}
