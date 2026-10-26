# Prompt caching

claudex supports upstream Codex prompt-cache reuse. It keeps session routing and
translated request prefixes stable where possible and reports the cached-input
counts returned by Codex. It does not store a local cache of prompts or answers.

## How it works

For generation requests with Claude's `metadata.user_id`, claudex derives a stable
hashed identity and sends it as both `prompt_cache_key` and the upstream
`Session_id` header. The translator omits Claude's per-request billing attribution
block so changing attribution does not change the reusable prompt prefix.
Application instructions, conversation content, and tool definitions remain.

The optional Auto Mode reviewer has its own namespaced identity, separate from
generation. Successive reviews preserve historical message boundaries and use a
stable output schema. System evidence and current tool definitions precede
history. Proposed calls end the final user evidence message and use exactly the
same JSON encoding as their later historical entries. The complete current
permission context follows in a developer message containing host-supplied JSON
data. Transcript text, system/memory evidence, tool definitions and proposed
arguments remain untrusted user evidence; no quoted value gains authority to
change the reviewer policy or grant consent. Every review evaluates the current
rules; cached verdicts or permissions are never reused.

This order preserves the previous implicit cache write boundary. A changing
permission snapshot placed in the final user message caused that boundary to
disappear on the next turn, even when most earlier bytes were unchanged. A
trailing developer message is outside the initial developer block and does not
become an automatic cache write boundary. Live Git state and fresh permission
rules can therefore change without invalidating the saved evidence endpoint.
Transport cache annotations are omitted from evidence, while memory,
instructions, tool arguments, and exact numeric values remain. Review identity
uses session metadata when present and falls back to the primary workspace.

These measures support reuse; the upstream service decides whether a request
actually uses its cache. claudex cannot guarantee a hit, a retention period, a
latency reduction, or a particular amount of subscription usage saved. Claude's
`cache_control` annotations are not translated into an Anthropic-managed cache.

## Reading the metrics

Open `claudex ctl` and press **g** to compare generation and Auto reviews separately.
Use upstream cached-input counts to establish reuse. Local history retention is
unrelated to upstream cache lifetime; see [metric definitions](tui.md#what-is-measured)
for usage accounting and overlapping generation/review durations.

The review inspector also shows bounded cache diagnostics: whether the stable
prefix or live context changed, and how many initial evidence messages match the
previous request in that model/session group. Fingerprints use a random private
key that exists only for the gateway process. They contain no prompt, command,
path, permission text, or original session identifier. At most 32 groups and the
first 256 message fingerprints per group are retained. An additional keyed
fingerprint checks the entire previous last-user boundary, including histories
longer than 256 messages. A matching local prefix or boundary shows that claudex
preserved evidence; it does not prove upstream cache reuse.

Concurrent workers can share session metadata while using different histories.
The local comparison uses the most recent review in its group, which may belong
to another worker. A changed prefix or unmatched previous boundary can therefore
coexist with an upstream cache hit from an older matching history. Use reported
cached-input counts to establish actual reuse.

## When reuse changes

A new session identity, model change, compaction, or an altered system prompt,
tool catalog, or earlier conversation content can change the reusable prefix.
Current permission changes are always supplied and enforced on every review.
Compare the same model and continuing session over several requests, and check
actual cached-input counts rather than treating one uncached request as proof of
a translation error. No local switch can force the upstream to reuse a cache.

The public [OpenAI prompt-caching guide](https://developers.openai.com/api/docs/guides/prompt-caching)
describes exact prefix matching and explicit cache breakpoints. The Codex
subscription endpoint has a different request contract: a live check rejected
`prompt_cache_breakpoint` with HTTP 400. claudex does not send that unsupported
field or change cache retention settings.
