# Claudex

Run the real **Claude Code CLI with GPT models through your Codex subscription**.
Claudex is a small local gateway: Claude messages go in and Codex Responses come
out. You need Claude Code and a Codex subscription for the selected model; no
OpenAI API key is required.

Claudex is an independent project. Anthropic and OpenAI do not endorse it.
Using a Codex subscription through a third-party client may violate provider
terms or cause rate limits. See [limitations](docs/limitations.md).

## Quick start

```sh
claudex setup
```

Or step by step:

```sh
claudex init
claudex login
claudex install
```

See [running Claudex](docs/running.md) for the macOS service, Docker, dashboard,
environment settings, and uninstall instructions.

## Choose a model

```sh
claude-gpt --model astra
claude-gpt --effort max
claudex status
```

| Claude family | GPT model | Alias |
| --- | --- | --- |
| Haiku | GPT-5.6 Luna | `luna` |
| Sonnet | GPT-5.6 Terra | `terra` |
| Opus | GPT-5.6 Sol | `sol` |
| Fable | GPT-6 Astra | `astra` |

The default is Astra with high effort. See the [model and effort details](docs/configuration.md#valid-values).

## Build from source

Go 1.27+ is the only build requirement. Node.js 24+ runs the dashboard checks.

```sh
make build
make check
make install       # macOS service
GOOS=linux GOARCH=arm64 make build
```

## Documentation

- [Running: binary, Docker, macOS service, and uninstall](docs/running.md)
- [Configuration and launcher settings](docs/configuration.md)
- [Architecture and request flow](docs/architecture.md)
- [Dashboard metrics and privacy](docs/dashboard.md)
- [Limitations and troubleshooting](docs/limitations.md)
- [Development, checks, and releases](docs/development.md)

MIT licensed. [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) lists third-party
licenses and the project that inspired Claudex.
