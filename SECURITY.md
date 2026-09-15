# Security policy

## Reporting a vulnerability

Use GitHub's private vulnerability reporting on this repository
(**Security → Report a vulnerability**). Do not open a public issue for
security problems. Please include the Claudex version (`claudex version`), the
platform, and steps to reproduce. Never include OAuth tokens, refresh tokens,
client keys, account identifiers, or prompt/conversation content in a report.

Only the latest release receives fixes.

## What Claudex protects

- The gateway binds to loopback unless `-listen` is passed explicitly, and every
  API route requires the local client key, compared in constant time.
- The client key and the Codex OAuth credential are written with mode 0600 in
  0700 directories, replaced atomically, and never logged.
- Sign-in uses OAuth PKCE with a random state on a loopback callback listener.
- Request and response bodies are never persisted; dashboard error excerpts are
  bounded and credential-redacted.

The HTTP listener has no TLS. Do not expose it beyond the local machine or a
trusted container network.
