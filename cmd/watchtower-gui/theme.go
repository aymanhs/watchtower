package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

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

// themeIcon mirrors the current theme source — sun for light, moon for
// dark, a monitor for "follow the system" (the default).
func themeIcon() *ui.SVG {
	switch mygo.Theme.Source() {
	case mygo.ThemeLight:
		return sunIcon
	case mygo.ThemeDark:
		return moonIcon
	default:
		return monitorIcon
	}
}

func themeTooltip() string {
	switch mygo.Theme.Source() {
	case mygo.ThemeLight:
		return "Theme: light (click for dark)"
	case mygo.ThemeDark:
		return "Theme: dark (click to follow system)"
	default:
		return "Theme: system (click for light)"
	}
}
