package main

import "github.com/egoist/mygo"

// MyGo already follows the OS light/dark preference by default
// (mygo.ThemeSystem) — every color in this GUI comes from c.Theme(), never
// hardcoded, so dark mode already works with zero extra code. This adds an
// explicit, visible override for when you want the app's theme independent
// of the OS (e.g. running light-OS but wanting a dark control room).
var themeCycle = []mygo.ThemeSource{mygo.ThemeSystem, mygo.ThemeLight, mygo.ThemeDark}

func cycleTheme() {
	cur := mygo.Theme.Source()
	for i, s := range themeCycle {
		if s == cur {
			mygo.Theme.SetSource(themeCycle[(i+1)%len(themeCycle)])
			return
		}
	}
	mygo.Theme.SetSource(mygo.ThemeSystem)
}

func themeLabel() string {
	switch mygo.Theme.Source() {
	case mygo.ThemeLight:
		return "☀ light"
	case mygo.ThemeDark:
		return "☾ dark"
	default:
		return "⚙ system"
	}
}
