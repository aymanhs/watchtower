# Watchtower status

Watchtower is a Go monitoring application with a shared collection engine and a
native GUI. Metrics, groups, and servers are configured
with YAML. Collectors run shell commands or fetch JSON over HTTP, using either
SSH or the local machine.

The GUI shows current state, events, and user-composed dashboards. Dashboards
are independent YAML files and can select measurements across servers or
clusters. Historical observations power chart widgets.

## Next in line

- Investigation views, remaining phases: raw collector output per metric,
  table-shaped results (services/processes/logs), "add to dashboard" from a measurement.
  Phase 1 (global Metrics tab with text/glob, status, and selected-server
  filters, sortable columns, row selection, last hour/day/all time range, copy as
  TSV, CSV export; Events tab has the same range, copy, and export) is done.
- Metrics tab: chart of selected rows (needs design so the table stays
  uncluttered); min/avg/max window columns arrive with aggregates.
- History aggregates (downsampled rollups) together with a selectable chart
  range; chart history is currently fixed to one hour.

## Ideas

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
