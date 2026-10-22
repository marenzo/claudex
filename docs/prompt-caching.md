# Prompt caching

Claudex supports upstream Codex prompt-cache reuse. It keeps session routing and
translated request prefixes stable where possible and reports the cached-input
counts returned by Codex. It does not store a local cache of prompts or answers.

## How it works

For generation requests with Claude's `metadata.user_id`, Claudex derives a stable
hashed identity and sends it as both `prompt_cache_key` and the upstream
`Session_id` header. The translator omits Claude's per-request billing attribution
block so changing attribution does not change the reusable prompt prefix.
Application instructions, conversation content, and tool definitions remain.

The optional Auto Mode reviewer has its own namespaced identity, separate from
generation. Successive reviews preserve historical message boundaries and use a
stable output schema. Permission context and current tool definitions precede
history, so changes to either invalidate reuse of that prefix. Review identity
uses session metadata when present and falls back to the primary workspace.

These measures support reuse; the upstream service decides whether a request
actually uses its cache. Claudex cannot guarantee a hit, a retention period, a
latency reduction, or a particular amount of subscription usage saved. Claude's
`cache_control` annotations are not translated into an Anthropic-managed cache.

## Reading the metrics

Open `claudex ctl`. Overview, Models, and Requests show reported usage:

- **Cached input** is the subset of input tokens the upstream reused.
- **New input** is reported input minus cached input.
- **Input from cache** is cached input divided by reported input. It measures
  tokens, not the percentage of requests that had any cache reuse.
- **Output** includes reasoning tokens; reasoning is not added a second time.
- Missing token usage is unavailable, rather than a zero-token result. Summary
  views show how many requests supplied usage counts.

Reviewer requests contribute their own token usage to gateway metrics. Claude
Code's displayed generation usage does not include the separate reviewer call.
The dashboard's 500-request and two-hour history limits describe local monitoring
retention, not the upstream prompt cache's lifetime.

## When reuse changes

A new session identity, model change, compaction, or an altered system prompt,
tool catalog, or earlier conversation content can change the reusable prefix.
Reviewer permission changes also take precedence over preserving a prefix.
Compare the same model and continuing session over several requests, and check
actual cached-input counts rather than treating one uncached request as proof of
a translation error. No local switch can force the upstream to reuse a cache.
