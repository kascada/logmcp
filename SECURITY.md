# Security Policy

## Supported Versions

Only the latest release receives security fixes.

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Report privately by emailing: **security.kamran@kleist.de**

Include:
- A description of the vulnerability and its potential impact
- Steps to reproduce or a proof-of-concept
- Your LogMCP version (`logmcp --version`) and OS

You will receive an acknowledgement within 72 hours.
We aim to release a fix within 14 days for critical issues.

## Scope

Areas of particular interest:

- Auth bypass (bearer token validation)
- Path traversal outside whitelisted directories
- Information disclosure via audit log or error messages
- Privilege escalation via the systemd service or Debian package scripts
