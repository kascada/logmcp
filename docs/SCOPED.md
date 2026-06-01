# LogMCP — Scopes

Bearer tokens carry a list of scopes that control which MCP tools and HTTP endpoints a client may use. Scopes are checked server-side; a missing scope returns a `scope_denied` error to the caller.

## Defined scopes

| Scope | Who gets it | What it grants |
|-------|-------------|----------------|
| `logmcp:read` | Regular clients | All standard MCP tools: `list_logs`, `read_log`, `search_log`, `log_info`, `check_environment`, `check_config`, `server_status`, all extension tools |
| `logmcp:admin` | Admin clients | Everything in `logmcp:read`, plus admin views across all extensions (e.g. all Telegram channels and their user mappings) |

## Configuration

Scopes are set per token in `config.yaml`:

```yaml
auth:
  tokens:
    - name: "claude-code"
      token: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
      scopes: ["logmcp:read"]
    - name: "admin"
      token: "yyyyyyyy-yyyy-yyyy-yyyy-yyyyyyyyyyyy"
      scopes: ["logmcp:read", "logmcp:admin"]
```

When `auth.authenticator` is used instead of `auth.tokens`, the external program returns the scopes as part of its response (see [CLITOOL.md](CLITOOL.md)).

## Extension scopes

Extensions can define their own scopes (e.g. `switchboard:admin`). These are checked by the extension worker itself via the `requiredScope` field in the tool definition. Extension scopes are independent of LogMCP core scopes and documented alongside the extension.

## Scope behaviour by feature

### Telegram notifications (when configured)

| Scope | `notify_send` | `notify_channels` |
|-------|---------------|-------------------|
| `logmcp:read` | Own channels only | Own channels only |
| `logmcp:admin` | Any channel (by name) | All channels with full user mappings |
