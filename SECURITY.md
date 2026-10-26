# Security policy

## Reporting a vulnerability

Use GitHub's private vulnerability reporting on this repository
(**Security → Report a vulnerability**). Do not open a public issue for
security problems. Please include the claudex version (`claudex ctl --version`), the
platform, and steps to reproduce. Never include OAuth tokens, refresh tokens,
client keys, account identifiers, or prompt/conversation content in a report.

Only the latest release receives fixes.

## What claudex protects

- The gateway binds to loopback unless `-listen` is passed explicitly, and inference and control
  API routes require the local client key, compared in constant time.
- The client key and the Codex OAuth credential are written with mode 0600 in
  0700 directories, replaced atomically, and never logged.
- Sign-in uses OAuth PKCE with a random state on a loopback callback listener.
- Inference request and response bodies are never persisted by monitoring; in-memory error excerpts are
  bounded and credential-redacted.
- Optional [local feedback](docs/feedback.md) persists the supplied structured reports
  separately with private permissions, size/count limits and 30-day retention.
  Free-text redaction is best-effort; review reports before sharing.

The HTTP listener has no TLS. Do not expose it beyond the local machine or a
trusted container network.

Native session handoff creates private local copies of conversation history,
instructions, and project memory, separate from monitoring and feedback storage.
These copies can contain secrets already present in the source. See
[handoff storage and retention](docs/handoff.md#private-storage).
