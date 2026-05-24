# Changelog

All notable changes to LogMCP will be documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

---

## [0.1.0] — 2026-05-24

Initial release.

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
