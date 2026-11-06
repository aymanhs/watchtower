package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/dashboard"
	"aymanhs/watchtower/internal/objstore"
	"aymanhs/watchtower/internal/selector"
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

// objectsPane shows the selected server's objects, pretty-printed and
// colored, plus a picker over their stored changes.
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
	ui.Scroll(c).Fill().Children(func() {
		jsonView(c, shown.Value)
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

// jsonView draws raw pretty-printed and syntax colored, keeping key order.
func jsonView(c *ui.Context, raw json.RawMessage) {
	t := c.Theme()
	p := &jsonPrinter{muted: t.TextMuted}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := p.value(dec, 0); err != nil {
		ui.Text(c, string(raw)).FontSize(11)
		return
	}
	ui.RichText(c, p.spans...).Font("monospace").FontSize(11.5)
}

var (
	jsonKeyColor     = ui.RGB(86, 156, 214)
	jsonStringColor  = ui.RGB(106, 168, 79)
	jsonNumberColor  = ui.RGB(230, 145, 56)
	jsonKeywordColor = ui.RGB(180, 120, 200)
)

type jsonPrinter struct {
	spans []ui.Span
	muted ui.Color
}

func (p *jsonPrinter) add(text string, col ui.Color) {
	p.spans = append(p.spans, ui.Span{Text: text, Color: col})
}

func (p *jsonPrinter) value(dec *json.Decoder, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case json.Delim:
		open, closer := "{", "}"
		if v == '[' {
			open, closer = "[", "]"
		}
		if !dec.More() {
			_, _ = dec.Token()
			p.add(open+closer, p.muted)
			return nil
		}
		p.add(open+"\n", p.muted)
		for first := true; dec.More(); first = false {
			if !first {
				p.add(",\n", p.muted)
			}
			p.add(strings.Repeat("  ", depth+1), ui.Color{})
			if v == '{' {
				k, err := dec.Token()
				if err != nil {
					return err
				}
				key, _ := json.Marshal(k)
				p.add(string(key), jsonKeyColor)
				p.add(": ", p.muted)
			}
			if err := p.value(dec, depth+1); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		p.add("\n"+strings.Repeat("  ", depth), ui.Color{})
		p.add(closer, p.muted)
	case string:
		b, _ := json.Marshal(v)
		p.add(string(b), jsonStringColor)
	case json.Number:
		p.add(v.String(), jsonNumberColor)
	case bool:
		p.add(strconv.FormatBool(v), jsonKeywordColor)
	case nil:
		p.add("null", jsonKeywordColor)
	}
	return nil
}

// objectTile is the dashboard widget: every object a series selector
// (server/object.*) matches, pretty-printed.
func objectTile(c *ui.Context, w dashboard.Widget, all []state.ServerView, idx int, maximized bool, onRemove, onToggleMaximize, onEdit func(), onReorder func(int)) {
	t := c.Theme()
	widgetCard(c, fmt.Sprintf("object-%d", idx), idx, w.EffectiveSpanX(), w.EffectiveSpanY(), widgetTitle(w), maximized, onEdit, onRemove, onToggleMaximize, onReorder, func() {
		entries := matchObjects(w, all)
		if len(entries) == 0 {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "no object matches").FontSize(11).TextColor(t.TextMuted)
			})
			return
		}
		ui.Scroll(c).Fill().Gap(8).Children(func() {
			for _, e := range entries {
				ui.Text(c, e.Name+" · "+ageText(e.Time, time.Now())+" ago").FontSize(10).TextColor(t.TextMuted)
				jsonView(c, e.Value)
			}
		})
	})
}

// matchObjects resolves w's selectors against the stored objects.
func matchObjects(w dashboard.Widget, all []state.ServerView) []objstore.Entry {
	if objectStore == nil {
		return nil
	}
	cluster := map[string]string{}
	for _, s := range all {
		cluster[s.Name] = s.Cluster
	}
	var out []objstore.Entry
	for _, e := range objectStore.Snapshot() {
		srv, def, _ := strings.Cut(e.Name, "/")
		for _, ref := range w.Series {
			sel, err := ref.Parsed()
			if err == nil && sel.Match(selector.Candidate{Server: srv, Cluster: cluster[srv], Metric: def, Measurement: "value"}) {
				out = append(out, e)
				break
			}
		}
	}
	return out
}
