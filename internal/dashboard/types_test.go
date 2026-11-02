package dashboard

import "testing"

func TestWidget_Validate(t *testing.T) {
	cases := []struct {
		name    string
		w       Widget
		wantErr bool
	}{
		{"chart with one series ok", Widget{Type: WidgetChart, Series: []SeriesRef{{Metric: "cpu", Measurement: "load1"}}}, false},
		{"chart with no series", Widget{Type: WidgetChart}, true},
		{"gauge with one series ok", Widget{Type: WidgetGauge, Series: []SeriesRef{{Metric: "cpu", Measurement: "usage"}}}, false},
		{"gauge with two series", Widget{Type: WidgetGauge, Series: []SeriesRef{{Metric: "cpu", Measurement: "usage"}, {Metric: "cpu", Measurement: "load1"}}}, true},
		{"number with one series ok", Widget{Type: WidgetNumber, Series: []SeriesRef{{Metric: "memory", Measurement: "available"}}}, false},
		{"pie with two series ok", Widget{Type: WidgetPie, Series: []SeriesRef{
			{Metric: "docker", Measurement: "containers_running"},
			{Metric: "docker", Measurement: "containers_stopped"},
		}}, false},
		{"pie with one series", Widget{Type: WidgetPie, Series: []SeriesRef{{Metric: "docker", Measurement: "containers_running"}}}, true},
		{"unknown type", Widget{Type: "sparkline", Series: []SeriesRef{{Metric: "cpu", Measurement: "load1"}}}, true},
		{"series missing measurement", Widget{Type: WidgetChart, Series: []SeriesRef{{Metric: "cpu"}}}, true},
		{"explicit span ok", Widget{Type: WidgetGauge, Series: []SeriesRef{{Metric: "cpu", Measurement: "usage"}}, Span: 2}, false},
		{"span too large", Widget{Type: WidgetGauge, Series: []SeriesRef{{Metric: "cpu", Measurement: "usage"}}, Span: 4}, true},
		{"negative span", Widget{Type: WidgetGauge, Series: []SeriesRef{{Metric: "cpu", Measurement: "usage"}}, Span: -1}, true},
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
