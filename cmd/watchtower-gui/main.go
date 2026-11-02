// Command watchtower-gui is the native GUI presentation, built on MyGo. It
// shares the exact same engine as cmd/watchtower (see internal/app) —
// only this file and view.go know mygo exists.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/app"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/scheduler"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
)

const maxConcurrentCollections = 16

// historyRetention is how long charted history is kept before its
// partition files get deleted. GUI-only for now — the console binary has
// nothing to chart, so it doesn't open a storage.Store at all.
const historyRetention = 30 * 24 * time.Hour

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	events := event.NewEngine(0)
	store := state.NewStore()

	history, err := storage.Open("data/storage", historyRetention)
	if err != nil {
		log.Fatal(err)
	}
	defer history.Close()
	if err := history.ApplyRetention(); err != nil {
		log.Printf("history: initial retention sweep: %v", err)
	}

	tasks, shells, servers, err := app.Bootstrap(ctx, app.DefaultPaths, events, store, history, false /* no console printing */)
	if err != nil {
		log.Fatal(err)
	}

	sched := scheduler.New(maxConcurrentCollections, func(taskName string, err error) {
		log.Printf("[%s] ERROR: %v", taskName, err)
	})
	go sched.Run(ctx, tasks)

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Watchtower",
			Width:     420,
			Height:    240,
			MinWidth:  140,
			MinHeight: 60,
			Content:   ui.View(func(c *ui.Context) { render(c, store, events, shells, history, servers) }),
		})

		// Collection runs on its own schedule in the background; nudge the
		// window to redraw periodically so values and status stay current.
		// A first PoC, not a fine-grained change-notification system.
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for range ticker.C {
				win.Update(func() {})
			}
		}()
	})

	// SIGTERM, not just SIGINT (Ctrl+C): a plain `kill <pid>` sends
	// SIGTERM by default, and an uncaught one terminates the process
	// immediately — skipping history.Close()'s defer above entirely,
	// which matters now that Append no longer syncs every single write
	// (see internal/storage's doc comment): an uncaught kill could lose
	// whatever's been written since the last periodic sync. Trapping it
	// here routes a plain `kill` through the same graceful
	// cancel+Quit→Run returns→defers run path Ctrl+C already had.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
		mygo.App.Quit()
	}()

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
