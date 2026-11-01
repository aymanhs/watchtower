// Command watchtower-gui is the native GUI presentation, built on MyGo. It
// shares the exact same engine as cmd/watchtower (see internal/app) —
// only this file and view.go know mygo exists.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/app"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/scheduler"
	"aymanhs/watchtower/internal/state"
)

const maxConcurrentCollections = 16

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	events := event.NewEngine(0)
	store := state.NewStore()

	tasks, err := app.Bootstrap(ctx, app.DefaultPaths, events, store, false /* no console printing */)
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
			Content:   ui.View(func(c *ui.Context) { render(c, store) }),
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
		mygo.App.Quit()
	}()

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
