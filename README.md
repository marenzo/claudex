# claudex

Run the real Claude Code CLI with GPT models through your Codex subscription.
One local Go binary translates Claude's Messages API into Codex's Responses API,
launches Claude, and includes a terminal dashboard.

You need Claude Code and a Codex subscription. No OpenAI API key is required.
claudex is an independent project, unaffiliated with Anthropic or OpenAI. Your
providers' terms and subscription limits still apply.

## Core capabilities

- **Native Claude Code workflows.** Use the real CLI, local tools, MCP servers,
  skills, and resumed sessions through the gateway.
- **Auto Mode support.** Keep Claude Code's permission workflow, with an optional
  separate GPT command reviewer. Server-side review is experimental and opt-in;
  see [Auto Mode](docs/auto-mode.md).
- **Prompt caching.** Stable session routing and request prefixes support Codex
  prompt-cache reuse. The TUI separates generation and reviewer cache usage;
  cache hits depend on the upstream service. See
  [prompt caching](docs/prompt-caching.md).
- **Model and reasoning controls.** Choose the five supported Sol, Terra, Luna,
  and Astra models, set reasoning effort, and configure the reviewer separately.
- **Built-in terminal monitoring.** Inspect live charts, request outcomes, token
  breakdowns, errors, and settings in the bundled TUI. The automatic gateway starts
  with Claude and exits when unused; persistent Homebrew management is optional.

Transient upstream transport failures receive one retry before message delivery.
The TUI shows retry attempts and specific review failure categories. See
[running and recovery](docs/running.md#upstream-recovery).

## Get started

Download the Linux or macOS archive for your architecture from
[Releases](https://github.com/marenzo/claudex/releases) and unpack it. The archive
contains `dist/claudex`. To build the current source instead, run `make build`.

```sh
./dist/claudex
```

On first interactive use, claudex creates its private configuration and client
key and opens Codex sign-in. It starts a temporary gateway and launches your
installed Claude Code. There is no service registration, shell modification, or
separate dashboard installation. You can run the downloaded binary directly.

Use another terminal to open the dashboard while the gateway is running:

```sh
./dist/claudex ctl
```

The gateway is shared by concurrent claudex sessions. It exits five seconds after
its last Claude or dashboard session disconnects and active requests finish.
Open dashboard sessions keep it alive. Foreground and Homebrew-managed gateways
remain running until stopped by their owner.

Claude arguments pass through:

```sh
./dist/claudex --resume
./dist/claudex --model sol
./dist/claudex -p 'Explain this repository'
```

For noninteractive use, complete `./dist/claudex ctl setup` first. Native Claude
commands such as `--help`, `--version`, and `mcp` work without a running gateway.

To put the binary on PATH, copy it to a directory already on your PATH. Source
builds can use `make install`, which copies it to `~/.local/bin` by default and
performs no setup or service registration.

The default private files live under `~/.config/claudex`: `config.json`,
`client-key`, and `auth/codex.json`. The client key authenticates Claude Code and
the TUI to the gateway; the Codex sign-in authenticates the gateway upstream.
See [configuration and credential paths](docs/configuration.md#private-files).

## Terminal dashboard

`claudex ctl` opens a responsive terminal workspace with live traffic charts,
request outcome donuts, token and cache breakdowns, response timelines, and
searchable request records. Wide terminals show the request table and inspector
side by side; smaller terminals use focused views. Colors follow the terminal's
light or dark background. Press **1–4** to switch views, **Tab** to focus a pane,
**Ctrl+k** for commands, or **?** for shortcuts.

Monitoring starts with generation requests. Press **g** to switch between
generation, Auto reviews, and all upstream calls. The inspector links reviews to
their generation request and separates model output, review time, and the first
content delivered to Claude Code.

The Settings view changes model, effort, command reviewer, and conversation-size
preferences. Valid changes save and apply without interrupting requests; new
Claude processes pick up changed startup defaults. All runtime dependencies are
compiled into the binary.

```sh
claudex ctl config              # open Settings
claudex ctl status --json       # status for scripts; no terminal required
claudex ctl logs --follow       # bounded local gateway log
```

Metrics are always collected in bounded memory: 500 recent requests and 120 minutes
of history, cleared on gateway restart. Prompts, replies, and tool payloads are
not retained in monitoring history. Opt-in [local feedback](docs/feedback.md) stores
separate structured reports for 30 days and can be toggled with
`claudex ctl feedback on|off`. Native Anthropic feedback is disabled in launched
sessions. Error excerpts are bounded and redacted; review them before sharing.
See [the TUI guide](docs/tui.md) for controls and exact metric definitions.

## Optional persistent gateway

Run in a terminal:

```sh
claudex ctl run                 # Ctrl-C stops the foreground gateway
```

Or let Homebrew install and manage it. The formula lives in this repository and
currently builds `main` with `--HEAD`; Homebrew installs Go for the build.

```sh
brew tap marenzo/claudex https://github.com/marenzo/claudex
brew install --HEAD marenzo/claudex/claudex
claudex ctl setup
brew services start marenzo/claudex/claudex
```

Homebrew service management is optional. `brew install` and `ctl setup` do not start
or register a service. Use `brew services stop marenzo/claudex/claudex` to stop it.
The same TUI attaches to automatic, foreground, Homebrew, and Docker gateways.

## Models

| Alias | Model |
| --- | --- |
| `sol` | GPT-6.1 Sol |
| `terra` | GPT-5.6 Terra |
| `luna` | GPT-6 Luna |
| `astra` | GPT-6 Astra (default) |
| `sol6` | GPT-6 Sol |

The Claude model picker includes only these models and Default. Claude families
map `opus` to Sol, `sonnet` to Sol 6, `haiku` to Luna, and `fable` to Astra. The
experimental permission reviewer is configured separately; its effort is fixed
at high. See [auto mode](docs/auto-mode.md).

## Build and documentation

Go 1.27.3 or newer is required to build. Running the binary needs neither Go nor Node.

```sh
make check build
make install                    # optional binary copy; PREFIX defaults to ~/.local
GOOS=linux GOARCH=arm64 make build
```

- [Running](docs/running.md): portable binary, foreground, Homebrew, Docker, and uninstall
- [Configuration](docs/configuration.md): settings, model routing, and Claude overrides
- [Auto Mode](docs/auto-mode.md): permission review, setup, and current limits
- [Prompt caching](docs/prompt-caching.md): cache reuse, accounting, and diagnostics
- [Response formatting](docs/response-formatting.md): readable source citations in Claude Code
- [TUI](docs/tui.md): dashboard controls, measurements, and retention
- [Architecture](docs/architecture.md): APIs and translation behavior
- [Limitations](docs/limitations.md): compatibility and troubleshooting
- [Development](docs/development.md): tests, checks, and releases
- [Changelog](CHANGELOG.md): release changes and upgrade notes

MIT licensed. See [third-party notices](THIRD_PARTY_NOTICES.md).
