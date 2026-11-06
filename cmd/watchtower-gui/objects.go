package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/objstore"
	"aymanhs/watchtower/internal/state"
)

// objectStore is set once at startup; nil only before then.
var (
	objectStore *objstore.Store
	objectSel   string // chosen object name within the selected server
	objectAt    = latestLabel
	objectHist  struct {
		name   string
		newest time.Time
		labels []string
		byText map[string]objstore.Entry
	}
)

const latestLabel = "latest"

// objectsPane shows the selected server's objects: a table for a list of
// records, key/value rows for a single record, plus a picker over its
// stored changes.
func objectsPane(c *ui.Context, all []state.ServerView) {
	t := c.Theme()
	srv, ok := findServer(all, selectedServer)
	if !ok || objectStore == nil {
		ui.Text(c, "select a server to see its objects").FontSize(11).TextColor(t.TextMuted)
		return
	}
	prefix := srv.Name + "/"
	var names []string
	for _, e := range objectStore.Snapshot() {
		if strings.HasPrefix(e.Name, prefix) {
			names = append(names, strings.TrimPrefix(e.Name, prefix))
		}
	}
	if len(names) == 0 {
		ui.Text(c, "no objects collected for "+srv.Name+" yet").FontSize(11).TextColor(t.TextMuted)
		return
	}
	if !contains(names, objectSel) {
		objectSel, objectAt = names[0], latestLabel
	}
	full := prefix + objectSel
	latest, _ := objectStore.Latest(full)

	hist := objectHistory(full, latest.Time)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		prev := objectSel
		ui.Select(c, &objectSel, names)
		if objectSel != prev {
			objectAt = latestLabel
		}
		ui.Select(c, &objectAt, hist.labels)
		ui.Text(c, "collected "+ageText(latest.Time, time.Now())+" ago · "+fmt.Sprintf("%d stored changes (30d)", len(hist.labels)-1)).
			FontSize(11).TextColor(t.TextMuted).Grow(1)
	})

	shown := latest
	if e, ok := hist.byText[objectAt]; ok {
		shown = e
	}
	cols, rows := flatten(shown.Value)
	if len(rows) == 0 {
		ui.Text(c, "empty").FontSize(11).TextColor(t.TextMuted)
		return
	}
	tableCols := make([]ui.TableColumn, len(cols))
	for i, name := range cols {
		tableCols[i] = ui.TableColumn{Title: name, Width: 130}
	}
	ui.Table(c, nil, tableCols, len(rows), func(row, col int) {
		ui.Text(c, rows[row][col]).FontSize(11)
	})
}

// objectHistory caches the stored changes of name, reloading only when a
// newer value arrives or another object is chosen.
func objectHistory(name string, newest time.Time) *struct {
	name   string
	newest time.Time
	labels []string
	byText map[string]objstore.Entry
} {
	h := &objectHist
	if h.name == name && h.newest.Equal(newest) {
		return h
	}
	h.name, h.newest = name, newest
	h.labels, h.byText = []string{latestLabel}, map[string]objstore.Entry{}
	entries, _ := objectStore.History(name, time.Now().Add(-historyRetention), time.Now())
	for i := len(entries) - 1; i >= 0; i-- {
		label := entries[i].Time.Local().Format("Jan 2 15:04:05")
		h.labels = append(h.labels, label)
		h.byText[label] = entries[i]
	}
	return h
}

// flatten turns a JSON value into columns and string rows: an array of
// objects becomes one row per element, a lone object becomes key/value
// rows, anything else a single value row.
func flatten(raw json.RawMessage) (cols []string, rows [][]string) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return []string{"value"}, [][]string{{string(raw)}}
	}
	switch x := v.(type) {
	case []any:
		keys := map[string]bool{}
		for _, el := range x {
			if m, ok := el.(map[string]any); ok {
				for k := range m {
					keys[k] = true
				}
			}
		}
		if len(keys) == 0 {
			for _, el := range x {
				rows = append(rows, []string{cell(el)})
			}
			return []string{"value"}, rows
		}
		for k := range keys {
			cols = append(cols, k)
		}
		sort.Strings(cols)
		for _, el := range x {
			m, _ := el.(map[string]any)
			r := make([]string, len(cols))
			for i, k := range cols {
				r[i] = cell(m[k])
			}
			rows = append(rows, r)
		}
		return cols, rows
	case map[string]any:
		for k := range x {
			cols = append(cols, k)
		}
		sort.Strings(cols)
		for _, k := range cols {
			rows = append(rows, []string{k, cell(x[k])})
		}
		return []string{"key", "value"}, rows
	}
	return []string{"value"}, [][]string{{cell(v)}}
}

func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
