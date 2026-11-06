# Watchtower status

Watchtower is a Go monitoring application with a shared collection engine and a
native GUI. Metrics, groups, and servers are configured
with YAML. Collectors run shell commands or fetch JSON over HTTP, using either
SSH or the local machine.

The GUI shows current state, events, and user-composed dashboards. Dashboards
are independent YAML files and can select measurements across servers or
clusters. Historical observations power chart widgets.

## Next in line

Nothing queued. Pick from Ideas.

## Ideas

- Objects follow-ups: more object definitions (docker containers, listening
  ports, pending updates), and a table view for objects shaped as flat lists.
- Server management follow-ups: a "test connection" button, and per-server
  reload instead of swapping the whole collection (active alarms of a removed
  server stay raised in the event engine until restart).
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

- Keeping raw collector output: collector failures are events whose message
  quotes a bounded excerpt of the offending output or stderr instead.
- Aggregates in the Metrics table: it shows raw values at exact instants;
  aggregation lives in charts only.
- Shared SSH connections per host: entries for the same host intentionally get
  separate connections and backoff state; one entry can carry any number of
  metrics.

## Build and test

- `make build` cross-compiles the Windows GUI; `make test` runs the Go tests
  with the race detector.

Server configuration belongs under `data/servers` (gitignored); metric and
group definitions are committed under `metrics/` and `groups/`.
