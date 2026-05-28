# LogMCP — Documentation Index

This is the entry point for all LogMCP documentation available as MCP resources.

## Available Resources

| URI | Description |
|-----|-------------|
| `logmcp://docs/index` | This file — table of contents and entry point |
| `logmcp://docs/config` | Configuration reference: all config keys, defaults, and examples |
| `logmcp://docs/logging` | Logging and audit reference: syslog format, fail2ban integration |
| `logmcp://docs/ansible` | Ansible role reference: automated deployment via the official Debian package |
| `logmcp://docs/macro` | Macro reference: define custom MCP tools in YAML |
| `logmcp://docs/examples` | Extension examples: clitool + auth, RPC transport, macros |

## How to access

Fetch any resource by its URI via the MCP protocol. Example: to read the configuration reference, access `logmcp://docs/config`.

## Quick orientation

- **New installation?** Start with `logmcp://docs/config` for a full example config and field-by-field reference.
- **Deploying with Ansible?** See `logmcp://docs/ansible`.
- **Diagnosing auth or audit issues?** See `logmcp://docs/logging`.
- **Checking the current server config at runtime?** Use the `check_config` MCP tool.
- **Defining custom MCP tools from YAML?** See `logmcp://docs/macro`.

## Diagnostic tools

| What | How |
|------|-----|
| MCP layer responding? Extensions active? | `server_status` MCP tool (requires auth) |
| Full environment check (TLS, files, systemd) | `check_environment` MCP tool (requires auth) |
| HTTP server alive? (no auth, no MCP) | `GET /status` — returns `{"ok":true,"checks":[...]}` |
