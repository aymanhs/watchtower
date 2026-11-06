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
	"aymanhs/watchtower/internal/dashboard"
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
	// SetName first — RequestSingleInstanceLock's lock is keyed by app
	// name. Then the lock itself, before anything else starts (SSH
	// connects, history opens, collection schedules): a second launch
	// while one's already running should just hand off to it and exit,
	// not duplicate every connection and pile up another tray icon next
	// to the first (the actual "many tray icons" bug) — on both Windows
	// and Linux, mygo's own mechanism, not anything OS-specific here.
	mygo.App.SetName("Watchtower")
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

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

	tasks, shells, _, err := app.Bootstrap(ctx, app.DefaultPaths, events, store, history, false /* no console printing */)
	if err != nil {
		log.Fatal(err)
	}

	// Dashboards live in their own files now, independent of server
	// config (see internal/dashboard's package doc) — loaded here, not
	// inside app.Bootstrap, same reason history is: a GUI-only concern
	// the console binary has no use for at all.
	dashStore, err := dashboard.LoadStore("data/dashboards")
	if err != nil {
		log.Fatal(err)
	}

	sched := scheduler.New(maxConcurrentCollections, func(taskName string, err error) {
		log.Printf("[%s] ERROR: %v", taskName, err)
	})
	go sched.Run(ctx, tasks)

	// Quit can arrive from the tray menu's "Quit" (RoleQuit), the OS
	// signal handler below, or Cmd+Q — all of them must let the window's
	// own Close go through instead of wireTray's OnClose turning it into
	// a Hide (see quitting in tray.go).
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting = true })
	mygo.App.WhenReady(func() {
		// var, not :=: the Content closure below refers to win from
		// inside the very statement that would otherwise declare it —
		// win's scope with := wouldn't begin until after that statement,
		// so the closure (which only actually runs later, once the
		// window exists) needs it declared first instead.
		var win *mygo.Window
		win = mygo.NewWindow(mygo.WindowOptions{
			Title: "Watchtower",
			// Large enough on first launch to actually see a dashboard
			// grid (several tiles) at once, not just its top-left
			// corner — the old 420x240 was sized for the toolbar alone,
			// from before dashboards existed.
			Width:     1200,
			Height:    800,
			MinWidth:  140,
			MinHeight: 60,
			// Starts as a normal, visible window — only minimizing or
			// closing it shrinks it down to the tray (wireTray's
			// OnMinimize/OnClose), never the other way round at launch.
			Content: ui.View(func(c *ui.Context) { render(c, win, store, events, shells, history, dashStore) }),
		})

		tray := wireTray(win, events)
		mygo.App.OnWillQuit(func(*mygo.QuitEvent) { tray.Destroy() })

		// A second launch (RequestSingleInstanceLock returned false
		// there) means someone tried to start Watchtower again while
		// it's already running — bring this one to the front rather
		// than silently doing nothing, so it doesn't look like the
		// second launch failed.
		mygo.App.OnSecondInstance(func(args []string, workingDir string) {
			win.Show()
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
