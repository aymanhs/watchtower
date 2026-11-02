package main

import "testing"

// TestCenterLabelFontSize_ShrinksForLongerLabels reproduces the real bug
// this was built to fix: a dashboard gauge widget's longer label (its own
// formatted value, e.g. "23.6GiB", not the short "42%" pressureGauge was
// first built around) at a now much larger diameter overlapped the ring
// at a flat diameter*0.22 — caught live from a real screenshot.
func TestCenterLabelFontSize_ShrinksForLongerLabels(t *testing.T) {
	short := centerLabelFontSize(140, "92%")
	if want := float32(140) * 0.22; short != want {
		t.Errorf("short label: got %v, want the unshrunk %v", short, want)
	}

	long := centerLabelFontSize(140, "23.6GiB")
	if long >= short {
		t.Errorf("long label font (%v) should be smaller than short label font (%v)", long, short)
	}
	if long < 9 {
		t.Errorf("long label font = %v, should never go below the floor of 9", long)
	}

	// The actual fit guarantee, not just "smaller than before": at the
	// returned size, the label's estimated rendered width must stay
	// within the ring's own inner diameter, not merely within the outer
	// one — that's exactly the distinction the live overlap bug was
	// about.
	diameter := float32(140)
	inner := diameter * 0.7
	label := "23.6GiB"
	estimatedWidth := float32(len(label)) * 0.62 * long
	if estimatedWidth > inner {
		t.Errorf("estimated label width %v exceeds the ring's inner diameter %v", estimatedWidth, inner)
	}
}
