# Running Claudex

## Portable binary

Linux and macOS run the Go gateway directly. Download the archive for your
platform from the [GitHub releases](https://github.com/marenzo/claudex/releases),
verify its checksum, and unpack it. You can also build from source with
`make build`, which writes `dist/claudex`. The single `claudex` binary is all you
need at runtime.

```sh
./claudex -init
./claudex -login
./claudex                      # foreground; Ctrl-C stops it
# Or enable the optional dashboard:
./claudex -dashboard
```

`-init` creates a private config and a random client key. It never overwrites an
existing setup. The default config is `~/.config/claudex/config.json`. For another
location, pass `-config /absolute/path/config.json` to every command.
`-login -no-browser` prints the sign-in URL for you to open. The browser callback
must reach `127.0.0.1:1455` on the machine running Claudex.

On macOS, `claudex install` and `claude-gpt` apply every launcher setting. With a
foreground gateway on Linux, set this environment in a second terminal and run the
installed Claude CLI:

```sh
export ANTHROPIC_BASE_URL=http://127.0.0.1:8317
export ANTHROPIC_AUTH_TOKEN="$(cat "$HOME/.config/claudex/client-key")"
export ANTHROPIC_API_KEY=
export ANTHROPIC_MODEL=gpt-6-astra
export ANTHROPIC_DEFAULT_HAIKU_MODEL=gpt-5.6-luna
export ANTHROPIC_DEFAULT_SONNET_MODEL=gpt-5.6-terra
export ANTHROPIC_DEFAULT_OPUS_MODEL=gpt-5.6-sol
export ANTHROPIC_DEFAULT_FABLE_MODEL=gpt-6-astra
export ANTHROPIC_SMALL_FAST_MODEL=gpt-5.6-luna
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1
export CLAUDE_CODE_MAX_CONTEXT_TOKENS=1000000
export CLAUDE_CODE_AUTO_COMPACT_WINDOW=900000
export API_TIMEOUT_MS=600000
unset CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_CUSTOM_HEADERS
unset CLAUDE_CODE_USE_BEDROCK CLAUDE_CODE_USE_VERTEX CLAUDE_CODE_USE_FOUNDRY
unset CLAUDE_CODE_USE_ANTHROPIC_AWS CLAUDE_CODE_USE_MANTLE
claude --model gpt-6-astra --effort high --disallowedTools Artifact
```

If your config points to a client key elsewhere, use its `client_key_file` path in
the command above. These variables cover routing, context, and authentication. The
macOS launcher also adds friendly model names and a generated Claude settings file.
See [the launcher overrides](configuration.md#macos-launcher-overrides). Never use a
Codex access token as the client key.

## Docker

The image contains only the gateway. Claude Code runs on the host. Build the image
from a source checkout with `make docker`, because binary archives do not include
the Go source. Docker must be running and port 8317 must be free, so stop any host
Claudex service first. This setup keeps Docker credentials separate from a native
installation.

The sign-in callback listener binds to loopback, so sign in on the host with the
native binary first:

```sh
make docker                                    # builds claudex:local
./dist/claudex -config "$HOME/.config/claudex-docker/host.json" -init
./dist/claudex -config "$HOME/.config/claudex-docker/host.json" -login
cp examples/config.json "$HOME/.config/claudex-docker/config.json"
chmod 600 "$HOME/.config/claudex-docker/config.json"
docker run --rm --name claudex \
  --user "$(id -u):$(id -g)" \
  --publish 127.0.0.1:8317:8317 \
  --volume "$HOME/.config/claudex-docker:/data" \
  claudex:local
```

Then use the Claude environment from [the portable binary section](#portable-binary),
reading the client key from `~/.config/claudex-docker/client-key`. To enable the
dashboard, append these arguments after `claudex:local`:

```sh
-config /data/config.json -listen 0.0.0.0:8317 -dashboard
```

Mount a writable **directory**, not a single file. Refreshing the Codex sign-in
replaces `auth/codex.json` atomically, which fails on a file mount. The host UID and
GID let the non-root process write its mounted credential files. The config and key
are private, so keep the directory private too.

The example publishes the host port on loopback only. Inside the container,
`-listen 0.0.0.0:8317` is required for Docker's port forwarding, so the startup
warning about a non-loopback address is expected. Config files can only bind
loopback. Do not expose this plaintext HTTP port to an untrusted network. API
requests still require the client key.

The image has a Docker `HEALTHCHECK` that probes `/healthz`, so `docker ps` shows
the container's health.

To sign in again, stop the container, repeat the host `-login` command, and start
the container again. Only one gateway should own a credential store at a time.

## macOS service

```sh
claudex install                  # -dashboard enables usage metrics; -no-start installs only
claudex service start
claudex service stop
claudex service restart
claudex service status
claudex service models
claudex service login -no-browser
claude-gpt                       # shorthand for: claudex launch
```

`claudex install` does the following:

- Copies the running binary to `~/.local/share/claudex/bin/claudex`.
- Writes `~/.config/claudex/config.json` and the generated Claude settings.
- Installs the `claude-gpt` and `claudex-service` wrappers in `~/.local/bin`.
- Registers the launchd agent `local.claudex.proxy`.

The service log is `~/.local/share/claudex/logs/service.log`. Install keeps an
existing config, client key, and Codex sign-in. It backs up every replaced file
under `~/.local/share/claudex/backups/`. A failed installation restores them.

To update, download the new binary and run `claudex install` again. Install stops
the old service during replacement, so finish active Claude requests first. Pass
`-dashboard` again to keep the dashboard. An installation without that flag
disables it. `claudex service stop` works even when the config is missing or
malformed, so you can stop a broken setup before repairing it.

## Check an installation

```sh
claudex status                   # default config: ~/.config/claudex/config.json
claudex status -config "$HOME/.config/claudex-docker/host.json"
```

`claudex status` changes nothing. It prints one line per check with a level, the
check name, and a detail. The level is `ok`, `warn`, or `fail`. The command exits
with status 1 when any check fails. Checks run in this order:

| Check | What it does |
| --- | --- |
| `config` | Loads the config. Fails with a hint to run `-init` when the file is missing or invalid. The next four checks need the config and are skipped on failure. |
| `client key` | Reads `client_key_file`. Warns when group or other users can read it and suggests `chmod 600`. |
| `codex sign-in` | Fails with a hint to run `claudex -login` when the sign-in is missing, malformed, disabled, or incomplete. Shows when the access token expires. It never refreshes the token. |
| `gateway` | Sends `GET /healthz` to the listen address. Warns when nothing answers and says how to start the gateway. Fails when another program answers or the gateway rejects the client key. |
| `client key accepted` | Sends `GET /v1/models` with the client key. Runs only when the gateway answers. |
| `macos service` | Runs only on macOS. Reports that the service is not installed, or installed and loaded. Warns when it is installed but not loaded. |
| `claude code` | Looks for `claude` on `PATH` and at `~/.local/bin/claude`. Warns when neither exists. |

The second example checks the Docker setup from the host, because `host.json`
shares the container's client key and sign-in.

## Uninstall

```sh
claudex service stop
rm -f ~/Library/LaunchAgents/local.claudex.proxy.plist
rm -f ~/.local/bin/claude-gpt ~/.local/bin/claudex-service
rm -rf ~/.local/share/claudex
rm -rf ~/.config/claudex          # also removes the client key and Codex sign-in
```
