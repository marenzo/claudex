# Running claudex

## Portable binary

Use the [getting-started commands](../README.md#get-started) to download the
latest release for your OS and architecture into `~/.local/bin`. Each archive
contains the executable `claudex` at its root, alongside documentation and licenses.
Only the executable is extracted into the installation directory.

If you prefer `wget`, use the same `version` and `archive` variables from those
commands and replace the `curl` download step with:

```sh
wget -O "$HOME/.local/bin/$archive" \
  "https://github.com/marenzo/claudex/releases/download/${version}/${archive}"
```

For an archive downloaded manually from
[Releases](https://github.com/marenzo/claudex/releases), extract and run:

```sh
mkdir -p "$HOME/.local/bin"
tar -xzf /path/to/claudex-VERSION-OS-ARCH.tar.gz -C "$HOME/.local/bin" claudex
export PATH="$HOME/.local/bin:$PATH"
claudex ctl --version
```

The supported archive suffixes are `darwin-amd64` (Intel Mac), `darwin-arm64`
(Apple Silicon), `linux-amd64` (x86-64), and `linux-arm64` (ARM64).
Linux and macOS use the same launcher:

```sh
claudex                         # prepare sign-in if needed; start Claude
claudex --resume
claudex ctl                     # dashboard, in another terminal
```

The binary can also run directly as `./claudex` after unpacking the full archive.
For a source checkout, `make install` builds and copies it into
`$(PREFIX)/bin`, with `PREFIX=$HOME/.local` by default. It does not edit shell files,
sign in, register a service, or run the gateway.

`ctl setup` creates a private config, a random client key, and a Codex sign-in. It
keeps an existing configuration and sign-in. Use `--login` to sign in again,
`--no-browser` to print the URL, or `--no-login` to prepare configuration only.
The browser callback must reach `127.0.0.1:1455` on the machine running claudex.

The default config is `~/.config/claudex/config.json`. Set `CLAUDEX_CONFIG` to use
another configuration when launching Claude. Control commands also accept
`--config /absolute/path/config.json`.

Fresh setup puts the client key at `~/.config/claudex/client-key` and the Codex
sign-in at `~/.config/claudex/auth/codex.json`. Existing setups keep their configured
locations. See [private files](configuration.md#private-files) before moving them.

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
| `claudex ctl handoff [--source PATH] [--launch \| --json]` | [Transfer a Claude session to native Codex](handoff.md) |
| `claudex ctl imagegen --prompt TEXT [--image PATH ...] [--transparent] [--out PATH] [--json]` | [Generate or edit an image](imagegen.md) |
| `claudex ctl --version` | Print the gateway build version; root `--version` belongs to Claude |

Use `--config PATH` on setup, run, logs, status, or the TUI. TUI and status accept
`--url URL --key-file PATH` instead. `ctl run --listen` is the only way to bind a
non-loopback address. It logs a warning because the HTTP listener has no TLS.
Run `claudex ctl --help` or a command's `--help` for its complete syntax.

Usage mistakes exit with status 2; startup failures exit with status 1. Once
Claude is running, claudex propagates its exit status.

## Gateway lifetime

`claudex` reuses a compatible authenticated gateway at the configured address. If
none is listening, it starts a temporary worker from the same binary. Concurrent
launchers serialize startup. A different program or a gateway rejecting the key
produces an error rather than being replaced.

Each launched Claude process and open TUI holds an authenticated connection. Closing
one terminal leaves other clients working; crashes release the connection too.
After five seconds without clients or active requests, an automatic gateway exits.
An open TUI keeps it alive, even when live polling is paused.

For a persistent foreground gateway:

```sh
claudex ctl setup
claudex ctl run                  # Ctrl-C initiates graceful shutdown
```

Launch Claude normally in another terminal. Closing Claude or the TUI does not
stop a foreground gateway. Startup errors are plain text; serving logs are JSON.
Every mode also writes a private rotating log beside the config, under
`config.json.runtime/gateway.log`: 2 MiB plus three rotated files.

```sh
claudex ctl status
claudex ctl status --json
claudex ctl logs --follow
```

## Upstream recovery

Recognized upstream transport failures receive at most one retry after a
randomized 250–499 ms delay. Eligible failures include transport timeouts,
connection resets, premature EOF and selected HTTP/2 stream or connection errors.
There is no separate retry setting.

Generation retries only before any message event reaches Claude Code;
keep-alive pings can continue across attempts. Partial text, reasoning and tool
buffers from the failed attempt are discarded. Once a message has started, an
interruption is reported without replaying it. HTTP errors and malformed responses
are reported directly. The TUI request inspector shows **Upstream attempts** for
retried calls. Auto Mode reviews have their own recovery within the existing
review deadline; see [Auto Mode](auto-mode.md#execution-and-failure-handling).
Token totals retain any usage reported by an interrupted attempt; usage that the
upstream never reports cannot be counted.

The boundary includes an empty `message_start`, not just visible text. This
prevents duplicate text, reasoning or client tool calls. A ping alone keeps the
connection alive without committing the message. If recovery fails after pings
have started a streamed reply, the failure is sent as an SSE error event; the
retained request records the effective failure status.

Logs record `upstream retry` with the call's trace, role, reason category and next
attempt number. A recovered call completes successfully with
`upstream_attempts: 2`; the failed attempt is not an additional completed request.
An interrupted stream log has `retrying: true` when the gateway will attempt
recovery. Inspect the final `messages` record to determine the generation outcome.
Sign-in refresh after an HTTP 401 is handled separately by the credential store.

## Homebrew

The repository is also a Homebrew tap using an explicit remote URL. Its HEAD-only
formula builds current `main`, with Go required only at build time:

```sh
brew tap marenzo/claudex https://github.com/marenzo/claudex
brew install --HEAD marenzo/claudex/claudex
claudex ctl setup
brew services start marenzo/claudex/claudex
```

Service management belongs to Homebrew. Installation never enables the service.
Use `brew services stop` or `brew services restart` with the same formula name.
Run services as your user, so they find your private config and Codex credential.
Set up and sign in interactively before starting a background service.

To update the HEAD build, use `brew upgrade --fetch-HEAD marenzo/claudex/claudex`.
Restart a running Homebrew service after upgrading its binary.

## Docker

The image contains the gateway and TUI binary; Claude Code runs on the host.
Build it from a source checkout with `make docker`. The image runs as a non-root
user, exposes port 8317, and includes a `/healthz` health check.

Sign in on the host because the callback listener binds to loopback. Prepare a
separate directory for Docker, retaining private permissions:

```sh
make install docker
claudex ctl setup --config "$HOME/.config/claudex-docker/host.json"
cp examples/config.json "$HOME/.config/claudex-docker/config.json"
chmod 600 "$HOME/.config/claudex-docker/config.json"
docker run --rm --name claudex \
  --user "$(id -u):$(id -g)" \
  --publish 127.0.0.1:8317:8317 \
  --volume "$HOME/.config/claudex-docker:/data" \
  claudex:local
```

The default container command is `claudex ctl run --config /data/config.json
--listen 0.0.0.0:8317`. The host port is published only on loopback. Mount a writable
**directory**, because credential refresh and settings saves replace files
atomically. A single-file mount cannot support those replacements.

Keep the container running. In another host terminal, launch Claude; open the TUI
in a third terminal:

```sh
CLAUDEX_CONFIG="$HOME/.config/claudex-docker/host.json" claudex
claudex ctl --url http://127.0.0.1:8317 \
  --key-file "$HOME/.config/claudex-docker/client-key"
```

The launcher reads active preferences from the authenticated gateway; host and
container config paths do not need to match. The TUI needs only the gateway URL
and client key, not a local Codex credential. Keep the HTTP listener on trusted
interfaces; use an SSH tunnel or HTTPS reverse proxy for remote access.

## Updating and uninstalling

After updating a binary, restart its running gateway and reopen the TUI to load
the new build. `claudex ctl --version` identifies the CLI binary;
`claudex ctl status --json` identifies the running gateway. An existing process
continues using the binary it loaded until restarted. Foreground gateways stop on
Ctrl-C; Homebrew gateways use `brew services restart`; automatic gateways exit
after their attached clients disconnect and active requests finish.
Ctrl-C or SIGTERM gives active requests up to 30 seconds to finish during shutdown.

For portable binaries, close attached sessions and replace the binary. Unsupported
control protocols are rejected. Reopen the TUI after the gateway restarts.

Uninstall a portable copy by removing that binary. For Homebrew:

```sh
brew services stop marenzo/claudex/claudex
brew uninstall marenzo/claudex/claudex
brew untap marenzo/claudex
```

Configuration, logs, and credentials remain after removing the binary. Fresh
setups keep them under `~/.config/claudex`; removing that directory also deletes
the Codex sign-in and client key. If your configuration uses custom credential
paths, those files live separately and are not removed with the config directory.
