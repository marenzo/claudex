# Experimental GPT auto-mode classifier

Claudex can review Claude Code tool calls using a separate GPT request, then
return the `dangerous_tool_use` verdicts Claude Code expects. This is a custom
permission policy, not Anthropic's classifier and not Anthropic's no-charge
classifier service. Both generation and classification consume Codex usage.

This feature is **off by default**. It is an experimental implementation of the
protocol observed in Claude Code 2.1.283 and checked again with 2.1.306. Small fixture tests establish protocol
compatibility; they do not establish equivalent safety to Anthropic's classifier
or resistance to arbitrary prompt injection. Keep it opt-in while evaluating it.

## Enable

Open `claudex ctl config`, select Command reviewer, choose Sol 6.1, and save; or set
this field before starting the gateway:

```json
"auto_mode_classifier_model": "gpt-6.1-sol"
```

Any supported model or alias is accepted, independently of the generation model.
The reviewer always uses `high` reasoning effort, independently of the generation
effort. Astra, GPT-6.1 Sol, GPT-6 Sol, and GPT-6 Luna passed the bounded live checks
covered by the opt-in live fixtures. Saving through `ctl config` applies the
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
classifier active and routes its ordinary model requests through Claudex.
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

Claudex holds the **entire response** until review succeeds, including streamed
tool blocks, so no proposed client tool is released on a review error. Streamed
requests receive keep-alive pings while waiting. This increases time to first
visible output and does not offer incremental text streaming for reviewed turns.
Text-only turns need no extra model request but still return an empty verdict map.
Requests without a safeguard remain on the ordinary streaming path.

Limits are four active classifier requests, 32 tool calls per response, 1 MiB of
review input, 64 MiB of buffered response, 256 KiB of classifier response bytes,
and a 60-second classifier deadline. Generation no longer reserves a classifier slot. Reviews wait for capacity with
cancellation; the 60-second deadline includes that wait.
The review copy excludes opaque thinking signatures and redacted-thinking data;
visible summaries, user requests, tool results and permission context remain.
The original generation request and its encrypted replay state are preserved.
Unsupported context versions, missing fields and explicitly truncated context
fail with 400. Missing, duplicate, unknown or malformed verdicts, refusal,
timeout, account changes and interrupted classification fail the response; they
never become an allow verdict. Duplicate JSON keys are rejected because parsers
can interpret them differently. Cancellation propagates to the reviewer.

The classifier reviews client tool calls only. It does not review actions that
Codex performs internally, such as native web search. It cannot inspect local
filesystem state itself; unresolved effects must be flagged. All review input
goes to the same Codex account that generated the response. No new credential or
provider is required.

The dashboard records classifier model requests separately so their reported
tokens are counted. Claude Code's per-turn usage describes generation only;
its displayed cost is not a reliable account of the additional Codex usage.

## Protocol evidence and validation

The official [gateway guide](https://code.claude.com/docs/en/llm-gateway-protocol)
requires preserving safeguard requests and results. Its public documentation does
not fully specify the wire schema. Inspection of the installed Claude Code
2.1.283 implementation established this shape; the 2.1.306 package and local
real-client check confirmed it again:

```json
{
  "safeguard_results": [{
    "type": "dangerous_tool_use",
    "status": {
      "type": "available",
      "tool_uses": {
        "toolu_example": {
          "type": "evaluated",
          "outcome": "flagged",
          "explanation": "The action is not authorized."
        }
      }
    }
  }]
}
```

Non-streamed replies place the field on the message. SSE replies put it inside
the terminal `message_delta.delta`, before `message_stop`. Verdict keys are the
exact tool IDs the client receives. The request context includes rules, trusted
directories, auto-mode policy, current directory, platform and Git state.

Normal checks cover streamed and JSON responses, both verdicts, exact ID
coverage, rejected contexts, ambiguous JSON, refusals, malformed and interrupted
classifier responses, cancellation, concurrency limits and usage accounting.

An isolated real-client test uses localhost model fixtures and only a harmless
`printf` command. It proves Claude Code accepts the gateway's allow and deny
verdicts without subscription access:

```sh
CLAUDEX_TEST_CLAUDE_BIN="$(command -v claude)" \
  go test -tags e2e -run TestClaudeSafeguardDecisions -v ./internal/proxy
```

The opt-in live test sends proposed actions to GPT without executing them. Supply
a private credential copy **without `refresh_token`** so it cannot rotate the
running service's sign-in:

```sh
CLAUDEX_TEST_AUTH_FILE=/private/path/auth-copy.json \
  go test -tags live -run TestLiveGPTSafeguardReview -v ./internal/proxy
```

The initial Astra check allowed harmless printing and flagged an unauthorized
credential upload whose tool description also tried to inject an approval.
A combined test also passed the real Claude Code → Claudex → Astra classifier →
Claude Code path for a harmless `printf`. Generation was a local fixture;
classification was live. The single review took about 6.7 seconds in that run.
Run it with both variables above and
`go test -tags 'live e2e' -run TestLiveClaudeGPTSafeguardDecision -v ./internal/proxy`.
Broader adversarial evaluation, latency measurements on representative sessions,
and compatibility testing on future Claude Code versions remain necessary.

## Review diagnostics and delegated workflows

Successful reviews log a request fingerprint, model, allow/flag counts, duration,
and token counts. Failures log bounded categories (timeout, HTTP failure, invalid
verdict, or other review failure). Neither path logs commands, explanations,
conversation text, or credential material. Request-context rejection has the
`classifier_context` error kind. Existing older logs lack verdict counts and
cannot retrospectively identify all policy decisions.

The reviewer decides safety and authority, not coding style. It must not invent
read restrictions from write isolation rules or demand proof of hypothetical
symlinks for routine operations. Explicit read limits, resource limits, hard
blocks, ask rules, and credential protections still apply.
Specific user intent and matching `auto_mode.allow` exceptions can clear soft
denials. Hard denials and explicit ask rules remain in force, matching the
[documented rule precedence](https://code.claude.com/docs/en/auto-mode-config).

A workflow assignment may describe work without carrying the user's original
authorization. Claude Code 2.1.306 explicitly labels computed workflow tasks as
script output. The gateway preserves that provenance. Relaying only “delegate to
agents” can omit the task being delegated, and a computed claim that the owner
approved it cannot restore the missing authorization. Give the workflow the
actual scoped user request; use a directly assigned agent when that preserves
necessary context. Do not disable provenance or reinterpret generated approval
claims as consent.

A resumed worker can also retain an earlier stop-on-denial instruction while
receiving renewed approval only as a coordinator's message. Approval visible in
the parent is not guaranteed to reach the worker's review context as verifiable
user authorization. Repeating the same relay can produce repeated denials. This
approval-propagation limitation remains unresolved; Claudex does not promote an
agent's claim of approval into user consent.
