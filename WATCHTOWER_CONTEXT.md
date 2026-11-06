# Watchtower project context

This note preserves the project understanding gathered during a code review and
cleanup. It is intended as a handoff for future work in this repository; check
the source when implementation details may have changed.

## What the project does

Watchtower collects measurements from configured machines and services, applies
threshold rules, tracks current health and event history, and presents the
results through a native GUI. The GUI adds tray behavior,
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
   `internal/storage` history. A history
   write failure is logged and does not turn a successful collection into a
   failed collection.
6. `cmd/watchtower-gui` is the only presentation (a headless console command
   was removed; restore it from git history if wanted). The GUI uses MyGo; keep
   toolkit imports out of `internal/*`. The GUI loads dashboards separately.
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
- Event persistence: `internal/eventlog` (append-only JSONL per UTC day,
  `data/storage/events-YYYY-MM-DD.jsonl`, same retention as metric history).
  The GUI seeds `event.Engine` from it at startup via `Seed` and appends via
  `OnEvent`. Only history is restored, not alarm state, so a target still
  failing after a restart raises again (pairing treats it as the same outage).
  Acknowledgements are not persisted yet.
- Dashboard model and resolution: `internal/dashboard`
- GUI entry point/lifecycle: `cmd/watchtower-gui/main.go`
- Dashboard rendering/editor: `cmd/watchtower-gui/dashboard.go`
- Main layout, right-pane tabs, events table, terminal, and chart helpers: `cmd/watchtower-gui/panel.go`
- Events tab (type/range filters, collector outage durations, 100-per-page pager over up to 20000 in-memory events): `cmd/watchtower-gui/events.go`
- Server management modal (add/edit/delete, `~/.ssh/config` import, live reload; unavailable in demo): `cmd/watchtower-gui/servers.go`, backed by `internal/server` (`Save`, `Delete`, `LoadSSHConfig`) and `internal/app.Runner`
- Metrics tab (global measurement table with server, text/glob, and status filters, sortable columns, time range, copy/CSV export): `cmd/watchtower-gui/details.go`

## Configuration and runtime data

- Metric definitions: `metrics/` (examples are grouped by domain).
- Group overrides: `groups/` (optional; absence is allowed).
- The data directory is `data/` by default; `-data <dir>` relocates it
  (`servers/`, `storage/`, `dashboards/`, `watchtower.log` all live under it).
  `-demo` defaults it to `data/demo/` so synthetic and real data never mix.
- Per-server connection and override files: `data/servers/` (gitignored), one YAML file per server. The GUI writes them; a missing directory just means no servers yet. Saving or deleting a server reloads the collection live: `app.Runner` bootstraps the new configuration before stopping the old run, so a broken config keeps the previous one running. Keys are file paths only; no passwords are stored. YAML comments are not preserved on edit.
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
Thirty days of history (5-minute steps, 30-second for the last six hours) are
regenerated at each launch. Demo data lives under `data/demo/` by default, or
the `-data` directory (storage is wiped each run; a sample dashboard is seeded if none
exists). `make run-wsl` runs the same demo natively under WSLg for a faster
dev loop (no tray there: the app logs that and runs without it). The single-instance lock still applies, so quit a real instance first.

## Chart history

Chart widgets read aggregated history, not raw points: `storage.QueryAgg`
buckets each series by a window picked from the widget's `range` (1h to 30d,
default 1h). `cmd/watchtower-gui/aggcache.go` caches results per (series,
range), fetches in a background goroutine and keeps the old data on screen
while refreshing, so the 1s repaint never queries storage. Each point is one
window's average, drawn over a translucent min/max band, and the line breaks
where data is missing. Each chart tile has a per-session range selector; the
saved range is set in the widget editor. The refresh icon on the dashboard
toolbar invalidates every entry. The Metrics tab deliberately shows raw
values only, never aggregates.

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
- SSH reconnects use a per-server backoff gate (`internal/sshmgr`): the delay
  ceiling doubles from 2s to a 5min cap with half jitter, resets on success,
  and is shared by every metric on that server, so a down host gets one dial
  per window and other callers fail fast ("backing off, next attempt in ...").
  Auth and host-key failures go straight to the cap. Opening the interactive
  terminal bypasses the gate once.
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
- Multiple server entries for one physical host use separate SSH connections.
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
