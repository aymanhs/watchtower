package main

import (
	"testing"
	"time"

	"aymanhs/watchtower/internal/metric"
	"aymanhs/watchtower/internal/state"
	"aymanhs/watchtower/internal/storage"
)

// TestGroupByUnit_PrefersLargerGroupOverAlphabetical reproduces the real
// cpu.yaml shape: usage (percent, Primary) + load1/load5/load15 (load, 3
// members) + cores (count, 1 member, just a scaling reference for the
// load bars). Alphabetical-only tie-breaking among non-primary units
// would pick "count" over "load" for the second slot and silently drop
// the loads this feature exists to chart together — caught live, not in
// review, when a real chart showed "usage"+"cores" with no load lines at
// all.
func TestGroupByUnit_PrefersLargerGroupOverAlphabetical(t *testing.T) {
	measurements := []state.MeasurementView{
		{Key: "usage", Unit: "percent", Primary: true},
		{Key: "load1", Unit: "load"},
		{Key: "load5", Unit: "load"},
		{Key: "load15", Unit: "load"},
		{Key: "cores", Unit: "count"},
	}
	items := make([]chartItem, len(measurements))
	for i, m := range measurements {
		items[i] = chartItem{mv: m, historyKey: "jumpbox/cpu." + m.Key}
	}
	groups := groupByUnit(items)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if groups[0].unit != "percent" {
		t.Fatalf("group 0 unit = %q, want %q (the Primary's)", groups[0].unit, "percent")
	}
	if groups[1].unit != "load" {
		t.Fatalf("group 1 unit = %q, want %q (3 members beats count's 1)", groups[1].unit, "load")
	}
	if len(groups[1].items) != 3 {
		t.Fatalf("got %d measurements in the load group, want 3", len(groups[1].items))
	}
}

// TestAxisUnitScale_BytesAlwaysGiB documents the bytes special case: GiB
// regardless of the group's own range, since it's unit-driven, not a
// magnitude guess.
func TestAxisUnitScale_BytesAlwaysGiB(t *testing.T) {
	if div, suf := axisUnitScale("bytes", 100); div != 1<<30 || suf != "GiB" {
		t.Errorf("got (%v, %q), want (1<<30, GiB)", div, suf)
	}
}

// TestAxisUnitScale_PicksMagnitudeFromRange reproduces the real bug this
// was built to fix: a narrow range (here, bytes would be the live case,
// but the same collision applies to any unit) whose ticks all round to
// the same label at a fixed decimal count. A non-bytes unit's divisor
// instead comes from the group's own hi, not a blanket guess, so whatever
// range it actually has gets chart.Linear.Ticks' own adaptive precision
// applied to small, nicely-scaled numbers.
func TestAxisUnitScale_PicksMagnitudeFromRange(t *testing.T) {
	cases := []struct {
		hi       float64
		wantDiv  float64
		wantSuff string
	}{
		{500, 1, ""},
		{1500, 1e3, "K"},
		{2_500_000, 1e6, "M"},
		{3_500_000_000, 1e9, "B"},
	}
	for _, tc := range cases {
		div, suf := axisUnitScale("count", tc.hi)
		if div != tc.wantDiv || suf != tc.wantSuff {
			t.Errorf("axisUnitScale(count, %v) = (%v, %q), want (%v, %q)", tc.hi, div, suf, tc.wantDiv, tc.wantSuff)
		}
	}
}

func TestRateOf_ComputesPerSecondRate(t *testing.T) {
	base := time.Now()
	points := []storage.Point{
		{Timestamp: base, Value: 1000},
		{Timestamp: base.Add(2 * time.Second), Value: 3000}, // +2000 over 2s = 1000/s
		{Timestamp: base.Add(4 * time.Second), Value: 5000}, // +2000 over 2s = 1000/s
	}
	got := rateOf(points)
	if len(got) != 2 {
		t.Fatalf("got %d points, want 2 (one shorter than the input)", len(got))
	}
	if got[0].Value != 1000 || got[1].Value != 1000 {
		t.Fatalf("got %+v, want both rates = 1000/s", got)
	}
}

func TestRateOf_ClampsNegativeDeltaToZero(t *testing.T) {
	base := time.Now()
	points := []storage.Point{
		{Timestamp: base, Value: 5000},
		{Timestamp: base.Add(time.Second), Value: 100}, // counter reset (reboot)
	}
	got := rateOf(points)
	if len(got) != 1 || got[0].Value != 0 {
		t.Fatalf("got %+v, want one point clamped to 0 (counter reset, not a real negative rate)", got)
	}
}

func TestRateOf_FewerThanTwoPointsYieldsNil(t *testing.T) {
	if got := rateOf(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := rateOf([]storage.Point{{Timestamp: time.Now(), Value: 1}}); got != nil {
		t.Fatalf("got %v, want nil for a single point", got)
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		unit   string
		format metric.Format
		v      float64
		want   string
	}{
		// FormatDefault: plain two decimals, except bytes (unit-driven
		// GiB scaling, not format-driven).
		{"count", metric.FormatDefault, 500, "500.00"},
		{"percent", metric.FormatDefault, 57.3, "57.30"},
		{"bytes", metric.FormatDefault, 42603728896, "39.7GiB"},
		// FormatInt: no decimals — docker's container counts.
		{"count", metric.FormatInt, 3, "3"},
		{"count", metric.FormatInt, 0, "0"},
		// FormatHuman: K/M/B suffix — network's packet counters.
		{"count", metric.FormatHuman, 13972777, "13.97M"},
		{"count", metric.FormatHuman, 10258720, "10.26M"},
		{"count", metric.FormatHuman, 500, "500.00"}, // below 1K: no suffix
		{"count", metric.FormatHuman, 1500, "1.50K"},
		{"count", metric.FormatHuman, 2_500_000_000, "2.50B"},
		// bytes still wins over FormatHuman — one unit-driven rule, not two competing ones.
		{"bytes", metric.FormatHuman, 42603728896, "39.7GiB"},
	}
	for _, tc := range cases {
		if got := formatValue(tc.unit, tc.format, tc.v); got != tc.want {
			t.Errorf("formatValue(%q, %q, %v) = %q, want %q", tc.unit, tc.format, tc.v, got, tc.want)
		}
	}
}

func TestRateOf_SkipsNonIncreasingTimestamps(t *testing.T) {
	base := time.Now()
	points := []storage.Point{
		{Timestamp: base, Value: 100},
		{Timestamp: base, Value: 200}, // same timestamp: zero elapsed time, must be skipped not divide-by-zero
		{Timestamp: base.Add(time.Second), Value: 300},
	}
	got := rateOf(points)
	if len(got) != 1 {
		t.Fatalf("got %d points, want 1 (the zero-duration step skipped)", len(got))
	}
	if got[0].Value != 100 {
		t.Fatalf("got %+v, want rate = 100/s (the zero-duration step skipped; 300-200 over the remaining 1s)", got)
	}
}
