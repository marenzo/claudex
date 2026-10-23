# Configuration

The default config is `~/.config/claudex/config.json`. `claudex ctl setup` creates it
with a private random client key. Claudex rejects unknown config fields.
[The example](../examples/config.json) uses Docker paths, so do not copy it
unchanged into a native installation.

| Field | Default | Meaning |
| --- | --- | --- |
| `listen` | `127.0.0.1:8317` | Loopback HTTP listener |
| `auth_file` | None. Required absolute path. Setup writes `<config directory>/auth/codex.json`. | Codex sign-in file, refreshed atomically |
| `client_key_file` | None. Required absolute path. Setup writes `<config directory>/client-key`. | Client key for inference and gateway control |
| `model` | `gpt-6-astra` | Model used when the request does not select one |
| `reasoning_effort` | `high` | Default reasoning effort |
| `context_window` | `1000000` | Context window advertised to Claude Code |
| `compact_window` | `900000` | Claude Code's automatic compaction window |
| `feedback.enabled` | `false` | Opt-in local feedback collection; see [feedback](feedback.md) |
| `auto_mode_classifier_model` | Empty (disabled) | Experimental separate GPT permission reviewer; see [auto mode](auto-mode.md) |

## Private files

A fresh native setup uses this layout:

```text
~/.config/claudex/
  config.json                      Gateway configuration
  client-key                       Local gateway access secret
  auth/codex.json                   Codex subscription sign-in
  config.json.runtime/gateway.log   Rotating gateway log
  config.json.feedback/            Opt-in feedback records
```

The client key is a random secret used by Claude Code and the TUI for inference
and gateway control. It is separate from the Codex OAuth credentials. The gateway
reads `auth/codex.json` to authenticate upstream and refreshes that file atomically.
The TUI shows credential file locations, never their contents.

Setup creates private directories and files, with permissions `0700` and `0600`
respectively. It preserves an existing configuration and its configured paths;
running setup again does not relocate credentials. A custom `--config` directory
gets its own `client-key` and `auth/codex.json` on first setup.

To relocate existing files, stop the gateway first, move the existing client key
and Codex sign-in without regenerating either, update `client_key_file` and
`auth_file` to absolute paths, and restart using the same configuration. Coordinate
with any other process sharing the sign-in file and move its `.lock` sidecar while
all users of that path are stopped. Keep the client key unchanged so existing
clients can reconnect. This is a local file move, not a new sign-in or an automatic
migration performed by Claudex. The two paths shown by the running TUI must match
the new locations after restart.

## Valid values

| Field | Accepted values |
| --- | --- |
| `listen` | A loopback IP literal and a port from 1 to 65535, such as `127.0.0.1:8317` |
| `model` | `gpt-6.1-sol`, `gpt-5.6-terra`, `gpt-6-luna`, `gpt-6-astra`, `gpt-6-sol`, or an alias or Claude family name listed below |
| `reasoning_effort` | `low`, `medium`, `high`, `xhigh`, `max`, `ultra` |
| `context_window` | A positive integer |
| `compact_window` | A positive integer below `context_window` |
| `auto_mode_classifier_model` | Empty, or any supported model/alias |

Model names are case-insensitive and may end in `[1m]`. Claudex rewrites the
loaded value to the canonical model ID.

| Name | Model |
| --- | --- |
| `astra`, `fable` | `gpt-6-astra` |
| `sol6`, `sonnet` | `gpt-6-sol` |
| `sol`, `opus` | `gpt-6.1-sol` |
| `luna`, `haiku` | `gpt-6-luna` |
| `terra` | `gpt-5.6-terra` |

The model picker contains only these five models, plus Claude Code's Default row
(which resolves to the configured model). Claudex replaces Claude Code's built-in
lineup, so image models, other versions, and duplicate gateway/family rows are
hidden. This applies only to Claude Code launched through `claudex`; ordinary
Claude Code settings are unchanged. Restart the Claude Code process to reload the
launcher settings; a gateway restart alone cannot update an open picker.

Terra remains GPT-5.6 Terra; there is no GPT-6 Terra in the verified catalog.
GPT-6 Sol is the only older model retained.

A family name also matches `claude-<family>` and `claude-<family>-<version>`, such
as `claude-opus-4-7`.

The config file can only bind loopback. Use the `-listen` flag on `claudex ctl run` to
bind another address, such as `0.0.0.0:8317` inside a container.

## Applying changes

Open `claudex ctl config` or the TUI Settings view. Model, effort, reviewer,
context, compaction, and local-feedback changes validate, save atomically, and apply live. Each
in-flight request retains its original settings, including its classifier model.
New Claude processes load changed startup defaults; an existing process may keep
sending its explicitly selected model, effort, and context settings.

Concurrent edits are rejected rather than overwritten. Discard a draft to load
new settings. If the config changed outside the gateway, restart it to load the
file before saving through the TUI. A failed write leaves running settings unchanged.

`listen`, `auth_file`, and `client_key_file` are read-only in the TUI. Stop the
gateway before editing these startup fields. The parser rejects unknown fields.
There is no web dashboard switch or service configuration in the schema.

Set `CLAUDEX_CONFIG` to select a non-default config for the Claude launcher.
Control commands accept `--config`; TUI and status also accept `--url` together
with `--key-file` to connect without local setup. Never pass a client key as an
argument or use a Codex OAuth token as the client key.

## Compaction window

`compact_window` sets `CLAUDE_CODE_AUTO_COMPACT_WINDOW` for Claude Code. Claude
Code runs the compaction itself. This override takes precedence over an existing
Claude Code compaction setting. For example, the 900,000 default replaces a user
setting of 800,000. A lower window makes each compaction request process less
history. Neither window changes the model's server-side limits.

## Commands and flags

| Command | Behavior |
| --- | --- |
| `claudex [Claude arguments]` | Launch Claude through an automatic or existing gateway |
| `claudex ctl` | Open the TUI; requires a terminal |
| `claudex ctl config` | Open TUI settings |
| `claudex ctl status [--json]` | Query authenticated running state |
| `claudex ctl setup` | Create config/client key and sign in, without installing anything |
| `claudex ctl setup --login` | Sign in again using the credential store's file lock |
| `claudex ctl setup --no-browser` | Print the sign-in URL without opening a browser |
| `claudex ctl setup --no-login` | Initialize without signing in |
| `claudex ctl run [--listen IP:PORT]` | Run a persistent foreground gateway |
| `claudex ctl logs [--follow] [--lines N]` | Read the bounded local gateway log |
| `claudex ctl feedback [on|off|status|list]` | Control local feedback collection or read stored reports |
| `claudex ctl --version` | Print the gateway build version; root `--version` belongs to Claude |

Use `--config PATH` on setup, run, logs, status, or the TUI. TUI and status accept
`--url URL --key-file PATH` instead. `ctl run --listen` is the only way to bind a
non-loopback address. It logs a warning because the HTTP listener has no TLS.
Run `claudex ctl --help` or a command's `--help` for its complete syntax.

Usage mistakes exit with status 2; startup failures exit with status 1. Once
Claude is running, Claudex propagates its exit status.

## Launcher overrides

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
| `ANTHROPIC_SMALL_FAST_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL` | `gpt-6-luna` |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | `gpt-6-sol` |
| `ANTHROPIC_DEFAULT_OPUS_MODEL` | `gpt-6.1-sol` |
| `ANTHROPIC_DEFAULT_FABLE_MODEL` | `gpt-6-astra` |
| `ANTHROPIC_DEFAULT_{FABLE,OPUS,SONNET,HAIKU}_MODEL_NAME` | Matching GPT name plus ` (Codex subscription)` |
| `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY` | `1` |
| `CLAUDE_CODE_MAX_CONTEXT_TOKENS` | `1000000` |
| `CLAUDE_CODE_AUTO_COMPACT_WINDOW` | `900000` |
| `CLAUDE_CODE_AUTO_MODE_SERVER` | `1` with the experimental GPT reviewer configured, otherwise `0` for client-side classification |
| `DISABLE_FEEDBACK_COMMAND` | `1` |
| `CLAUDE_CODE_SEND_FEEDBACK` | `0` |
| `CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY` | `1` |
| `API_TIMEOUT_MS` | `600000` |

Before applying its settings, the launcher removes inherited `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_CUSTOM_HEADERS`,
`CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_FOUNDRY`,
`CLAUDE_CODE_USE_ANTHROPIC_AWS`, and `CLAUDE_CODE_USE_MANTLE`. It does not set
`CLAUDE_CODE_EFFORT_LEVEL`.

Claude settings derived at launch contain the configured `model`, `effortLevel`, and
`permissions.deny: [Artifact, SendFeedback, SendUserFeedback]`. `effortLevel` defaults to `high`. A configured
`ultra` becomes Claude's `ultracode` effort. The launcher passes
`--disallowedTools Artifact,SendFeedback,SendUserFeedback --settings <JSON>`.
When local feedback is enabled, it also adds the session MCP configuration and
allows its collector tool. See [feedback](feedback.md). Explicit Claude CLI options
still work.

## Monitoring data

The dashboard uses the same client key as Claude Code. It keeps history in memory
and clears it on restart. See [the TUI guide](tui.md) for what it
measures and retains.
