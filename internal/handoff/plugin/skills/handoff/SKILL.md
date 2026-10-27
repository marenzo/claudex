---
name: handoff
description: Move the current Claude Code session to the native Codex CLI when the user asks to continue or transfer their work to Codex. Preserves conversation history and project memory in a native resumable thread. Does not delegate a background coding task.
---

Run `"$CLAUDEX_BINARY" ctl handoff --to codex` with Bash. The SessionStart hook binds the exact
current transcript; do not search for a recent transcript or infer a session ID.
If no binding exists, explain the error and ask for an explicit transcript path.
Use `--source` only for a path the user supplies.

Show the returned `codex resume` command and any transfer limitations. The command
imports locally and does not start a model turn, close Claude Code, or change
Codex permissions. Do not launch an interactive TUI inside a tool subprocess.
The user can exit Claude Code and run the printed command in their terminal.

Never claim an exact context-window or hidden-state transfer. The handoff keeps
full textual tool evidence and snapshots project memory and instructions; native
Codex controls the new model, context window, compaction, tools, and permissions.
