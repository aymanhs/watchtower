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
- Detailed tier now renders percent-unit measurements (`unit: percent`) as a circular gauge (ring + center %) and a bar, both colored by the event engine's current severity — confirmed live. Non-percent measurements still print as plain text.
- Added real percentage measurements to close a previously-deferred gap: `memory.used_percent` (computed in the existing `free` one-liner) and `cpu.usage` (via `vmstat 1 2`'s second, steady-state sample — a stateless collector trick, no two-cycle delta logic needed in the engine). Both have warning/critical event rules mirroring the brief's own disk example.
- Still just this one view. No click-through to a detail/investigation view, no hover-for-details, no history/graphs, no events/alerts panel, no SSH terminal, no systemd/docker/logs views — all of those are still open per the brief's "full investigation view" list.
- Redraw is a 1s polling ticker (`win.Update(func(){})`), not real change notification — fine for a PoC at this scale, worth revisiting if it ever matters.
- Value formatting (`formatValue` in view.go) is a hand-rolled, bytes-only special case — real unit-aware display formatting belongs with the metadata "display hints" work the brief describes, not hardcoded in the GUI.
- Gauge/bar color comes from our own event-engine severity, not `ui.Meter`'s built-in threshold levels — deliberate (ours has hysteresis memory, Meter's doesn't), but means we hand-rolled the bar/ring drawing instead of reusing the stock widget.

## Phase 6 — Expand the monitoring library ⬜ barely started

- Have: `memory` (full, incl. `used_percent`), `cpu` (load averages + real `usage` % via `vmstat`).
- Missing: disk, network, systemd, docker, nats, mqtt, postgres.

---

## Other deliberately deferred / known limitations

- **SSH reconnect has no backoff** — `sshmgr.Client` redials immediately on the next use after a dropped connection; fine at this scale, would hammer a genuinely down server at larger scale.
- **Concurrency cap is a hardcoded const** (`maxConcurrentCollections = 16` in `main.go`), not exposed as config yet.
- **No credentials beyond SSH key file / ssh-agent** — satisfies "no plaintext passwords," nothing fancier (vault, etc.) built or asked for.

## Repo housekeeping

- Public-safe: audited for secrets/real hosts, clean. Real server configs live in `data/` (gitignored); `metrics/` and `groups/` are the committed product library/examples.
- Branch is `main`; commit identity is repo-local (`aymanhs <aymanhs@users.noreply.github.com>`), not your work email.
- **I don't run `git commit` in this repo (or any other) — that's left to you.**
