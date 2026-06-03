# LogMCP — stdio Mode (Local MCP Server)

`logmcp stdio` starts LogMCP as a local MCP server over stdin/stdout. No HTTP server is started, no bearer token is required, and no Caddy or TLS configuration is needed.

## When to use stdio mode

- **Local log analysis** — run LogMCP on the same machine as your AI client (Claude Desktop or Claude Code) and give it direct access to local log files without exposing an HTTP endpoint.
- **Development and testing** — try LogMCP without setting up TLS or a reverse proxy.
- **Read-only local access** — the stdio client gets the same MCP tools as HTTP clients (`list_logs`, `read_log`, `search_log`, etc.) with scopes controlled via config.

## How it works

When run in stdio mode, LogMCP:

1. Loads `/etc/logmcp/config.yaml` (same config as the HTTP server).
2. Applies the scopes from `auth.stdio.scopes` (default: `["logmcp:read"]`).
3. Speaks the MCP protocol over stdin/stdout — no network socket is involved.
4. Writes audit log entries to syslog as usual, with caller name `stdio`.

## Configuration

The `auth.stdio` block in `/etc/logmcp/config.yaml` controls the scopes granted to the local stdio client:

```yaml
auth:
  stdio:
    scopes: ["logmcp:read"]
```

When `auth.stdio.scopes` is empty or the block is absent, the default scope `["logmcp:read"]` is used.

If your config file is for stdio-only use (no HTTP clients), you do not need an `auth.tokens` list. Set `auth.stdio.scopes` and omit `auth.tokens`:

```yaml
name: "logmcp-local"

auth:
  stdio:
    scopes:
      - logmcp:read

logs:
  whitelist:
    - /var/log/*

audit:
  syslog: true
```

## Claude Desktop setup

Add an entry to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or the equivalent path on your OS:

```json
{
  "mcpServers": {
    "logmcp": {
      "command": "/usr/local/bin/logmcp",
      "args": ["stdio"]
    }
  }
}
```

Restart Claude Desktop after editing the file.

## Claude Code setup

Add an entry to `.claude/settings.json` in your project, or to `~/.claude/settings.json` for global access:

```json
{
  "mcpServers": {
    "logmcp": {
      "command": "/usr/local/bin/logmcp",
      "args": ["stdio"],
      "type": "stdio"
    }
  }
}
```

Alternatively, use `claude mcp add` from the command line:

```bash
claude mcp add logmcp --type stdio -- /usr/local/bin/logmcp stdio
```

## Permissions

The stdio process runs as the user that starts the AI client (Claude Desktop or Claude Code). Make sure that user can read the log files listed in `logs.whitelist`. On most systems, adding the user to the `adm` group grants access to `/var/log/*`:

```bash
sudo usermod -aG adm $USER
```

Log out and back in for the group change to take effect.

## Audit log

All tool calls in stdio mode are recorded in syslog with caller name `stdio` — the same format as HTTP clients. View with:

```bash
grep 'logmcp.*stdio' /var/log/syslog
# or
journalctl -t logmcp
```
