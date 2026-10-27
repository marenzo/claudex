# Session handoff

Continue work from Claude Code in native Codex CLI, or from Codex in Claude Code
through claudex. Handoff creates an independent saved conversation in the same
local workspace, preserving recorded text, textual tool evidence, and supported
instructions and memory. Your project files, including uncommitted changes, stay
where they are.

Preparation prints a command to resume the destination. It does not send a model
request, close the source client, or execute historical tools. You decide when to
switch by running the returned command in your terminal.

## Before you start

1. Install a claudex build containing bidirectional handoff. See
   [installation and updates](running.md#portable-binary).
2. Install Claude Code and native Codex CLI on the same machine. Both should be
   available on PATH. The source workspace must still exist locally.
3. Complete `claudex ctl setup` for claudex configuration and subscription sign-in.
   Continuing in native Codex uses its own configuration and sign-in; claudex does
   not transfer credentials between clients.
4. If claudex is already configured, run `claudex ctl setup --no-login` after an
   upgrade to install or update its Codex skill without signing in again. Start a
   fresh Codex session to discover the installed skill. Start or resume Claude
   through the updated claudex executable to load its bundled Claude skill.

The integrations were qualified with Codex CLI 0.164.0 and Claude Code 2.1.312.
The transfer logic is bundled in the Go binary; it needs no separate plugin
download or Node/Python helper. Native client compatibility is described under
[limits](#limits).

## From Claude Code to Codex

1. Start or resume Claude Code through claudex:

   ```sh
   claudex
   # Or choose an existing Claude conversation:
   claudex --resume
   ```

2. In the Claude conversation you want to transfer, invoke:

   ```text
   /claudex:handoff
   ```

3. Read the receipt, including its memory status and any limitations. It contains
   a command of this form, with your actual thread ID and workspace:

   ```sh
   codex resume <thread-id> --cd '<workspace>'
   ```

4. Exit Claude Code or open another terminal, then run the returned command.
   Codex opens the saved conversation in the original workspace. Its own model,
   tools, permissions, and sign-in apply when you continue.

The skill uses a SessionStart hook to bind the exact current Claude transcript.
It never searches for the most recent session. If the binding is unavailable,
restart through the updated claudex binary or select a transcript explicitly with
the [terminal command](#terminal-commands).

The previous `/claudex:codex` skill has been replaced by `/claudex:handoff`.

## From Codex to Claude Code

1. After setup has installed the Codex skill, start a fresh Codex CLI session or
   resume the saved conversation you want to transfer.
2. Invoke the native Codex skill in that conversation:

   ```text
   $claudex:handoff
   ```

   You can also select it through Codex's `/skills` picker. This is a Codex prompt,
   not a shell command.
3. Read the receipt and copy its `resume_command`. The command changes to the
   source workspace and starts the current claudex executable with the new
   Claude transcript. It also preserves the selected claudex configuration,
   Claude home, and project memory directory.
4. Exit Codex or open another terminal, then run that exact command. Continue in
   the new Claude session using claudex's configured model and Claude Code's
   current tools and permission checks.

Any locally saved Codex main session can be transferred, including one that did
not originate in claudex. The skill selects the exact `CODEX_THREAD_ID` supplied
by its host. If that binding is missing, supply a known thread ID or absolute
rollout path with the terminal command; claudex does not guess another session.

The destination runs through claudex. This workflow does not configure vanilla
Claude Code's Anthropic sign-in or provider routing.

## Terminal commands

Inside a correctly bound source session, prepare a handoff directly:

```sh
# Current Claude session to native Codex; codex is also the default destination.
claudex ctl handoff --to codex

# Current Codex session to Claude through claudex.
claudex ctl handoff --to cc
```

Outside the source client, select a saved conversation explicitly:

```sh
# Claude transcript inside the selected Claude home's projects/ directory.
claudex ctl handoff --to codex --source /absolute/path/to/session.jsonl

# Exact saved Codex thread UUID.
claudex ctl handoff --to cc --thread-id 12345678-1234-4234-8234-123456789abc

# Explicit Codex rollout; write a structured receipt for a script or tool.
claudex ctl handoff --to cc --source /absolute/path/to/rollout.jsonl --json

# Import and open the destination from an interactive terminal.
claudex ctl handoff --to cc --source /absolute/path/to/rollout.jsonl --launch
```

| Option | Behavior |
| --- | --- |
| `--to codex` | Continue Claude history in native Codex; the default if omitted |
| `--to cc` | Continue Codex history in Claude through claudex; destination values are case insensitive, so `CC` also works |
| `--source PATH` | Select an absolute JSONL transcript of the source client |
| `--thread-id UUID` | Select one saved Codex main thread; supported only with `--to cc` |
| `--json` | Print a structured receipt with destination identity, resume command, snapshot paths, and memory status |
| `--launch` | Open the destination after import; requires a terminal and cannot be combined with `--json` |

`--source` and `--thread-id` are mutually exclusive. UUID lookup searches only
`sessions/` and `archived_sessions/` under the selected Codex home, verifies the
rollout's metadata, and rejects multiple matches. An explicit Codex rollout may
be outside those directories. A Claude source must be inside the selected Claude
home's `projects/` directory.

Skills prepare the transfer and display the receipt. They do not use `--launch`
or start an interactive client inside their tool subprocess.

### Selecting configuration and storage

| Environment variable | Purpose |
| --- | --- |
| `CODEX_HOME` | Codex source sessions and cited memory, or the native Codex destination; defaults to `~/.codex` |
| `CLAUDE_CONFIG_DIR` | Claude source sessions or the Claude destination; defaults to `~/.claude` |
| `CLAUDEX_CONFIG` | claudex configuration used when continuing in Claude; defaults to `~/.config/claudex/config.json` |

For example, select another Codex home and the claudex configuration to use for
the destination:

```sh
CODEX_HOME=/absolute/path/to/codex-home \
CLAUDEX_CONFIG=/absolute/path/to/claudex/config.json \
  claudex ctl handoff --to cc --thread-id 12345678-1234-4234-8234-123456789abc
```

The returned Claude resume command pins its destination paths so it can be run
from a normal terminal. Native Codex import removes claudex's Claude provider
routing from its subprocess environment.

## How the transfer works

1. **Resolve the source.** Use the current client's exact session binding or the
   explicit selection. Read and validate a complete JSONL snapshot before import.
2. **Preserve evidence.** Archive the original bytes and normalize transferable
   conversation into text. Tool calls and outputs become historical evidence,
   without pending tool actions. Unsupported attachments and opaque provider
   records remain in the private source archive.
3. **Snapshot local context.** Collect supported instructions and memory with
   original scope and source labels. Their contents contribute to snapshot
   identity, so context changes produce a different handoff.
4. **Create the destination.** Claude-to-Codex uses Codex's native migration API
   over a temporary local app-server and verifies the saved thread and workspace.
   Codex-to-Claude writes a new native Claude JSONL session and records its UUID
   in a locked import manifest. The writable transcript is a separate copy from
   the source archive and initial transcript snapshot.
5. **Import memory and return a receipt.** Memory is handled separately so a
   failed memory write can still leave a usable conversation and resume command.

For Codex sources, the importer honors recorded rollbacks, preserves original
pre-compaction text when it is present, and labels compaction checkpoints without
duplicating replacement history. Mirrored UI events are not replayed. Recorded
delegation calls and reports in the main conversation carry over; separate child
transcripts and running agents are not imported.

### What carries over

| Material | Claude Code to Codex | Codex to Claude Code |
| --- | --- | --- |
| Main conversation | Recorded user/assistant text | Recorded user/assistant text and commentary on the retained branch |
| Tool evidence | Full recorded textual calls and results | Full recorded textual calls and results |
| Project files | Existing local workspace, including uncommitted work | Existing local workspace, including uncommitted work |
| Instructions | Global/ancestor `CLAUDE.md`, ancestor `CLAUDE.local.md` and `.claude/CLAUDE.md`, global/ancestor `.claude/rules/*.md` recursively | Global/ancestor `AGENTS.md`, with `AGENTS.override.md` preferred in each directory, plus instructions already recorded as user messages |
| Memory | Source project's Markdown memory snapshots; native Codex memory-resource import for standard Claude homes | Only memory file ranges cited by retained assistant messages; attributed notes and index links in Claude project memory |
| Original transcript | Exact private archive | Exact private archive |

Instruction snapshots retain their original directory scope. They do not replace
the destination's native instruction or configuration files. Instruction `@`
imports, descendant instruction files, and external attachments are not
recursively collected. Codex provider/runtime developer policies remain in the
archive rather than becoming destination instructions.

## Memory and retry behavior

In Claude-to-Codex transfers, memory snapshots are included immediately in the
conversation. Native memory import also copies project-scoped resources and
queues Codex's later consolidation; it does not promise immediate consolidation.
claudex enables the experimental memory-import feature only in its importer
subprocess. Custom Claude homes receive conversation snapshots of memory without
native memory-resource migration.

In Codex-to-Claude transfers, the importer reads only file ranges cited by retained
assistant messages from `CODEX_HOME/memories/`. It writes attributed notes under
the destination project's `memory/claudex-handoffs/` and adds a deduplicated link
section near the top of its actual `memory/MEMORY.md`. Existing index content is
preserved. These are source snapshots, not newly verified facts or a copy of the
whole global Codex memory store.

| Receipt memory status | Meaning |
| --- | --- |
| `none` | No memory was selected for migration |
| `imported` | The direction's destination memory import completed |
| `snapshotted_only` | Claude memory is in the conversation snapshot, without confirmed native Codex resource import |
| `partial` | The Claude conversation is ready, but some cited memory was unavailable or a destination memory write failed; inspect warnings |

A memory error can return a nonzero exit status while still printing a usable
`resume_command`. You can continue with that conversation after reading the
warnings, or repair the memory problem first. Rerun the same command against an
unchanged snapshot to repair failed destination memory writes using the same
session UUID. A continued Claude destination is preserved; retries do not replace
it with the initial history. Unexpected destination edits fail explicitly.

An identical source and context snapshot reuses its imported session. New source,
instruction, or selected memory content creates a new snapshot and destination.
If a source transcript is still being written, wait for the turn to finish and
retry. Referenced files are snapshotted as they exist at preparation time; they
may have changed since the source conversation originally read them.

## Limits

- The destination controls its model, context capacity, compaction, tools, MCP
  servers, skills, authentication, and permissions. A saved history transfer does
  not reproduce the source's exact active context window.
- Hidden reasoning, opaque provider state, running processes or agents, and
  pending approvals cannot transfer as active state. Historical tool evidence
  does not execute. Attachments and opaque blocks remain archived, with references
  where supported in the imported history.
- Handoff targets the main saved conversation. Claude meta/sidechain records and
  separate child-agent transcripts are archived or left in their source storage,
  rather than replayed as independent destination agents.
- This is a local continuation workflow. It does not copy the workspace to another
  machine or make deleted and unavailable source files reappear.
- Sources are bounded to 256 MiB per transcript and 64 MiB per JSONL record.
  Collected instruction/memory context is bounded to 16 MiB and 1,000 files or
  cited ranges. Unsupported formats, unsafe memory paths, and exceeded limits
  fail explicitly or produce a partial memory receipt as appropriate.
- Codex's native migration API is experimental. Reverse import depends on Claude's
  native session format and explicit-path resume. Client changes can require a
  claudex update even when the source transcript is still present.

## Private storage

Source archives contain the conversation exactly as recorded, including sensitive
content already present in it. Files use `0600` permissions beneath private
`0700` snapshot directories. Preparation does not send those files to a model;
sending a turn after resume uses the destination's configured provider normally.

Claude-to-Codex snapshots live under
`~/.claude/projects/claudex-handoffs/<digest>/`, as required by Codex's native
importer. Codex-to-Claude snapshots use the selected Claude home's
`projects/claudex-handoffs/<digest>/`. Each keeps the original transcript,
normalized history, context snapshots, and manifests. Reverse imports also keep
an initial native transcript independently of the writable conversation at
`<Claude home>/projects/<project-key>/<new-session-id>.jsonl`.

The source session is never deleted, and no project files are changed. Snapshots
have no automatic expiry. Remove a particular snapshot directory when no longer
needed; imported destination history and memory remain separate client-managed
copies. Removing claudex does not remove those copies.

Setup installs `~/.agents/skills/claudex-handoff` as a symlink to a versioned
embedded bundle under `~/.local/share/claudex/codex-skills/`. Rerun setup after
replacing or moving claudex so the skill uses its current executable path. Setup
refuses to overwrite user-owned or edited skills. `make install` remains
binary-only. To remove the Codex skill, remove that symlink; unused versioned
bundles can then be removed from `~/.local/share/claudex/codex-skills/`.

## Troubleshooting

| Symptom | Action |
| --- | --- |
| Skill missing in Claude | Start or resume through the updated claudex executable so its plugin and SessionStart hook load |
| Skill missing in Codex | Rerun setup with the updated binary, then start a fresh Codex session; check `/skills` |
| No exact source binding | Use the skill in the intended source session or supply `--source` / `--thread-id`; do not choose the newest file by guesswork |
| Multiple rollouts match a UUID | Select the intended absolute rollout with `--source` |
| Incomplete or invalid JSONL | Wait for source writes to finish, then retry |
| Resume command returned with memory error | Read the receipt warnings; repair failed memory writes and retry unchanged, or use the saved conversation with the stated limitation |
| Setup refuses a skill update | Review the existing user-owned or edited skill instead of overwriting it; restore the managed installation only after preserving your changes |
| Native API or transcript format unsupported | Update the native client and claudex to compatible versions; keep the archived source for retry |

For implementation details, see [architecture](architecture.md#native-session-handoff).
For native fixture tests and qualification commands, see [development](development.md#tests).
The forward transfer follows the native API used by
[OpenAI's Claude Code plugin](https://github.com/openai/codex-plugin-cc).
