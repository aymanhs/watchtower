# Watchtower project guidance

Watchtower is a Go monitoring application. Read [WATCHTOWER_CONTEXT.md](../WATCHTOWER_CONTEXT.md) for the architecture, data flow, configuration conventions, design decisions, and known limitations before making broad changes. Check [TODO.md](../TODO.md) for the current short status list.

## Working in this repository

- Preserve the shared engine boundary: `internal/app` and `internal/*` must not depend on the MyGo GUI. The GUI is a presentation over the collectors, event engine, scheduler, and state model.
- Keep presentation choices in dashboards, not metric definitions. Metrics describe collection and alert thresholds; dashboard widgets choose labels, formats, scales, and visual forms.
- Treat selectors and collector output as runtime data. A selector may match zero or many measurements, and per-instance measurement keys are split at the last dot.
- Preserve configuration precedence: metric defaults, then group overrides in the server's declared group order, then server-specific overrides.
- Keep server credentials and runtime data under gitignored `data/`; never add real host configuration, credentials, or generated storage/build output to source control.
- Follow existing Go patterns and keep errors explicit. If collection succeeds but history persistence fails, log the history error without changing the collection result.
- After Go changes, run `make test` (race-enabled), `go vet ./...`, and `git diff --check` as appropriate. `make build` cross-compiles the Windows GUI.
- Keep docs current when changing architecture, configuration, commands, or known limitations. Avoid retaining iteration-by-iteration implementation diaries in the status document.
- GUI buttons are always icon-only (or emoji) with a tooltip, never text labels; use `squareIconButton` and the glyphs in `cmd/watchtower-gui/icons.go`.
