# Configuration

The default config is `~/.config/claudex/config.json`. `claudex ctl setup` creates it
with a private random client key. Claudex rejects unknown config fields.
[The example](../examples/config.json) uses Docker paths, so do not copy it
unchanged into a native installation.

| Field | Default | Meaning |
| --- | --- | --- |
| `listen` | `127.0.0.1:8317` | Loopback HTTP listener |
| `auth_file` | None. Required absolute path. Setup writes `<config directory>/auth/codex.json`. | Codex sign-in file, refreshed atomically |
| `client_key_file` | None. Required absolute path. Setup writes `<config directory>/client-key`. | Client key for Claude Code and the dashboard |
| `model` | `gpt-6-astra` | Model used when the request does not select one |
| `reasoning_effort` | `high` | Default reasoning effort |
| `context_window` | `1000000` | Context window advertised to Claude Code |
| `compact_window` | `900000` | Claude Code's automatic compaction window |
| `auto_mode_classifier_model` | Empty (disabled) | Experimental separate GPT permission reviewer; see [auto mode](auto-mode.md) |
| `dashboard` | `false` | Serve the optional authenticated usage dashboard |

## Valid values

| Field | Accepted values |
| --- | --- |
| `listen` | A loopback IP literal and a port from 1 to 65535, such as `127.0.0.1:8317` |
| `model` | `gpt-6-astra`, `gpt-5.6-terra`, `gpt-5.6-sol`, `gpt-5.6-luna`, or an alias or Claude family name listed below |
| `reasoning_effort` | `low`, `medium`, `high`, `xhigh`, `max`, `ultra` |
| `context_window` | A positive integer |
| `compact_window` | A positive integer below `context_window` |
| `auto_mode_classifier_model` | Empty, or any supported model/alias |
| `dashboard` | `true` or `false` |

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

The config file can only bind loopback. Use the `-listen` flag on `claudex ctl run` to
bind another address, such as `0.0.0.0:8317` inside a container.

## Applying changes

Use `claudex ctl config` for guided model, effort, reviewer, and dashboard changes.
For scripts, use `claudex ctl config model astra` or `claudex ctl config reviewer client`.
`claudex ctl config edit` opens the full config in your editor. Changes are validated,
saved, and applied to the managed macOS gateway after active requests finish.
Start a new Claude Code session to load changed startup settings. If you edit the
JSON file yourself, run `claudex ctl restart`; foreground gateways need a manual
restart.

## Compaction window

`compact_window` sets `CLAUDE_CODE_AUTO_COMPACT_WINDOW` for Claude Code. Claude
Code runs the compaction itself. This override takes precedence over an existing
Claude Code compaction setting. For example, the 900,000 default replaces a user
setting of 800,000. A lower window makes each compaction request process less
history. Neither window changes the model's server-side limits.

## Commands

| Command | Behavior |
| --- | --- |
| `claudex [Claude args]` | Launch Claude Code through the gateway; `--resume`, `-p`, and native commands pass through |
| `claudex ctl [--verbose\|--json]` | Check config, sign-in, gateway, and service without changing them |
| `claudex ctl setup` | Initialize, sign in, and install the macOS service; run from a newer binary to reinstall |
| `claudex ctl setup --login` | Refresh Codex sign-in even if current credentials are valid |
| `claudex ctl config [KEY [VALUE]]` | View, guide, or set model, effort, reviewer, and dashboard |
| `claudex ctl config edit` | Edit and validate advanced JSON settings |
| `claudex ctl logs [--follow]` | Show the gateway service log |
| `claudex ctl start\|stop\|restart` | Manage the macOS service |
| `claudex ctl run [flags]` | Run the gateway in the foreground |
| `claudex ctl uninstall` | Remove the service and command, retaining config and credentials |
| `claudex ctl --version` | Print Claudex build version; root `--version` belongs to Claude |

## Flags

Run `claudex ctl --help` for the compact command list, or `claudex ctl <command> --help` for its flags.

| Flag | Command | Behavior |
| --- | --- | --- |
| `--config PATH` | ctl, ctl config, ctl setup, ctl run | Use another config file |
| `--no-browser` | ctl setup | Print the sign-in URL without opening it |
| `--no-login` | ctl setup | Initialize without signing in |
| `--login` | ctl setup | Sign in again |
| `--dashboard` | ctl run | Override the saved dashboard choice for this foreground run |
| `--listen ADDRESS` | ctl run | Override the listener. This is the only way to bind a non-loopback address. The gateway then logs a warning, because the client key travels over plaintext HTTP. |
| `--no-start` | ctl setup | Install without starting the service |

Command-line and startup errors print one plain-text line, `claudex: <message>`, to
standard error. Usage mistakes exit with status 2. Other startup errors exit with
status 1. Once the gateway is serving, it writes JSON log lines.

## macOS launcher overrides

`claudex` inherits unrelated environment variables and applies the values
below. The table shows defaults. Your configured model, effort, listener, and
windows change the generated values.

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
| `CLAUDE_CODE_AUTO_MODE_SERVER` | `1` with the experimental GPT reviewer configured, otherwise `0` for client-side classification |
| `API_TIMEOUT_MS` | `600000` |

Before applying its settings, the launcher removes inherited `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_CUSTOM_HEADERS`,
`CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_FOUNDRY`,
`CLAUDE_CODE_USE_ANTHROPIC_AWS`, and `CLAUDE_CODE_USE_MANTLE`. It does not set
`CLAUDE_CODE_EFFORT_LEVEL`.

Claude settings derived at launch contain the configured `model`, `effortLevel`, and
`permissions.deny: [Artifact]`. `effortLevel` defaults to `high`. A configured
`ultra` becomes Claude's `ultracode` effort. The launcher passes
`--disallowedTools Artifact --settings <JSON>`. Explicit Claude CLI
options still work.

## Dashboard data

The dashboard uses the same client key as Claude Code. It keeps history in memory
and clears it on restart. See [the dashboard notes](dashboard.md) for what it
measures and retains.
