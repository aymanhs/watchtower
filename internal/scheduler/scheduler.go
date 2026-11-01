// Package scheduler runs independently-scheduled tasks on their own
// intervals, under a global concurrency cap. It is deliberately generic: it
// has no notion of metrics, servers or SSH — a Task is just something with
// a name, an interval, a timeout and a Run method. This keeps the core
// collection engine's scheduling logic independent of what it schedules.
package scheduler

import (
	"context"
	"sync"
	"time"
)

// Task is one independently schedulable unit of work.
type Task interface {
	Name() string
	Interval() time.Duration
	Timeout() time.Duration
	Run(ctx context.Context) error
}

// ErrorHandler is invoked whenever a task's Run returns an error. It must
// return quickly; it runs on the task's own goroutine and a slow handler
// delays that task's next tick.
type ErrorHandler func(taskName string, err error)

// Scheduler runs a set of Tasks, each on its own ticker in its own
// goroutine, so one task blocking, failing or timing out never affects
// another. A shared semaphore bounds how many Run calls may execute at
// once across all tasks.
type Scheduler struct {
	sem       chan struct{}
	newTicker tickerFactory
	onError   ErrorHandler
}

// New returns a Scheduler that allows at most maxConcurrent Task.Run calls
// to execute concurrently. maxConcurrent < 1 is treated as 1. onError may
// be nil.
func New(maxConcurrent int, onError ErrorHandler) *Scheduler {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if onError == nil {
		onError = func(string, error) {}
	}
	return &Scheduler{
		sem:       make(chan struct{}, maxConcurrent),
		newTicker: realTickerFactory,
		onError:   onError,
	}
}

// Run starts every task — each executes once immediately, then again on
// every tick of its own Interval, bounded by its own Timeout — and blocks
// until ctx is cancelled and all tasks have returned from whatever they
// were doing.
func (s *Scheduler) Run(ctx context.Context, tasks []Task) {
	var wg sync.WaitGroup
	for _, task := range tasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runTask(ctx, task)
		}()
	}
	wg.Wait()
}

func (s *Scheduler) runTask(ctx context.Context, task Task) {
	execute := func() {
		select {
		case s.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-s.sem }()

		runCtx, cancel := context.WithTimeout(ctx, task.Timeout())
		defer cancel()
		if err := task.Run(runCtx); err != nil {
			s.onError(task.Name(), err)
		}
	}

	execute() // first collection happens immediately, not after a full interval
	t := s.newTicker(task.Interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			execute()
		}
	}
}
