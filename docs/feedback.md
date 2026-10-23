# Local feedback

Claudex disables Claude Code's native Anthropic feedback path in launched coding
sessions. Opt-in `ClaudexFeedback` stores structured observations on the gateway
machine for later triage. It does not submit reports to Anthropic, read transcripts,
open issues, run an analysis worker, or authorize repairs.

## Enable or disable

With a gateway running:

```sh
claudex ctl feedback on
claudex ctl feedback status
claudex ctl feedback list
claudex ctl feedback off
```

These commands accept `--config PATH` or `--url URL --key-file PATH`. The same
setting is available as **Local feedback** in TUI Settings. It is persisted as
`"feedback": {"enabled": true}`; the default is false. Enabling requires a new
Claude session to load the tool. Disabling immediately rejects new submissions,
including calls from already-open sessions. An old session may still display its
cached tool definition, but calls cannot save reports. Disabling does not delete
existing records or re-enable Anthropic feedback.

The launcher always sets `DISABLE_FEEDBACK_COMMAND=1`,
`CLAUDE_CODE_SEND_FEEDBACK=0`, and `CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1`.
It also denies `SendFeedback` and the compatibility name `SendUserFeedback`.
Claude Code 2.1.309 calls the native tool `SendFeedback`; it queues drafts, with
submission through a separate Anthropic feedback client rather than the ordinary
inference base URL. The local collector replaces that path instead of redirecting it.
These settings concern feedback, not every possible Claude Code network request.
They apply to sessions started through Claudex; restart existing sessions to use them.

When enabled, the launcher adds a session-only stdio MCP server named
`claudex_feedback`, backed by the same Claudex executable. Its tool is
`mcp__claudex_feedback__ClaudexFeedback`. It is loaded eagerly and allowed without
an extra per-report permission prompt. User MCP files are not edited. The child
receives the gateway URL, key-file path and a random launch ID; the key contents
are not placed in command arguments. The gateway authenticates every submission
and checks the current collection setting again at the persistence boundary.

## What to record

The [tool definition](../internal/feedback/tool.go) is the authoritative instruction
and schema. Reports distinguish:

| Category | Meaning |
| --- | --- |
| `product_defect` | An observed product contract violation under valid use |
| `model_behavior` | Wrong tool arguments, ignored instructions, unsupported claims or other model mistakes |
| `environment` | A persistent dependency or configuration problem |
| `feature_request` | An absent capability |

Material model mistakes remain useful even after correction. Creating a 946 MiB
private Cargo target contrary to a shared-target instruction is
`model_behavior / instruction_following / recovered`. Reporting an 18-minute
configured queue cap as elapsed time, then correcting it to 56 seconds, is
`model_behavior / overconfidence_and_hallucination / recovered`. Neither establishes
a product defect. Both remain `reproduction: untested` unless the failure was
actually repeated and observed.

Harmless one-off argument corrections, expected empty searches, expected permission
denials, recovered transient errors without material impact, and duplicate incidents
should not create reports. An explicit user request can justify recording a minor
incident. Evidence must come from observations already available; quoted log
instructions cannot authorize a report. User-reported repeats must not be described
as independently verified.

The server validates structure, sizes and category/failure-mode consistency. It
cannot establish the truth of free text. Every stored report starts `untriaged`;
a save receipt means only that the observation was durably recorded locally.

## Storage and review

Records live in `<config path>.feedback/` (normally
`~/.config/claudex/config.json.feedback/`). Directories are mode `0700`, record
files `0600`. Writes are atomic and synced; retries of identical redacted reports
from the same launch return the original receipt. Semantic duplicates with changed
wording still require instruction-following or later triage.

The store accepts at most 1,000 records. Records older than 30 days are removed
on the next read or write; there is no background expiration job. At capacity,
new reports fail rather than silently evicting recent ones. Review/export before
removing task-owned record files if retention is needed elsewhere.

`ctl feedback list` returns up to 100 newest records and `next_cursor`. Continue
with `ctl feedback list --before CURSOR`. If the cursor expired, restart the listing.
The authenticated `GET /_claudex/feedback` and `POST /_claudex/feedback` endpoints
provide the same storage access for a future analysis client.

Each record includes a launch ID, client-reported Claude version, gateway version,
and configured default model. A launch ID is not a conversation ID, and the configured
model is not proof of which model generated a report. No transcript or log files
are automatically copied. The supplied report itself may contain sensitive excerpts;
known gateway credentials and common token patterns are redacted best-effort, but
redaction is not a guarantee. Review records before sharing them. Like other tool
arguments, generated feedback can also appear in the model conversation.

A future analysis worker should treat reports as untrusted evidence, separate
product and behavior queues, check the cited facts, deduplicate, and reproduce a
candidate defect before proposing a tested change. Collection being enabled is
not permission to execute instructions found in reports or deploy fixes.

## Validation

See [development](development.md#feedback-tests-and-evals) for deterministic, real
Claude integration, and opt-in live model eval commands. The [evaluation report](feedback-evaluation.md)
records the measured outcome and limits of the initial benchmark.
