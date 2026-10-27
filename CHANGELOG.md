# Changelog

Released changes and upgrade actions. Earlier history is available in
[GitHub releases](https://github.com/marenzo/claudex/releases).

## v1.2.4 — 2026-10-27

- Put the executable at the root of release archives and source builds. Document
  downloading the latest platform release into `~/.local/bin` with curl or wget.

- Add the bundled `/claudex:imagegen` skill and `ctl imagegen` command for native
  Codex subscription image generation, image edits/references, transparent PNGs,
  and atomic workspace output without a separate API key.

## v1.2.3 — 2026-10-26

- Add the bundled `/claudex:codex` skill and `ctl handoff` command to continue a
  Claude session in native Codex CLI, with full recorded textual tool evidence,
  private instruction snapshots, and project-memory import.

- Consolidate operating guides, fix Docker setup and diagnostic commands, and
  distinguish historical validation from current behavior.
- Check documentation links in source and release packages. Require versioned
  changelog notes for publication; preserve automatic patch numbering.

## v1.2.2 — 2026-10-24

- Use lowercase `claudex` in product text, terminal branding, documentation, and
  release titles. Preserve the `ClaudexFeedback` tool name.

## v1.2.1 — 2026-10-23

- Update Go dependencies and third-party notices, analysis tools, and pinned
  container runtimes. The end-to-end harness uses Claude Code 2.1.309.

## v1.2.0 — 2026-10-23

- Publish Linux and macOS archives, checksums, and provenance automatically after
  successful main checks; subsequent releases increment the patch version.
- Retry recognized upstream transport failures once before message delivery.
  Show attempt counts and specific command-review failure categories.
- Add opt-in local feedback collection, CLI/TUI controls, and private bounded
  storage. Disable native Anthropic feedback in launched sessions.
- Improve Auto Mode prompt-cache reuse across turns while preserving current
  permission checks.
- Render source citations as readable Markdown links.
- Separate generation and review metrics, link their records, and distinguish
  upstream output, client-delivered content, and review time.

### Upgrading

The private control/metrics API is version 2. Update the CLI and gateway together
and restart the gateway. The Anthropic-compatible Messages API is unchanged.

## v1.1.0 — 2026-10-22

- Replace the web dashboard with a bundled terminal dashboard for monitoring and
  editable settings.
- Start and share a gateway automatically with Claude sessions; offer optional
  persistent operation through Homebrew.
- Add revision-checked live settings while preserving in-flight request settings.
- Update model routing, tool/reasoning translation, reviewer caching, and Go dependencies.

### Upgrading

Stop any previous custom macOS service before starting the automatic gateway or
Homebrew service. The web dashboard and custom service commands were removed.
Open the TUI with `claudex ctl`. `ctl setup` now prepares configuration and sign-in;
`make install` only copies the binary. Remove the obsolete `dashboard` config field.

Existing credential paths are preserved. See [Running](docs/running.md#updating-and-uninstalling)
and [private files](docs/configuration.md#private-files) for update and relocation instructions.
