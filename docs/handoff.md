# Move between Codex and Claude Code

Both hosts expose `claudex:handoff`. Each skill prepares a new saved session and
prints a command to resume it in the same workspace. Exit the source client and
run that command in your terminal. Preparation runs locally, without a model
request, closing the source, or executing historical tools.

## Codex to Claude Code

Install the updated claudex binary, then run setup once to install its user-wide
Codex skill (existing installations can skip sign-in):

```sh
claudex ctl setup --no-login
```

Start a fresh Codex CLI session and invoke the skill:

```text
$claudex:handoff
```

It is also available through Codex's `/skills` picker. The destination is a new,
independent Claude Code session resumed **through claudex**, using claudex's
configured model. Any locally saved Codex main session can be selected; it does
not need to have originated in claudex. This does not configure vanilla Claude
Code's Anthropic sign-in or routing.

The skill uses the exact `CODEX_THREAD_ID` binding. Outside the source session,
select its UUID or absolute rollout path explicitly:

```sh
claudex ctl handoff --to cc --thread-id 12345678-1234-4234-8234-123456789abc
claudex ctl handoff --to CC --source /absolute/path/to/rollout.jsonl --json
```

`cc` is case insensitive. `--source` and `--thread-id` are mutually exclusive.
Thread lookup searches `CODEX_HOME/sessions/` and `archived_sessions/` for that
exact UUID, verifies its metadata, and rejects ambiguous matches. It never guesses
the newest session. An explicit rollout can be outside those directories.

The printed command changes to the source workspace and runs the current claudex
executable with `--resume` and the new transcript path. It preserves the selected
`CLAUDE_CONFIG_DIR`, `CLAUDEX_CONFIG`, and Claude project directory. Install Claude
Code on PATH before preparing a transfer. Its normal permission checks apply
when work continues.

### History, instructions, and memory

The importer preserves recorded main-thread user/assistant text, commentary,
textual tool calls/results, and recorded delegation calls/reports. It honors
rollbacks, retains original pre-compaction text, and labels compaction checkpoints
without duplicating replacement history. It does not fetch separate child-agent
transcripts or recreate running agents. Mirrored UI events are not replayed.

Global and ancestor `AGENTS.md` files are snapshotted with original scope labels;
`AGENTS.override.md` takes precedence within each directory. Instructions already
recorded as user messages remain in history. Provider/runtime developer policies
remain in the source archive. Descendant or externally referenced instruction
files are not recursively collected.

Only Codex memory file ranges cited by retained assistant messages are copied
from the selected `CODEX_HOME/memories/`. The whole global memory store is never
copied. The cited ranges are included in the conversation and written as attributed
notes under the destination project's `memory/claudex-handoffs/`. A deduplicated
link section near the top of its actual `memory/MEMORY.md` makes the notes
discoverable while preserving existing index content. These are snapshots of
session-used notes, not a claim that their facts have been reverified. Missing or
invalid citations produce warnings and a partial memory receipt.

Setup installs `~/.agents/skills/claudex-handoff` as a symlink to a versioned,
embedded skill bundle under `~/.local/share/claudex/codex-skills/`. Rerun setup
after replacing or moving claudex to update the skill's executable path. It refuses
to overwrite an unowned or edited skill. `make install` remains binary-only.

## Claude Code to Codex

In a Claude Code session launched by claudex, run:

```text
/claudex:handoff
```

The bundled skill prints a verified
`codex resume <thread-id> --cd '<workspace>'` command.

Install the native Codex CLI on PATH and configure its own sign-in before
continuing work. The importer is tested with Codex CLI 0.164.0 and Claude Code
2.1.312. It uses Codex's experimental native migration API; incompatible versions
fail explicitly. No Node runtime or separately installed Claude plugin is needed.
Start or resume Claude through the updated claudex binary to load the skill.

### What carries over

| Material | Treatment |
| --- | --- |
| Conversation | Recorded user/assistant text imported into a native resumable thread |
| Tool calls and results | Full recorded textual evidence, without the native importer's tool-summary truncation; historical tools do not execute |
| Project files | Stay in the existing workspace, including uncommitted work |
| Instructions | Snapshot global and ancestor `CLAUDE.md`, ancestor `CLAUDE.local.md` and `.claude/CLAUDE.md`, plus global/ancestor `.claude/rules/*.md` recursively |
| Project memory | Snapshot Markdown under the source session's project `memory/`; standard Claude homes also import it into Codex's project-scoped memory resources |
| Original transcript | Preserve the exact bytes privately, including records excluded from the native conversation |

The destination chooses its own model, context capacity, compaction, authentication, tools,
MCP servers, skills, and permissions. Hidden reasoning, running processes, pending
approvals, and Claude's exact active context window cannot be transferred. Opaque
blocks and attachments remain in the original archive with references in the
imported history. Meta/sidechain records are archived, not replayed as main turns.
Instruction `@` imports, descendant-directory instructions, and external attachments
are not recursively collected. File snapshots retain their original scope; they
do not replace native `AGENTS.md` or Codex configuration.

Memory snapshots are immediately present in the imported conversation. Native
memory migration copies resources and queues Codex's later consolidation; it does
not promise immediate consolidation. claudex enables the experimental memory
import feature only in its importer subprocess. Custom `CLAUDE_CONFIG_DIR` homes
receive conversation snapshots of memory, without native memory-resource migration.

### Direct command

The skill uses `claudex ctl handoff --to codex`. Its SessionStart hook binds the exact current
transcript; claudex never selects the newest session by guesswork. Outside that
session, select a transcript explicitly:

```sh
claudex ctl handoff --to codex --source /absolute/path/to/session.jsonl
claudex ctl handoff --to codex --source /absolute/path/to/session.jsonl --launch
```

Sources must be inside the selected Claude home's `projects/` directory.
The default destination remains `codex` for existing ctl callers; the old
`/claudex:codex` skill has been replaced by `/claudex:handoff`.
`CODEX_HOME` selects the native destination.
The native subprocess removes claudex's Claude provider-routing environment.

## Receipts, launching, and retries

Both directions support `--json` for a structured receipt containing the exact
resume command, snapshot paths, memory status, and limitations. Reverse receipts
also include warnings and the new Claude `session_id`. Skills only prepare and
display receipts. `--launch` is for direct terminal use: it opens the selected
destination and cannot be combined with `--json` or used without a terminal.

An unchanged snapshot resolves to the same imported session on retry. New transcript
or instruction content creates a new snapshot. A failed memory import reports an
error but retains the verified resume command and conversation snapshots. The
source session is never deleted. Reverse retries preserve a continued destination
and repair failed memory writes using the same UUID. Unexpected destination edits
fail explicitly instead of being overwritten. If a transcript is still being written,
wait for the turn to finish and retry. Unsupported shapes, escaping memory citations,
and size limits fail explicitly: 256 MiB transcript, 64 MiB per JSONL record,
16 MiB or 1,000 instruction/memory files.

## Private storage

Handoffs contain conversation content, which may include secrets already present
in the source. Files are owner-readable/writable (`0600`) beneath private (`0700`)
`~/.claude/projects/claudex-handoffs/<digest>/` directories. Reverse handoffs use
the selected `CLAUDE_CONFIG_DIR` instead. Each contains the original transcript,
normalized history, context snapshots, and manifests. Reverse imports also keep
the initial native Claude transcript separately from its writable destination at
`<Claude home>/projects/<project-key>/<new-session-id>.jsonl`. They are separate
copies, so continuing Claude cannot alter the source archive. No project files
are changed. Sending a turn after resume uses the destination's configured provider.

Snapshots have no automatic expiry. Remove a specific handoff directory when no
longer needed; imported history and memory are separate copies managed by each
client. Removing claudex does not remove these copies. To remove its installed
Codex skill, remove only the `~/.agents/skills/claudex-handoff` symlink; versioned
bundles can then be removed from `~/.local/share/claudex/codex-skills/`.

The design follows the native transfer API used by
[OpenAI's Claude Code plugin](https://github.com/openai/codex-plugin-cc), with a
Go implementation embedded in the claudex binary. Reverse import uses the native
Claude JSONL session format and explicit-path resume, qualified against Claude
Code 2.1.312 and Codex CLI 0.164.0. There is no Node/Python runtime dependency.
