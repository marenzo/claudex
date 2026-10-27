---
name: handoff
description: Continue the current Codex session in Claude Code through claudex when the user asks to switch or hand off their work. Preserve the recorded conversation, textual tool evidence, project guidance, and session-used memory in a new resumable Claude Code session.
---

Run `{{CLAUDEX_BINARY}} ctl handoff --to cc --json` using the shell tool. The command
uses `CODEX_THREAD_ID` to select this exact Codex session. Never select the newest
rollout or infer another session ID. If the binding is unavailable, explain the
error and ask the user for a thread ID or transcript path; use `--thread-id` or
`--source` only for their explicit selection.

Show the returned `resume_command`, project-memory status, and any warnings.
The user can exit Codex and run that command in their terminal. Do not use
`--launch` or start an interactive client inside a tool subprocess. Preparing the
handoff does not send a model request, close Codex, or continue historical tools.

Report a partial memory import accurately: the saved conversation remains usable,
and retrying an unchanged snapshot repairs memory work without another session.
The destination uses claudex's configured model and Claude Code's current tools
and permissions. Hidden reasoning, active processes/subagents, approvals, and the
exact context window are not transferred. Opaque records stay in the archive.
