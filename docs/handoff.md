# Continue in native Codex

In a Claude Code session launched by claudex, run:

```text
/claudex:codex
```

The bundled skill imports the current session and prints a verified
`codex resume <thread-id> --cd '<workspace>'` command. Exit Claude Code and run
that command in your terminal. Codex opens the saved conversation in the same
workspace. The transfer does not start a model turn or close Claude Code.

Install the native Codex CLI on PATH and configure its own sign-in before
continuing work. The importer is tested with Codex CLI 0.164.0 and Claude Code
2.1.309. It uses Codex's experimental native migration API; incompatible versions
fail explicitly. No Node runtime or separately installed Claude plugin is needed.
Start or resume Claude through the updated claudex binary to load the skill.

## What carries over

| Material | Treatment |
| --- | --- |
| Conversation | Recorded user/assistant text imported into a native resumable thread |
| Tool calls and results | Full recorded textual evidence, without the native importer's tool-summary truncation; historical tools do not execute |
| Project files | Stay in the existing workspace, including uncommitted work |
| Instructions | Snapshot global and ancestor `CLAUDE.md`, ancestor `CLAUDE.local.md` and `.claude/CLAUDE.md`, plus global/ancestor `.claude/rules/*.md` recursively |
| Project memory | Snapshot Markdown under the source session's project `memory/`; standard Claude homes also import it into Codex's project-scoped memory resources |
| Original transcript | Preserve the exact bytes privately, including records excluded from the native conversation |

Codex chooses its own model, context capacity, compaction, authentication, tools,
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

## Direct command and retries

The skill uses `claudex ctl handoff`. Its SessionStart hook binds the exact current
transcript; claudex never selects the newest session by guesswork. Outside that
session, select a transcript explicitly:

```sh
claudex ctl handoff --source /absolute/path/to/session.jsonl
claudex ctl handoff --source /absolute/path/to/session.jsonl --launch
```

Sources must be inside the selected Claude home's `projects/` directory.
`--launch` opens Codex directly and requires an interactive terminal; use `--json`
instead for a structured receipt. `CODEX_HOME` selects the native destination.
The native subprocess removes claudex's Claude provider-routing environment.

An unchanged snapshot resolves to the same imported thread on retry. New transcript
or instruction content creates a new snapshot. A failed memory import reports an
error but retains the verified resume command and conversation snapshots. The
original Claude session is never deleted. If a transcript is still being written,
wait for the turn to finish and retry. Unsupported shapes, symlinked context files,
and size limits fail explicitly: 256 MiB transcript, 64 MiB per JSONL record,
16 MiB or 1,000 instruction/memory files.

## Private storage

Handoffs contain conversation content, which may include secrets already present
in the source. Files are owner-readable/writable (`0600`) beneath private (`0700`)
`~/.claude/projects/claudex-handoffs/<digest>/` directories. Each contains an original
transcript, import transcript, context snapshots, and manifest. No files are put in
the repository or sent by the importer to a model. Resuming and sending a turn
uses native Codex's configured provider normally.

Snapshots have no automatic expiry. Remove a specific handoff directory when no
longer needed; imported Codex history and memory are separate copies managed in
`CODEX_HOME`. Removing claudex does not remove these copies.

The design follows the native transfer API used by
[OpenAI's Claude Code plugin](https://github.com/openai/codex-plugin-cc), with a
Go implementation embedded in the claudex binary.
