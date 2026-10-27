# claudex

Run Claude Code with GPT models through your Codex subscription. One local Go
binary launches Claude Code, translates its API requests, and provides a terminal
dashboard (TUI).

You need an installed Claude Code CLI and a Codex subscription. No OpenAI API key
is required. claudex is independent of Anthropic and OpenAI; provider terms and
subscription limits apply.

## Get started

Download the latest Linux or macOS [release](https://github.com/marenzo/claudex/releases)
and extract its root-level binary into `~/.local/bin`:

```sh
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m | sed 's/^x86_64$/amd64/; s/^aarch64$/arm64/')"
release_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/marenzo/claudex/releases/latest)"
version="${release_url##*/}"
archive="claudex-${version}-${os}-${arch}.tar.gz"

mkdir -p "$HOME/.local/bin"
curl -fL "https://github.com/marenzo/claudex/releases/download/${version}/${archive}" \
  -o "$HOME/.local/bin/$archive"
tar -xzf "$HOME/.local/bin/$archive" -C "$HOME/.local/bin" claudex
rm "$HOME/.local/bin/$archive"
export PATH="$HOME/.local/bin:$PATH"
claudex
```

Add `export PATH="$HOME/.local/bin:$PATH"` to `~/.zshrc` or `~/.bashrc` to keep the
command available in new terminals. See [Running](docs/running.md#portable-binary)
for a `wget` alternative and manual extraction.

First interactive use prepares private configuration, opens Codex sign-in, and
launches Claude Code. The launcher starts or reuses a local gateway. Concurrent
sessions share it; an automatic gateway exits when its clients disconnect and
active requests finish.

In another terminal, open the dashboard:

```sh
claudex ctl
```

Claude arguments pass through, including resumed sessions and noninteractive work:

```sh
claudex --resume
claudex --model sol
claudex -p 'Explain this repository'
```

Complete `claudex ctl setup` before first noninteractive use. For source builds,
`make install` builds and installs the command into `~/.local/bin`.

See [Running](docs/running.md) for foreground, Homebrew, Docker, updates, and
uninstall instructions.

## Capabilities

- Use Claude Code's local tools, MCP servers, skills, and resumed sessions.
- Move sessions between Claude Code and native Codex CLI with
  [`claudex:handoff`](docs/handoff.md): `/claudex:handoff` in Claude,
  `$claudex:handoff` in Codex after `claudex ctl setup`.
- Generate or edit images with the built-in
  [`/claudex:imagegen` skill](docs/imagegen.md) through the same Codex subscription.
- Choose a supported GPT model and reasoning effort through Claude arguments or
  TUI Settings. See [Configuration](docs/configuration.md#valid-values).
- Inspect generation and review usage, cache reuse, timing, and failures in the
  bundled [TUI](docs/tui.md).
- Optionally enable a separate GPT command reviewer for
  [Auto Mode](docs/auto-mode.md). It is experimental and off by default.
- Optionally collect [local feedback](docs/feedback.md). Reports stay on the
  gateway machine; native Anthropic feedback is disabled in launched sessions.

Protocol translation has limits. Review [compatibility and troubleshooting](docs/limitations.md),
including unsupported controls, approximate context estimates, and credential ownership.

## Documentation

| Topic | Guide |
| --- | --- |
| Install, operate, update, recover | [Running](docs/running.md) |
| Settings, models, credentials, launcher overrides | [Configuration](docs/configuration.md) |
| Controls, metrics, monitoring privacy | [Terminal dashboard](docs/tui.md) |
| Move between Claude Code and Codex CLI | [Session handoff](docs/handoff.md) |
| Generate or edit images | [Native image generation](docs/imagegen.md) |
| Permission review | [Auto Mode](docs/auto-mode.md) |
| Cache reuse and diagnostics | [Prompt caching](docs/prompt-caching.md) |
| Request flow, APIs, translation | [Architecture](docs/architecture.md) |
| Feedback collection and retention | [Local feedback](docs/feedback.md) |
| Build, test, contribute, release | [Development](docs/development.md) |
| Release changes and upgrade actions | [Changelog](CHANGELOG.md) |

MIT licensed. See [third-party notices](THIRD_PARTY_NOTICES.md) and the
[security policy](SECURITY.md).
