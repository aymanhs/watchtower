# Watchtower status

Watchtower is a Go monitoring application with a shared collection engine and a
native GUI. Metrics, groups, and servers are configured
with YAML. Collectors run shell commands or fetch JSON over HTTP, using either
SSH or the local machine.

The GUI shows current state, events, and user-composed dashboards. Dashboards
are independent YAML files and can select measurements across servers or
clusters. Historical observations power chart widgets.

## Next in line

- History aggregates are in: `storage.QueryAgg` (streaming min/max/sum/count
  per window, one buffered pass per day file, 30d x 400 series at 15s takes
  ~5s, one series ~1.4s, so no stored rollups yet) feeds chart widgets through
  `aggCache` (background fetch, stale-while-revalidate, refreshed only while
  rendered). Charts have a per-widget `range` (1h/6h/1d/7d/30d) with an
  in-tile selector, draw the avg line over a min/max band per window, and
  break the line at gaps. Aggregates stay in charts; the Metrics tab shows
  raw values only. Stored rollups only if 30d queries get too slow.
- Metrics tab: selecting rows shows a chart of them under the table with its
  own range selector (aggregates live only in that chart, never in the
  table). Nothing else is queued; pick from Ideas.
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
