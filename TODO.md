# Watchtower — status

Phases per the original project brief. Check `git log` for commit-level detail; this is the glanceable summary.

## Phase 1 — Prove the collection engine ✅ done

- YAML metric definitions, YAML server configs, SSH connection, shell collector, line-oriented `key=value` parsing, namespaced timestamped observations.
- Proven live against two real lab servers.

## Phase 2 — Scheduling and generic metrics ✅ done

- Per-metric/per-server scheduling, independent goroutines (`internal/scheduler`), bounded global concurrency.
- Group-based metric selection (`metric.Registry.ResolveGroups`).
- Full override precedence chain: **definition default → group config (`internal/group`, order-deterministic by the server's own group list) → server-specific override**, all via `metric.Definition.Resolve`.
- Collection failure isolation — one server/metric failing never affects another.
- Bug found & fixed along the way: SSH connections now warm up concurrently at start-up so a cold dial never eats a metric's own command timeout.

## Phase 3 — Storage ⏸️ deferred

- Explicitly parked. Console/in-memory only for now.
- Plan: reuse/adapt NanoTDB when this becomes the priority, not reinvent it.
- Nothing persists across restarts. No historical range queries, no retention, no "last known value, marked stale" (that needs storage).

## Phase 4 — Event engine ✅ done (in-memory)

- `internal/event`: generic threshold evaluation — raise → escalate → dedup → hysteresis-gated recovery (partial or full) → dedup. Operational events (collector failure/recovery) tracked as a separate category, also deduped.
- Bounded history + acknowledge.
- **Known gap:** alert thresholds are not yet overridable per group/server with separate ownership from the collector definition (brief requires "updating a collector definition must not silently overwrite user alert configuration"). Today, event rules live only in the metric definition YAML. Not started.

## Phase 5 — Native GUI 🟡 first PoC done

- `cmd/watchtower-gui`: real MyGo native-UI window, built on the exact same engine as the console (`internal/app.Bootstrap` shared by both binaries — the console binary was slimmed down to a thin caller of it in the process).
- `internal/state`: the "Application API / data model" layer (brief #8) — a thread-safe `Store` holding each server's current metric status, read via `Store.Snapshot()`. GUI-agnostic; console binary doesn't use it (`Store` is nil there).
- Adaptive layout by window width (`cmd/watchtower-gui/view.go`), three tiers, confirmed live by resizing the actual window: compact (dot + server name) → medium (+ one dot per metric) → detailed (+ each metric's measurements and values).
- Green/amber/red/grey status (`state.Status`): grey (`StatusUnknown`) is a first-class state, not a bug — a collector failure marks the metric stale **without discarding its last-known value** (`Store.MarkStale`), satisfying the brief's "last-known values remain available but are clearly marked stale" requirement directly.
- Proven live against two real lab servers via WSLg (real window on the real desktop, not a screenshot).
- Dark mode: automatic (MyGo follows the OS by default; every color comes from `c.Theme()`, nothing hardcoded), plus an explicit top-right toggle (system/light/dark) — confirmed live.
- Added real percentage measurements to close a previously-deferred gap: `memory.used_percent` (computed in the existing `free` one-liner) and `cpu.usage` (via `vmstat 1 2`'s second, steady-state sample — a stateless collector trick, no two-cycle delta logic needed in the engine).
- **The GUI no longer decides what a measurement looks like — the metric YAML does, via a `display:` block** (`metric.Display`/`Scale`/`Widget`, resolved into `state.MeasurementView` by `internal/app`). This directly closes the brief's own principle #6 gap we'd been violating: `view.go` used to branch on `unit == "percent"`/`"load"`/`"bytes"` to pick a widget; now it only ever reads `mv.Widget`/`mv.Primary`/`mv.Scale`, and a measurement with no `display:` block renders as plain text — no guessing, no per-metric GUI code. `display.scale.max_ref` resolves a sibling measurement's live value each cycle (e.g. `used` scaled against `total`), never a hardcoded number.
- Gauge zones (the colored green/amber/red bands on the dial) are **derived from the measurement's own event rules**, direction-aware (above/below) — not a separate config, so they can never drift out of sync with what actually alerts. `state.ResolveZones` is pure and unit-tested for both directions against the two real production cases (disk-style above, memory.available-style below).
- Tooltips: compact/medium-tier dots (which show no text label at all) carry a generic `shortAbbrev` (first 3 letters, upper-cased — no per-metric name table) plus the stale reason when applicable.
- Stale rendering fixed: a stale metric used to short-circuit to a bare error line; it now keeps rendering its last-known gauges/bars/values, just in grey, with the reason in the dot's tooltip. Proven through a full success → induced-failure → grey (value preserved) → recovery cycle live, against a throwaway local SSH fixture (not the real lab servers), fully torn down after.
- Still just this one view. No click-through to a detail/investigation view, no history/graphs, no events/alerts panel, no SSH terminal, no systemd/docker/logs views — all of those are still open per the brief's "full investigation view" list.
- Redraw is a 1s polling ticker (`win.Update(func(){})`), not real change notification — fine for a PoC at this scale, worth revisiting if it ever matters.
- Value formatting (`formatValue` in view.go) is still a hand-rolled, bytes-only special case, separate from the widget-selection work above — a real unit-aware formatter is a natural next piece of the same "display hints" idea.
- **Closed: bars now get zone bands too**, same mechanism as the gauge (`internal/app` resolves `Zones` for `WidgetBar` as well as `WidgetGauge`; `bar()` draws colored segments + a marker line instead of a flat fill when zones are present). Confirmed via a new `internal/app` test (`used` — a bar — gets the same zones a gauge would) and live.
- **Closed: `load1`/`load5`/`load15` no longer hardcode `max: 4`.** `cpu.yaml` now collects a `cores` measurement (`nproc`) and the load bars scale against it via `display.scale.max_ref: cores` — the exact same generic mechanism `memory`'s bars already used for `total`, zero new Go code. Confirmed live: jumpbox (8 cores) and sbnats (2 cores) visibly scale differently for similar raw load values.
- **GUI now groups servers by cluster** (`server.Config.Cluster`, a generic free-form label, not NATS-specific; `state.Store.SetCluster` / `ServerView.Cluster`). A cluster renders as a header (rolled-up worst-of-members status, same rule `Store` already applies across one server's metrics, exported as `state.Worst`) with each member indented beneath via the exact same `serverRow` every standalone server uses — "look at the cluster" and "look at one member" are the same view, just how far down you read. Confirmed live with 2 real clusters.
- **Collector commands are now parameterized templates** (`metric.Collector.Render`, Go `text/template` against the fully-resolved group/server `config` map — the brief's own illustrative `metrics.nats.config.endpoint` example, finally implemented). `Definition.Validate` parse-checks the template at load time; a server missing a required config key fails loudly at run time (`missingkey=error`), never silently. `internal/app`'s `effective()` was silently discarding this resolved config before — now it's wired all the way through to `collect.Execute`.

## Phase 6 — Expand the monitoring library 🟡 started

- Have: `memory` (full, incl. `used_percent`), `cpu` (load averages + real `usage` % via `vmstat`), `nats` (connections/msgs/bytes/slow_consumers/mem/cpu via its HTTP `/varz` monitoring endpoint).
- `nats.yaml`'s collector is written defensively on purpose: it captures `curl`'s own exit code and fails explicitly *before* piping to `jq`, and uses `--fail` — a bare `curl | jq` reports jq's exit code, not curl's, so a down/unmonitored endpoint (connection refused, empty stdin to jq) would otherwise look like a *successful* collection with zero measurements. Verified this exact failure mode live before fixing it.
- Real-world gap found while wiring this up, not fixed (by design — see conversation): `sbnats` runs 6 NATS processes in 2 clusters (`connetic-nats`: nats1/2/3, `connetic-nats-2`: nats4/5/6), but only one node per cluster (nats1, nats4) has HTTP monitoring enabled. The other 4 are modeled with placeholder ports and correctly show stale/critical until monitoring is enabled on them server-side — not something this app can or should fix.
- Missing: disk, network, systemd, docker, mqtt, postgres.

---

## Other deliberately deferred / known limitations

- **SSH reconnect has no backoff** — `sshmgr.Client` redials immediately on the next use after a dropped connection; fine at this scale, would hammer a genuinely down server at larger scale.
- **Concurrency cap is a hardcoded const** (`maxConcurrentCollections = 16` in `main.go`), not exposed as config yet.
- **No credentials beyond SSH key file / ssh-agent** — satisfies "no plaintext passwords," nothing fancier (vault, etc.) built or asked for.
- **No SSH connection sharing across server entries on the same host** — `sbnats`'s 7 server configs (1 OS-level + 6 NATS processes) each open their own independent SSH connection to the identical machine. Simple and correct, but observed a real (self-recovering) transient timeout during the initial connection burst with 7 at once; worth revisiting if a host ever hosts many more monitored processes than that.

## Build

- **Windows is the primary target** — `make build` (default) cross-compiles the GUI with MyGo's own `mygo build` tool from this Linux/WSL machine, no cgo/mingw/NSIS required for the plain executable. Output: `dist/windows-amd64/Watchtower.exe` (GUI subsystem, no console flash) + `watchtower-console.exe` (the diagnostic console binary, console subsystem, on purpose).
- `make build-linux` / `make run` / `make test` for local dev iteration.
- `mygo.json` is intentionally minimal (name/slug/main/version) — no icon yet (`resources/icon.png`), no installer (needs `makensis`/NSIS, currently skipped gracefully), no code signing. All straightforward to add later via the same `mygo.json`.

## Repo housekeeping

- Public-safe: audited for secrets/real hosts, clean. Real server configs live in `data/` (gitignored); `metrics/` and `groups/` are the committed product library/examples.
- Branch is `main`; commit identity is the user's own real name/email (their call).
- **I don't run `git commit` in this repo (or any other) — that's left to you.**
