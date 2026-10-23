# Changelog

## Unreleased

## v1.2.0 — 2026-10-23

- Publish verified Linux and macOS archives automatically after successful main
  checks. Start at v1.2.0 and increment patch versions on subsequent main pushes.

- Align architecture, recovery/troubleshooting, TUI metrics and validation guides
  with the current reviewer cache layout, retry boundaries and error categories.
- Recover from recognized transient upstream transport failures with one bounded
  retry before message delivery. Preserve streaming pings, discard partial state,
  and keep classifier retries within their original deadline and permission scope.
  Show attempt counts and specific classifier failure categories in diagnostics.
- Disable native Anthropic feedback in launched sessions. Add an opt-in local
  `ClaudexFeedback` tool, `ctl feedback` controls, bounded private storage, and a
  TUI setting. Separate model mistakes from product defects and include synthetic
  live-model evals plus a real Claude/MCP integration test.
- Preserve the reviewer's actual implicit cache boundary across turns by keeping
  fresh permission JSON after the final user evidence message. Canonicalize
  proposed and historical call batches identically, and verify the full previous
  boundary even beyond the 256-message diagnostic sample.
- Render GPT source-reference markers as inline Markdown links from returned URL
  annotations. Preserve existing links, code examples, and grouped citations;
  separate unpositioned sources and identify missing source metadata clearly.
- Separate generation and Auto review usage, outcomes, active counts, model totals,
  and minute history. Default the TUI to generation; press `g` to change call type.
- Link review records to their generation request and distinguish upstream output,
  client-delivered content, and review duration. Keep blocked verdicts distinct
  from failed review requests.
- Preserve a stable review evidence prefix when live permission or Git context
  changes. Add bounded, keyed cache diagnostics without retaining source content.
- Upgrade the private control/metrics API to version 2; update both local binaries
  together. The Anthropic-compatible Messages API is unchanged.

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
