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
need at runtime. Both the archive and source build place it at `dist/claudex`.

## Quick start

### macOS

```sh
./dist/claudex ctl setup          # sign in and install the macOS service
export PATH="$HOME/.local/bin:$PATH"
claudex                           # start Claude Code through the gateway
```

A bare `claudex` in a terminal can also guide first-time setup. Claude options
pass straight through: `claudex --resume`, `claudex -p "hello"`, and
`claudex --model astra` all use the local gateway. Run `claudex ctl` for a short
health and preferences overview.

### Linux

The service installer is macOS-only. Run the gateway in the foreground instead:

```sh
./dist/claudex ctl setup
./dist/claudex ctl run
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
claudex --model sol
claudex --effort max
```

To change the defaults, edit `model` and `reasoning_effort` in the config, then
[apply the changes](docs/configuration.md#applying-changes). See
[valid values](docs/configuration.md#valid-values) for every model name and effort
level. `claudex ctl config` changes defaults directly, and `claudex ctl` checks
the setup without changing it.

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
- [Experimental GPT auto-mode classifier](docs/auto-mode.md)
- [Development, checks, and releases](docs/development.md)

## License

MIT. [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) lists third-party licenses
and the project that inspired Claudex.
