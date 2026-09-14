# Contributing

Thanks for helping improve Claudex. Read [the architecture](docs/architecture.md)
and the [development guide](docs/development.md) first.

## Setup

Go 1.26+ builds everything. Node.js 24+ runs only the dashboard JavaScript checks
in `make check`. The tests need no live Codex subscription, installed macOS service,
or credentials.

```sh
make check build      # tests with -race, vet, gofmt, dashboard checks, dist/claudex
make lint             # staticcheck and govulncheck; needs network
```

CI runs both. Run `make lint` before opening a pull request.

## Scope and expectations

- Keep changes focused on Claude Code talking to one Codex subscription.
- Protocol fixes include a small synthetic regression fixture that reproduces
  the behavior. Cover replay, ordering, cancellation, errors, and both streamed
  and non-streamed output where relevant.
- Never include credentials, account identifiers, or private conversations in
  fixtures, issues, logs, screenshots, or commits.
- In the pull request, explain the trigger, the resulting behavior, and how you
  validated the change.

## Security issues

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

Contributions are accepted under the project's [MIT license](LICENSE).
