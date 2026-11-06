package dashboard

import (
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestWidget_Validate(t *testing.T) {
	cases := []struct {
		name    string
		w       Widget
		wantErr bool
	}{
		{"chart with one series ok", Widget{Type: WidgetChart, Series: []SeriesRef{{Selector: "jumpbox/cpu.load1"}}}, false},
		{"chart with no series", Widget{Type: WidgetChart}, true},
		{"gauge with one series ok", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}}, false},
		// A gauge can now carry several Series (each possibly expanding
		// to several matches at render time) — no longer capped at one.
		{"gauge with two series ok", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}, {Selector: "jumpbox/cpu.load1"}}}, false},
		{"gauge with wildcard server ok", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "*/cpu.usage"}}}, false},
		{"number with one series ok", Widget{Type: WidgetNumber, Series: []SeriesRef{{Selector: "jumpbox/memory.available"}}}, false},
		{"pie with two series ok", Widget{Type: WidgetPie, Series: []SeriesRef{
			{Selector: "jumpbox/docker.containers_running"},
			{Selector: "jumpbox/docker.containers_stopped"},
		}}, false},
		// A pie can also be driven by one selector expanding to several
		// matches at render time, so a single Series is no longer
		// statically rejected here — the "needs 2+ slices" check moves
		// to the resolver.
		{"pie with one series no longer statically rejected", Widget{Type: WidgetPie, Series: []SeriesRef{{Selector: "jumpbox/docker.containers_running"}}}, false},
		{"bars with wildcard selector ok", Widget{Type: WidgetBars, Series: []SeriesRef{{Selector: "jumpbox/disk.*.used_percent"}}}, false},
		{"unknown type", Widget{Type: "sparkline", Series: []SeriesRef{{Selector: "jumpbox/cpu.load1"}}}, true},
		{"series with unparseable selector", Widget{Type: WidgetChart, Series: []SeriesRef{{Selector: "jumpbox-cpu"}}}, true},
		{"series with empty selector", Widget{Type: WidgetChart, Series: []SeriesRef{{}}}, true},
		{"explicit legacy span ok", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, Span: 2}, false},
		{"legacy span too large", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, Span: MaxSpan + 1}, true},
		{"negative legacy span", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, Span: -1}, true},
		{"explicit span_x/span_y ok", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, SpanX: 3, SpanY: 1}, false},
		{"span_x too large", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, SpanX: MaxSpan + 1}, true},
		{"span_y too large", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, SpanY: MaxSpan + 1}, true},
		{"negative span_x", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, SpanX: -1}, true},
		{"negative span_y", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, SpanY: -1}, true},
		{"min/max both set ok", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, Min: ptr(0.0), Max: ptr(100.0)}, false},
		{"min without max rejected", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, Min: ptr(0.0)}, true},
		{"max without min rejected", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, Max: ptr(100.0)}, true},
		{"min equals max rejected", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, Min: ptr(5.0), Max: ptr(5.0)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.w.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

func TestWidget_EffectiveSpan(t *testing.T) {
	cases := []struct {
		w     Widget
		wantX int
		wantY int
	}{
		{Widget{Type: WidgetChart}, 2, 2}, // unset: chart's own default, both directions
		{Widget{Type: WidgetBars}, 2, 2},  // unset: bars' own default, same reasoning as chart
		{Widget{Type: WidgetGauge}, 1, 1}, // unset: everything else's default
		// SpanX/SpanY override independently — a tile need not be square.
		{Widget{Type: WidgetChart, SpanX: 3, SpanY: 1}, 3, 1},
		{Widget{Type: WidgetGauge, SpanX: 2}, 2, 1}, // only X set: Y still falls to the type default
		{Widget{Type: WidgetGauge, SpanY: 2}, 1, 2}, // only Y set: X still falls to the type default
		// The legacy square Span still applies equally to both when
		// SpanX/SpanY themselves are unset.
		{Widget{Type: WidgetNumber, Span: 3}, 3, 3},
		// SpanX/SpanY always win over the legacy Span when both are present.
		{Widget{Type: WidgetNumber, Span: 3, SpanX: 1}, 1, 3},
	}
	for _, tc := range cases {
		if gotX := tc.w.EffectiveSpanX(); gotX != tc.wantX {
			t.Errorf("EffectiveSpanX(%+v) = %d, want %d", tc.w, gotX, tc.wantX)
		}
		if gotY := tc.w.EffectiveSpanY(); gotY != tc.wantY {
			t.Errorf("EffectiveSpanY(%+v) = %d, want %d", tc.w, gotY, tc.wantY)
		}
	}
}

func TestWidget_RawSpan(t *testing.T) {
	cases := []struct {
		w     Widget
		wantX int
		wantY int
	}{
		{Widget{}, 0, 0}, // nothing ever set: "auto", not the type's default
		{Widget{SpanX: 2, SpanY: 3}, 2, 3},
		{Widget{Span: 2}, 2, 2}, // legacy fallback, both directions
		{Widget{Span: 2, SpanX: 4}, 4, 2},
	}
	for _, tc := range cases {
		if gotX := tc.w.RawSpanX(); gotX != tc.wantX {
			t.Errorf("RawSpanX(%+v) = %d, want %d", tc.w, gotX, tc.wantX)
		}
		if gotY := tc.w.RawSpanY(); gotY != tc.wantY {
			t.Errorf("RawSpanY(%+v) = %d, want %d", tc.w, gotY, tc.wantY)
		}
	}
}

func TestWidget_Scale(t *testing.T) {
	cases := []struct {
		name    string
		w       Widget
		wantMin float64
		wantMax float64
		wantOK  bool
	}{
		{"neither set", Widget{}, 0, 0, false},
		{"both set", Widget{Min: ptr(0.0), Max: ptr(100.0)}, 0, 100, true},
		{"only min set", Widget{Min: ptr(0.0)}, 0, 0, false},
		{"only max set", Widget{Max: ptr(100.0)}, 0, 0, false},
		{"min equals max", Widget{Min: ptr(5.0), Max: ptr(5.0)}, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			min, max, ok := tc.w.Scale()
			if ok != tc.wantOK || (ok && (min != tc.wantMin || max != tc.wantMax)) {
				t.Errorf("Scale() = (%v, %v, %v), want (%v, %v, %v)", min, max, ok, tc.wantMin, tc.wantMax, tc.wantOK)
			}
		})
	}
}

func TestSeriesRef_Parsed(t *testing.T) {
	s := SeriesRef{Selector: "jumpbox/cpu.usage"}
	parsed, err := s.Parsed()
	if err != nil {
		t.Fatalf("Parsed() error: %v", err)
	}
	if got := parsed.String(); got != "jumpbox/cpu.usage" {
		t.Errorf("Parsed().String() = %q, want %q", got, "jumpbox/cpu.usage")
	}
}

func TestDashboard_Validate(t *testing.T) {
	cases := []struct {
		name    string
		d       Dashboard
		wantErr bool
	}{
		{"valid", Dashboard{Name: "overview", Widgets: []Widget{{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}}}}, false},
		{"missing name", Dashboard{Widgets: []Widget{{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}}}}, true},
		{"no widgets ok", Dashboard{Name: "empty"}, false},
		{"invalid widget", Dashboard{Name: "bad", Widgets: []Widget{{Type: "sparkline"}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.d.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

func TestWidgetRange(t *testing.T) {
	w := Widget{Type: WidgetChart, Series: []SeriesRef{{Selector: "a/b.c"}}}
	if w.EffectiveRange() != DefaultRange {
		t.Fatalf("unset range must default, got %q", w.EffectiveRange())
	}
	w.Range = "7d"
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	if d, ok := RangeDuration("7d"); !ok || d != 7*24*time.Hour {
		t.Fatalf("7d = %v %v", d, ok)
	}
	w.Range = "2w"
	if err := w.Validate(); err == nil {
		t.Fatal("unknown range must be rejected")
	}
}
