// Tray wiring: the piece that lets this be a background app — the
// window starts normally, on screen, but minimizing or closing it hides
// it to the tray/notification area instead of either state stopping
// anything; collection keeps running regardless of whether the window
// is open, and a new event surfaces as a desktop notification clicking
// which opens the window straight to that server's events.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"runtime"

	"github.com/egoist/mygo"

	"aymanhs/watchtower/internal/event"
)

// quitting gates win.OnClose below: Cmd+Q/Alt+F4/the titlebar close button
// should hide the window (the collector keeps running in the tray), but
// mygo.App.Quit's own close of the same window — from the tray menu's Quit
// or Ctrl+C/SIGTERM in main.go — must still go through. Package-level for
// the same reason selectedServer is in panel.go: there's exactly one
// window and nowhere else for "are we actually quitting" to live between
// callbacks.
var quitting = false

// wireTray gives win a tray icon: left-click toggles the window (right-
// click on Linux's AppIndicator, which has no click events), closing the
// window hides it instead of quitting, and every new event fires a
// desktop notification that opens the window to that event's server.
// Returns the Tray so main can Destroy it in OnWillQuit, or nil if the
// platform has no tray.
func wireTray(win *mygo.Window, events *event.Engine) *mygo.Tray {
	show := func() {
		win.Show()
		// Show alone can leave a hidden window's last-painted frame on
		// screen until the next periodic tick (main.go's once-a-second
		// win.Update) — a real, if usually brief, delay between "the
		// window is back" and "it shows the event you just got notified
		// about". Forcing an Update right here, with selectedServer and
		// rightTab already set by the caller, means content is current
		// the instant it becomes visible, not up to a second later.
		win.Update(func() {})
	}

	// No tray (e.g. WSL without an AppIndicator library) must not stop the
	// app, and without one closing/minimizing has to behave normally —
	// hiding the window would strand it.
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon(),
		IconIsTemplate: true,
		ToolTip:        "Watchtower",
	})
	if err != nil {
		log.Printf("tray unavailable, running without it: %v", err)
		return nil
	}

	win.OnClose(func(e *mygo.CloseEvent) {
		if !quitting {
			e.PreventDefault()
			win.Hide()
		}
	})
	// Minimizing shrinks to the tray too, not just closing — Hide instead
	// of leaving it minimized in the taskbar is what "only shrink to
	// tray" means here: there's one way to tuck this app out of the way,
	// not a taskbar-minimized window and a separate hidden-to-tray state.
	win.OnMinimize(func() { win.Hide() })

	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open Watchtower", Click: func(*mygo.MenuItem, *mygo.Window) { show() }},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
	if runtime.GOOS == "linux" {
		// AppIndicator only supports menus, no click events or bounds —
		// same constraint mygo's own menubar-native example documents.
		tray.SetMenu(menu)
	} else {
		tray.OnClick(func() {
			if win.IsVisible() {
				win.Hide()
			} else {
				show()
			}
		})
		tray.OnRightClick(func() {
			tray.SetMenu(menu)
			defer tray.SetMenu(nil)
			tray.PopUpMenu()
		})
	}

	events.OnEvent(func(ev event.Event) {
		if ev.Kind != event.KindRaised {
			return // a problem clearing isn't worth a notification; it's still in the events panel
		}
		notifyEvent(ev, show)
	})

	return tray
}

// notifyEvent shows one desktop notification per event.Engine transition.
// Clicking it opens the window and jumps straight to the event's server —
// the one-glance "an event happened, open the app and look at it" flow
// the tray exists for — rather than just raising the window generically.
func notifyEvent(ev event.Event, show func()) {
	title, body := notificationText(ev)
	n := mygo.NewNotification(mygo.NotificationOptions{
		Title: title,
		Body:  body,
		Group: ev.Server,
	})
	n.OnClick(func() {
		selectedServer = ev.Server
		rightTab = tabEvents
		show()
	})
	if err := n.Show(); err != nil {
		// Denied/unsupported notifications are not fatal — the event is
		// still in the engine's History for the events panel to show.
		log.Printf("notification: %v", err)
	}
}

// notificationText renders one KindRaised Event as a title/body pair —
// wireTray only ever calls this for a raised event, a recovery not being
// worth a notification (see its OnEvent filter). Mirrors the "raised"
// half of internal/app's printEvent (who, what, how bad, and the value),
// just as a notification instead of a log line.
func notificationText(ev event.Event) (title, body string) {
	if ev.Category == event.CategoryOperational {
		return ev.Server + ": collector failing", fmt.Sprintf("%s: %s", ev.Metric, ev.Message)
	}
	return fmt.Sprintf("%s: %s", ev.Server, ev.Severity), fmt.Sprintf("%s = %v (threshold %v)", ev.Metric, ev.Value, ev.Threshold)
}

// trayIcon draws a plain monitor glyph — black on transparent so macOS
// can tint it, a visible blue elsewhere — same approach and reasoning as
// mygo's own menubar-native example's trayIcon.
func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := color.NRGBA{R: 79, G: 124, B: 255, A: 255}
	if runtime.GOOS == "darwin" {
		ink = color.NRGBA{A: 255}
	}
	for _, r := range []image.Rectangle{
		image.Rect(4, 5, 28, 22),   // screen outline (top)
		image.Rect(4, 5, 7, 22),    // screen outline (left)
		image.Rect(25, 5, 28, 22),  // screen outline (right)
		image.Rect(4, 19, 28, 22),  // screen outline (bottom)
		image.Rect(14, 22, 18, 26), // stand neck
		image.Rect(9, 26, 23, 29),  // stand base
	} {
		draw.Draw(img, r, image.NewUniform(ink), image.Point{}, draw.Src)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
