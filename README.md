# Claudex

Run the real **Claude Code CLI with GPT models through your Codex subscription**.

Claudex is a small local gateway. Claude Code sends Anthropic Messages requests to
it, and Claudex translates them into Codex Responses requests. You need Claude
Code and a Codex subscription that covers the selected model. No OpenAI API key is
required.

> [!IMPORTANT]
> Claudex is an independent project. Anthropic and OpenAI do not endorse it.
> Using a Codex subscription through a third-party client may violate provider
> terms or trigger rate limits. See [limitations](docs/limitations.md).

## Install

Download the archive for your platform from the
[releases page](https://github.com/marenzo/claudex/releases) and unpack it, or
[build from source](#build-from-source). The single `claudex` binary is all you
need at runtime.

## Quick start

### macOS

```sh
claudex setup    # init + login + install the launchd service
claudex launch   # start Claude Code through the gateway
```

`setup` is the same as running `claudex init`, `claudex login`, and
`claudex install` in turn.

### Linux

The service installer is macOS-only. Run the gateway in the foreground instead:

```sh
claudex init
claudex login
claudex run
```

Then point Claude Code at it from a second terminal with the environment shown in
[running the portable binary](docs/running.md#portable-binary).

[Running Claudex](docs/running.md) also covers Docker, the dashboard, checking an
installation, and uninstalling.

## Models and effort

Each Claude model family maps to a GPT model:

| Claude family | GPT model | Alias |
| --- | --- | --- |
| Haiku | GPT-5.6 Luna | `luna` |
| Sonnet | GPT-5.6 Terra | `terra` |
| Opus | GPT-5.6 Sol | `sol` |
| Fable | GPT-6 Astra | `astra` |

The default is Astra with `high` effort. Override either per session:

```sh
claudex launch --model sol
claudex launch --effort max
```

To change the defaults, edit `model` and `reasoning_effort` in the config, then
[apply the changes](docs/configuration.md#applying-changes). See
[valid values](docs/configuration.md#valid-values) for every model name and effort
level. `claudex status` checks the whole setup without changing it.

## Build from source

Go 1.27+ is the only build requirement. Node.js 24+ runs the dashboard checks.

```sh
make build                       # writes dist/claudex
make check                       # tests, vet, and gofmt
make install                     # build and install the macOS service
GOOS=linux GOARCH=arm64 make build
```

## Documentation

- [Running: binary, Docker, macOS service, and uninstall](docs/running.md)
- [Configuration, commands, and launcher settings](docs/configuration.md)
- [Architecture and request flow](docs/architecture.md)
- [Dashboard metrics and privacy](docs/dashboard.md)
- [Limitations and troubleshooting](docs/limitations.md)
- [Development, checks, and releases](docs/development.md)

## License

MIT. [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) lists third-party licenses
and the project that inspired Claudex.
