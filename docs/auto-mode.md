# Experimental GPT auto-mode classifier

Claudex can review Claude Code tool calls using a separate GPT request, then
return the `dangerous_tool_use` verdicts Claude Code expects. This is a custom
permission policy, not Anthropic's classifier and not Anthropic's no-charge
classifier service. Both generation and classification consume Codex usage.

This feature is **off by default**. It is an experimental implementation of the
protocol observed in Claude Code 2.1.283. Small fixture tests establish protocol
compatibility; they do not establish equivalent safety to Anthropic's classifier
or resistance to arbitrary prompt injection. Keep it opt-in while evaluating it.

## Enable

Run `claudex ctl config reviewer astra`, or add this field to your Claudex config:

```json
"auto_mode_classifier_model": "astra"
```

Any supported model or alias is accepted, independently of the generation model.
The reviewer uses `high` reasoning effort. Only Astra has been live-tested for
this feature. `ctl config` waits for active requests and applies the change.
Start a fresh `claudex` session in auto mode. The launcher selects
`CLAUDE_CODE_AUTO_MODE_SERVER=1` when this field is nonempty.

For a portable or Docker gateway, restart it with the changed config and set
`CLAUDE_CODE_AUTO_MODE_SERVER=1` in the shell that starts Claude Code. `/status`
inside Claude Code reports whether the session uses server classification.

Run `claudex ctl config reviewer client`, or remove the field or set it to an
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
destructive or shared-system changes. Uncertainty is a reason to flag a call.
Prompts, retrieved content and tool arguments are evidence, not instructions to
the reviewer. Claude Code still applies its own permission rules and executes
or denies the tool after receiving the result.

Claudex holds the **entire response** until review succeeds, including streamed
tool blocks, so no proposed client tool is released on a review error. Streamed
requests receive keep-alive pings while waiting. This increases time to first
visible output and does not offer incremental text streaming for reviewed turns.
Text-only turns need no extra model request but still return an empty verdict map.
Requests without a safeguard remain on the ordinary streaming path.

Limits are four active reviewed requests, 32 tool calls per response, 1 MiB of
review input, 64 MiB of buffered response, 256 KiB of classifier response bytes,
and a 60-second classifier deadline. Requests beyond capacity fail with 429.
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
2.1.283 implementation established this shape:

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
