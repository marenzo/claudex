# Development and releases

Use the Go version declared in `go.mod` or newer. The binary includes the launcher,
TUI, and gateway; no frontend build is required. Test tooling additionally uses
Python 3. Claude Code and Docker are needed only for the indicated integration tests.

```sh
make check                         # Go race tests, vet, gofmt check
make docs-check                    # documentation links, contracts, release tests
make lint                          # gitleaks, staticcheck, and govulncheck; needs network
make secrets                       # only the gitleaks scan of every commit
make build                         # dist/claudex
./dist/claudex ctl --version
make package                       # archive for this OS/architecture in dist/
```

Cross-build with `GOOS=linux GOARCH=arm64 make build`. Output always goes to
`dist/claudex`. Override `VERSION`, `COMMIT`, or `BUILD_DATE` for a reproducible
build stamp. The binary does not depend on the source checkout at runtime.

`make lint` downloads tools pinned in `Makefile`, so it needs network access.
Override `GITLEAKS_VERSION`, `STATICCHECK_VERSION`, or `GOVULNCHECK_VERSION` when needed.
Gitleaks scans every commit reachable from the checkout, so CI fetches
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

`internal/imagegen` fixtures verify the native generation/edit wire contract,
reference bytes, PNG/alpha preservation, output collision recovery, invalid inputs,
quota errors, cancellation, and prevention of automatic generation retries.

The real Claude Code image-skill fixture uses scripted local inference and a
stubbed image command, without subscription credentials. It verifies skill
discovery, helper invocation, receipt delivery, and Read inspection of the PNG:

```sh
CLAUDEX_TEST_CLAUDE_BIN="$(command -v claude)" \
  go test -race -tags e2e -run '^TestClaudeNativeImagegenSkill$' -v ./internal/proxy
```

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

The native handoff fixtures use temporary Claude/Codex homes, a scripted local
provider, and no subscription credentials. They verify actual skill discovery,
SessionStart binding, tool execution, native import/resume, full tool evidence,
project memory, and retry identity:

```sh
make build
CLAUDEX_TEST_CLAUDE_BIN="$(command -v claude)" \
CLAUDEX_TEST_CODEX_BIN="$(command -v codex)" \
CLAUDEX_TEST_BINARY="$PWD/dist/claudex" \
  go test -tags e2e -count=1 -timeout=3m -v \
  -run '^(TestNativeCodexHandoff|TestClaudeToNativeCodexSkill)$' \
  ./internal/handoff ./internal/proxy
```

The live Codex integration test sits behind the `live` build tag. Give it a copy of
your credential without a `refresh_token`, so it cannot rotate a running sign-in:

```sh
CLAUDEX_TEST_AUTH_FILE=/path/to/credential-copy.json go test -tags live ./internal/codex/
```

Additional opt-in reviewer checks use the same non-refreshable credential copy:

```sh
CLAUDEX_TEST_AUTH_FILE=/path/to/credential-copy.json \
  go test -tags live -run TestLiveGPTSafeguardReview -v ./internal/proxy
# Add CLAUDEX_TEST_CLAUDE_BIN and -tags 'live e2e' for
# TestLiveClaudeGPTSafeguardDecision (local generation, live classification).
```

Keep packages scoped to their responsibilities. Prefer direct functions over
provider registries or generic plugin interfaces. Protocol fixes need behavioral
tests for replay, ordering, cancellation, errors, and streamed and non-streamed
output. Use deterministic synchronization in concurrency tests. Never commit private
configuration, credentials, conversation captures, or generated binaries.

## End-to-end harness test

`make e2e` builds a minimal Docker image with the Claude Code CLI and the gateway.
Runtime and Claude Code versions are pinned in `e2e/Dockerfile`, including image digests. It mounts a private copy of your Codex sign-in and
client key, found through
`~/.config/claudex/config.json`. Set `CLAUDEX_E2E_CONFIG` to use another config.

The test drives one resumed Claude Code session through 18 prompts. They cover:

- file writes and edits, shell commands, and glob and grep
- single and parallel subagents, and the todo list
- CLAUDE.md memory, structured output, and web search
- error recovery and automatic compaction, with the window forced to 100k tokens
- a fresh session that must recall the memory

The test uses real subscription quota and takes several minutes. Unlike the live
unit fixtures, this harness retains refresh capability. Cleanup copies a refreshed
credential back to the host only if the host file still matches the initial copy.
Do not share that refresh-token file with an unrelated running client during the run.
`CLAUDEX_E2E_MODEL` and `CLAUDEX_E2E_EFFORT` select the model and effort.
`CLAUDEX_E2E_SKIP_BUILD=1` reuses the last image. The test sits behind the `e2e`
build tag, so `make check` and CI never run it. Claude Code 2.1.x does not offer
Glob, Grep, or TodoWrite to the model by default, so those turns accept shell
fallbacks. Claude Code's autocompact thrash guard can end the compaction read loop
early. That step still passes after at least one successful compaction.

## Binary artifacts and releases

GitHub Actions run checks on pushes and pull requests and build Linux and macOS
archives for amd64 and arm64. Every successful push to the current `main` revision publishes a GitHub release
after Linux/macOS checks, lint, and all four package builds pass. The release has
Linux and macOS archives for amd64 and arm64, SHA-256 checksums, and build
provenance attestations. Pull requests and manual CI runs do not publish.

`VERSION` sets the minimum next stable version, not the installed or latest release. If that version
already exists, the next main push increments the highest stable tag's patch
number. Change `VERSION` to request a higher minor or major version. Reruns reuse
a tag already attached to the same commit and leave published assets unchanged.
Releases are serialized; superseded main revisions skip publication. The job
assembles assets in a draft before automatically publishing the complete release.

`CHANGELOG.md` is the release-notes source. Before merging, prepare a dated section
for the version computed by the release planner. Publication fails if that section
is missing or empty; commit subjects are not a fallback. Automatic patch numbering
is unchanged. If main advances, recheck the planned version. Existing tags remain
immutable. The release job does not commit changelog updates back to main.
Both workflows use `make package`; set `GOOS`, `GOARCH`, `VERSION`, and optionally
`ARCHIVE` to package another target locally.

## Documentation maintenance

Keep one authoritative explanation per topic: Running owns commands and lifecycle;
Configuration owns defaults and aliases; TUI owns metric definitions; Architecture
owns protocols. Other guides summarize and link. Use `claudex` for product prose,
Auto Mode for the Claude feature, and command reviewer for its optional GPT reviewer.
Preserve API identifiers. Record measured results with dates and tested versions.

`make docs-check` checks local paths and anchors, packaged-document links, documented
commands/defaults, and release-planner tests. Release archives include current guides
and historical validation reports, but exclude design studies. Keep license notices
intact and update dependency versions with `go.mod` changes.

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

The default evaluation compares generic and production descriptions across 24
synthetic cases, two models, and two repeats (192 requests). Set
`CLAUDEX_FEEDBACK_EVAL_MODELS` to comma-separated model IDs or
`CLAUDEX_FEEDBACK_EVAL_ARM=clarified` for production instructions only. Clarified
case failures and infrastructure failures fail the run; baseline failures are reported.
Artifacts retain calls, final text, fingerprints, and separate field scores.
Free-text truth still needs human review. See the [dated evaluation report](history/feedback-evaluation-2026-10-23.md)
for interpretation, grader changes, and next validation work.

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

The release planner uses only the Python standard library and is checked on both
CI hosts with `python3 -m unittest discover -s .github/scripts -p 'test_*.py'`.
The native binary does not require Python. `.github/workflows/release.yml` is called
by CI after all required jobs succeed; creating a tag alone does not bypass checks
or start a second release workflow.
