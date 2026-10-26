# Limitations and assumptions

claudex translates between two protocols. It cannot make their capabilities
identical.

Auto Mode can use Claude Code's client-side classifier or the opt-in
[experimental GPT server classifier](auto-mode.md). Neither gives this gateway
Anthropic's no-charge classifier service; classification consumes Codex usage.

- **Unofficial subscription integration.** OpenAI and Anthropic do not endorse
  claudex. Using a Codex subscription through a third-party client may breach
  OpenAI's terms of use. The gateway presents itself upstream as the Codex CLI.
  Rate limits or account action are your risk. The Codex subscription endpoints and
  OAuth flow can change without notice. Model availability depends on the signed-in
  account and upstream behavior. claudex uses one account, with no API-key mode or
  account failover.
- **Claude Code remains the harness.** It owns orchestration, tools, and automatic
  compaction. Reasoning settings are translated rather than copied verbatim;
  see [reasoning and tools](architecture.md#reasoning-and-tools). Disabled thinking
  cannot guarantee zero reasoning on every model.
- **Some generation controls are not forwarded.** `max_tokens`, `temperature`,
  `top_p`, and `stop_sequences` lose their Anthropic meaning. Do not rely on them to
  limit spend, response length, sampling, or stopping.
- **Tools have limits.** Native Codex search replaces WebSearch. Anthropic
  `blocked_domains` and `max_uses` search controls are not enforced. WebFetch stays
  a Claude Code tool. Search-grounded text and Markdown citation links are kept.
  Raw search call and result blocks are dropped so sessions stay valid when resumed
  with Claude. Citations can repeat. Artifact publishing is disabled, but ordinary
  file generation works. Anthropic server tools such as Advisor, server WebFetch,
  and server tool search return explicit unsupported-tool errors. Advisor errors
  use Claude Code's documented retry shape. Programmatic-only tools cannot be
  converted into directly callable functions.
- **Provider state is only partly portable.** Codex assistant message phases and
  provider-owned compaction/thread state have no equivalent round trip through
  ordinary Claude transcript blocks. Exact phase replay is not implemented.
  Do not assume identical continuation behavior to the native Codex harness.
- **Context estimates are approximate.** Local counts use O200k for text and tools
  and exclude image and PDF bytes. The default 1,000,000 context window and 900,000
  compaction window are client settings. They do not prove server capacity.
  Full-window and sustained multi-agent workloads are untested. Compaction can be
  slow in long sessions.
- **Requests and streams are bounded.** Request bodies and individual upstream
  events are limited to 64 MiB. A whole upstream stream is limited to 128 MiB. Very
  large tool output, images, or long generated streams can exceed these limits.
  The request body must arrive within 2 minutes. A stalled or broken body returns
  400, and only an oversized body returns 413. Codex must send response headers
  within 2 minutes. A stream fails with error kind `upstream_stalled` after 10
  minutes without any upstream event. Keep-alive pings to Claude Code do not hide
  such a stall. The gateway sets no cap on total inference duration. The launcher
  sets Claude Code's timeout to 600,000 ms.
- **Quota recovery is conservative.** The once-a-minute read-only check clears a
  stale local cooldown only after positive evidence. It cannot bypass real
  subscription exhaustion. Cooldown state disappears on restart.
- **Credentials need one owner.** claudex instances coordinate through a credential
  lock. Unrelated or older clients may ignore it. Do not run another client against
  the same refresh-token file at the same time. In containers, mount directories,
  not individual credential files.
- **Local trust boundary.** HTTP has no built-in TLS. Loopback binding and the
  client key protect the local endpoint. The `--listen` override is for controlled
  setups such as Docker. TUI error excerpts can contain text echoed by the
  upstream server; see [dashboard privacy](tui.md#privacy-and-retention).
  Gateway logs rotate at 2 MiB with three older files.
- **Platform support.** The launcher, foreground gateway, and TUI run on Linux and
  macOS. Releases target amd64 and arm64. Windows is not packaged. macOS binaries
  are not signed or notarized.

## Troubleshooting

**Start with `claudex ctl status`.** It queries the authenticated gateway.
Use `claudex ctl` for live metrics and settings, or `ctl status --json` in scripts.
If nothing is running, launch `claudex` or `claudex ctl run`.

**Invalid client key.** Claude Code sent a missing or wrong client key. The gateway
logs `request rejected` with `reason` `invalid_client_key`. Use the value in
`client_key_file`, never a Codex token. Start a new `claudex` session. If connecting manually, check `ANTHROPIC_AUTH_TOKEN`.

**A model is unavailable or rate-limited.** Check `claudex ctl` and pick
a model your account can use. All requests share one subscription. You may need to
wait for upstream quota to recover. Restarting does not create quota.

**The server is overloaded.** claudex translates explicit Codex overload errors to
Claude's `overloaded_error`. Before response headers are sent, this is HTTP 529.
During streaming, it is an error event. The dashboard keeps the original upstream
status and error code. Overload does not start a quota cooldown. The gateway does
not replay a partially delivered response.

**Not signed in to Codex.** Requests fail with 401, and the gateway logs a WARN
`Codex sign-in unavailable` at startup. Run `claudex ctl setup --login`. The
credential store coordinates sign-in and token refresh with its shared file lock.
Port 1455 must be free for the browser callback.

**Codex sign-in could not be used or refreshed.** The sign-in exists, but the
gateway could not use it or refresh it. Retry once. If the error continues, run
`claudex ctl setup --login`. Client messages contain no file paths. The gateway log records
the underlying error.

**The Codex sign-in switched to a different account.** A refresh during the request
returned another account, so the gateway did not retry it. Retry the request.

**The old model mapping is still used.** Start a fresh `claudex` session.
Existing sessions can keep environment values that already resolved a family name.

**Codex stream was interrupted.** The gateway could not read the upstream response
to completion. Before message delivery, recognized transient failures get one
retry; after delivery starts, the interruption is returned without replay.
This alone does not point to a quota or translation bug. The gateway
log records a cause category, elapsed time, received bytes, and whether output had
started. The dashboard shows the category and a redacted error excerpt with
upstream type, code, and HTTP status. Known HTTP/2 errors, resets, timeouts,
truncated reads, and event-size limits each get a category. Unrecognized errors
show as `transport_read_error`. `upstream_stalled` means Codex stopped sending
events. The `messages` log line of every failed request includes `error_type` and
`error_kind`. See [upstream recovery](running.md#upstream-recovery) for attempt
counts, ping behavior and recovery limits.

**Compaction is slow.** First check whether the request is slow or actually failed.
The dashboard shows request duration and usage, but it does not label compaction
requests. The default compaction window is 900,000 tokens. You can lower it; see
[compaction window](configuration.md#compaction-window).

**TUI cannot connect.** Run `claudex ctl status` and check the address/client key.
Start `claudex` or `claudex ctl run` first. Use `ctl status --json` without a terminal.
