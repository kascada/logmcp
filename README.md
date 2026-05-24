# LogMCP

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

MCP server — read-only log access for AI assistants. Debug your Linux server with AI, without giving the AI shell access.

No SSH. No write permissions. The AI reads your logs over HTTPS and helps you diagnose problems, while your server stays fully under your control.

LogMCP is an open-source [MCP](https://modelcontextprotocol.io) server that exposes log files on a remote Linux server to AI assistants (Claude Code, VS Code, Claude Desktop). Access is read-only, token-authenticated, and fully audited via syslog.

## Get Started

**1. Install**

```sh
go install github.com/kascada/logmcp@latest
```

Or download a pre-built `.deb` from the [releases page](https://github.com/kascada/logmcp/releases).

**2. Run the setup wizard**

```sh
sudo logmcp setup
```

Guides you through TLS mode, bearer token, and systemd service — then prints a ready-to-paste client config snippet.

**3. Add to your AI client**

```sh
logmcp client-config claude-code   # or: vscode | claude-desktop
```

Paste the output into your MCP client config. Done — your AI can now read the server's logs.

---

## The Key Advantage: Let the AI Debug Without Touching Your Server

You give the AI a read-only window into your logs. That's it.

- No SSH access required — the AI connects like any HTTPS client
- No write permissions — the AI cannot change, delete, or execute anything
- No shell access — not even read access beyond the whitelisted log paths
- Works from anywhere — your laptop, Claude Desktop, a remote CI agent
- Every access is audited via syslog on the server

This makes LogMCP ideal for situations where you need AI-assisted debugging but cannot or do not want to grant shell access: production servers, customer machines, hardened environments, or any setup where least-privilege matters.

> **SSH is also supported.** If your AI client is Claude Code running locally with an SSH key, you can use LogMCP over an SSH tunnel instead of a public HTTPS endpoint. See [SSH Tunnel Setup](#ssh-tunnel-setup) below.

## Features

- Read-only access to log files — the AI cannot modify anything
- Whitelist/blacklist glob patterns for fine-grained access control
- systemd journal (`journald://`) as a virtual log source
- Multi-token auth — per-client bearer tokens with revocation
- TLS support: self-signed, custom cert, or behind Caddy reverse proxy
- Audit trail via syslog (access only, no log content)
- Guided interactive setup wizard
- Systemd service integration

## Setup Details

The wizard (`sudo logmcp setup`) covers:
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

## Case Studies

Real-world scenarios where LogMCP makes the difference — see [docs/case-studies.md](docs/case-studies.md):

- [Asterisk/VoIP — Paket-Kompilierung und Abhängigkeitsfehler debuggen](docs/case-studies.md#asterisk--voip-server--paket-kompilierung-und-abh%C3%A4ngigkeitsfehler-debuggen)
- [Webserver mit Caddy Reverse Proxy — produktive Installation](docs/case-studies.md#webserver-mit-caddy-reverse-proxy--produktive-installation)
- [Standalone-Betrieb mit eigenem TLS](docs/case-studies.md#standalone-betrieb-mit-eigenem-tls)

## SSH Tunnel Setup

If you are using Claude Code locally and already have SSH access to the server, you can run LogMCP without a public HTTPS endpoint:

```sh
# Forward remote port 7788 to localhost
ssh -L 7788:127.0.0.1:7788 user@yourserver

# Then point your MCP client at https://localhost:7788
```

LogMCP still requires a bearer token over the tunnel — the SSH layer adds transport security, the token controls which client can connect.

## License

MIT License — see [LICENSE](LICENSE).
