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
	mv := state.MeasurementView{Key: "load1"}
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

func TestBarLabel(t *testing.T) {
	mv := state.MeasurementView{Key: "used_percent"}

	// Instance (e.g. a disk's mount point) wins over the measurement's
	// own generic label — the whole point of a bars widget.
	withInstance := resolvedSeries{ref: dashboard.SeriesRef{}, match: dashboard.Match{Server: "jumpbox", Instance: "root", Measurement: mv}}
	if got := barLabel(withInstance, false); got != "root" {
		t.Errorf("got %q, want root (the instance)", got)
	}
	if got := barLabel(withInstance, true); got != "jumpbox: root" {
		t.Errorf("got %q, want jumpbox: root (server-qualified)", got)
	}

	// No instance: falls back to the measurement's own label, same as
	// matchLabel.
	noInstance := resolvedSeries{ref: dashboard.SeriesRef{}, match: dashboard.Match{Server: "jumpbox", Measurement: mv}}
	if got := barLabel(noInstance, false); got != "used_percent" {
		t.Errorf("got %q, want used_percent (measurement key fallback)", got)
	}

	// ref.Label still wins outright over the instance.
	withOverride := resolvedSeries{ref: dashboard.SeriesRef{Label: "Volume"}, match: dashboard.Match{Server: "jumpbox", Instance: "root", Measurement: mv}}
	if got := barLabel(withOverride, false); got != "Volume" {
		t.Errorf("got %q, want the ref's own Label override: Volume", got)
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
		// Every position (server, metric) varies independently —
		// "*/*.x" (both wildcarded) included — so typing a "*" into
		// either slot, or both at once, always has something to
		// autocomplete toward, regardless of how many servers currently
		// share a given measurement.
		"*/*.load1", "*/*.usage", "*/*.used",
		"*/cpu.load1", "*/cpu.usage",
		"*/memory.used",
		"jumpbox/*.load1", "jumpbox/*.usage", "jumpbox/*.used",
		"jumpbox/cpu.load1", "jumpbox/cpu.usage",
		"jumpbox/memory.used",
		"other-server/*.usage",
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

func TestDefaultSeriesPick(t *testing.T) {
	all := []state.ServerView{
		{Name: "jumpbox", Metrics: []state.MetricView{
			{Name: "disk", Measurements: []state.MeasurementView{
				{Key: "root.total"},
				{Key: "root.used_percent"},
			}},
		}},
	}
	// A fixed, hand-built options list (not availableSeries' own output,
	// which generates several suggestions per measurement and isn't what
	// this test is about). There's no more per-measurement Unit to prefer
	// a percent-scaled option by (see internal/metric's package doc), so
	// every widget type just gets whatever sorts first.
	options := []string{"jumpbox/disk.root.total", "jumpbox/disk.root.used_percent"}

	if got := defaultSeriesPick(options, all, dashboard.WidgetBars); got != options[0] {
		t.Errorf("bars: got %q, want the plain first option %q", got, options[0])
	}
	if got := defaultSeriesPick(options, all, dashboard.WidgetGauge); got != options[0] {
		t.Errorf("gauge: got %q, want the plain first option %q", got, options[0])
	}
	if got := defaultSeriesPick(options, all, dashboard.WidgetNumber); got != options[0] {
		t.Errorf("number: got %q, want the plain first option %q", got, options[0])
	}
	if got := defaultSeriesPick(nil, all, dashboard.WidgetBars); got != "" {
		t.Errorf("got %q, want \"\" for no options at all", got)
	}
}

func TestAvailableSeries_SuggestsInstanceWildcard(t *testing.T) {
	all := []state.ServerView{
		{Name: "disk-server", Metrics: []state.MetricView{
			{Name: "disk", Measurements: []state.MeasurementView{
				{Key: "root.used_percent"},
				{Key: "data.used_percent"},
			}},
		}},
	}
	got := availableSeries(all)
	wantPresent := []string{
		"disk-server/disk.root.used_percent", // exact, per-instance
		"disk-server/disk.data.used_percent", // exact, per-instance
		"disk-server/disk.*.used_percent",    // this server, any instance — an Autocomplete can reach by typing toward it
		"disk-server/disk.used_percent",      // equivalent 2-segment form (selector.Match is instance-agnostic with no instance segment)
		// Server and instance wildcarded together — "every volume on
		// every server" is exactly as real a thing to type toward as
		// either wildcarded alone, and needs its own suggestion since
		// an Autocomplete can't combine two single-wildcard matches on
		// its own.
		"*/disk.*.used_percent",
		"*/disk.used_percent",
		// Metric name wildcarded too (rarer, but the same reasoning
		// applies: "any metric's used_percent, any instance, any server").
		"*/*.used_percent",
	}
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
	mv := state.MeasurementView{Value: 23.6 * (1 << 30)}
	ref := dashboard.SeriesRef{Format: "%.1f GiB custom"}
	// Proves ref.Format wins over the plain default formatter, not that
	// any unit-driven division happened (the format string is the
	// caller's own responsibility — there's no Unit left to divide by,
	// see internal/metric's package doc).
	if got := seriesValue(ref, mv); got == formatValue(mv.Value) {
		t.Errorf("seriesValue with an explicit Format fell back to the default formatter: %q", got)
	}

	noFormat := dashboard.SeriesRef{}
	if got, want := seriesValue(noFormat, mv), formatValue(mv.Value); got != want {
		t.Errorf("seriesValue with no Format = %q, want the default formatter's %q", got, want)
	}
}

func TestTileSize(t *testing.T) {
	w1, h1 := tileSize(1, 1)
	if w1 != tileWidth || h1 != tileHeight {
		t.Errorf("tileSize(1, 1) = (%v, %v), want (%v, %v)", w1, h1, tileWidth, tileHeight)
	}
	w2, h2 := tileSize(2, 2)
	wantW2 := tileWidth*2 + gridGap
	wantH2 := tileHeight*2 + gridGap
	if w2 != wantW2 || h2 != wantH2 {
		t.Errorf("tileSize(2, 2) = (%v, %v), want (%v, %v) (2 cells + the gap between them)", w2, h2, wantW2, wantH2)
	}
	// Independent directions: a wide-short tile isn't forced square.
	w3, h3 := tileSize(3, 1)
	wantW3 := tileWidth*3 + gridGap*2
	if w3 != wantW3 || h3 != tileHeight {
		t.Errorf("tileSize(3, 1) = (%v, %v), want (%v, %v)", w3, h3, wantW3, tileHeight)
	}
}

func TestMinSpan(t *testing.T) {
	cases := []struct{ x, y, want int }{
		{1, 1, 1},
		{3, 1, 1},
		{1, 3, 1},
		{2, 5, 2},
	}
	for _, tc := range cases {
		if got := minSpan(tc.x, tc.y); got != tc.want {
			t.Errorf("minSpan(%d, %d) = %d, want %d", tc.x, tc.y, got, tc.want)
		}
	}
}

func TestResetWidgetDraft_ClearsSeriesPicks(t *testing.T) {
	addWidgetSeriesPicks = []dashboard.SeriesRef{{Selector: "a"}, {Selector: "b"}, {Selector: "c"}}
	resetWidgetDraft()
	if addWidgetSeriesPicks != nil {
		t.Errorf("addWidgetSeriesPicks = %v, want nil after reset", addWidgetSeriesPicks)
	}
}

// TestOpenWidgetEditor_PrefillsDraft checks every widget type now prefills
// the same way: w.Series copies straight into addWidgetSeriesPicks,
// Format included — there's no more gauge/number-only single-pick shape.
func TestOpenWidgetEditor_PrefillsDraft(t *testing.T) {
	gauge := dashboard.Widget{
		Type: dashboard.WidgetGauge, Title: "Mem", Span: 2,
		Series: []dashboard.SeriesRef{{Selector: "jumpbox/memory.free", Label: "Free Memory", Format: "%.1f GiB"}},
	}
	openWidgetEditor(3, gauge)
	if editWidgetIdx != 3 {
		t.Errorf("editWidgetIdx = %d, want 3", editWidgetIdx)
	}
	if addWidgetType != dashboard.WidgetGauge || addWidgetTitle != "Mem" || addWidgetSpanX != 2 || addWidgetSpanY != 2 {
		t.Errorf("draft = type:%v title:%q spanX:%v spanY:%v, want gauge/Mem/2/2 (legacy square Span falls back to both)", addWidgetType, addWidgetTitle, addWidgetSpanX, addWidgetSpanY)
	}
	if len(addWidgetSeriesPicks) != 1 || addWidgetSeriesPicks[0].Selector != "jumpbox/memory.free" ||
		addWidgetSeriesPicks[0].Label != "Free Memory" || addWidgetSeriesPicks[0].Format != "%.1f GiB" {
		t.Errorf("gauge draft series = %+v, want [{jumpbox/memory.free Free Memory %%.1f GiB}]", addWidgetSeriesPicks)
	}

	chart := dashboard.Widget{
		Type: dashboard.WidgetChart,
		Series: []dashboard.SeriesRef{
			{Selector: "jumpbox/cpu.load1"},
			{Selector: "jumpbox/cpu.load5"},
		},
	}
	openWidgetEditor(0, chart)
	if len(addWidgetSeriesPicks) != 2 || addWidgetSeriesPicks[0].Selector != "jumpbox/cpu.load1" || addWidgetSeriesPicks[1].Selector != "jumpbox/cpu.load5" {
		t.Errorf("multi-series draft = %+v, want [jumpbox/cpu.load1 jumpbox/cpu.load5]", addWidgetSeriesPicks)
	}

	wide := dashboard.Widget{
		Type: dashboard.WidgetBars, SpanX: 3, SpanY: 1,
		Series: []dashboard.SeriesRef{{Selector: "jumpbox/disk.*.used_percent"}},
	}
	openWidgetEditor(1, wide)
	if addWidgetSpanX != 3 || addWidgetSpanY != 1 {
		t.Errorf("independent spanX/spanY draft = %v/%v, want 3/1", addWidgetSpanX, addWidgetSpanY)
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
	addWidgetSeriesPicks = []dashboard.SeriesRef{{Selector: "example/cpu.usage"}}

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

// TestConfirmAddWidget_DropsBlankTrailingRow checks the series table's
// own standing "type a new one here" row — a blank Selector, present by
// construction, never an entry the user actually asked to save — is
// filtered out rather than failing Validate.
func TestConfirmAddWidget_DropsBlankTrailingRow(t *testing.T) {
	dashStore, err := dashboard.LoadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := &dashboard.Dashboard{Name: "example"}
	if err := dashStore.Save(d); err != nil {
		t.Fatal(err)
	}

	resetWidgetDraft()
	addWidgetType = dashboard.WidgetChart
	addWidgetSeriesPicks = []dashboard.SeriesRef{
		{Selector: "example/cpu.usage"},
		{Selector: "   "}, // blank (whitespace-only) trailing row
	}

	confirmAddWidget(d, dashStore)
	if addWidgetErr != "" {
		t.Fatalf("confirmAddWidget: %s", addWidgetErr)
	}
	got, ok := dashStore.Get("example")
	if !ok {
		t.Fatal("dashboard \"example\" not found in store")
	}
	if len(got.Widgets) != 1 || len(got.Widgets[0].Series) != 1 || got.Widgets[0].Series[0].Selector != "example/cpu.usage" {
		t.Fatalf("got widgets %+v, want exactly 1 widget with 1 series (example/cpu.usage), blank row dropped", got.Widgets)
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

func TestParseScale(t *testing.T) {
	if lo, hi, err := parseScale("", ""); lo != nil || hi != nil || err != nil {
		t.Errorf("blank = %v %v %v, want no scale", lo, hi, err)
	}
	lo, hi, err := parseScale("", "50")
	if err != nil || *lo != 0 || *hi != 50 {
		t.Errorf("max only = %v %v %v, want 0..50", lo, hi, err)
	}
	if _, _, err := parseScale("x", ""); err == nil {
		t.Error("non-numeric min should error")
	}
}

func TestApplyFormatBytesAndFactor(t *testing.T) {
	for _, tc := range []struct {
		format string
		value  float64
		want   string
	}{
		{"bytes", 268888507093.01, "250.4 GiB"},
		{"bytes", 512, "512 B"},
		{"/1073741824:%.1f GB", 268888507093, "250.4 GB"},
		{"/0:%.0f", 5, "/0:5"}, // invalid divisor: left as written
	} {
		if got := applyFormat(tc.format, tc.value); got != tc.want {
			t.Errorf("applyFormat(%q, %v) = %q, want %q", tc.format, tc.value, got, tc.want)
		}
	}
}
