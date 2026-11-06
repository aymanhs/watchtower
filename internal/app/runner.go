package app

import (
	"context"
	"fmt"
	"sync"

	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/scheduler"
	"aymanhs/watchtower/internal/server"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
)

// Runner owns the running collection: it bootstraps from the config on
// disk, runs the scheduler, and can swap the whole thing for a freshly
// loaded one while the process keeps running (live reload after servers
// are added, edited or removed).
type Runner struct {
	Paths         Paths
	Events        *event.Engine
	Store         *state.Store
	History       *storage.Store
	MaxConcurrent int
	OnError       scheduler.ErrorHandler

	mu     sync.Mutex
	parent context.Context
	cancel context.CancelFunc
	done   chan struct{}
	shells Shells
}

// Start loads the configuration and begins collecting until parent ends.
func (r *Runner) Start(parent context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parent = parent
	return r.swapLocked()
}

// Reload re-reads the configuration. The new configuration is bootstrapped
// before the old run is stopped, so one that fails to load leaves the
// running collection untouched and returns the error.
func (r *Runner) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.parent == nil {
		return fmt.Errorf("runner not started")
	}
	return r.swapLocked()
}

func (r *Runner) swapLocked() error {
	ctx, cancel := context.WithCancel(r.parent)
	tasks, shells, err := Bootstrap(ctx, r.Paths, r.Events, r.Store, r.History)
	if err != nil {
		cancel()
		return err
	}
	r.stopLocked()
	if cfgs, err := server.LoadServers(r.Paths.Servers); err == nil {
		names := make([]string, len(cfgs))
		for i, c := range cfgs {
			names[i] = c.Name
		}
		r.Store.Retain(names)
	}
	done := make(chan struct{})
	sched := scheduler.New(r.MaxConcurrent, r.OnError)
	go func() {
		defer close(done)
		sched.Run(ctx, tasks)
	}()
	r.cancel, r.done, r.shells = cancel, done, shells
	return nil
}

func (r *Runner) stopLocked() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
	r.shells.Close()
	r.cancel, r.done, r.shells = nil, nil, nil
}

// Shells returns the current run's SSH clients, for interactive terminals.
func (r *Runner) Shells() Shells {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.shells
}

// Wait blocks until the current run has stopped.
func (r *Runner) Wait() {
	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}
