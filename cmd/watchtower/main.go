// Command watchtower is the console presentation: load configuration,
// connect over SSH, collect the resolved metrics on independent
// per-metric/per-server schedules, and print observations and event
// transitions to the console. No GUI — see cmd/watchtower-gui for that;
// both share the exact same engine via internal/app.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"aymanhs/watchtower/internal/app"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/scheduler"
)

// maxConcurrentCollections bounds how many SSH commands may be in flight at
// once across every server and metric combined. TODO: make configurable.
const maxConcurrentCollections = 16

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		cancel()
	}()

	events := event.NewEngine(0)
	tasks, _, err := app.Bootstrap(ctx, app.DefaultPaths, events, nil /* no GUI store */, nil /* no history yet for console */, true /* print */)
	if err != nil {
		log.Fatal(err)
	}

	sched := scheduler.New(maxConcurrentCollections, func(taskName string, err error) {
		log.Printf("[%s] ERROR: %v", taskName, err)
	})
	sched.Run(ctx, tasks)
	log.Println("all collectors stopped")
}
