# Watchtower status

Watchtower is a Go monitoring application with a shared collection engine and a
native GUI. Metrics, groups, and servers are configured
with YAML. Collectors run shell commands or fetch JSON over HTTP, using either
SSH or the local machine.

The GUI shows current state, events, and user-composed dashboards. Dashboards
are independent YAML files and can select measurements across servers or
clusters. Historical observations power chart widgets.

## Next in line

- History aggregates (decided design): `storage.QueryAgg(names, from, to,
  window)` reads each day file once (buffered, not ReadFile), routes records
  by metric ID into streaming min/max/sum/count buckets (avg derived), aligned
  to epoch multiples and clipped to the range, empty buckets skipped; the
  aggregation is a pure function over a point stream. Window is auto-picked
  from range and chart width (15s ... 1d steps). Charts draw one avg sample
  per window and break the line at gaps (> ~2 windows) so outages show. Range
  is per widget (`range` field, 1h/6h/1d/7d/30d, default 1h) with date x-axis
  labels for long ranges. History-backed views never re-query per redraw (the
  UI repaints every second): results sit in a shared cache keyed by (series,
  range, window), fetched in a background goroutine (one per key, previous
  data shown meanwhile, repaint on completion), refreshed only when older
  than the window (clamped 10s to 5min), only for views actually on screen,
  and anchored to the fetch time so the time axis doesn't drift. Gauge and
  number widgets read live state and are unaffected. Query-time only to start: measure on a
  generated realistic dataset and add stored 1m/1h rollup files only if 7d/30d
  are too slow. Later: min/max band, percentiles, and min/avg/max window
  columns in the Metrics tab (one scan for all visible rows).
- Metrics tab: chart of selected rows (needs design so the table stays
  uncluttered; follows aggregates so it can respect the range).
- Cleanup: investigation view phase 1 is complete. Raw collector output is
  deliberately not kept: collector failures are events whose message quotes
  a bounded excerpt of the offending output or stderr.

## Ideas

- Table-shaped metrics: collectors whose result is a list of rows, not one
  number per dotted name (services and their state, top processes with
  CPU/memory/PID, containers, mounts, recent failed logins or log lines).
  key=value output cannot carry these without losing information. It needs
  its own collector output contract (header line plus rows), a table widget
  instead of a chart/gauge, rule kinds such as "any row where state is not
  running" or "row count", and a decision on what history keeps (probably only
  derived counts, not whole tables).
- Additional credential providers; authentication currently supports SSH keys
  and the SSH agent only.
- Warn at startup when several server entries share the same host/port/user.

## Deferred

- Group- or server-specific threshold overrides: thresholds live with metric
  definitions. Revisit when alert noise from heterogeneous hosts shows a need
  (separate metric definitions/groups work meanwhile).
- Configurable collection concurrency: capped at 16 in the command entry
  points, which is ample until a fleet size needs more.

## Decided against

- Shared SSH connections per host: entries for the same host intentionally get
  separate connections and backoff state; one entry can carry any number of
  metrics.

## Build and test

- `make build` cross-compiles the Windows GUI; `make test` runs the Go tests
  with the race detector.

Server configuration belongs under `data/servers` (gitignored); metric and
group definitions are committed under `metrics/` and `groups/`.
