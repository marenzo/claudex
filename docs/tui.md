# Terminal dashboard

Run `claudex ctl` while a gateway is running, or `claudex ctl config` for Settings.
The TUI is compiled into the gateway binary; nothing else needs installation.
For another gateway, use `--url http://127.0.0.1:8317 --key-file /path/to/client-key`.
It requires no local Codex sign-in. Noninteractive callers use `ctl status --json`.

## Layout and controls

Colors follow the terminal background. Wide terminals show independent panes;
smaller terminals use focused views. No special font is required.

- **1–4:** Overview, Requests, Models, Settings. Click a navigation label to switch.
- **Tab / Shift-Tab:** focus the next or previous pane; on Overview, select a chart.
- **Ctrl+k** or **:** open the searchable command palette. **?** opens keyboard help.
- **w:** cycle 15-minute, one-hour, and two-hour chart windows.
- **g:** switch generation, Auto reviews, or all upstream calls in Overview,
  Requests, and Models. Generation is selected initially; the call type stays
  selected when switching views.
- **p:** pause/resume updates. An open TUI keeps its gateway session attached.
- **q:** quit. Unsaved settings open a discard confirmation; Esc keeps the draft.
- **PgUp / PgDn**, mouse wheel: scroll the focused pane or page through requests.

**Overview** shows request activity, token flow, response-time distributions, and
model/effort counts. Tab selects a chart and **z** expands it; Esc restores the grid.
On smaller terminals, one chart fills the workspace. **Left/right** chooses an exact
minute; **Enter** opens its retained requests. Minute totals can outlive individual
request records, so an older minute may have counts but no matching records.

**Requests** shows retained calls, an outcome summary, filters, and an inspector.
Click outcome labels or filter chips to filter records.

- Arrows or **j/k** select a row; Enter focuses details; Esc returns to the list.
- **/** searches; **m** cycles models; **e** cycles effort; **f** cycles outcomes.
- **o** sorts by newest completion, longest duration, or highest input-cache reuse.
- **x** clears request filters, including a selected minute, within the selected
  call type. **g** changes the call type.
- The inspector shows a response timeline, input and output breakdowns, timestamps,
  and any retained error excerpt. Tab focuses the inspector for scrolling.
- Selection stays attached to the request ID during live updates. If that record
  leaves the bounded history or no longer matches, selection moves to a visible row.

Wide tables label each call's type. A successful review can block a proposed
tool; the row shows the blocked count instead of treating that verdict as a
gateway error. Review details include proposed/blocked tool counts and the parent
generation trace. Search a trace to find its related retained records.
Calls that attempted transport recovery show **Upstream attempts** in the
inspector. See [recovery behavior](running.md#upstream-recovery) and
[review failure categories](auto-mode.md#review-failure-categories).

**Models** shows the five supported models. Arrows choose a model; Tab focuses its
statistics for scrolling. Lifetime totals and time-window response statistics are
labeled separately.

**Settings** uses arrows to choose a field, left/right to cycle choices, and Enter
to type a value. **Ctrl+s** or **s** saves; **d** discards. Tab focuses connection and
file information on wide terminals. Unsaved drafts survive navigation and polling.
A gateway restart preserves the draft but requires discarding it before saving,
so a draft from an earlier gateway cannot overwrite the new configuration.
**Local feedback** controls the opt-in collector. Enable it and start a new Claude
session to load the tool; disabling rejects new reports immediately. Stored reports
remain available through `claudex ctl feedback list`. See [feedback](feedback.md).

Colors are paired with labels. Missing usage and timing remain unavailable.
See [Prompt caching](prompt-caching.md) for cache behavior.

## Privacy and retention

- The TUI reads the client key from its private file and sends it in the
  Authorization header. It never places keys in command arguments or URLs.
- Metrics responses are marked `no-store`. The TUI uses no external assets, browser, or analytics.
- The gateway keeps the latest 500 completed requests in memory. Each record holds
  request metadata, token usage, and a bounded error excerpt for failures.
- Prompts, replies, and tool payloads are never retained in monitoring history.
  Review cache comparisons keep only keyed fingerprints and counts, bounded to
  32 groups, 256 evidence-message boundaries, and one complete cache endpoint
  fingerprint per group. Their random private key resets with the process,
  preventing fingerprint comparisons across runs.
- Error excerpts have known credentials and common token, account, and email
  patterns redacted. An upstream error can still echo input fragments that pattern
  redaction misses. Review excerpts before sharing them.
- Excerpts are visible only through the authenticated dashboard. The gateway log
  never stores them.
- Monitoring history and cache fingerprints reset on gateway restart. Gateway logs
  and opt-in feedback records persist separately; see [Running](running.md#gateway-lifetime)
  and [Feedback](feedback.md#storage-and-review).

## Polling

Live polling runs every three seconds after the previous poll completes. Each
metrics request has a ten-second deadline, including its response body. After a
timeout or connection failure, the last snapshot stays visible with a stale label,
and polling retries automatically. Pause and quit abort the current poll.
Resume keeps the stale notice until a fresh snapshot arrives. These deadlines apply
only to dashboard requests. They do not limit inference duration.

## What is measured

- **Totals.** Generation totals include completed `/v1/messages` requests. Auto
  reviews have separate lifetime, model, minute, and in-flight counters. All calls
  combines both; a turn that needs review contributes one generation record and
  one review record. A transport retry adds no extra completed-request record.
  Dashboard polling and token-count requests do not affect these totals.
- **Cancellations.** Client cancellations use status 499. They have their own
  `canceled` count in process, model, and minute totals. They stay in
  completed-request totals and keep any reported usage, but `errors` excludes them.
  Traffic charts split completions into successful, failed, and canceled requests.
- **Rates.** Success and failure rates divide by completed requests minus
  cancellations. A sample with only cancellations has no rate and displays "—".
  Failures include every other 4xx and 5xx outcome, including rate limits.
- **Minute history.** The minute ring keeps 120 buckets, assigned by
  **completion** time. Every completion counts, even after its individual record
  leaves the request ring. Empty minutes stay zero, and the current minute is
  partial. The 15m, 1h, and 2h controls select calendar-minute buckets ending in
  the current minute.
- **Sampled statistics.** P50, P95, and P99, duration histograms, model and effort
  mix, and per-model latency use only records in the 500-request ring within the
  selected time window. They include failures and cancellations. Percentiles use
  the nearest-rank definition. A cancellation timing does not mean the server
  finished a response.
- **Durations.** Generation duration runs from entry into the messages handler
  through completion and includes Auto review. Review duration includes waiting
  for a reviewer slot, retry delay and the model response. Generation recovery
  time also stays in generation duration. These durations overlap, so do not
  add them to calculate a response's waiting time.
- **Upstream attempts.** `upstream_attempts` counts the original Responses
  operation and any transport retry for that generation or review. The inspector
  shows the field when it exceeds one. A successful token refresh and resend
  after HTTP 401 is internal to a Responses operation. Calls rejected before
  reaching that operation have no attempt count.
- **Output timing.** **First from model** measures the first meaningful upstream
  delta: text, reasoning, or tool arguments. **First to client** measures the first
  translated content successfully written and, for streams, flushed to Claude
  Code. It excludes pings, headers, usage events and empty blocks. Content can be
  reasoning or tool data before text. Reviewed responses remain buffered until a
  decision, so client timing includes the review delay. Table and chart first-output
  statistics use client timing. Reviews have no client-output timing. Missing
  values remain unavailable, rather than zero.
- **Usage.** Usage exists only when the upstream reports numeric input and output
  counts. Cached input is part of input, and reasoning is part of output. Cache
  reuse is cached input divided by total reported input. It is not a per-request
  cache-hit rate. Unknown usage, including failures without upstream usage, is
  never shown as zero. The dashboard does not estimate cost or remaining
  subscription allowance. Reported usage from an interrupted generation attempt
  is retained when a retry succeeds; usage never reported upstream is unknown.
- **Quota recoveries.** This counts positive availability probes that cleared a
  cooldown. It is not a balance, a reset countdown, or a retry count.
- **Request records.** Request IDs are process-local completion sequence numbers.
  Each call also has a unique trace; reviews carry their parent generation trace.
  Error kinds are bounded transport or review categories. A failed request keeps
  an error excerpt with source (`upstream`, `proxy`, `transport`, or `review`),
  type, code, message, and upstream HTTP status when available. An HTTP 200 SSE
  response can still end with an effective 502 outcome. Messages are capped at
  4,096 UTF-8 bytes, and type
  and code at 128 bytes each. Truncation is marked.
- **Request log.** Error details appear in the inspector and stay open during polling.
  Search includes them. A cancellation record explains that the caller disconnected
  when no server error exists. The log has separate failure and cancellation
  filters. Cancellations use a neutral status marker and do not appear in the
  failure filter or in success traffic.

Minute history and the request ring expire independently. The request log filters
all retained records, regardless of the charts' time range. Date labels use the
terminal host's local time. Wire timestamps are RFC3339, and bucket calculations use
absolute timestamps.

The private control/metrics API is version 2. Update the CLI and gateway together.
See [Running](running.md#updating-and-uninstalling).

## Configuration

Use Settings to edit preferences. See [Configuration](configuration.md#applying-changes)
for validation, persistence, and when new Claude sessions are required.
