# How Claudex works

Claudex translates Claude Code's Messages API traffic for a single Codex
subscription account. It has no provider plugins, model downloads, generic OpenAI
endpoints, or account load balancing.

```mermaid
flowchart LR
  Claude[Real Claude Code CLI] -->|Messages + client key| Gateway[Claudex]
  Gateway -->|OAuth + Responses| Codex[Codex subscription]
  Codex -->|Response events| Gateway
  Gateway -->|Claude SSE / JSON| Claude
  Gateway -->|Read-only usage check| Usage[Subscription usage endpoint]
  TUI[Bundled terminal dashboard] -->|Client key| Gateway
```

## Request lifecycle

1. The launcher starts or reuses a gateway, derives active Claude settings, and
   injects the client key. Claude Code never receives the Codex sign-in token.
2. Claudex authenticates the request and bounds its body. It resolves the requested
   model and translates Claude system prompts, messages, and tools to Codex
   Responses input.
3. The credential store rereads the Codex sign-in file and refreshes credentials
   shortly before they expire. It writes refreshed tokens atomically. A
   cross-process file lock stops cooperating Claudex instances from racing the same
   refresh token. A transient refresh failure does not fail the request while the
   current access token is still valid.
4. The fixed Codex transport sends the request to the subscription Responses
   endpoint. After a 401, the gateway refreshes the sign-in once and retries. It
   does not retry when the refreshed sign-in belongs to a different account.
   Separately, the proxy permits one retry for a recognized transient transport
   failure before any message event reaches Claude Code. A retry discards partial
   translation state and retains the original request and configuration snapshot.
   For streamed responses, the local input token estimate runs in parallel with
   this request, so it adds no delay before the first byte.
5. The translator builds Claude-compatible JSON or streaming events. It preserves
   tool IDs, errors, refusals, images, parallel calls, URL citations, and encrypted
   reasoning state for later turns. Codex search activity is left out of history.
   Anthropic accepts search-result replay only with opaque state that Anthropic
   issued. A terminal failure never becomes an empty successful turn.
6. Requests with supported Auto Mode safeguards hold their entire response. When
   client tools are proposed, a separate GPT request reviews them. The reviewer
   has no tools and returns structured verdicts. Only a successful complete review
   releases the response; a failed review releases no tools. Its transport retry
   shares the original 60-second deadline. See [Auto Mode](auto-mode.md).
7. The gateway records generation and review metadata and token usage separately.
   Reviews link to their parent generation trace; retry attempts belong to the
   same logical record. See [the TUI guide](tui.md) for retention and accounting.

## HTTP routes

These routes require the client key. Send it as `Authorization: Bearer <key>` or
as `X-Api-Key`. The `Bearer` scheme matches in any letter case. Each header is
checked on its own.

| Route | Purpose |
| --- | --- |
| `POST /v1/messages` | Messages, streamed or not |
| `POST /v1/messages/count_tokens` | Local token estimate |
| `GET /v1/models` | Model list |
| `GET /v1/models/{id}` | One model |
| `GET /_claudex/metrics` | Bounded metrics snapshot |
| `GET /_claudex/status` | Authenticated gateway state for `claudex ctl` |

These routes need no key:

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Liveness probe for Docker and `claudex ctl` |
| `HEAD /api/hello` | Connectivity probe for Claude Code |

`GET /healthz` returns `product`, `status`, `version`, and the default `model`.
`GET /_claudex/status` identifies the control API, process instance, lifetime mode,
attached clients, active requests, revision, and active preferences. The launcher
reuses authenticated compatible gateways and uses their active defaults.

`POST /_claudex/sessions` opens an authenticated NDJSON connection with kind
`claude` or `tui`. Disconnecting releases ownership. Only automatic gateways exit
when they have no attached clients or active requests for five seconds.

`GET /_claudex/config` returns preferences, startup paths, revision and writability.
`PUT /_claudex/config` accepts only live preference fields and requires `If-Match`
with the quoted revision, scoped to this process instance and configuration.
Conflicts and drafts from before a restart return 412. Saves validate, persist atomically,
and then publish an immutable configuration snapshot. In-flight requests, including
permission reviews, keep their admitted snapshot. Startup fields require a restart.

## Reasoning and tools

Claudex picks one GPT effort per request. It applies these rules in order:

| Claude request | Effort sent to GPT |
| --- | --- |
| `thinking.type: disabled` | `none` where supported; Astra and GPT-6.1 Sol get `low`. |
| Explicit `output_config.effort` | That effort, after the rewrites below |
| `thinking.type: enabled` with `budget_tokens: -1` | Configured default |
| `budget_tokens: 0` | `none` where supported, otherwise `low` |
| `budget_tokens` up to 1024 | `low` |
| `budget_tokens` up to 8192 | `medium` |
| `budget_tokens` up to 24576 | `high` |
| Larger `budget_tokens` | `xhigh` |
| No effort and no budget | Configured default |

Claudex then rewrites a few values:

| Effort | Becomes |
| --- | --- |
| `auto` or empty | Configured default |
| `ultra`, `ultracode` | `xhigh` for Astra/6.1 Sol; `max` for the other catalog models |
| `minimal` | `low` |
| `max` | `max` |

An effort the model does not support returns an error. A `budget_tokens` value
that is not an integer of at least -1 also returns an error. The Codex API accepts
only `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`. It rejects
`ultra`.

Codex's own Ultra setting is a client mode, not an API level. Upstream Codex 430a167 resolves it using each model's
`multi_agent_reasoning_effort`, falling back to `max`. Max carries `max`. Under Claudex, Claude Code's ultracode workflow supplies the
delegation. The gateway does not implement Codex's multi-agent harness.

Astra/6.1 Sol's `low` fallback for disabled thinking cannot promise zero reasoning.
`thinking.display: omitted` hides visible summaries but keeps encrypted reasoning
for replay. Disabled thinking also hides visible summaries.

Claude's WebSearch function and typed web-search declarations become Codex native
`web_search`. The search-grounded answer returns as text, and URL annotations
return as Markdown links. Claudex does not synthesize Anthropic `server_tool_use`
or `web_search_tool_result` blocks. Those blocks cannot be resumed without
`encrypted_content` issued by Anthropic. WebFetch and ordinary local tools still run
in Claude Code. Artifact publishing is blocked because no subscription bridge for
it exists. Creating ordinary files still works.

## Prompt caching

Generation requests derive a hashed cache identity from Claude's user/session
metadata and send it through `prompt_cache_key` and `Session_id`. The translator
removes the changing billing attribution block while retaining actual application
instructions. The optional reviewer has a separate stable identity and output
schema. System/tool evidence and history precede the proposed call batch, whose
canonical JSON matches its later historical entry. Fresh permission context
follows as host-supplied JSON in a trailing developer message, preserving the
previous implicit cache boundary while evaluating current rules on every review.
Quoted transcript and tool arguments remain untrusted evidence. Cache use is
decided upstream and reported through usage counts. See
[prompt caching](prompt-caching.md).

## Quota recovery

A subscription `usage_limit_reached` response records an in-memory cooldown. While
the cooldown lasts, a background worker checks a read-only usage endpoint once a
minute. The cooldown clears only when every returned inference pool explicitly
allows requests. Missing fields, failed probes, and stale results cannot clear a
newer failure. Short-term rate errors do not create a credential-wide cooldown.

This repairs stale local cooldowns without spending inference quota. It cannot
bypass an exhausted subscription, and it offers no account or model failover.

## Code layout

| Package | Responsibility |
| --- | --- |
| `cmd/claudex` | Flags, initialization, lifecycle, sign-in, graceful shutdown |
| `internal/config` | Strict config validation, model catalog, family aliases |
| `internal/auth` | OAuth PKCE, credential store, refresh and atomic persistence |
| `internal/codex` | Fixed upstream HTTP transport and read-only usage checks |
| `internal/proxy` | Claude HTTP surface, request policy, streaming lifecycle |
| `internal/translator/codex/claude` | Claude and Responses protocol conversion |
| `internal/quota` | Cooldown evidence, generation checks, recovery |
| `internal/tokens` | Approximate local token counting |
| `internal/metrics` | Bounded usage history and sampling |
| `internal/tui` | Terminal views, polling, and configuration editing |
| `internal/control` | Shared authenticated control protocol and client |
| `internal/launcher` | Automatic worker startup, session ownership, Claude settings and bounded logs |
| `internal/signature`, `internal/translator/common` | Reasoning-signature checks and shared Claude message helpers |
