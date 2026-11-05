package main

import (
	"testing"

	"aymanhs/watchtower/internal/dashboard"
	"aymanhs/watchtower/internal/state"
)

func twoServerFixture() []state.ServerView {
	return []state.ServerView{
		{
			Name: "jumpbox",
			Metrics: []state.MetricView{
				{Name: "cpu", Status: state.StatusWarning, Measurements: []state.MeasurementView{
					{Key: "load1", Value: 1.5},
					{Key: "usage", Value: 42},
				}},
			},
		},
		{
			Name: "other-server",
			Metrics: []state.MetricView{
				{Name: "cpu", Status: state.StatusOK, Measurements: []state.MeasurementView{
					{Key: "load1", Value: 2.5},
				}},
			},
		},
	}
}

func TestResolveTileSeries(t *testing.T) {
	all := twoServerFixture()

	w := dashboard.Widget{Series: []dashboard.SeriesRef{{Selector: "jumpbox/cpu.load1"}}}
	items := resolveTileSeries(w, all)
	if len(items) != 1 || items[0].match.Measurement.Value != 1.5 || items[0].match.MetricStatus != state.StatusWarning {
		t.Fatalf("got %+v, want exactly jumpbox/cpu.load1=1.5 warning", items)
	}

	if items := resolveTileSeries(dashboard.Widget{Series: []dashboard.SeriesRef{{Selector: "jumpbox/cpu.nonexistent"}}}, all); len(items) != 0 {
		t.Error("found a measurement that was never there")
	}
	if items := resolveTileSeries(dashboard.Widget{Series: []dashboard.SeriesRef{{Selector: "jumpbox/nonexistent.load1"}}}, all); len(items) != 0 {
		t.Error("found a measurement under a metric that was never there")
	}
	if items := resolveTileSeries(dashboard.Widget{Series: []dashboard.SeriesRef{{Selector: "not a selector"}}}, all); len(items) != 0 {
		t.Error("an unparseable selector must never match, not panic")
	}

	// A wildcard server pattern resolves against every server.
	wild := resolveTileSeries(dashboard.Widget{Series: []dashboard.SeriesRef{{Selector: "*/cpu.load1"}}}, all)
	if len(wild) != 2 {
		t.Fatalf("got %d matches, want 2 (one per server reporting cpu.load1)", len(wild))
	}
}

func TestNeedsServerQualifier(t *testing.T) {
	one := []resolvedSeries{{match: dashboard.Match{Server: "jumpbox"}}}
	if needsServerQualifier(one) {
		t.Error("one server shouldn't need qualifying")
	}
	two := []resolvedSeries{{match: dashboard.Match{Server: "jumpbox"}}, {match: dashboard.Match{Server: "other-server"}}}
	if !needsServerQualifier(two) {
		t.Error("two distinct servers should need qualifying")
	}
}

func TestMatchLabel(t *testing.T) {
	mv := state.MeasurementView{Key: "load1"} // DisplayLabel falls back to Key with no Label set
	it := resolvedSeries{ref: dashboard.SeriesRef{}, match: dashboard.Match{Server: "jumpbox", Measurement: mv}}
	if got := matchLabel(it, false); got != "load1" {
		t.Errorf("got %q, want load1", got)
	}
	if got := matchLabel(it, true); got != "jumpbox: load1" {
		t.Errorf("got %q, want jumpbox: load1", got)
	}
	withLabel := resolvedSeries{ref: dashboard.SeriesRef{Label: "Load"}, match: dashboard.Match{Server: "jumpbox", Measurement: mv}}
	if got := matchLabel(withLabel, false); got != "Load" {
		t.Errorf("got %q, want the ref's own Label override: Load", got)
	}
}

func TestAvailableSeries(t *testing.T) {
	all := []state.ServerView{
		{Name: "jumpbox", Metrics: []state.MetricView{
			{Name: "cpu", Measurements: []state.MeasurementView{{Key: "usage"}, {Key: "load1"}}},
			{Name: "memory", Measurements: []state.MeasurementView{{Key: "used"}}},
		}},
		{Name: "other-server", Metrics: []state.MetricView{
			{Name: "cpu", Measurements: []state.MeasurementView{{Key: "usage"}}},
		}},
	}
	got := availableSeries(all)
	want := []string{
		"*/cpu.usage", // reported by both servers
		"jumpbox/cpu.load1", "jumpbox/cpu.usage", "jumpbox/memory.used",
		"other-server/cpu.usage",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAvailableSeries_SuggestsCluster(t *testing.T) {
	all := []state.ServerView{
		{Name: "nats1", Cluster: "connetic-nats", Metrics: []state.MetricView{
			{Name: "nats", Measurements: []state.MeasurementView{{Key: "connections"}}},
		}},
		{Name: "nats2", Cluster: "connetic-nats", Metrics: []state.MetricView{
			{Name: "nats", Measurements: []state.MeasurementView{{Key: "connections"}}},
		}},
		{Name: "jumpbox", Metrics: []state.MetricView{ // no cluster: must not suggest one
			{Name: "cpu", Measurements: []state.MeasurementView{{Key: "usage"}}},
		}},
	}
	got := availableSeries(all)
	wantPresent := []string{"cluster:connetic-nats/nats.connections", "*/nats.connections"}
	for _, w := range wantPresent {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("availableSeries() = %v, missing %q", got, w)
		}
	}
	for _, g := range got {
		if g == "cluster:/cpu.usage" || g == "cluster:cpu.usage" {
			t.Errorf("suggested a cluster selector for a cluster-less server: %q", g)
		}
	}
}

func TestWidgetTitle(t *testing.T) {
	cases := []struct {
		w    dashboard.Widget
		want string
	}{
		{dashboard.Widget{Title: "My Title", Series: []dashboard.SeriesRef{{Selector: "jumpbox/cpu.usage"}}}, "My Title"},
		{dashboard.Widget{Series: []dashboard.SeriesRef{{Selector: "jumpbox/cpu.usage", Label: "CPU %"}}}, "CPU %"},
		{dashboard.Widget{Series: []dashboard.SeriesRef{{Selector: "jumpbox/cpu.usage"}}}, "jumpbox/cpu.usage"},
		{dashboard.Widget{Type: dashboard.WidgetChart, Series: []dashboard.SeriesRef{
			{Selector: "jumpbox/cpu.load1"}, {Selector: "jumpbox/cpu.load5"},
		}}, "chart"},
	}
	for _, tc := range cases {
		if got := widgetTitle(tc.w); got != tc.want {
			t.Errorf("widgetTitle(%+v) = %q, want %q", tc.w, got, tc.want)
		}
	}
}

func TestApplyFormat(t *testing.T) {
	cases := []struct {
		format string
		value  float64
		want   string
	}{
		{"%.0f", 23.6, "24"},
		{"%5.2fGb", 23.6, "23.60Gb"},
		{"%6.2f%%", 92.346, " 92.35%"},
		{"%d", 9.0, "9"},             // integer verb: must not print float64's "%!d(float64=9)"
		{"%d items", 9.7, "9 items"}, // rounds toward zero via int64(), not nearest
		{"%x", 255.0, "ff"},
	}
	for _, tc := range cases {
		if got := applyFormat(tc.format, tc.value); got != tc.want {
			t.Errorf("applyFormat(%q, %v) = %q, want %q", tc.format, tc.value, got, tc.want)
		}
	}
}

func TestSeriesValue_FormatOverridesDefault(t *testing.T) {
	mv := state.MeasurementView{Unit: "bytes", Value: 23.6 * (1 << 30)}
	ref := dashboard.SeriesRef{Format: "%.1f GiB custom"}
	// A bogus "value in raw bytes, printed as GiB with %.1f" format
	// wouldn't make sense without dividing first — this just proves
	// ref.Format wins over the unit-driven default, not that the division
	// happened (the format string is the caller's own responsibility).
	if got := seriesValue(ref, mv); got == formatValue(mv.Unit, mv.Format, mv.Value) {
		t.Errorf("seriesValue with an explicit Format fell back to the default formatter: %q", got)
	}

	noFormat := dashboard.SeriesRef{}
	if got, want := seriesValue(noFormat, mv), formatValue(mv.Unit, mv.Format, mv.Value); got != want {
		t.Errorf("seriesValue with no Format = %q, want the default formatter's %q", got, want)
	}
}

func TestTileSize(t *testing.T) {
	w1, h1 := tileSize(1)
	if w1 != tileWidth || h1 != tileHeight {
		t.Errorf("tileSize(1) = (%v, %v), want (%v, %v)", w1, h1, tileWidth, tileHeight)
	}
	w2, h2 := tileSize(2)
	wantW2 := tileWidth*2 + gridGap
	wantH2 := tileHeight*2 + gridGap
	if w2 != wantW2 || h2 != wantH2 {
		t.Errorf("tileSize(2) = (%v, %v), want (%v, %v) (2 cells + the gap between them)", w2, h2, wantW2, wantH2)
	}
}

// TestOpenWidgetEditor_PrefillsDraft checks both shapes: a single-series
// widget (gauge/number) populates addWidgetSinglePick/addWidgetFormat, a
// multi-series one (chart/pie) populates addWidgetSeriesPicks instead.
func TestOpenWidgetEditor_PrefillsDraft(t *testing.T) {
	gauge := dashboard.Widget{
		Type: dashboard.WidgetGauge, Title: "Mem", Span: 2,
		Series: []dashboard.SeriesRef{{Selector: "jumpbox/memory.free", Format: "%.1f GiB"}},
	}
	openWidgetEditor(3, gauge)
	if editWidgetIdx != 3 {
		t.Errorf("editWidgetIdx = %d, want 3", editWidgetIdx)
	}
	if addWidgetType != dashboard.WidgetGauge || addWidgetTitle != "Mem" || addWidgetSpan != 2 {
		t.Errorf("draft = type:%v title:%q span:%d, want gauge/Mem/2", addWidgetType, addWidgetTitle, addWidgetSpan)
	}
	if addWidgetSinglePick != "jumpbox/memory.free" || addWidgetFormat != "%.1f GiB" {
		t.Errorf("single-series draft = pick:%q format:%q, want jumpbox/memory.free / %%.1f GiB", addWidgetSinglePick, addWidgetFormat)
	}

	chart := dashboard.Widget{
		Type: dashboard.WidgetChart,
		Series: []dashboard.SeriesRef{
			{Selector: "jumpbox/cpu.load1"},
			{Selector: "jumpbox/cpu.load5"},
		},
	}
	openWidgetEditor(0, chart)
	if len(addWidgetSeriesPicks) != 2 || addWidgetSeriesPicks[0] != "jumpbox/cpu.load1" || addWidgetSeriesPicks[1] != "jumpbox/cpu.load5" {
		t.Errorf("multi-series draft = %v, want [jumpbox/cpu.load1 jumpbox/cpu.load5]", addWidgetSeriesPicks)
	}
	if addWidgetSinglePick != "" {
		t.Errorf("multi-series edit left a stale single-series pick: %q", addWidgetSinglePick)
	}
}

// TestConfirmAddWidget_EditReplacesInPlace reproduces the real edit flow
// end to end against a real file, same pattern internal/server's own
// SaveDashboard tests use: editing widget 0 of 2 must replace it, not
// append a third.
// TestReorderWidget covers both directions of the index-shift arithmetic
// (dragging a widget later vs. earlier in the list) plus the no-op
// guards — this is exactly the kind of off-by-one that's easy to get
// wrong once an element is removed before being reinserted.
func TestReorderWidget(t *testing.T) {
	widget := func(title string) dashboard.Widget {
		return dashboard.Widget{Type: dashboard.WidgetNumber, Title: title, Series: []dashboard.SeriesRef{{Selector: "example/cpu.usage"}}}
	}
	titles := func(ws []dashboard.Widget) []string {
		out := make([]string, len(ws))
		for i, w := range ws {
			out[i] = w.Title
		}
		return out
	}
	newDash := func(t *testing.T, widgets []dashboard.Widget) (*dashboard.Store, *dashboard.Dashboard) {
		t.Helper()
		s, err := dashboard.LoadStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		d := &dashboard.Dashboard{Name: "example", Widgets: widgets}
		if err := s.Save(d); err != nil {
			t.Fatal(err)
		}
		return s, d
	}

	t.Run("move later", func(t *testing.T) {
		s, d := newDash(t, []dashboard.Widget{widget("A"), widget("B"), widget("C"), widget("D")})
		reorderWidget(s, d, 0, 2) // drag A onto C's spot
		if got := titles(d.Widgets); !slicesEqual(got, []string{"B", "C", "A", "D"}) {
			t.Fatalf("got %v, want [B C A D]", got)
		}
	})

	t.Run("move earlier", func(t *testing.T) {
		s, d := newDash(t, []dashboard.Widget{widget("A"), widget("B"), widget("C"), widget("D")})
		reorderWidget(s, d, 2, 0) // drag C onto A's spot
		if got := titles(d.Widgets); !slicesEqual(got, []string{"C", "A", "B", "D"}) {
			t.Fatalf("got %v, want [C A B D]", got)
		}
	})

	t.Run("drop on self is a no-op", func(t *testing.T) {
		s, d := newDash(t, []dashboard.Widget{widget("A"), widget("B")})
		reorderWidget(s, d, 1, 1)
		if got := titles(d.Widgets); !slicesEqual(got, []string{"A", "B"}) {
			t.Fatalf("got %v, want unchanged [A B]", got)
		}
	})

	t.Run("out of range is a no-op", func(t *testing.T) {
		s, d := newDash(t, []dashboard.Widget{widget("A"), widget("B")})
		reorderWidget(s, d, 5, 0)
		if got := titles(d.Widgets); !slicesEqual(got, []string{"A", "B"}) {
			t.Fatalf("got %v, want unchanged [A B]", got)
		}
	})
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestConfirmAddWidget_EditReplacesInPlace(t *testing.T) {
	dashStore, err := dashboard.LoadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := &dashboard.Dashboard{Name: "example", Widgets: []dashboard.Widget{
		{Type: dashboard.WidgetNumber, Title: "Old", Series: []dashboard.SeriesRef{{Selector: "example/cpu.usage"}}},
		{Type: dashboard.WidgetNumber, Title: "Keep", Series: []dashboard.SeriesRef{{Selector: "example/memory.free"}}},
	}}
	if err := dashStore.Save(d); err != nil {
		t.Fatal(err)
	}

	resetWidgetDraft()
	editWidgetIdx = 0
	addWidgetType = dashboard.WidgetNumber
	addWidgetTitle = "New"
	addWidgetSinglePick = "example/cpu.usage"

	confirmAddWidget(d, dashStore)
	if addWidgetErr != "" {
		t.Fatalf("confirmAddWidget: %s", addWidgetErr)
	}
	got, ok := dashStore.Get("example")
	if !ok {
		t.Fatal("dashboard \"example\" not found in store")
	}
	if len(got.Widgets) != 2 {
		t.Fatalf("got %d widgets, want 2 (edit must replace, not append)", len(got.Widgets))
	}
	if got.Widgets[0].Title != "New" {
		t.Errorf("widget 0 title = %q, want New", got.Widgets[0].Title)
	}
	if got.Widgets[1].Title != "Keep" {
		t.Errorf("widget 1 title = %q, want Keep (untouched)", got.Widgets[1].Title)
	}
}

func TestBuildPieSlice_FullCircleCloses(t *testing.T) {
	// A degenerate but real check: a 0-degree sweep must not panic and
	// must produce a closed (zero-area) path rather than an open one a
	// FillPath call would otherwise render unpredictably.
	p := buildPieSlice(50, 50, 40, -90, 0)
	if p == nil {
		t.Fatal("buildPieSlice returned nil")
	}
	// A non-degenerate slice should not panic either, across the
	// segment-splitting boundary (>45 degrees needs >1 Bezier segment).
	p2 := buildPieSlice(50, 50, 40, -90, 270)
	if p2 == nil {
		t.Fatal("buildPieSlice returned nil for a 270-degree slice")
	}
}
