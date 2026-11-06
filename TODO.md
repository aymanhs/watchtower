# Watchtower status

Watchtower is a Go monitoring application with a shared collection engine, a
console command, and a native GUI. Metrics, groups, and servers are configured
with YAML. Collectors run shell commands or fetch JSON over HTTP, using either
SSH or the local machine.

The GUI shows current state, events, and user-composed dashboards. Dashboards
are independent YAML files and can select measurements across servers or
clusters. Historical observations power chart widgets.

## Remaining work

- Event thresholds are defined with metric definitions; group- or
  server-specific threshold overrides are not supported.
- Investigation views for service/process/filesystem details, system logs, and
  connection status are not implemented.
- Chart history is fixed to a one-hour window.
- Collection concurrency is capped at 16 in the command entry points and is
  not configurable.
- SSH reconnects have no backoff, and multiple server entries for the same
  host use separate SSH connections.
- Authentication supports SSH keys and the SSH agent; no other credential
  providers are integrated.

## Build and test

- `make build` cross-compiles the Windows GUI; `make test` runs the Go tests
  with the race detector.

Server configuration belongs under `data/servers` (gitignored); metric and
group definitions are committed under `metrics/` and `groups/`.
