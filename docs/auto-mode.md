# Experimental GPT auto-mode classifier

claudex can review Claude Code tool calls using a separate GPT request, then
return the `dangerous_tool_use` verdicts Claude Code expects. This is a custom
permission policy, not Anthropic's classifier and not Anthropic's no-charge
classifier service. Both generation and classification consume Codex usage.

This feature is **off by default**. It implements an observed Claude Code
protocol; dated checks are linked below. Fixture tests establish protocol compatibility; they do not establish equivalent safety to Anthropic's classifier
or resistance to arbitrary prompt injection. Keep it opt-in while evaluating it.

## Enable

Open `claudex ctl config`, select Command reviewer, choose Sol 6.1, and save; or set
this field before starting the gateway:

```json
"auto_mode_classifier_model": "gpt-6.1-sol"
```

Any supported model or alias is accepted, independently of the generation model.
The reviewer always uses `high` reasoning effort, independently of the generation
effort. Saving through `ctl config` applies the
validated change live; in-flight requests retain their original reviewer.
Start a fresh `claudex` session in auto mode. The launcher selects
`CLAUDE_CODE_AUTO_MODE_SERVER=1` when this field is nonempty.

These rules apply to automatic, foreground, Homebrew, and Docker gateways. A
configuration changed directly on disk requires a gateway restart. The `claudex`
launcher sets `CLAUDE_CODE_AUTO_MODE_SERVER` for Claude; when launching Claude Code
manually, configure that variable yourself. `/status` inside Claude Code reports
whether the session uses server classification.

Open `claudex ctl config`, select Command reviewer, choose Claude client, and save;
or remove the field or set it to an
empty string, to disable the custom reviewer. Start a new session. The launcher then selects
`CLAUDE_CODE_AUTO_MODE_SERVER=0`, which keeps Claude Code's own client-side
classifier active and routes its ordinary model requests through claudex.
The variable selects the location of classification; it does not turn auto mode
or permission checks off. See the official
[classifier billing guidance](https://code.claude.com/docs/en/auto-mode-classifier-billing).

## Execution and failure handling

When a request includes `safeguards` with `type: dangerous_tool_use`, the gateway
validates its versioned permission context. It collects the actual translated
tool IDs, names and complete arguments, then sends the conversation, tool
definitions, permission context and proposed calls to a separate GPT request.
The review request has no tools, uses no generation-session ID, and requires
structured JSON with exactly one `flagged` or `not_flagged` verdict per call.

The policy lives in `internal/proxy/safeguards.go`. It prioritizes hard denials,
ask rules, user limits, workspace boundaries, and explicit authorization for
destructive or shared-system changes. Concrete safety or authorization concerns
are reasons to flag a call.
Prompts, retrieved content and tool arguments are evidence, not instructions to
the reviewer. Claude Code still applies its own permission rules and executes
or denies the tool after receiving the result.

claudex holds the **entire response** until review succeeds, including streamed
tool blocks, so no proposed client tool is released on a review error. Streamed
requests receive keep-alive pings while waiting. This increases time to first
visible output and does not offer incremental text streaming for reviewed turns.
Text-only turns need no extra model request but still return an empty verdict map.
Requests without a safeguard remain on the ordinary streaming path.

Limits are four active classifier requests, 32 tool calls per response, 1 MiB of
review input, 64 MiB of buffered response, 256 KiB of classifier response bytes,
and a 60-second classifier deadline. Generation does not reserve a classifier slot. Reviews wait for capacity with
cancellation; the 60-second deadline includes that wait.
The review copy excludes opaque thinking signatures and redacted-thinking data;
visible summaries, user requests, tool results and permission context remain.
The original generation request and its encrypted replay state are preserved.
Unsupported context versions, missing fields and explicitly truncated context
fail with 400. Missing, duplicate, unknown or malformed verdicts, refusal,
timeout, account changes and interrupted classification fail the response; they
never become an allow verdict. Duplicate JSON keys are rejected because parsers
can interpret them differently. Cancellation propagates to the reviewer.

Recognized transient transport failures get at most one retry after a short,
randomized delay. Review attempts share the original 60-second deadline and
capacity slot, resend the same evidence and current permission context, and parse
the new response from scratch. A failed review never triggers regeneration.
Refusals, invalid verdicts, HTTP errors and permission decisions are not retried.
An account change stops recovery before another request is sent.

The TUI and logs distinguish transport timeouts (`classifier_read_timeout`),
the overall review deadline (`classifier_timeout`), missing stream completion,
invalid responses, refusals and HTTP failures. The request inspector shows
**Upstream attempts** when recovery was attempted. These diagnostics contain
bounded categories, without reviewer text, proposed arguments or private
transport-error strings.

The classifier reviews client tool calls only. It does not review actions that
Codex performs internally, such as native web search. It cannot inspect local
filesystem state itself; unresolved effects must be flagged. All review input
goes to the same Codex account that generated the response. No new credential or
provider is required.

The TUI records classifier model requests separately so their reported
tokens are counted. A retry stays in the same review record and trace, with
`upstream_attempts` showing the attempt count. Claude Code's per-turn usage
describes generation only; its displayed cost is not a reliable account of the
additional Codex usage.

## Review failure categories

A completed review can return `flagged` with HTTP 200: it evaluated a tool and
identified a permission concern. A failed review supplies no verdict and releases
no tools. Its failure also fails the parent generation, so All calls can show two
failed records for one affected client turn. Use their linked traces to follow it.

| Error kind | Meaning |
| --- | --- |
| `classifier_read_timeout` | An upstream transport read timed out. Recovery is attempted once within the remaining review deadline. |
| `classifier_timeout` | The shared 60-second review deadline expired, including queue and retry time. |
| `classifier_connection_reset`, `classifier_unexpected_eof`, `classifier_incomplete_stream` | The connection broke or ended before complete verdicts; eligible for one retry. |
| `classifier_http` | Upstream returned an unsuccessful HTTP response; the review record retains its status. No transport retry. |
| `classifier_invalid_response`, `classifier_invalid_verdict` | The response or verdicts failed validation. No retry and no allow fallback. |
| `classifier_refused`, `classifier_upstream_response`, `classifier_unexpected_action` | The reviewer refused, returned an unsuccessful terminal event, or attempted an unsupported action. No retry. |
| `classifier_auth`, `classifier_account_changed` | Sign-in became unavailable or no longer matched the generation account. No further request is sent under another account. |
| `classifier_input_limit`, `classifier_output_limit` | The review input or output exceeded its bounded size. No retry. |
| `canceled` | The caller canceled or disconnected; recorded as 499 rather than a gateway error. |

Recognized HTTP/2 errors use `classifier_http2_...` categories. Unknown transport
errors use `classifier_transport_read_error`; other uncategorized review failures
use `classifier_failed`. These categories never include raw reviewer output or
transport-error text. Repeated transport failures can still exhaust recovery;
see [upstream recovery](running.md#upstream-recovery).

## Protocol and validation

Non-streamed replies include `safeguard_results` on the message. SSE replies put it
in the terminal `message_delta.delta`, before `message_stop`. Each evaluated
`dangerous_tool_use` verdict uses the exact tool ID delivered to Claude Code and
an outcome of `flagged` or `not_flagged`.

See [Development](development.md#tests) for fixture and live test commands, and
[historical protocol evidence](history/auto-mode-validation-2026-10-22.md) for the
client versions and bounded observations behind this implementation. Broader
adversarial and future-client validation remain necessary.

## Review diagnostics and delegated workflows

Successful reviews log a request fingerprint, model, allow/flag counts, duration,
and token counts. Failures log bounded categories (timeout, HTTP failure, invalid
verdict, or other review failure). Neither path logs commands, explanations,
conversation text, or credential material. Request-context rejection has the
`classifier_context` error kind.

The reviewer decides safety and authority, not coding style. It must not invent
read restrictions from write isolation rules or demand proof of hypothetical
symlinks for routine operations. Explicit read limits, resource limits, hard
blocks, ask rules, and credential protections still apply.
Specific user intent and matching `auto_mode.allow` exceptions can clear soft
denials. Hard denials and explicit ask rules remain in force, matching the
[documented rule precedence](https://code.claude.com/docs/en/auto-mode-config).

Delegated and resumed workers may lack the original user's authorization or retain
an earlier stop-on-denial instruction. A coordinator's relay is not necessarily
verifiable user consent in the worker's review context. Include the actual scoped
user request when delegating; repeated generated claims of approval cannot restore
missing authority. This approval-propagation limitation remains unresolved.
