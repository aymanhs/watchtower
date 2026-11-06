package main

// Deliberately empty of the old groupByUnit/axisUnitScale/rateOf/formatValue
// tests: all four were removed along with internal/metric's Unit/Type
// (see that package's doc) — no more axis-grouping-by-unit, no more chart
// rate-conversion for counters, accepted losses of that simplification.
// renderSeriesChart's own single-axis behavior is exercised through the
// dashboard chart widget tests in dashboard_test.go instead.
