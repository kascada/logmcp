# server_status

Report the runtime status of this LogMCP server: whether the MCP layer is responding, how many tools are registered, and whether each configured extension is accessible.

## When to use

Use as a first step when diagnosing MCP connectivity issues — before the deeper `check_environment` checks (file system, systemd, TLS). Returns quickly and confirms that the MCP tool layer itself is functional.

## Response

`ok` — true if all checks passed.

`checks` — array of check result objects, each with:

- `name` — check identifier
- `ok` — true if the check passed, false if it failed
- `detail` — human-readable description of the result or error (omitted when empty)
