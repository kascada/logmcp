# Contributing to LogMCP

Thanks for your interest. Contributions are welcome — bug reports, feature ideas, and pull requests.

## Reporting Bugs

Use the [Bug Report](.github/ISSUE_TEMPLATE/bug_report.yml) template.
Please include the output of `logmcp check` and the relevant section of your `config.yaml` (with tokens redacted).

## Suggesting Features

Use the [Feature Request](.github/ISSUE_TEMPLATE/feature_request.yml) template.
Describe the use case, not just the solution — that helps assess fit and scope.

## Pull Requests

- One concern per PR; keep diffs small
- Run `go vet ./...` and `go test ./...` before opening
- Match the existing code style — `gofmt` is non-negotiable
- Update `CHANGELOG.md` under `[Unreleased]` with a short entry
- Update `docs/CONFIG.md` if you add or change any config keys

## Building Locally

```sh
go build -o logmcp .
./logmcp check
```

No external build tools required beyond the Go toolchain.

## Security Vulnerabilities

**Do not open a public issue for security bugs.**
See [SECURITY.md](SECURITY.md) for the disclosure process.
