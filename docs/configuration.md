# Configuration

The default config is `~/.config/claudex/config.json`. `claudex init` creates it
with a private random client key. Claudex rejects unknown config fields.
[The example](../examples/config.json) uses Docker paths, so do not copy it
unchanged into a native installation.

| Field | Default | Meaning |
| --- | --- | --- |
| `listen` | `127.0.0.1:8317` | Loopback HTTP listener |
| `auth_file` | None. Required absolute path. `init` writes `<config directory>/auth/codex.json`. | Codex sign-in file, refreshed atomically |
| `client_key_file` | None. Required absolute path. `init` writes `<config directory>/client-key`. | Client key for Claude Code and the dashboard |
| `model` | `gpt-6-astra` | Model used when the request does not select one |
| `reasoning_effort` | `high` | Default reasoning effort |
| `context_window` | `1000000` | Context window advertised to Claude Code |
| `compact_window` | `900000` | Claude Code's automatic compaction window |

## Valid values

| Field | Accepted values |
| --- | --- |
| `listen` | A loopback IP literal and a port from 1 to 65535, such as `127.0.0.1:8317` |
| `model` | `gpt-6-astra`, `gpt-5.6-terra`, `gpt-5.6-sol`, `gpt-5.6-luna`, or an alias or Claude family name listed below |
| `reasoning_effort` | `low`, `medium`, `high`, `xhigh`, `max`, `ultra` |
| `context_window` | A positive integer |
| `compact_window` | A positive integer below `context_window` |

Model names are case-insensitive and may end in `[1m]`. Claudex rewrites the
loaded value to the canonical model ID.

| Name | Model |
| --- | --- |
| `astra`, `fable` | `gpt-6-astra` |
| `terra`, `sonnet` | `gpt-5.6-terra` |
| `sol`, `opus` | `gpt-5.6-sol` |
| `luna`, `haiku` | `gpt-5.6-luna` |

A family name also matches `claude-<family>` and `claude-<family>-<version>`, such
as `claude-opus-4-7`.

The config file can only bind loopback. Use the `-listen` flag on `claudex run` to
bind another address, such as `0.0.0.0:8317` inside a container.

## Applying changes

Config changes require a gateway restart. After changing model, effort, context,
compaction, or listener settings, run `claudex install` again. It regenerates the
launcher's Claude settings. Start a new Claude Code session to load them.

## Compaction window

`compact_window` sets `CLAUDE_CODE_AUTO_COMPACT_WINDOW` for Claude Code. Claude
Code runs the compaction itself. This override takes precedence over an existing
Claude Code compaction setting. For example, the 900,000 default replaces a user
setting of 800,000. A lower window makes each compaction request process less
history. Neither window changes the model's server-side limits.

## Commands

| Command | Behavior |
| --- | --- |
| `claudex run [flags]` | Run the gateway in the foreground |
| `claudex setup [flags]` | Initialize, sign in, and install in one step |
| `claudex init [-config PATH]` | Create the config and a client key |
| `claudex login [-no-browser] [-config PATH]` | Sign in with a Codex subscription |
| `claudex install [-dashboard] [-no-start]` | macOS: install and start the service. See [the macOS service](running.md#macos-service). |
| `claudex start` | Start the installed service |
| `claudex stop` | Stop the installed service |
| `claudex restart` | Restart the installed service |
| `claudex status [-config PATH]` | Check the setup without changing it. See [checking an installation](running.md#check-an-installation). |
| `claudex models` | List models from the running gateway |
| `claudex launch [claude args]` | Start the installed `claude` through the gateway. `claude-gpt` runs this. |
| `claudex uninstall` | Remove the installed service, binary, and wrappers |
| `claudex version` | Print version, commit, and build date |

## Flags

Run `claudex <command> -help` for the authoritative list.

| Flag | Command | Behavior |
| --- | --- | --- |
| `-config PATH` | run, init, login, status | Use another config file |
| `-no-browser` | login, setup | Print the sign-in URL without opening it |
| `-dashboard` | run, install, setup | Enable `/dashboard` and its authenticated metrics API |
| `-listen ADDRESS` | run | Override the listener. This is the only way to bind a non-loopback address. The gateway then logs a warning, because the client key travels over plaintext HTTP. |
| `-no-start` | install, setup | Install without starting the service |

Command-line and startup errors print one plain-text line, `claudex: <message>`, to
standard error. Usage mistakes exit with status 2. Other startup errors exit with
status 1. Once the gateway is serving, it writes JSON log lines.

## macOS launcher overrides

`claude-gpt` runs `claudex launch`. It inherits unrelated environment variables and
applies the values below. The table shows defaults. Your configured model, effort,
listener, and windows change the generated values.

| Variable | Value |
| --- | --- |
| `ANTHROPIC_BASE_URL` | `http://127.0.0.1:8317` |
| `ANTHROPIC_AUTH_TOKEN` | Client key, read when the launcher runs |
| `ANTHROPIC_API_KEY` | Empty |
| `ANTHROPIC_MODEL`, `ANTHROPIC_DEFAULT_MODEL` | `gpt-6-astra` |
| `ANTHROPIC_CUSTOM_MODEL_OPTION` | `gpt-6-astra` |
| `ANTHROPIC_CUSTOM_MODEL_OPTION_NAME` | `GPT-6 Astra (Codex subscription)` |
| `ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION` | `Local Claudex proxy` |
| `ANTHROPIC_SMALL_FAST_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL` | `gpt-5.6-luna` |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | `gpt-5.6-terra` |
| `ANTHROPIC_DEFAULT_OPUS_MODEL` | `gpt-5.6-sol` |
| `ANTHROPIC_DEFAULT_FABLE_MODEL` | `gpt-6-astra` |
| `ANTHROPIC_DEFAULT_{FABLE,OPUS,SONNET,HAIKU}_MODEL_NAME` | Matching GPT name plus ` (Codex subscription)` |
| `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY` | `1` |
| `CLAUDE_CODE_MAX_CONTEXT_TOKENS` | `1000000` |
| `CLAUDE_CODE_AUTO_COMPACT_WINDOW` | `900000` |
| `API_TIMEOUT_MS` | `600000` |

Before applying its settings, the launcher removes inherited `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_CUSTOM_HEADERS`,
`CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_FOUNDRY`,
`CLAUDE_CODE_USE_ANTHROPIC_AWS`, and `CLAUDE_CODE_USE_MANTLE`. It does not set
`CLAUDE_CODE_EFFORT_LEVEL`.

Generated Claude settings contain the configured `model`, `effortLevel`, and
`permissions.deny: [Artifact]`. `effortLevel` defaults to `high`. A configured
`ultra` becomes Claude's `ultracode` effort. The launcher passes
`--disallowedTools Artifact --settings <settings-file>`. Explicit Claude CLI
options still work.

## Dashboard data

The dashboard uses the same client key as Claude Code. It keeps history in memory
and clears it on restart. See [the dashboard notes](dashboard.md) for what it
measures and retains.
