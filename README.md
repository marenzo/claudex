# claudex

Run Claude Code with GPT models through your Codex subscription. One local Go
binary launches Claude Code, translates its API requests, and provides a terminal
dashboard (TUI).

You need an installed Claude Code CLI and a Codex subscription. No OpenAI API key
is required. claudex is independent of Anthropic and OpenAI; provider terms and
subscription limits apply.

## Get started

Download and unpack the Linux or macOS archive for your architecture from
[Releases](https://github.com/marenzo/claudex/releases), then run:

```sh
./dist/claudex
```

First interactive use prepares private configuration, opens Codex sign-in, and
launches Claude Code. The launcher starts or reuses a local gateway. Concurrent
sessions share it; an automatic gateway exits when its clients disconnect and
active requests finish.

In another terminal, open the dashboard:

```sh
./dist/claudex ctl
```

Claude arguments pass through, including resumed sessions and noninteractive work:

```sh
./dist/claudex --resume
./dist/claudex --model sol
./dist/claudex -p 'Explain this repository'
```

Complete `./dist/claudex ctl setup` before first noninteractive use. To run
`claudex` without the relative path, copy the binary into a directory on PATH.
For source builds, `make build` creates the same binary and `make install`
optionally copies it to `~/.local/bin`.

See [Running](docs/running.md) for foreground, Homebrew, Docker, updates, and
uninstall instructions.

## Capabilities

- Use Claude Code's local tools, MCP servers, skills, and resumed sessions.
- Continue a session in native Codex CLI with the built-in
  [`/claudex:codex` skill](docs/handoff.md).
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
| Continue in native Codex CLI | [Session handoff](docs/handoff.md) |
| Generate or edit images | [Native image generation](docs/imagegen.md) |
| Permission review | [Auto Mode](docs/auto-mode.md) |
| Cache reuse and diagnostics | [Prompt caching](docs/prompt-caching.md) |
| Request flow, APIs, translation | [Architecture](docs/architecture.md) |
| Feedback collection and retention | [Local feedback](docs/feedback.md) |
| Build, test, contribute, release | [Development](docs/development.md) |
| Release changes and upgrade actions | [Changelog](CHANGELOG.md) |

MIT licensed. See [third-party notices](THIRD_PARTY_NOTICES.md) and the
[security policy](SECURITY.md).
