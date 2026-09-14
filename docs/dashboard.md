# Dashboard

The `-dashboard` flag enables `/dashboard` on the existing loopback HTTP listener.
The HTML page and an exact allowlist of embedded assets need no key.
`GET /dashboard/api` requires the same client key as Claude Code requests.

## Privacy and retention

- The browser sends the client key in the Authorization header. It keeps the key
  only in the tab's memory. It never writes the key to browser storage or the URL.
  Disconnecting, navigating away, or reloading the page clears it.
- Metrics responses are marked `no-store`. The dashboard makes no CDN requests and
  uses no cookies, local storage, or external analytics.
- The gateway keeps the latest 500 completed requests in memory. Each record holds
  request metadata, token usage, and a bounded error excerpt for failures.
- Prompts, replies, and tool payloads are never retained.
- Error excerpts have known credentials and common token, account, and email
  patterns redacted. An upstream error can still echo input fragments that pattern
  redaction misses. Review excerpts before sharing them.
- Excerpts are visible only through the authenticated dashboard. The service log
  never stores them.
- Everything resets when the gateway restarts.

## Polling

Live polling runs every three seconds after the previous poll completes. Each
metrics request has a ten-second deadline, including its response body. After a
timeout or connection failure, the last snapshot stays visible with a stale label,
and polling retries automatically. Pause and disconnect abort the current poll.
Resume keeps the stale notice until a fresh snapshot arrives. These deadlines apply
only to dashboard requests. They do not limit inference duration.

## What is measured

- **Totals.** Process totals and per-model totals include all completed
  `/v1/messages` requests since start. In-flight requests are counted separately.
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
- **Durations.** Full duration runs from entry into the messages handler through
  completion. **First output** is the time to the first meaningful upstream delta:
  text, reasoning, or tool arguments. It is not always the time to visible text.
  A request without such a delta has no first-output value, rather than a zero.
- **Usage.** Usage exists only when the upstream reports numeric input and output
  counts. Cached input is part of input, and reasoning is part of output. Cache
  reuse is cached input divided by total reported input. It is not a per-request
  cache-hit rate. Unknown usage, including failures without upstream usage, is
  never shown as zero. The dashboard does not estimate cost or remaining
  subscription allowance.
- **Quota recoveries.** This counts positive availability probes that cleared a
  cooldown. It is not a balance, a reset countdown, or a retry count.
- **Request records.** Request IDs are process-local completion sequence numbers.
  Optional error kinds are static transport categories. A failed request keeps an
  error excerpt with source (`upstream`, `proxy`, or `transport`), type, code, message,
  and upstream HTTP status when available. An HTTP 200 SSE response can still end
  with an effective 502 outcome. Messages are capped at 4,096 UTF-8 bytes, and type
  and code at 128 bytes each. Truncation is marked.
- **Request log.** Error details expand below a row and stay open during polling.
  Search includes them. A cancellation record explains that the caller disconnected
  when no server error exists. The log has separate failure and cancellation
  filters. Cancellations use a neutral status marker and do not appear in the
  failure filter or in success traffic.

Minute history and the request ring expire independently. The request log filters
all retained records, regardless of the charts' time range. Date labels use the
browser's local time. Wire timestamps are RFC3339, and bucket calculations use
absolute timestamps.

## Frontend and validation

`analytics.js` contains pure sampling, percentile, histogram, and filtering logic.
`app.js` handles rendering, ECharts options, polling, and tab-local authentication.
`style.css` supplies responsive layouts, keyboard focus, and reduced-motion rules.
No frontend build toolchain is needed. The binary embeds an unmodified,
integrity-verified Apache ECharts 6.1.0 distribution. Its provenance and licenses
are in `internal/dashboard/vendor/` in the source tree and in
`THIRD_PARTY_NOTICES.md` in release archives. ECharts tooltips use the canvas
rich-text renderer rather than HTML.

```sh
go test -race ./internal/dashboard ./internal/proxy
node --check internal/dashboard/app.js
node --test internal/dashboard/analytics_test.cjs
```

Node runs only the frontend checks. Building or running Claudex does not need it.
Backend checks cover concurrent totals, separate history retention, exact bucket
expiry, idle zero filling, owned snapshots, asset allowlists, and authentication.
Frontend checks use a small DOM adapter and deterministic timers. They verify
polling timeouts, recovery, pause, disconnect, superseded requests, metric
calculations, and literal error rendering.
