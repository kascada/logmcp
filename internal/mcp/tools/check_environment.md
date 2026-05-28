# check_environment

Run a set of server-side environment checks and return their pass/fail status. Covers configuration validity, TLS setup, log file whitelist, syslog connectivity, and database connectivity.

## When to use

Use to verify that the LogMCP server is configured correctly and that all configured backends are reachable. Useful when diagnosing why a tool is not working as expected, or after a configuration change.

## Response

Array of check result objects, each with:

- `name` — check identifier (e.g. `config`, `tls`, `whitelist`, `syslog`)
- `ok` — true if the check passed, false if it failed
- `detail` — human-readable description of the result or error (omitted when empty)
