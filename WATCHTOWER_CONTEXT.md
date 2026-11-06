# Watchtower project context

This note preserves the project understanding gathered during a code review and
cleanup. It is intended as a handoff for future work in this repository; check
the source when implementation details may have changed.

## What the project does

Watchtower collects measurements from configured machines and services, applies
threshold rules, tracks current health and event history, and presents the
results through a console command or a native GUI. The GUI adds tray behavior,
an interactive SSH terminal, user-configurable dashboards, and charts backed by
local time-series storage.

The Windows GUI is the only build target; it is cross-compiled from Linux.

## Architecture and data flow

1. `internal/metric` loads metric YAML definitions. A definition describes its
   collector, interval/timeout defaults, optional collector config, and event
   thresholds. It does not declare measurement labels, units, types, or widgets.
2. `internal/server` and `internal/group` load server connections, selected
   metric groups, and optional overrides. `internal/app.Bootstrap` resolves
   definitions for each server and connects SSH clients concurrently before
   scheduling begins.
3. `internal/collect` executes either a shell collector or an HTTP/JSON
   collector. `internal/sshmgr` provides persistent SSH connections and SSH
   tunnels for HTTP requests and interactive shells; `internal/localrun`
   executes locally for local-type servers.
4. `internal/scheduler` runs each server/metric task on its own interval while
   enforcing a global concurrency limit.
5. `internal/app.MetricTask` evaluates operational and threshold events, writes
   successful observations to optional `internal/state` live state and
   `internal/storage` history, and optionally prints to the console. A history
   write failure is logged and does not turn a successful collection into a
   failed collection.
6. `cmd/watchtower` and `cmd/watchtower-gui` are separate presentations over
   the same engine. The GUI uses MyGo; keep toolkit imports out of `internal/*`.
   The GUI loads dashboards separately because they are a GUI-only concern.
7. `internal/dashboard` describes and persists global dashboards;
   `internal/selector` parses selectors; `internal/dashboard/resolve.go` maps
   selectors against a live state snapshot; the GUI renders dashboard widgets
   and queries history for chart tiles.

Useful starting points:

- Startup and assembly: `internal/app/bootstrap.go`
- One collection cycle: `internal/app/task.go`
- Collector execution: `internal/collect`
- Scheduler and concurrency: `internal/scheduler`
- Live state: `internal/state`
- Event state/transitions: `internal/event`
- History storage: `internal/storage`
- Dashboard model and resolution: `internal/dashboard`
- GUI entry point/lifecycle: `cmd/watchtower-gui/main.go`
- Dashboard rendering/editor: `cmd/watchtower-gui/dashboard.go`
- Main layout, events, terminal, and chart helpers: `cmd/watchtower-gui/panel.go`

## Configuration and runtime data

- Metric definitions: `metrics/` (examples are grouped by domain).
- Group overrides: `groups/` (optional; absence is allowed).
- Per-server connection and override files: `data/servers/` (gitignored).
- Global dashboard files: `data/dashboards/`.
- Metric history: `data/storage/`.
- `data/` is intentionally ignored by Git. It may contain real server names,
  connection details, user dashboards, and generated history; inspect it
  locally when needed, but do not commit or expose its contents.

Metric collector config is seeded from collector defaults, then overrides are
merged in this order: definition defaults -> group overrides in the order the
server lists its groups -> the server's own metric override. The last source
wins for conflicting values. Current collectors include shell and native
HTTP/JSON; HTTP avoids requiring `curl`/`jq` on the monitored host and can use
the existing SSH session as a tunnel for loopback endpoints.

Measurements are the keys actually emitted by the collector; there is no
declared measurement schema. Per-instance output uses keys such as
`root.used_percent`. `metric.SplitInstance` splits at the last dot into base
measurement `used_percent` and instance `root`; event rules name the base key
so one rule applies to each instance, while each instance retains independent
event state.

Dashboard selectors can target a server, glob across servers, or target a
cluster. Examples:

- `jumpbox/mem.free_pct`
- `*/mem.free_pct`
- `cluster:connetic-nats/nats.connections`
- Instance-aware selectors such as `jumpbox/disk.*.used_percent`

Selectors can resolve to zero or many live measurements. The UI should handle
empty and multi-match results rather than assuming a static one-to-one
measurement mapping. Widget labels, formats, and gauge/bar min/max are
dashboard choices, not metric metadata.

## Demo fixtures

On Windows, `Watchtower.exe -demo` runs the GUI against a synthetic fleet
from `internal/demo` instead of real servers. Fake servers emit realistic
CPU/memory/disk/network/docker/NATS measurements as deterministic functions of
time, through the normal `MetricTask` pipeline (`MetricTask.Collect` replaces
only the collector), so events, live state, and history behave as in real use.
Three hours of history are regenerated at each launch. Demo data lives under
`data/demo/` (storage is wiped each run; a sample dashboard is seeded if none
exists). The single-instance lock still applies, so quit a real instance first.

## Notable design decisions

- A metric definition owns collection and alert rules, not visual presentation.
  Dashboards are explicit, global user compositions rather than an implicit
  rendering of every metric or a dashboard embedded in each server config.
- Historical storage is deliberately small: append-only daily partitions and a
  catalog, with retention by deleting old partitions. There is no WAL,
  compression, or background rollup. Writes are periodically synced; close and
  partition rollover sync unconditionally.
- Charts use the history store and render-time downsampling. The current chart
  window is a fixed one hour.
- The GUI's tray/terminal behavior is separate from the collection engine.
  Interactive shells reuse the persistent SSH client rather than authenticating
  again. The GUI's shutdown should cancel and join the scheduler before closing
  history storage.
- SSH warmup is concurrent and bounded; failed initial connections are logged
  and left to the client's reconnect behavior instead of blocking other
  servers.

## Current known limitations

See `TODO.md` for the maintained short list. In particular:

- Threshold rules are not overridable independently by group/server.
- Detailed service/process/filesystem inspection, system logs, and connection
  status views remain unimplemented.
- Chart history range is not user-selectable.
- Collection concurrency is a hardcoded limit of 16 in each command entry
  point.
- SSH reconnects have no backoff, and multiple server entries for one physical
  host use separate SSH connections.
- Authentication is based on SSH key files/agent; no other credential
  providers are integrated.

## Build and verification

Run commands from the repository root:

- `make test` — `go test ./... -race`
- `go vet ./...` — static checks
- `make build` — cross-compile the Windows GUI into `dist/`
- `make clean` — remove `dist/`

`go.mod` pins MyGo at v0.7.4, and the Makefile pins the MyGo packaging tool to
the same version. The Windows GUI packaging path is intended to cross-compile
from Linux without cgo.

## Recent review and cleanup

The review confirmed that the GUI dashboard had superseded the old
auto-rendered metric pane. Recent cleanup removed obsolete state/metric display
metadata, unused bootstrap server-config results, the no-op measurement label
method, stale YAML description/tag fields, and an empty GUI test file.

The same pass found a shutdown lifecycle issue: the GUI previously allowed
history storage to close while the background scheduler could still be
collecting. The GUI now cancels and waits for the scheduler before returning
from its run loop and closing history. Verify this ordering if changing quit
paths, signal handling, or scheduler startup.

At the end of that pass, `make test`, `go vet ./...`, `go mod tidy -diff`, and
`git diff --check` passed. The working tree contained the cleanup changes; they
were not committed. Recheck `git status` before starting new work and preserve
any existing edits.
