# Development and releases

Use Go 1.27.3 or newer. Bubble Tea v2, Lip Gloss v2, and Bubbles v2 are pinned
Go dependencies. The binary includes the launcher, TUI, and gateway. No browser
assets, frontend package manager, Node installation, or separate TUI installation
is needed.

```sh
make check                         # Go race tests, vet, gofmt check
make lint                          # gitleaks, staticcheck, and govulncheck; needs network
make secrets                       # only the gitleaks scan of every commit
make build                         # dist/claudex
./dist/claudex ctl --version
make package                       # archive for this OS/architecture in dist/
```

Cross-build with `GOOS=linux GOARCH=arm64 make build`. Output always goes to
`dist/claudex`. Override `VERSION`, `COMMIT`, or `BUILD_DATE` for a reproducible
build stamp. The binary does not depend on the source checkout at runtime.

`make lint` runs gitleaks v8.30.1, staticcheck v0.8.2, and govulncheck v1.8.0. It
downloads the pinned tools, so it needs network access. Override
`GITLEAKS_VERSION`, `STATICCHECK_VERSION`, or `GOVULNCHECK_VERSION` to use another
version. gitleaks scans every commit reachable from the checkout, so CI fetches
full history. Give fake credentials in tests a `fixture-` prefix;
`.gitleaks.toml` allows only values that start with it.

`make install` copies the built binary to `$(PREFIX)/bin` (default `~/.local/bin`).
It does not run setup or register a service. The optional Homebrew formula is
HEAD-only and runs the same foreground command through `brew services`.
See [the TUI guide](tui.md) for metric definitions and interface behavior.

## Tests

Tests use local fixtures. They must not require a live subscription, a running
gateway, an existing build, or user credentials. Opt-in integration tests are
separate.

The local real-client permission tests need an installed Claude Code binary but
use localhost model fixtures and an isolated Claude configuration. They execute
only a harmless `printf` for an allowed verdict and verify that a flagged call is
denied. Both outcomes are checked with an injected classifier transport timeout:

```sh
CLAUDEX_TEST_CLAUDE_BIN="$(command -v claude)" \
  go test -race -tags e2e \
  -run '^TestClaudeSafeguard(Decisions|TransportRecovery)$' -v ./internal/proxy
```

This command uses no live inference or subscription credentials. Default proxy
tests cover bounded retry exhaustion, cancellation, account changes, simultaneous
transport failures, discarded partial verdicts and tool buffers, and prevention
of replay after message delivery. See [recovery behavior](running.md#upstream-recovery)
and [review diagnostics](auto-mode.md#review-failure-categories).

The live Codex integration test sits behind the `live` build tag. Give it a copy of
your credential without a `refresh_token`, so it cannot rotate a running sign-in:

```sh
CLAUDEX_TEST_AUTH_FILE=/path/to/credential-copy.json go test -tags live ./internal/codex/
```

Keep packages scoped to their responsibilities. Prefer direct functions over
provider registries or generic plugin interfaces. Protocol fixes need behavioral
tests for replay, ordering, cancellation, errors, and streamed and non-streamed
output. Use deterministic synchronization in concurrency tests. Never commit private
configuration, credentials, conversation captures, or generated binaries.

## End-to-end harness test

`make e2e` builds a minimal Docker image with the Claude Code CLI and the gateway.
It mounts a private copy of your Codex sign-in and client key, found through
`~/.config/claudex/config.json`. Set `CLAUDEX_E2E_CONFIG` to use another config.

The test drives one resumed Claude Code session through 18 prompts. They cover:

- file writes and edits, shell commands, and glob and grep
- single and parallel subagents, and the todo list
- CLAUDE.md memory, structured output, and web search
- error recovery and automatic compaction, with the window forced to 100k tokens
- a fresh session that must recall the memory

The test uses real subscription quota and takes several minutes.
`CLAUDEX_E2E_MODEL` and `CLAUDEX_E2E_EFFORT` select the model and effort.
`CLAUDEX_E2E_SKIP_BUILD=1` reuses the last image. The test sits behind the `e2e`
build tag, so `make check` and CI never run it. Claude Code 2.1.x does not offer
Glob, Grep, or TodoWrite to the model by default, so those turns accept shell
fallbacks. Claude Code's autocompact thrash guard can end the compaction read loop
early. That step still passes after at least one successful compaction.

## Binary artifacts and releases

GitHub Actions run checks on pushes and pull requests and build Linux and macOS
archives for amd64 and arm64. A version tag builds the same archives and creates
a draft GitHub release for maintainer review. Its notes list the commit subjects
since the previous tag, so write commit subjects as user-facing change lines.
Both workflows use `make package`; set `GOOS`, `GOARCH`, `VERSION`, and optionally
`ARCHIVE` to package another target locally.

## Security reports

See [SECURITY.md](../SECURITY.md) for private vulnerability reporting.

## Feedback tests and evals

`make check` covers the schema, category consistency, redaction, atomic private
storage, retry deduplication, retention/capacity, MCP lifecycle, authentication,
settings persistence, live disable, pagination, CLI/TUI controls, and eval graders.
No live provider or production config is needed.

To exercise real Claude Code discovery, execution and a durable receipt with
scripted inference (no provider request):

```sh
make build
CLAUDEX_TEST_CLAUDE_BIN=/absolute/path/to/claude \
CLAUDEX_TEST_BINARY="$PWD/dist/claudex" \
go test -tags e2e -run '^TestClaudeLocalFeedbackMCP$' -count=1 -timeout=2m -v ./internal/proxy
```

The live feedback eval uses actual subscription quota. Use a private credential
copy with `refresh_token` removed, as for other live tests. Generated tool calls
are graded and never executed or sent to a feedback collector:

```sh
CLAUDEX_TEST_AUTH_FILE=/path/to/credential-copy.json \
CLAUDEX_FEEDBACK_EVAL=1 CLAUDEX_FEEDBACK_EVAL_REPEATS=2 \
CLAUDEX_FEEDBACK_EVAL_OUTPUT=/tmp/feedback-results.json \
go test -tags live -run '^TestLiveFeedbackEvaluation$' -count=1 -timeout=20m -v ./internal/proxy
```

By default this runs 24 synthetic scenarios against GPT-6.1 Sol and GPT-6 Astra
at low reasoning effort, comparing a generic description with the production
instructions while holding the name/schema constant. Two repeats produce 192
requests. Override models with comma-separated `CLAUDEX_FEEDBACK_EVAL_MODELS`, or
set `CLAUDEX_FEEDBACK_EVAL_ARM=clarified` to run only the production description.
The test requires every clarified case to pass; baseline failures are reported
without failing the test. Infrastructure failures are separate and fail the run.

The artifact retains generated calls, final text, model metadata, input fingerprints,
latency and separate selection/schema/classification/recovery/reproduction/evidence
scores. No hidden reasoning is requested. Evidence grading checks known anchors
and literal sensitive strings, not arbitrary semantic truth. Review outputs before
claiming grounding or production reliability. Expected labels are never sent to
models. The baseline is not Anthropic's native tool.

Replay retained synthetic outputs after a grader correction without spending quota:

```sh
CLAUDEX_FEEDBACK_EVAL_REPLAY=/tmp/feedback-results.json \
CLAUDEX_FEEDBACK_EVAL_OUTPUT=/tmp/feedback-regraded.json \
go test -tags live -run '^TestReplayFeedbackEvaluation$' -count=1 -v ./internal/proxy
```

Keep the original artifact; regrade metadata records the source and current fixture
fingerprints. Regrading is meaningful only when model-facing scenarios and tool
instructions match the original run. Delete the temporary credential copy after
live runs. Do not commit credentials or private conversation captures.
