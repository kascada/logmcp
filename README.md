# LogMCP

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

LogMCP is an open-source MCP (Model Context Protocol) server that exposes local log files to AI clients (Claude Code, VS Code, Claude Desktop) over HTTPS + Bearer Token. It allows AI assistants to read and search log files on a remote Linux server without requiring SSH access or write permissions. All access is audited via syslog.

## Features

- Read-only access to log files — the AI cannot modify anything
- Whitelist/blacklist glob patterns for fine-grained access control
- systemd journal (`journald://`) as a virtual log source
- Multi-token auth — per-client bearer tokens with revocation
- TLS support: self-signed, custom cert, or behind Caddy reverse proxy
- Audit trail via syslog (access only, no log content)
- Guided interactive setup wizard
- Systemd service integration

## Install

```sh
go install github.com/kleist-dev/logmcp@latest
```

Or download a pre-built binary from the [releases page](https://github.com/kleist-dev/logmcp/releases).

## Quickstart

Run the interactive setup wizard (requires root for writing `/etc/logmcp/`):

```sh
sudo logmcp setup
```

This will guide you through:
- Deployment mode (direct TLS or behind Caddy)
- Port and bearer token configuration
- Systemd service installation
- Client configuration snippets for Claude Code, VS Code, and Claude Desktop

Whitelist/blacklist and journald are configured directly in `/etc/logmcp/config.yaml` after setup.

## Environment variable substitution

Any value in `config.yaml` can reference environment variables using `${VAR}` or `$VAR` syntax. The substitution happens before the file is parsed, so it works everywhere — tokens, paths, DSNs, etc.

```yaml
auth:
  tokens:
    - name: claude
      token: ${LOGMCP_TOKEN}
      scopes: [read]

extensions:
  databases:
    mysql:
      - name: prod
        dsn: ${DB_USER}:${DB_PASS}@tcp(localhost:3306)/mydb
```

Unset variables expand to an empty string. To keep a literal `$` in a value, use `$$`.

After setup, start the server:

```sh
sudo systemctl start logmcp
```

## Commands

| Command | Description |
|---|---|
| `logmcp serve` | Start the MCP server (default) |
| `logmcp setup` | Interactive setup wizard |
| `logmcp check` | Verify configuration and environment |
| `logmcp token list` | List configured bearer tokens |
| `logmcp token add --name <n>` | Add a new bearer token |
| `logmcp token remove <name>` | Remove a bearer token |
| `logmcp token renew <name>` | Generate a new value for an existing token |
| `logmcp logs list` | List accessible log files |
| `logmcp logs read <path>` | Read a log file or `journald://` |
| `logmcp logs search <path>` | Search a log file or `journald://` |
| `logmcp logs info <path>` | Show log file metadata |
| `logmcp service install` | Install systemd service |
| `logmcp service remove` | Remove systemd service |
| `logmcp service status` | Show service status |
| `logmcp service caddy-snippet` | Print Caddyfile configuration |
| `logmcp client-config claude-code` | Print Claude Code MCP config |
| `logmcp client-config vscode` | Print VS Code MCP config |
| `logmcp client-config claude-desktop` | Print Claude Desktop MCP config |

## License

MIT License — see [LICENSE](LICENSE).
