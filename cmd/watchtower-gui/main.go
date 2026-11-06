// Command watchtower-gui is the native GUI presentation, built on MyGo. It
// runs the engine from internal/app —
// only the cmd/watchtower-gui package knows mygo exists.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/app"
	"aymanhs/watchtower/internal/dashboard"
	"aymanhs/watchtower/internal/demo"
	"aymanhs/watchtower/internal/event"
	"aymanhs/watchtower/internal/eventlog"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/scheduler"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
)

const maxConcurrentCollections = 16

// historyRetention is how long charted history is kept before its
// partition files get deleted.
const historyRetention = 30 * 24 * time.Hour

// initLogFile sends log output to data/watchtower.log: the Windows GUI
// build has no console, so otherwise startup errors are invisible.
func initLogFile() {
	if err := os.MkdirAll("data", 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join("data", "watchtower.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
}

func main() {
	initLogFile()
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// demoBackfill is how much synthetic history demo mode pre-populates.
const (
	demoBackfillSpan = 3 * time.Hour
	demoBackfillStep = 30 * time.Second
)

func run() error {
	demoMode := flag.Bool("demo", false, "run against a synthetic fleet (no real servers); data goes under data/demo")
	flag.Parse()

	// SetName first — RequestSingleInstanceLock's lock is keyed by app
	// name. Then the lock itself, before anything else starts (SSH
	// connects, history opens, collection schedules): a second launch
	// while one's already running should just hand off to it and exit,
	// not duplicate every connection and pile up another tray icon next
	// to the first (the actual "many tray icons" bug) — on both Windows
	// and Linux, mygo's own mechanism, not anything OS-specific here.
	mygo.App.SetName("Watchtower")
	if !mygo.App.RequestSingleInstanceLock() {
		log.Print("another Watchtower instance is already running; handing off to it")
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := event.NewEngine(0)
	store := state.NewStore()

	storageDir, dashDir := "data/storage", "data/dashboards"
	if *demoMode {
		// Demo history is regenerated every launch so it always ends "now"
		// and never mixes with (or pollutes) real history.
		storageDir, dashDir = filepath.Join("data", "demo", "storage"), filepath.Join("data", "demo", "dashboards")
		if err := os.RemoveAll(storageDir); err != nil {
			return fmt.Errorf("reset demo history: %w", err)
		}
	}

	history, err := storage.Open(storageDir, historyRetention)
	if err != nil {
		return fmt.Errorf("open history: %w", err)
	}
	defer func() {
		if err := history.Close(); err != nil {
			log.Printf("history close: %v", err)
		}
	}()
	if err := history.ApplyRetention(); err != nil {
		log.Printf("history: initial retention sweep: %v", err)
	}

	// Events persist beside metric history (same directory, same retention)
	// so outages survive restarts; seeded before any task can evaluate.
	evlog, err := eventlog.Open(storageDir, historyRetention)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	defer func() {
		if err := evlog.Close(); err != nil {
			log.Printf("event log close: %v", err)
		}
	}()
	if err := evlog.ApplyRetention(); err != nil {
		log.Printf("event log: initial retention sweep: %v", err)
	}
	if past, err := evlog.Load(time.Now().Add(-historyRetention)); err != nil {
		log.Printf("event log: load: %v", err)
	} else {
		events.Seed(past)
	}
	events.OnEvent(func(ev event.Event) {
		if err := evlog.Append(ev); err != nil {
			log.Printf("event log: %v", err)
		}
	})

	var (
		tasks  []scheduler.Task
		shells app.Shells
	)
	if *demoMode {
		defs, err := metric.LoadDefinitions(app.DefaultPaths.Metrics)
		if err != nil {
			return fmt.Errorf("load metric definitions: %w", err)
		}
		if err := demo.Backfill(history, defs, time.Now(), demoBackfillSpan, demoBackfillStep); err != nil {
			return fmt.Errorf("demo backfill: %w", err)
		}
		tasks = demo.Tasks(defs, events, store, history)
	} else {
		tasks, shells, err = app.Bootstrap(ctx, app.DefaultPaths, events, store, history, false /* no console printing */)
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
	}

	// Dashboards live in their own files now, independent of server
	// config (see internal/dashboard's package doc) — loaded here, not
	// inside app.Bootstrap, same reason history is: a GUI-only concern.
	dashStore, err := dashboard.LoadStore(dashDir)
	if err != nil {
		return fmt.Errorf("load dashboards: %w", err)
	}
	if *demoMode {
		if err := demo.SeedDashboard(dashStore); err != nil {
			return fmt.Errorf("seed demo dashboard: %w", err)
		}
	}

	sched := scheduler.New(maxConcurrentCollections, func(taskName string, err error) {
		log.Printf("[%s] ERROR: %v", taskName, err)
	})
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		sched.Run(ctx, tasks)
	}()

	// Quit can arrive from the tray menu's "Quit" (RoleQuit), the OS
	// signal handler below, or Cmd+Q — all of them must let the window's
	// own Close go through instead of wireTray's OnClose turning it into
	// a Hide (see quitting in tray.go).
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		quitting = true
		cancel()
	})
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
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					win.Update(func() {})
				}
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
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
			mygo.App.Quit()
		case <-ctx.Done():
		}
	}()

	runErr := mygo.App.Run()
	cancel()
	<-schedulerDone
	if runErr != nil {
		return fmt.Errorf("run GUI: %w", runErr)
	}
	return nil
}
