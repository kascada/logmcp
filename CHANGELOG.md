# Changelog

All notable changes to LogMCP will be documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

---

## [0.2.0] — 2026-05-25

Operational hardening release.

### Added

- YAML macro engine — composable MCP tools declarable without Go code changes
- Streaming I/O — `read_log`, `search_log`, `search_journald` stream instead of loading full files into memory
- Graceful shutdown on SIGTERM — systemd `ExecStop` no longer kills mid-request
- Two-tier rate limiting for auth failures — separate limits for IP and global to prevent brute-force
- TTL cache for glob patterns — `ListAccessible()` no longer re-evaluates every whitelist pattern on each call

### Changed

- Context and timeout propagation throughout the handler chain — all I/O respects the request deadline
- Systemd unit file consolidated to a single source of truth

### Internal

- MCP handler test suite covering all 6 tool handlers

---

## [0.1.0] — 2026-05-24

First Go release — rewrite of the original C++ implementation.

### Added

- MCP server exposing local log files over HTTPS + Bearer Token
- Read-only access — AI clients cannot modify, delete, or execute anything
- Whitelist/blacklist glob patterns for fine-grained path control
- systemd journal as virtual log source (`journald://`, `journald://unit.service`)
- Multi-token auth with per-client revocation (`logmcp token add/remove/renew`)
- TLS modes: self-signed, custom cert, or off (behind reverse proxy)
- Audit trail via syslog on every MCP tool call
- Interactive setup wizard (`logmcp setup`)
- Caddy reverse proxy integration with auto-generated Caddyfile snippet
- Client config snippets for Claude Code, VS Code, and Claude Desktop
- Systemd service management (`logmcp service install/remove/status`)
- Environment variable substitution in `config.yaml` (`${VAR}`)
- `logmcp check` for pre-flight configuration validation
- Debian package (`.deb`) with `postinst`/`prerm`/`postrm` lifecycle scripts
- Ansible role for fleet deployment
