package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"aymanhs/watchtower/internal/dashboard"
	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Metric details tab filter state, package-level like the rest of the
// GUI's view state.
var (
	metricFilter       = ""
	metricStatusFilter = 0 // index into metricStatusLabels
	metricSelectedOnly = false
	metricTable        ui.ListState
	metricChosen       ui.Selection[string]
	metricSort         ui.SortOrder
	metricNotice       = ""
	metricRange        = rangeAll
)

var metricStatusLabels = []string{"All", "Alerting", "Stale"}

// metricRow is one measurement of one metric (or a measurement-less row
// for a metric that has produced nothing yet, so its error is still seen).
type metricRow struct {
	Server      string
	Metric      string
	Measurement string // full key, e.g. "root.used_percent"
	Instance    string
	Value       string
	Num         float64
	Status      state.Status
	Stale       bool
	Updated     time.Time
	Error       string
}

// metricRows flattens every server's metrics into filtered rows. text
// matches case-insensitively against "metric.measurement" or
// "server/metric.measurement" as a substring, or as a glob when it
// contains * or ?. statusFilter: 0 all, 1 warning/critical,
// 2 stale or failing.
func metricRows(servers []state.ServerView, text string, statusFilter int) []metricRow {
	return metricRowsSince(servers, text, statusFilter, time.Time{})
}

// metricRowsSince is metricRows keeping only rows last collected at or
// after since (the zero time keeps everything).
func metricRowsSince(servers []state.ServerView, text string, statusFilter int, since time.Time) []metricRow {
	text = strings.ToLower(strings.TrimSpace(text))
	glob := strings.ContainsAny(text, "*?")
	one := func(pattern, name string) bool {
		if glob {
			ok, _ := path.Match(pattern, name)
			return ok
		}
		return strings.Contains(name, pattern)
	}
	matches := func(server, name string) bool {
		if text == "" {
			return true
		}
		name = strings.ToLower(name)
		return one(text, name) || one(text, strings.ToLower(server)+"/"+name)
	}

	var rows []metricRow
	for _, sv := range servers {
		for _, m := range sv.Metrics {
			switch statusFilter {
			case 1:
				if m.Status != state.StatusWarning && m.Status != state.StatusCritical {
					continue
				}
			case 2:
				if !m.Stale && m.LastError == "" {
					continue
				}
			}
			if !since.IsZero() && m.Updated.Before(since) {
				continue
			}
			base := metricRow{Server: sv.Name, Metric: m.Name, Status: m.Status, Stale: m.Stale, Updated: m.Updated, Error: m.LastError}
			if len(m.Measurements) == 0 {
				if matches(sv.Name, m.Name) {
					rows = append(rows, base)
				}
				continue
			}
			for _, ms := range m.Measurements {
				if !matches(sv.Name, m.Name+"."+ms.Key) {
					continue
				}
				r := base
				r.Measurement = ms.Key
				_, r.Instance = metric.SplitInstance(ms.Key)
				r.Value, r.Num = detailValue(ms.Value), ms.Value
				rows = append(rows, r)
			}
		}
	}
	return rows
}

func (r metricRow) key() string { return r.Server + "|" + r.Metric + "|" + r.Measurement }

// selector is the exact dashboard series selector for this row.
func (r metricRow) selector() string { return r.Server + "/" + r.Metric + "." + r.Measurement }

// seriesForRows is the chosen rows as widget series, in table order.
func seriesForRows(rows []metricRow, chosen *ui.Selection[string]) []dashboard.SeriesRef {
	var out []dashboard.SeriesRef
	for _, r := range rows {
		if chosen.Has(r.key()) {
			out = append(out, dashboard.SeriesRef{Selector: r.selector()})
		}
	}
	return out
}

// addRowsToDashboard opens the regular add-widget modal on the Dashboard
// tab, pre-filled with the selected rows as series.
func addRowsToDashboard(series []dashboard.SeriesRef) {
	resetWidgetDraft()
	addWidgetSeriesPicks = series
	addWidgetOpen = true
	rightTab = tabDashboard
}

// sortMetricRows orders rows by a table column title; an empty or unknown
// column keeps the given order. Sorting is stable so ties keep server order.
func sortMetricRows(rows []metricRow, so ui.SortOrder) {
	var less func(a, b metricRow) bool
	switch so.Column {
	case "Server":
		less = func(a, b metricRow) bool { return a.Server < b.Server }
	case "Metric":
		less = func(a, b metricRow) bool { return a.Metric < b.Metric }
	case "Measurement":
		less = func(a, b metricRow) bool { return a.Measurement < b.Measurement }
	case "Value":
		less = func(a, b metricRow) bool { return a.Num < b.Num }
	case "Status":
		less = func(a, b metricRow) bool { return statusRank(a.Status) < statusRank(b.Status) }
	case "Last success", "Collected at":
		less = func(a, b metricRow) bool { return a.Updated.Before(b.Updated) }
	case "Error":
		less = func(a, b metricRow) bool { return a.Error < b.Error }
	default:
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if so.Descending {
			return less(rows[j], rows[i])
		}
		return less(rows[i], rows[j])
	})
}

func statusRank(s state.Status) int {
	switch s {
	case state.StatusCritical:
		return 3
	case state.StatusWarning:
		return 2
	case state.StatusUnknown:
		return 1
	}
	return 0
}

var exportHeader = []string{"server", "metric", "measurement", "value", "status", "collected_at", "error"}

func (r metricRow) record() []string {
	collected := ""
	if !r.Updated.IsZero() {
		collected = r.Updated.Format(time.RFC3339)
	}
	return []string{r.Server, r.Metric, r.Measurement, r.Value, string(r.Status), collected, r.Error}
}

// rowsCSV renders rows with a header as CSV.
func rowsCSV(rows []metricRow) (string, error) {
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	if err := w.Write(exportHeader); err != nil {
		return "", err
	}
	for _, r := range rows {
		if err := w.Write(r.record()); err != nil {
			return "", err
		}
	}
	w.Flush()
	return sb.String(), w.Error()
}

// rowsTSV renders rows tab-separated, no header, for pasting into a
// spreadsheet or chat.
func rowsTSV(rows []metricRow) string {
	var sb strings.Builder
	for _, r := range rows {
		sb.WriteString(strings.Join(r.record(), "\t"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// copyRows is the rows to copy: the chosen ones, or all shown if none.
func copyRows(rows []metricRow, chosen *ui.Selection[string]) []metricRow {
	if chosen.Len() == 0 {
		return rows
	}
	var out []metricRow
	for _, r := range rows {
		if chosen.Has(r.key()) {
			out = append(out, r)
		}
	}
	return out
}

// saveCSV asks for a file and writes data to it, off the UI thread because
// the dialog blocks. notify reports the outcome.
func saveCSV(prefix, data string, count int, notify func(string)) {
	go func() {
		name := prefix + "-" + time.Now().Format("20060102-150405") + ".csv"
		file, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title:       "Export CSV",
			DefaultPath: name,
			Filters:     []mygo.FileFilter{{Name: "CSV", Extensions: []string{"csv"}}},
		})
		if err != nil {
			notify("export failed: " + err.Error())
			return
		}
		if file == "" {
			return
		}
		if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
			notify("export failed: " + err.Error())
			return
		}
		notify(fmt.Sprintf("exported %d rows to %s", count, file))
	}()
}

// Time range choices shared by the Metrics and Events tabs.
var timeRangeLabels = []string{"1h", "1d", "All"}

const rangeAll = 2

// rangeCutoff is the oldest time a row may have to be shown, or the zero
// time for "All time".
func rangeCutoff(idx int, now time.Time) time.Time {
	switch idx {
	case 0:
		return now.Add(-time.Hour)
	case 1:
		return now.Add(-24 * time.Hour)
	}
	return time.Time{}
}

// detailValue prints a measurement without losing precision to a fixed
// decimal count: integers plain, others to 3 decimals.
func detailValue(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.3f", v)
}

// exactTime is the local wall-clock time of a collection, to the second.
func exactTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func ageText(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t).Round(time.Second)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// The state store's Updated is overwritten by failed collections, so the
// GUI keeps its own record of when each metric last succeeded: seen live
// while healthy, or — for a metric already stale when first seen — looked
// up once in history (the record of successful readings).
var (
	lastSuccess    = map[string]time.Time{}
	lastSuccessTry = map[string]bool{}
)

const lastSuccessLookback = 24 * time.Hour

func historyLastSuccess(history *storage.Store, server string, m state.MetricView, now time.Time) time.Time {
	var latest time.Time
	if history == nil {
		return latest
	}
	for _, ms := range m.Measurements {
		pts, err := history.Query(server+"/"+m.Name+"."+ms.Key, now.Add(-lastSuccessLookback), now)
		if err != nil || len(pts) == 0 {
			continue
		}
		if ts := pts[len(pts)-1].Timestamp; ts.After(latest) {
			latest = ts
		}
	}
	return latest
}

// withLastSuccess returns servers with each metric's Updated replaced by
// its last successful collection time (zero if unknown).
func withLastSuccess(servers []state.ServerView, history *storage.Store, now time.Time) []state.ServerView {
	out := make([]state.ServerView, len(servers))
	for i, sv := range servers {
		out[i] = sv
		out[i].Metrics = make([]state.MetricView, len(sv.Metrics))
		for j, m := range sv.Metrics {
			key := sv.Name + "/" + m.Name
			switch {
			case !m.Stale && !m.Updated.IsZero():
				lastSuccess[key] = m.Updated
			case !lastSuccessTry[key] && lastSuccess[key].IsZero():
				lastSuccessTry[key] = true
				lastSuccess[key] = historyLastSuccess(history, sv.Name, m, now)
			}
			m.Updated = lastSuccess[key]
			out[i].Metrics[j] = m
		}
	}
	return out
}

// metricDetailsPane shows every measurement the collectors gathered, across
// all servers, with a text filter, a status filter and an optional
// restriction to the sidebar-selected server.
func metricDetailsPane(c *ui.Context, all []state.ServerView, history *storage.Store) {
	t := c.Theme()
	servers := withLastSuccess(all, history, time.Now())
	if metricSelectedOnly {
		if sv, ok := findServer(servers, selectedServer); ok {
			servers = []state.ServerView{sv}
		}
	}
	rows := metricRowsSince(servers, metricFilter, metricStatusFilter, rangeCutoff(metricRange, time.Now()))
	sortMetricRows(rows, metricSort)
	metricTable.Key = func(i int) any { return rows[i].key() }
	metricTable.Selection = &metricChosen
	metricTable.Sort = &metricSort
	now := time.Now()

	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(10, 10, 6, 10).Children(func() {
			ui.TextInput(c, &metricFilter).Placeholder("filter: text or glob, e.g. web-*/disk.*used*").Width(300)
			ui.Segmented(c, &metricStatusFilter, metricStatusLabels...)
			ui.Segmented(c, &metricRange, timeRangeLabels...)
			ui.Checkbox(c, &metricSelectedOnly, "Selected server only")
			copyTip := "Copy all shown rows (tab-separated)"
			if metricChosen.Len() > 0 {
				copyTip = fmt.Sprintf("Copy %d selected rows (tab-separated)", metricChosen.Len())
			}
			if squareIconButton(c, "metrics-copy", copyIcon, copyTip).Clicked() {
				out := copyRows(rows, &metricChosen)
				mygo.Clipboard.WriteText(rowsTSV(out))
				metricNotice = fmt.Sprintf("copied %d rows", len(out))
			}
			if squareIconButton(c, "metrics-add-widget", addToDashboardIcon, "Add the selected rows to a dashboard widget").Clicked() {
				switch series := seriesForRows(rows, &metricChosen); {
				case len(series) == 0:
					metricNotice = "select rows first"
				case selectedDashboard == "":
					metricNotice = "create a dashboard first"
				default:
					addRowsToDashboard(series)
				}
			}
			if squareIconButton(c, "metrics-export", exportIcon, "Export shown rows as CSV").Clicked() {
				data, err := rowsCSV(rows)
				if err != nil {
					metricNotice = "export failed: " + err.Error()
				} else {
					saveCSV("watchtower-metrics", data, len(rows), func(m string) { metricNotice = m })
				}
			}
		})
		if len(rows) == 0 {
			ui.Box(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				ui.Text(c, "no measurements match").FontSize(11).TextColor(t.TextMuted)
			})
			statusLine(c, fmt.Sprintf("%d rows", len(rows)), metricNotice)
			return
		}
		columns := []ui.TableColumn{
			{Title: "Server", Width: 110, Sortable: true},
			{Title: "Metric", Width: 120, Sortable: true},
			{Title: "Measurement", Width: 200, Sortable: true},
			{Title: "Value", Width: 100, Sortable: true},
			{Title: "Status", Width: 80, Sortable: true},
			{Title: "Last success", Width: 90, Sortable: true},
			{Title: "Collected at", Width: 150, Sortable: true},
			{Title: "Error", Sortable: true},
		}
		ui.Table(c, &metricTable, columns, len(rows), func(row, col int) {
			r := rows[row]
			switch col {
			case 0:
				ui.Text(c, r.Server).FontSize(11)
			case 1:
				ui.Text(c, r.Metric).FontSize(11)
			case 2:
				ui.Text(c, r.Measurement).FontSize(11)
			case 3:
				ui.Text(c, r.Value).FontSize(11)
			case 4:
				ui.Text(c, string(r.Status)).FontSize(11).TextColor(statusColor(t, r.Status))
			case 5:
				ui.Text(c, ageText(r.Updated, now)).FontSize(11).TextColor(t.TextMuted)
			case 6:
				ui.Text(c, exactTime(r.Updated)).FontSize(11).TextColor(t.TextMuted)
			case 7:
				detail := r.Error
				if detail == "" && r.Stale {
					detail = "stale"
				}
				ui.Text(c, detail).FontSize(11).TextColor(t.Danger)
			}
		}).Fill()
		stats := fmt.Sprintf("%d rows", len(rows))
		if n := metricChosen.Len(); n > 0 {
			stats += fmt.Sprintf(" · %d selected", n)
		}
		statusLine(c, stats, metricNotice)
	})
}

// statusLine is the one-row footer under a table: stats on the left, the
// last action's outcome after them. It is always present, so the layout
// doesn't shift as the row count changes.
func statusLine(c *ui.Context, stats, notice string) {
	t := c.Theme()
	ui.Row(c).Gap(16).AlignItems(ui.Center).Padding(4, 10, 4, 10).Background(t.SurfaceHover).Children(func() {
		ui.Text(c, stats).FontSize(11).TextColor(t.TextMuted)
		if notice != "" {
			ui.Text(c, notice).FontSize(11).TextColor(t.TextMuted)
		}
	})
}
