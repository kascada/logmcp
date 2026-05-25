LogMCP provides read-only access to server-side log files and optional application extensions via the Model Context Protocol.

## What this server provides

- **Log access** — list, read, and search log files that the server administrator has explicitly whitelisted. No files outside the whitelist are accessible.
- **Environment checks** — verify that the server and all its configured backends are reachable and correctly configured.
- **Extensions** — optional domain-specific tools enabled by server configuration (e.g. `switchboard_debug` for Switchboard call diagnostics).

## How to navigate logs

1. Call `list_logs` to discover which log files are available.
2. Call `log_info` to check a file's size and line count before reading.
3. Use `read_log` with `tail: true` and a `since` duration for recent activity, or `search_log` with a pattern for targeted lookups.

## Access control

All log access is enforced server-side via a whitelist. The server returns an error for any path not on the whitelist. Audit entries are written for every successful access.

## Time filters

`since` and `until` parameters accept either RFC3339 timestamps (`2024-01-15T10:00:00Z`) or relative durations (`1h`, `30m`, `2h30m`). Relative durations are resolved against the server's current time.

## Documentation

Documentation is available as MCP resources. Start with `logmcp://docs/index` for an overview of all available docs. Use `check_config` to inspect the current server configuration and discover optional parameters.
