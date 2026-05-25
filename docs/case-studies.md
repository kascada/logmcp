# LogMCP — Case Studies

Real-world scenarios that show why read-only log access from an AI makes a tangible difference.

---

## Asterisk / VoIP Server — Debugging Package Compilation and Dependency Errors

### Why is this difficult?

Asterisk and related VoIP packages (DAHDI, PJSIP, codec_g729, etc.) are often not available pre-built in standard Debian repositories. New versions must be compiled from source — with custom `./configure` flags, kernel headers, and a long tail of C compilation errors spread across thousands of lines of build output.

A typical situation: the system pulls a new package, a module can no longer be loaded, the service won't start. The root cause is somewhere in `make` output, `dmesg`, `asterisk -rvvv`, or `journalctl` — but who has time to read all of that?

### How does LogMCP help?

LogMCP gives the AI assistant read access to:

- `/var/log/asterisk/full` — full Asterisk log
- `journald://` — systemd journal for service startup and kernel events
- Build logs (e.g. `/var/log/asterisk-build.log`), if whitelisted

**Typical workflow:**

> **Me:** The Asterisk service won't start after the last package update. Can you take a look at the logs?
>
> **AI:** Let me check the journal and the Asterisk log.
> *(reads `journald://` and `/var/log/asterisk/full`)*
>
> **AI:** In the journal I see `DAHDI: Failed to open /dev/dahdi/ctl — No such file or directory`. The `dahdi` kernel module is not loaded. At the same time, the Asterisk log shows `chan_dahdi.so: undefined symbol: dahdi_get_version` — this suggests that `chan_dahdi` was compiled against a different version of DAHDI than the one currently running.
>
> **Me:** What should I do?
>
> **AI:** First check which DAHDI version is installed (`dpkg -l | grep dahdi`) and which one is running in the kernel (`modinfo dahdi`). If the versions differ, `chan_dahdi` needs to be recompiled. I can walk you through the process.

### The advantage

The AI has no `sudo`, no shell access, no write permissions. It only reads — and finds the problem in seconds that an experienced admin might have spent minutes or hours on. The admin then carries out the fix themselves, informed by the diagnosis.

---

## Web Server with Caddy Reverse Proxy — Production Setup

### Why this approach?

For production setups with a public domain name, Caddy handles TLS termination (Let's Encrypt automatically) and forwards requests internally to LogMCP. LogMCP runs without its own TLS, only on `127.0.0.1`.

This is the recommended approach when:
- You have a public domain (e.g. `logs.example.com`)
- You want Let's Encrypt certificates without managing them yourself
- Multiple services run behind the same Caddy instance

### Wie sieht das aus?

**Caddyfile:**

```caddyfile
logs.example.com {
    reverse_proxy /logmcp/* localhost:7788
}
```

LogMCP generates the snippet for you:

```sh
logmcp service caddy-snippet
```

**LogMCP config (`/etc/logmcp/config.yaml`):**

```yaml
server:
  host: 127.0.0.1
  port: 7788
  tls:
    mode: off          # TLS is handled by Caddy

auth:
  tokens:
    - name: claude
      token: ${LOGMCP_TOKEN}
      scopes: [read]
```

**Client (Claude Code `~/.claude/mcp.json`):**

```json
{
  "mcpServers": {
    "myserver-logs": {
      "type": "http",
      "url": "https://logs.example.com/logmcp/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

### Questions and Answers

**Q: Do I need to restart LogMCP when Caddy restarts?**
No. LogMCP and Caddy are independent systemd services. Caddy is just an upstream proxy.

**Q: Can I change the `/logmcp/` path?**
Yes — in the Caddy config and in LogMCP's `server.prefix` setting. Both must match.

**Q: What happens if the token is leaked?**
`logmcp token renew <name>` — the token is immediately invalidated and a new one is generated. Active connections drop, but the service itself keeps running.

---

## Standalone Operation with Custom TLS

### When does this fit?

When no reverse proxy is available and you want to run LogMCP directly with HTTPS — e.g. on a homelab server without a public domain, or as a quick setup without a Caddy dependency.

LogMCP can:
- Automatically generate a **self-signed certificate** on first start
- Use a **custom certificate** (manually obtained Let's Encrypt, private CA)

### Setup

The wizard asks for the TLS mode during `logmcp setup`:

```
TLS mode?
  [1] self-signed (generated automatically)
  [2] custom cert + key
  [3] off (behind reverse proxy)
```

With a self-signed cert, LogMCP runs directly on port 7788 (or another port) with HTTPS. The client must set `tlsSkipVerify: true` or import the certificate.

### Questions and Answers

**Q: My AI client reports "certificate verify failed" — what to do?**

Either import the self-signed certificate on the client, or set `tlsSkipVerify: true` in the MCP client configuration. For internal networks this is acceptable; for public endpoints, prefer Caddy + Let's Encrypt.

**Q: Can I run LogMCP without root?**

Yes, if you listen on a port > 1024 (e.g. 7788) and the config file is in the home directory:

```sh
logmcp serve --config ~/.logmcp/config.yaml
```

The setup wizard as root is only needed to write to `/etc/logmcp/` and install the systemd service.

**Q: What is the default port?**

`7788`. Can be changed in `config.yaml` under `server.port`.

---

*More scenarios planned: database logs, multi-server setup, CI/CD integration.*
