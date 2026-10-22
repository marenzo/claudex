# Changelog

## v1.1.0 — 2026-10-22

- Replace the web dashboard with a bundled, responsive Bubble Tea TUI: live
  charts, request outcome donuts, token breakdowns, a request inspector, search,
  filters, a command palette, light/dark colors, and editable gateway preferences.
- Start and share a gateway automatically with Claude sessions. Stop the automatic
  worker when its clients disconnect and active requests finish. Offer Homebrew
  service management for users who want a persistent gateway.
- Add authenticated session ownership and revision-checked live settings. Preserve
  each in-flight request's original generation and reviewer settings.
- Support the agreed GPT model catalog, improve tool/reasoning translation and
  reviewer prompt-cache reuse, and document Auto Mode and caching behavior.
- Keep metrics bounded in memory and error excerpts redacted. Use a Go-only build
  and check pipeline, updated dependencies, and full-history secret scanning.

### Upgrading

The web dashboard and custom macOS service commands have been removed. Stop any
previous custom service before starting the automatic gateway or a Homebrew
service. Open the terminal dashboard with `claudex ctl`; `claudex ctl setup` now
prepares configuration and sign-in, and `make install` only copies the binary.
Remove the obsolete `dashboard` field from an existing configuration.

Existing credential paths are preserved. Fresh installations use
`~/.config/claudex/client-key` and `~/.config/claudex/auth/codex.json`; relocating an
existing sign-in is a separate local operation. See [running](docs/running.md)
and [private files](docs/configuration.md#private-files).

The separate GPT Auto Mode reviewer remains experimental and opt-in. Parent
approval propagation to resumed workers remains an open limitation; see
[Auto Mode](docs/auto-mode.md).
