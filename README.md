# LogMCP

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

MCP server — read-only log access for AI assistants. Debug your Linux server with AI, without giving the AI shell access.

No SSH. No write permissions. The AI reads your logs over HTTPS and helps you diagnose problems, while your server stays fully under your control.

LogMCP is an open-source [MCP](https://modelcontextprotocol.io) server that exposes log files on a remote Linux server to AI assistants (Claude Code, VS Code, Claude Desktop). Access is read-only, token-authenticated, and fully audited via syslog.

## Get Started

### Option A: Quickstart (no root required)

Try LogMCP in under a minute — no config file, no root, no systemd:

```sh
logmcp quickstart
```

The command checks your group memberships (`adm`, `systemd-journal`), generates a bearer token and a self-signed TLS certificate, starts the server, and prints a ready-to-paste `claude mcp add` command.

**Running as root?** Pass `--user <name>` and LogMCP will add the user to the required groups and re-launch as that user — root is not needed for future starts.

> **Note:** Token and certificate are ephemeral. The token changes on every start. For a permanent setup use Option B.

```sh
logmcp quickstart --port 7789 --token mytoken   # optional flags
```

After testing, remove the server from Claude Code:

```sh
claude mcp remove logmcp-quickstart
```

---

### Option B: Permanent installation (recommended)

**1. Install**

```sh
go install github.com/kascada/logmcp@latest
```

Or install the pre-built `.deb` (replace `x.y.z` with the [latest version](https://github.com/kascada/logmcp/releases)):

```sh
curl -LO https://github.com/kascada/logmcp/releases/download/vx.y.z/logmcp_x.y.z_amd64.deb
sudo dpkg -i logmcp_x.y.z_amd64.deb
```

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
- External authenticator support — delegate token verification to any CLI program
- TLS support: self-signed, custom cert, or behind Caddy reverse proxy
- Audit trail via syslog (access only, no log content)
- Guided interactive setup wizard
- Systemd service integration
- Extensions — expose external CLI tools or Redis-RPC workers as additional MCP tools
- Macros — define composite MCP tools as YAML files, no code required
- fail2ban integration and in-process rate limiting

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
  clitool:
    - name: switchboard
      command: /usr/local/bin/switchboard
      timeout_seconds: 10
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
| `logmcp quickstart` | Start instantly without config file (no root required) |
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
| `logmcp security install-fail2ban` | Install fail2ban filter and jail for logmcp |

## MCP Tools

These are the tools LogMCP exposes to AI assistants:

| Tool | Description |
|---|---|
| `list_logs` | List all log files the server has been configured to expose |
| `read_log` | Read lines from a log file — head, tail, offset, or time window |
| `search_log` | Search a log file by regexp with optional context lines and time filter |
| `log_info` | File metadata: size, line count, last modified |
| `check_environment` | Server-side health checks (config, TLS, whitelist, syslog, databases) |
| `check_config` | Show current server configuration and optional parameters at their defaults |
| `server_status` | Runtime status of the MCP layer and registered extensions |

Extensions may add further tools — their names are prefixed with the extension name (e.g. `myapp_status` for an extension named `myapp`).

## Extensions — Wrapping External Tools as MCP

LogMCP can expose any external program or service as additional MCP tools — no custom MCP server required. The AI sees them alongside the built-in log tools.

### CLI extension

Any program that implements the [`clitool` interface](docs/CLITOOL.md) (`list` / `call` subcommands) can be registered. LogMCP calls `<command> list` at startup to discover tools, and forwards each tool call to `<command> call <tool> --token-stdin`.

```yaml
extensions:
  clitool:
    - name: myapp
      command: /usr/local/bin/myapp-ctl
      timeout_seconds: 10
```

This spawns a subprocess per call — suitable for any program on the same or a remote host.

### RPC extension (Redis)

For programs running on the same host, the RPC variant avoids the per-call process-startup overhead (relevant for Python programs where interpreter startup and imports add noticeable latency). Instead of spawning a subprocess, LogMCP pushes a request onto a Redis list and waits for the worker's reply.

```yaml
extensions:
  clitool:
    - name: myapp
      command: /usr/local/bin/myapp-ctl   # still used for `list` at startup
      mode: rpc
      redis_addr: "127.0.0.1:6379"
      timeout_seconds: 5
```

The worker reads requests from a Redis list and pushes its reply to a per-request reply key. See [docs/RPC.md](docs/RPC.md) for the full protocol.

### Auth flow

The bearer token from each incoming MCP request is forwarded to the extension — either via stdin (CLI mode) or as `caller` metadata in the RPC envelope. The extension can verify it or trust the pre-resolved identity.

---

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
