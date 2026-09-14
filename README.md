# Claudex

Run the real **Claude Code CLI with GPT models through your Codex subscription**.
Claudex is a small local gateway. Claude messages go in, and Codex Responses come
out. You need Claude Code and a Codex subscription that can use the selected
model. No OpenAI API key is required.

Claudex is a single Go binary with no runtime dependencies. It ships as pre-built
binaries for Linux and macOS and as a Docker image you build with one command.

> **Unofficial.** Claudex is an independent project. Anthropic and OpenAI do not
> endorse it. Using a Codex subscription through a third-party client may break
> OpenAI's terms and can lead to rate limits or account action. The gateway
> identifies itself upstream as the Codex CLI. Use it at your own risk. See
> [limitations](docs/limitations.md).

## Install a pre-built binary

Download the archive for your platform from the
[GitHub releases page](https://github.com/marenzo/claudex/releases):

| Platform | Archive |
| --- | --- |
| macOS, Apple Silicon | `claudex-<version>-darwin-arm64.tar.gz` |
| macOS, Intel | `claudex-<version>-darwin-amd64.tar.gz` |
| Linux, x86-64 | `claudex-<version>-linux-amd64.tar.gz` |
| Linux, Arm64 | `claudex-<version>-linux-arm64.tar.gz` |

Verify and unpack, replacing the file names with the ones you downloaded:

```sh
shasum -a 256 -c SHA256SUMS --ignore-missing     # Linux: sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify claudex-<version>-<os>-<arch>.tar.gz --repo marenzo/claudex
tar -xzf claudex-<version>-<os>-<arch>.tar.gz
cd claudex-<version>-<os>-<arch>
```

The attestation step needs the GitHub CLI. It proves that the release workflow of
this repository built the archive. Binaries are not signed or notarized. If Gatekeeper blocks the binary on macOS,
allow it once with `xattr -d com.apple.quarantine dist/claudex`.

### macOS: background service and `claude-gpt`

```sh
./dist/claudex install              # add -dashboard for local usage metrics
export PATH="$HOME/.local/bin:$PATH"
claudex service login               # first install only; an existing sign-in is kept
claudex status                      # checks the whole setup and changes nothing
claude-gpt
```

`claudex install` starts a user launchd service. It installs two wrappers in
`~/.local/bin`. `claude-gpt` launches your installed `claude` with the gateway
settings, and `claudex-service` manages the service. Rerun `install` after
downloading a new version. Start a new Claude Code session after changing settings.

### Linux and macOS: foreground gateway

```sh
./dist/claudex -init                # writes ~/.config/claudex/config.json and a client key
./dist/claudex -login               # Codex sign-in in the browser
./dist/claudex                      # runs until Ctrl-C
```

Then point Claude Code at `http://127.0.0.1:8317` with the client key as
`ANTHROPIC_AUTH_TOKEN`. The full environment block is in
[docs/running.md](docs/running.md#portable-binary).

## Get started with Docker

The image contains only the gateway, and Claude Code runs on the host. Sign in with
the native binary once, then run the container against the same directory:

```sh
make docker                                                   # builds claudex:local
./dist/claudex -config "$HOME/.config/claudex-docker/host.json" -init
./dist/claudex -config "$HOME/.config/claudex-docker/host.json" -login
cp examples/config.json "$HOME/.config/claudex-docker/config.json"
docker run --rm --name claudex \
  --user "$(id -u):$(id -g)" \
  --publish 127.0.0.1:8317:8317 \
  --volume "$HOME/.config/claudex-docker:/data" \
  claudex:local
```

To enable the dashboard, add `-config /data/config.json -listen 0.0.0.0:8317
-dashboard` after the image name. [docs/running.md](docs/running.md#docker)
explains the details, including why the mount must be a directory.

## Pick a model

```sh
claude-gpt --model opus             # GPT-5.6 Sol
claude-gpt --model terra            # direct aliases also work
claude-gpt --effort max
claude-gpt --effort ultra           # Claude multi-agent ultracode, GPT xhigh (like Codex Ultra)
claudex service status
claudex service restart
```

| Claude family | GPT model | Direct alias |
| --- | --- | --- |
| Haiku | GPT-5.6 Luna | `luna` |
| Sonnet | GPT-5.6 Terra | `terra` |
| Opus | GPT-5.6 Sol | `sol` |
| Fable | GPT-6 Astra | `astra` |

The default is Astra with high effort. Model selection also applies to agent
requests and versioned Claude model IDs.

| Claude Code effort | Effort sent to GPT |
| --- | --- |
| `low`, `medium`, `high` | Same level |
| `max` | `max` |
| `ultra` (Claude Code's `ultracode`) | `xhigh`, with Claude Code's multi-agent workflow |

Ultra mirrors Codex's own Ultra setting, which is not a separate API level. Codex
sends `xhigh` and turns on proactive task delegation. With Claudex, Claude Code
provides the delegation. Codex does not offer Ultra for Luna. See
[the translation notes](docs/architecture.md#reasoning-and-tools) for details.

## Optional dashboard

```sh
claudex install -dashboard          # or: claudex -dashboard for the foreground gateway
```

Open `http://127.0.0.1:8317/dashboard` and enter the client key. Its path is
`client_key_file` in `~/.config/claudex/config.json`. The dashboard shows request
timelines, latency percentiles, token and cache usage, model and effort
comparisons, and searchable request logs. Data stays in memory and resets on
restart. See [docs/dashboard.md](docs/dashboard.md).

## Troubleshooting

Run `claudex status` first. It checks the config, client key, Codex sign-in,
gateway, macOS service, and Claude Code. It exits with status 1 when a check fails.
[docs/running.md](docs/running.md#check-an-installation) explains each check.
[docs/limitations.md](docs/limitations.md#troubleshooting) covers common errors.

## Build from source

Go 1.26+ is the only build requirement. Node.js 24+ runs the dashboard JavaScript
checks in `make check`.

```sh
make build            # dist/claudex for this machine
make check            # tests, vet, gofmt
make install          # macOS: build and run `claudex install`
make docker           # claudex:local image
GOOS=linux GOARCH=arm64 make build
```

## More

- [Running: portable binary, Docker, macOS service, uninstall](docs/running.md)
- [Architecture and request flow](docs/architecture.md)
- [Configuration and every launcher environment override](docs/configuration.md)
- [Dashboard metrics and privacy](docs/dashboard.md)
- [Limitations, assumptions, and troubleshooting](docs/limitations.md)
- [Development, checks, and releases](docs/development.md)
- [Contributing](CONTRIBUTING.md)

MIT licensed. [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) lists third-party
licenses and the project that inspired Claudex.
