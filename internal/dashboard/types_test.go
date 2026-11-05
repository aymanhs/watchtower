package dashboard

import "testing"

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
		{"explicit span ok", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, Span: 2}, false},
		{"span too large", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, Span: 4}, true},
		{"negative span", Widget{Type: WidgetGauge, Series: []SeriesRef{{Selector: "jumpbox/cpu.usage"}}, Span: -1}, true},
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
		w    Widget
		want int
	}{
		{Widget{Type: WidgetChart}, 2},          // unset: chart's own default
		{Widget{Type: WidgetBars}, 2},           // unset: bars' own default, same reasoning as chart
		{Widget{Type: WidgetGauge}, 1},          // unset: everything else's default
		{Widget{Type: WidgetChart, Span: 1}, 1}, // explicit override, smaller
		{Widget{Type: WidgetGauge, Span: 2}, 2}, // explicit override, bigger
		{Widget{Type: WidgetNumber, Span: 3}, 3},
	}
	for _, tc := range cases {
		if got := tc.w.EffectiveSpan(); got != tc.want {
			t.Errorf("EffectiveSpan(%+v) = %d, want %d", tc.w, got, tc.want)
		}
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
