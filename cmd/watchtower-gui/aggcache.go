package main

import (
	"sort"
	"strings"
	"sync"
	"time"

	"aymanhs/watchtower/internal/storage"
)

// The window UI repaints every second, so history-backed views must never
// query per redraw. aggCache holds aggregated results keyed by (series,
// range, window), fetches them in a background goroutine (one per key at a
// time, previous data shown meanwhile), and only refreshes an entry that is
// actually being rendered — a widget nobody is looking at is never
// re-fetched.

// aggWindows are the bucket sizes pickWindow chooses from, smallest first.
var aggWindows = []time.Duration{
	15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute,
	5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 6 * time.Hour, 24 * time.Hour,
}

// targetBuckets is roughly how many points a chart line should have.
const targetBuckets = 240

// pickWindow returns the smallest standard window that keeps a range at or
// below targetBuckets points.
func pickWindow(rng time.Duration) time.Duration {
	want := rng / targetBuckets
	for _, w := range aggWindows {
		if w >= want {
			return w
		}
	}
	return aggWindows[len(aggWindows)-1]
}

const (
	minRefresh = 10 * time.Second
	maxRefresh = 5 * time.Minute
	// idleEvict drops entries no widget has asked for in this long.
	idleEvict = 10 * time.Minute
)

// refreshAfter is how old an entry may get before it is re-fetched: one
// window (a new bucket's worth of data), clamped.
func refreshAfter(window time.Duration) time.Duration {
	if window < minRefresh {
		return minRefresh
	}
	if window > maxRefresh {
		return maxRefresh
	}
	return window
}

type aggKey struct {
	names string // sorted, NUL-joined
	rng   time.Duration
}

type aggEntry struct {
	window   time.Duration
	data     map[string][]storage.Bucket
	from, to time.Time // the axis: anchored to the fetch, so it doesn't drift
	fetched  time.Time
	err      error
	loaded   bool
	loading  bool
	stale    bool // forced refresh (manual refresh icon)
	lastUsed time.Time
}

// aggView is a render's snapshot of one cache entry.
type aggView struct {
	Window   time.Duration
	Data     map[string][]storage.Bucket
	From, To time.Time
	Err      error
	Loaded   bool // false only until the first fetch completes
}

type aggFetchFunc func(names []string, from, to time.Time, window time.Duration) (map[string][]storage.Bucket, error)

type aggCache struct {
	fetch   aggFetchFunc
	repaint func() // called after a fetch completes
	now     func() time.Time

	mu      sync.Mutex
	entries map[aggKey]*aggEntry
}

func newAggCache(fetch aggFetchFunc, repaint func()) *aggCache {
	return &aggCache{fetch: fetch, repaint: repaint, now: time.Now, entries: map[aggKey]*aggEntry{}}
}

// Get returns the current view for names over rng, starting a background
// fetch if the entry is missing or due for refresh. It never blocks.
func (a *aggCache) Get(names []string, rng time.Duration) aggView {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	key := aggKey{names: strings.Join(sorted, "\x00"), rng: rng}
	now := a.now()

	a.mu.Lock()
	defer a.mu.Unlock()
	for k, e := range a.entries {
		if now.Sub(e.lastUsed) > idleEvict && !e.loading {
			delete(a.entries, k)
		}
	}
	e := a.entries[key]
	if e == nil {
		e = &aggEntry{window: pickWindow(rng)}
		a.entries[key] = e
	}
	e.lastUsed = now
	if !e.loading && (!e.loaded || e.stale || now.Sub(e.fetched) >= refreshAfter(e.window)) {
		e.loading = true
		go a.load(key, sorted, rng, e.window)
	}
	return aggView{Window: e.window, Data: e.data, From: e.from, To: e.to, Err: e.err, Loaded: e.loaded}
}

func (a *aggCache) load(key aggKey, names []string, rng, window time.Duration) {
	to := a.now()
	from := to.Add(-rng)
	data, err := a.fetch(names, from, to, window)

	a.mu.Lock()
	if e := a.entries[key]; e != nil {
		e.loading = false
		e.loaded = true
		e.stale = false
		e.fetched = to
		e.err = err
		if err == nil {
			e.data, e.from, e.to = data, from, to
		}
	}
	a.mu.Unlock()
	if a.repaint != nil {
		a.repaint()
	}
}

// Invalidate forces every entry to refresh the next time it is rendered,
// keeping the old data on screen until the new fetch lands.
func (a *aggCache) Invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.entries {
		e.stale = true
	}
}
