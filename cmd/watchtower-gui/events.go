package main

import (
	"encoding/csv"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"aymanhs/watchtower/internal/event"
)

var (
	eventRange      = rangeAll
	eventKindFilter = 0 // index into eventKindLabels
	eventNotice     = ""
	eventPage       = 0 // 0 is the newest page
	eventPageSig    string
)

// eventPageSize is how many events one page of the table shows.
const eventPageSize = 100

// pageBounds clamps page to [0, pages) for n rows and returns the slice
// range of that page plus the page count.
func pageBounds(n, page, size int) (lo, hi, pages, clamped int) {
	pages = (n + size - 1) / size
	if pages == 0 {
		pages = 1
	}
	clamped = max(0, min(page, pages-1))
	lo = clamped * size
	hi = min(n, lo+size)
	return lo, hi, pages, clamped
}

var eventKindLabels = []string{"All", "Thresholds", "Collector"}

// eventRow is one event plus, for raised/recovered pairs, how long the
// episode lasted: the outage window the engine's transition events bound.
type eventRow struct {
	Ev      event.Event
	Span    string // "until 11:45:02 (22m)", "after 22m" or "ongoing 7m"
	Ongoing bool
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// buildEventRows pairs each raised event with its recovery (same category,
// server and metric) across the whole history, oldest first in, newest
// first out. An escalation inside an open episode doesn't start a new one.
func buildEventRows(history []event.Event, now time.Time) []eventRow {
	rows := make([]eventRow, len(history))
	open := map[string]int{}
	for i, ev := range history {
		rows[i].Ev = ev
		key := string(ev.Category) + "|" + ev.Server + "|" + ev.Metric
		switch ev.Kind {
		case event.KindRaised:
			if _, ok := open[key]; !ok {
				open[key] = i
			}
		case event.KindRecovered:
			if j, ok := open[key]; ok {
				d := ev.Timestamp.Sub(history[j].Timestamp)
				rows[j].Span = fmt.Sprintf("until %s (%s)", ev.Timestamp.Format("15:04:05"), fmtDuration(d))
				rows[i].Span = "after " + fmtDuration(d)
				delete(open, key)
			}
		}
	}
	for _, j := range open {
		rows[j].Span = "ongoing " + fmtDuration(now.Sub(history[j].Timestamp))
		rows[j].Ongoing = true
	}
	for l, r := 0, len(rows)-1; l < r; l, r = l+1, r-1 {
		rows[l], rows[r] = rows[r], rows[l]
	}
	return rows
}

// filterEventRows keeps rows at or after cutoff (zero keeps all) of the
// chosen kind: 0 all, 1 threshold, 2 collector (operational).
func filterEventRows(rows []eventRow, cutoff time.Time, kind int) []eventRow {
	var out []eventRow
	for _, r := range rows {
		if !cutoff.IsZero() && r.Ev.Timestamp.Before(cutoff) {
			continue
		}
		if kind == 1 && r.Ev.Category != event.CategoryThreshold {
			continue
		}
		if kind == 2 && r.Ev.Category != event.CategoryOperational {
			continue
		}
		out = append(out, r)
	}
	return out
}

func eventTypeLabel(ev event.Event) string {
	if ev.Category == event.CategoryOperational {
		return "collector"
	}
	return "threshold"
}

func eventRecord(r eventRow) []string {
	return []string{r.Ev.Server, r.Ev.Timestamp.Format(time.RFC3339), eventTypeLabel(r.Ev), string(r.Ev.Kind),
		string(r.Ev.Severity), r.Ev.Metric, r.Span, r.Ev.Message}
}

func eventsCSV(rows []eventRow) (string, error) {
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	w.Write([]string{"server", "time", "type", "kind", "severity", "metric", "duration", "message"})
	for _, r := range rows {
		w.Write(eventRecord(r))
	}
	w.Flush()
	return sb.String(), w.Error()
}

func eventsTSV(rows []eventRow) string {
	var sb strings.Builder
	for _, r := range rows {
		sb.WriteString(strings.Join(eventRecord(r), "\t"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// eventsPanel lists every server's events (threshold and collector) as a
// table, most recent first, with outage durations on raised/recovered pairs.
func eventsPanel(c *ui.Context, events *event.Engine) {
	t := c.Theme()
	now := time.Now()
	shown := filterEventRows(buildEventRows(events.History(), now), rangeCutoff(eventRange, now), eventKindFilter)
	n := len(shown)

	// A different filter is a different list, so start again at the newest.
	if sig := fmt.Sprint(eventKindFilter, eventRange); sig != eventPageSig {
		eventPageSig, eventPage = sig, 0
	}
	lo, hi, pages, page := pageBounds(n, eventPage, eventPageSize)
	eventPage = page

	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(10, 10, 6, 10).Children(func() {
			ui.Segmented(c, &eventKindFilter, eventKindLabels...)
			ui.Segmented(c, &eventRange, timeRangeLabels...)
			if squareIconButton(c, "events-newer", newerIcon, "Newer events").Clicked() && eventPage > 0 {
				eventPage--
			}
			ui.Text(c, fmt.Sprintf("page %d of %d", page+1, pages)).FontSize(11).TextColor(t.TextMuted)
			if squareIconButton(c, "events-older", olderIcon, "Older events").Clicked() && eventPage < pages-1 {
				eventPage++
			}
			if squareIconButton(c, "events-copy", copyIcon, "Copy shown events (tab-separated)").Clicked() {
				mygo.Clipboard.WriteText(eventsTSV(shown))
				eventNotice = fmt.Sprintf("copied %d events", n)
			}
			if squareIconButton(c, "events-export", exportIcon, "Export shown events as CSV").Clicked() {
				if data, err := eventsCSV(shown); err != nil {
					eventNotice = "export failed: " + err.Error()
				} else {
					saveCSV("watchtower-events", data, n, func(m string) { eventNotice = m })
				}
			}
		})
		if n == 0 {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "no events in this range").FontSize(11).TextColor(t.TextMuted)
			})
			statusLine(c, "0 events", eventNotice)
			return
		}
		columns := []ui.TableColumn{
			{Title: "Server", Width: 110},
			{Title: "Time", Width: 150},
			{Title: "Type", Width: 80},
			{Title: "Duration", Width: 190},
			{Title: "Event"},
		}
		ui.Table(c, nil, columns, hi-lo, func(row, col int) {
			r := shown[lo+row]
			switch col {
			case 0:
				ui.Text(c, r.Ev.Server).FontSize(11)
			case 1:
				ui.Text(c, r.Ev.Timestamp.Format("2006-01-02 15:04:05")).FontSize(11).TextColor(t.TextMuted)
			case 2:
				ui.Text(c, eventTypeLabel(r.Ev)).FontSize(11).TextColor(t.TextMuted)
			case 3:
				txt := ui.Text(c, r.Span).FontSize(11).TextColor(t.TextMuted)
				if r.Ongoing {
					txt.TextColor(t.Danger).Bold()
				}
			case 4:
				txt := ui.Text(c, eventText(r.Ev)).FontSize(11).TextColor(eventColor(t, r.Ev))
				if r.Ev.Category == event.CategoryOperational && r.Ev.Kind == event.KindRaised {
					txt.Bold()
				}
			}
		}).Fill()
		ongoing := 0
		for _, r := range shown {
			if r.Ongoing {
				ongoing++
			}
		}
		stats := fmt.Sprintf("%d events", n)
		if ongoing > 0 {
			stats += fmt.Sprintf(" · %d ongoing", ongoing)
		}
		statusLine(c, stats, eventNotice)
	})
}

func eventColor(t *ui.Theme, ev event.Event) ui.Color {
	switch ev.Severity {
	case "warning":
		return t.Warning
	case "critical":
		return t.Danger
	}
	return t.Success
}

// eventText is the one-line description shown in the events table.
func eventText(ev event.Event) string {
	if ev.Category == event.CategoryOperational {
		if ev.Kind == event.KindRaised {
			return fmt.Sprintf("DOWN %s: %s", ev.Metric, ev.Message)
		}
		return fmt.Sprintf("UP %s: %s", ev.Metric, ev.Message)
	}
	msg := ev.Message
	if msg == "" {
		msg = fmt.Sprintf("value=%v threshold=%v", ev.Value, ev.Threshold)
	}
	return fmt.Sprintf("%s %s: %s", ev.Kind, ev.Metric, msg)
}
