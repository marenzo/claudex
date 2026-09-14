# Changelog

All notable changes to Claudex are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- `claudex install`, `claudex service`, and `claudex launch` subcommands replace
  the former Python installer, service manager, and `claude-gpt` launcher. The
  `claude-gpt` and `claudex-service` wrappers now call the Go binary.
- `claudex status` checks the config, client key, Codex sign-in, gateway, macOS
  service, and Claude Code without changing anything. It exits with status 1 when
  a check fails.
- The gateway logs a warning at startup when the Codex sign-in is unusable, and
  when `-listen` binds a non-loopback address.
- Failed requests log `error_type` and `error_kind`. A wrong or missing client key
  logs `request rejected` with `reason` `invalid_client_key`.
- `make lint` runs pinned staticcheck and govulncheck. CI runs it as a separate job.
- Release archives carry build provenance attestations. Verify one with
  `gh attestation verify`.
- The Docker image has a `HEALTHCHECK` on `/healthz`.
- `make docker`, `make clean`, and `make e2e` (opt-in Docker end-to-end run of Claude Code through the gateway) targets.
- SECURITY.md, CODE_OF_CONDUCT.md, issue and pull request templates, Dependabot.

### Fixed
- Codex citation links are plain Markdown links without the `utm_source=openai`
  tracking parameter; the angle-bracket form was HTML-escaped by some clients.
- A silent upstream stream now fails with error kind `upstream_stalled` after 10
  minutes without events instead of hanging behind keep-alive pings. Codex
  response headers time out after 2 minutes.
- A transient token refresh failure no longer fails requests while the current
  access token is still valid.
- After a 401, the gateway refreshes once and retries, but not when the refreshed
  sign-in belongs to a different account.
- The request body must arrive within 2 minutes. Stalled or broken bodies return
  400; only oversized bodies return 413.
- The `Bearer` scheme is accepted in any letter case, and `Authorization` and
  `X-Api-Key` are checked independently.
- The e2e compaction step passes when Claude Code's autocompact thrash guard ends
  the read loop after at least one successful compaction.

### Changed
- Pre-built release archives no longer contain scripts; the single binary is
  all that is installed. Python is no longer required anywhere.
- Missing config, missing sign-in, and busy-port errors now say what to run next.
- Client-facing 401 messages name the next step and no longer contain file paths.
- Command-line and startup errors print as plain `claudex: <message>` text. Usage
  mistakes exit with status 2, other startup errors with 1.
- The local input token estimate for streamed responses runs in parallel with the
  upstream request and no longer delays the first byte.
- `claudex -init` reuses an existing client key instead of failing.
- `GET /healthz` no longer requires the client key, so Docker, launchd, and
  `claudex status` can probe it. It returns the Claudex health JSON with
  `product`, `status`, `version`, and the default `model`.
- The live Codex integration test requires the `live` build tag.
- Docker base images are pinned by digest, and Dependabot updates them.

### Removed
- Migration from pre-Claudex installations.
- The `internal/util` package. Its helpers moved to `internal/translator/common`.
