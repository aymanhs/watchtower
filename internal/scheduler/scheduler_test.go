package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeTicker is driven entirely by the test sending on ch; it never fires on
// its own, so tests control ticks precisely instead of racing wall-clock timers.
type fakeTicker struct{ ch chan time.Time }

func (f *fakeTicker) C() <-chan time.Time { return f.ch }
func (f *fakeTicker) Stop()               {}

// fakeTickerRegistry hands out a fakeTicker per distinct interval and lets
// the test fetch the channel for a given interval once the scheduler has
// created it.
type fakeTickerRegistry struct {
	mu   sync.Mutex
	byIv map[time.Duration]chan time.Time
}

func newFakeTickerRegistry() *fakeTickerRegistry {
	return &fakeTickerRegistry{byIv: make(map[time.Duration]chan time.Time)}
}

func (r *fakeTickerRegistry) factory(d time.Duration) ticker {
	ch := make(chan time.Time)
	r.mu.Lock()
	r.byIv[d] = ch
	r.mu.Unlock()
	return &fakeTicker{ch: ch}
}

// channel blocks briefly waiting for the scheduler to have registered a
// ticker for interval d (it does so right after the task's first run).
func (r *fakeTickerRegistry) channel(t *testing.T, d time.Duration) chan time.Time {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		ch := r.byIv[d]
		r.mu.Unlock()
		if ch != nil {
			return ch
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no ticker registered for interval %s", d)
	return nil
}

// fakeTask records every invocation on calls, a signal, not a measurement.
type fakeTask struct {
	name     string
	interval time.Duration
	timeout  time.Duration
	calls    chan struct{}
	err      error
}

func (f *fakeTask) Name() string            { return f.name }
func (f *fakeTask) Interval() time.Duration { return f.interval }
func (f *fakeTask) Timeout() time.Duration  { return f.timeout }
func (f *fakeTask) Run(ctx context.Context) error {
	f.calls <- struct{}{}
	return f.err
}

func expectCall(t *testing.T, calls chan struct{}, what string) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func expectNoCall(t *testing.T, calls chan struct{}, what string) {
	t.Helper()
	select {
	case <-calls:
		t.Fatalf("unexpected %s", what)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestScheduler_RunsImmediatelyThenOnEachTick(t *testing.T) {
	reg := newFakeTickerRegistry()
	task := &fakeTask{name: "t1", interval: 7 * time.Second, timeout: time.Second, calls: make(chan struct{}, 4)}

	s := New(5, nil)
	s.newTicker = reg.factory

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, []Task{task}); close(done) }()

	expectCall(t, task.calls, "immediate first run")

	tick := reg.channel(t, task.interval)
	tick <- time.Now()
	expectCall(t, task.calls, "run on tick")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not shut down after context cancel")
	}
}

func TestScheduler_StopsOnContextCancelWithoutFurtherTicks(t *testing.T) {
	reg := newFakeTickerRegistry()
	task := &fakeTask{name: "t1", interval: time.Second, timeout: time.Second, calls: make(chan struct{}, 4)}

	s := New(5, nil)
	s.newTicker = reg.factory

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, []Task{task}); close(done) }()

	expectCall(t, task.calls, "immediate first run")
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not shut down after context cancel")
	}
	expectNoCall(t, task.calls, "run after cancel")
}

func TestScheduler_ReportsTaskErrorsWithoutStoppingOthers(t *testing.T) {
	reg := newFakeTickerRegistry()
	failing := &fakeTask{name: "bad", interval: time.Second, timeout: time.Second, calls: make(chan struct{}, 2), err: errors.New("boom")}
	healthy := &fakeTask{name: "good", interval: 2 * time.Second, timeout: time.Second, calls: make(chan struct{}, 2)}

	var mu sync.Mutex
	var errs []string
	s := New(5, func(name string, err error) {
		mu.Lock()
		errs = append(errs, name)
		mu.Unlock()
	})
	s.newTicker = reg.factory

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, []Task{failing, healthy}); close(done) }()

	expectCall(t, failing.calls, "failing task's immediate run")
	expectCall(t, healthy.calls, "healthy task's immediate run")

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 1 || errs[0] != "bad" {
		t.Fatalf("onError calls = %v, want exactly [bad]", errs)
	}
}

func TestScheduler_LimitsConcurrentExecutions(t *testing.T) {
	started1 := make(chan struct{})
	started2 := make(chan struct{})
	proceed := make(chan struct{})

	t1 := &blockingTask{name: "t1", interval: 10 * time.Second, timeout: 5 * time.Second, started: started1, proceed: proceed}
	t2 := &blockingTask{name: "t2", interval: 20 * time.Second, timeout: 5 * time.Second, started: started2, proceed: proceed}

	s := New(1, nil) // cap of 1: the two tasks' immediate runs must not overlap

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx, []Task{t1, t2}); close(done) }()

	var firstStarted chan struct{}
	select {
	case <-started1:
		firstStarted = started2
	case <-started2:
		firstStarted = started1
	case <-time.After(2 * time.Second):
		t.Fatal("neither task started")
	}

	select {
	case <-firstStarted:
		t.Fatal("second task started while first was still running under concurrency cap of 1")
	case <-time.After(100 * time.Millisecond):
	}

	close(proceed)
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second task never started after first released the slot")
	}
}

// blockingTask signals on started when Run begins, then waits for proceed
// to close before returning — used to observe concurrency gating directly.
type blockingTask struct {
	name              string
	interval, timeout time.Duration
	started           chan struct{}
	proceed           chan struct{}
}

func (b *blockingTask) Name() string            { return b.name }
func (b *blockingTask) Interval() time.Duration { return b.interval }
func (b *blockingTask) Timeout() time.Duration  { return b.timeout }
func (b *blockingTask) Run(ctx context.Context) error {
	close(b.started)
	<-b.proceed
	return nil
}
