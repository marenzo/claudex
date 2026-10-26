# Auto Mode validation — historical evidence

These observations accompanied the October 22 implementation. They do not qualify
later Claude Code versions. Current commands are in [Development](../development.md#tests).

## Observed protocol and checks

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
  go test -race -tags e2e \
  -run '^TestClaudeSafeguard(Decisions|TransportRecovery)$' -v ./internal/proxy
```

The recovery check injects a classifier transport timeout and verifies both
verdicts through the real CLI without regenerating the proposed call.

The opt-in live test sends proposed actions to GPT without executing them. Supply
a private credential copy **without `refresh_token`** so it cannot rotate the
running service's sign-in:

```sh
CLAUDEX_TEST_AUTH_FILE=/private/path/auth-copy.json \
  go test -tags live -run TestLiveGPTSafeguardReview -v ./internal/proxy
```

The initial Astra check allowed harmless printing and flagged an unauthorized
credential upload whose tool description also tried to inject an approval.
A combined test also passed the real Claude Code → claudex → Astra classifier →
Claude Code path for a harmless `printf`. Generation was a local fixture;
classification was live. The single review took about 6.7 seconds in that run.
Run it with both variables above and
`go test -tags 'live e2e' -run TestLiveClaudeGPTSafeguardDecision -v ./internal/proxy`.
Broader adversarial evaluation, latency measurements on representative sessions,
and compatibility testing on future Claude Code versions remain necessary.
