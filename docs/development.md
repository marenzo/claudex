# Development and releases

Use Go 1.26+ to build. Node.js 24+ runs the dashboard JavaScript checks in
`make check`. The gateway has three direct Go dependencies and no frontend package
manager or web framework. The binary embeds the dashboard and a pinned Apache
ECharts distribution, so no CDN is contacted.

```sh
make check                         # Go tests, dashboard JS syntax/tests, vet, gofmt check
make lint                          # staticcheck and govulncheck; needs network
make test-dashboard                # only dashboard syntax and built-in Node tests
make build                         # dist/claudex
./dist/claudex -version
make package                       # archive for this OS/architecture in dist/
```

Cross-build with `GOOS=linux GOARCH=arm64 make build`. Output always goes to
`dist/claudex`. Override `VERSION`, `COMMIT`, or `BUILD_DATE` for a reproducible
build stamp. The binary does not depend on the source checkout at runtime.

`make lint` runs staticcheck v0.8.1 and govulncheck v1.8.0. It downloads the pinned
tools, so it needs network access. Override `STATICCHECK_VERSION` or
`GOVULNCHECK_VERSION` to use another version.

Node runs only JavaScript syntax checks and the built-in test runner. `make build`,
Docker builds, and running Claudex do not need it. There is no npm install or
frontend build step. See [the dashboard notes](dashboard.md) for metric
definitions, frontend structure, and vendor provenance.

## Tests

Tests use local fixtures. They must not require a live subscription, an installed
service, an existing build, or user credentials. Opt-in integration tests are
separate.

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

GitHub Actions workflows live in `.github/workflows`. Actions are pinned to
official release commit IDs. Docker base images are pinned by digest. Dependabot
updates Go modules, actions, and base images.

- **Check and build** runs on branch pushes, pull requests, and manual dispatch. It
  checks Linux and macOS and verifies amd64 and arm64 archives for both. A separate
  lint job runs `make lint`. It also builds and smoke-tests the Docker image.
  Branch checks upload no binaries or images.
- **Release binaries** runs when a maintainer pushes a version tag such as
  `v1.2.3`. It runs the checks, builds the same four archives, and attests build
  provenance for each archive with `actions/attest-build-provenance`. It then
  writes `SHA256SUMS` and creates a **draft** GitHub release. A maintainer reviews
  the draft before publishing it. Only that final job can write repository
  contents.

Both workflows use `make package`, which builds the binary and packages the public
files. Set `GOOS`, `GOARCH`, `VERSION`, and optionally `ARCHIVE` to package another
target locally. Archives contain `dist/claudex`, examples, docs, README, the MIT
license, and third-party notices. The binary also carries the macOS installer,
service manager, and Claude launcher as subcommands.

To verify archives, run `sha256sum -c SHA256SUMS` on Linux or
`shasum -a 256 -c SHA256SUMS` on macOS. Add `--ignore-missing` when you downloaded
only some archives. You can also run `sha256sum <archive>` or
`shasum -a 256 <archive>` and compare the line with the manifest. Checksums confirm
integrity against the published manifest. They do not prove publisher identity.

The provenance attestation proves that this repository's release workflow built an
archive. Verify it with the GitHub CLI:

```sh
gh attestation verify claudex-<version>-<os>-<arch>.tar.gz --repo marenzo/claudex
```

Binaries are not signed or notarized.

## Contributions and security reports

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the contribution workflow and
[SECURITY.md](../SECURITY.md) for private vulnerability reporting. Record
user-visible changes in [CHANGELOG.md](../CHANGELOG.md).
