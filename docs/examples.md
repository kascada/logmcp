# LogMCP — Extension Examples

Three concrete setups that show how clitool extensions, authentication delegation, RPC transport, and macros work together in a real environment.

The examples use **Switchboard** — a VoIP management system with its own CLI (`switchboard mcp`) — as the common thread. Each example builds on the previous one.

---

## 1 — One CLI, Two Roles: Extension + Authenticator

### The situation

You run Switchboard on a server and want to give your AI assistant two things at once: read access to the log files, and access to Switchboard's own MCP tools (status, routing, call diagnostics). Switchboard already has its own token database. You don't want to manage a second set of tokens just for LogMCP.

The solution: `switchboard mcp` acts as both the **clitool extension** (tools for the AI) and the **authenticator** (token verification for LogMCP). One CLI, one token system, one configuration.

### Setup

```yaml
# /etc/logmcp/config.yaml

auth:
  authenticator:
    command: switchboard mcp   # LogMCP delegates all token checks here

extensions:
  clitool:
    - name: switchboard
      command: switchboard mcp  # same binary, registered as an extension
```

That's it. There is no `auth.tokens` list — LogMCP hands every incoming token to `switchboard mcp verify` and trusts the result.

### How token verification works

When the AI client connects with a Bearer token, LogMCP calls:

```sh
echo "<token>" | switchboard mcp verify
```

Switchboard checks its own database and responds:

```json
{
  "authenticated": true,
  "name": "claude-assistant",
  "scopes": ["logmcp:read", "switchboard:read", "switchboard:status"]
}
```

LogMCP reads the `scopes` field. A token without `logmcp:read` is rejected before it can do anything. A token without `switchboard:read` can still read logs — it just can't call Switchboard tools.

### In practice

The AI assistant sees a unified tool list:

```
read_log              — built-in: read a whitelisted log file
search_log            — built-in: search a log file
switchboard_status    — extension: ARI, storage, and system status
switchboard_routing_get_plan  — extension: read a routing plan by name
```

A debugging session might look like this:

> **Me:** A customer reports choppy audio on calls to the 0800 number. Can you check what's going on?
>
> **AI:** I'll start with the current system status and then look at the Asterisk log.
> *(calls `switchboard_status`)*
>
> **AI:** ARI is connected, storage is fine. But I see the CPU load is at 94% — that's unusual. Let me check the Asterisk log for codec negotiation errors.
> *(calls `read_log` with `/var/log/asterisk/full`, tail)*
>
> **AI:** Found it. There are hundreds of `codec_g729: license limit reached` messages in the last two minutes. The G.729 codec is hitting its seat limit and falling back to G.711 — that causes the choppy audio.

One token, one connection, full picture. The admin only needs to increase the G.729 license count and reload Asterisk.

---

## 2 — RPC Transport: No Cold Starts on the Hot Path

### The situation

The setup above works well — but when the AI calls `switchboard_status` or `switchboard_routing_get_plan`, LogMCP starts a new subprocess each time: Python interpreter, imports, database connection. On a busy server under load that adds 150–200 ms per call. During an active debugging session with dozens of tool calls, this adds up.

With RPC transport, LogMCP sends requests directly to the already-running Switchboard worker process via Redis — no subprocess overhead. The round-trip drops to under 10 ms.

### What changes — and what doesn't

RPC only replaces the **`call` path** — the hot path, once per AI request.

| Operation | Transport | Reason |
|---|---|---|
| `list` (at startup) | CLI subprocess | Runs once; overhead irrelevant |
| `verify` (per request, cached 10 min) | CLI subprocess | Cached; cold start amortized |
| `call` (per tool invocation) | **Redis RPC** | Hot path; called repeatedly |

### Setup

Add `redis_addr` to the extension config:

```yaml
extensions:
  clitool:
    - name: switchboard
      command: switchboard mcp
      redis_addr: 127.0.0.1:6379   # enables RPC for call
```

No changes to the auth config, no changes to the Switchboard side — as long as Switchboard's worker already listens on the same Redis queue (`sb:rpc:req`).

### How a call flows

```
AI client
  │
  └─► LogMCP (validates token via cache)
         │
         └─► Redis LPUSH sb:rpc:req
                  │
                  └─► Switchboard worker (BRPOP, already running)
                           ├─ checks expires_at
                           ├─ executes tool
                           └─► Redis LPUSH sb:rpc:reply:<uuid>
         │
         └─► LogMCP (BLPOP reply, 5 s timeout)
  │
  └─► AI client receives result
```

If the worker is not running, the BLPOP times out after 5 seconds and the AI receives `"service unavailable"` — no silent hangs.

### Failure behavior

A worker restart drains the queue safely: old requests are popped one by one and silently discarded because their `expires_at` timestamp has passed. No backlog, no phantom replies.

---

## 3 — Macros: One Question, One Tool Call

### The situation

Every call investigation starts the same way. The AI asks for the Asterisk log — one tool call. Then it asks for the Switchboard journal for the same time window — a second tool call. Two round-trips, two separate results, twice the context tokens. And the AI has to mentally correlate timestamps between the two.

A macro bundles both into a single call. The AI gets a combined snapshot in one response.

### The macro file

```yaml
# /etc/logmcp/macros/call_snapshot.yaml

name: call_snapshot
description: |
  Combined diagnostic snapshot for a Switchboard instance.
  Returns the last 300 lines of the Asterisk log and the last
  60 seconds of the Switchboard systemd journal in a single call.
  Use this as the first step in any call quality investigation.

steps:
  - internal: read_file
    id: asterisk_log
    args:
      path: /var/log/asterisk/full
      tail: true
      lines: 300

  - internal: journalctl
    id: switchboard_journal
    args:
      unit: switchboard
      window_s: 60
```

After `sudo systemctl restart logmcp`, the tool appears in the list:

```
call_snapshot  — Combined diagnostic snapshot for a Switchboard instance.
```

### In practice

> **Me:** Customer on extension 210 reports calls dropping after about 30 seconds.
>
> **AI:** Let me pull the combined snapshot first.
> *(calls `call_snapshot` — one tool call)*
>
> **AI:** In the Asterisk log I see `res_pjsip: Session timer expired for dialog` recurring for extension 210, always at the 30-second mark. In the Switchboard journal at the same timestamps, I see `keepalive timeout: no response from 210.local`. The phone isn't responding to SIP session timer refreshes.
>
> **Me:** What's the fix?
>
> **AI:** Either increase `session-timers-min-se` in the PJSIP config on the Asterisk side, or check whether the phone's firmware has a broken session timer implementation and disable session timers for that endpoint.

Two data sources, one round-trip, timestamps already aligned.

### A parameterised variant

For teams that run multiple services, a generalised version is more flexible:

```yaml
# /etc/logmcp/macros/service_snapshot.yaml

name: service_snapshot
description: |
  Journal snapshot for any named systemd service.
  Returns the last N seconds of journal output for the given unit.

parameters:
  unit:
    type: string
    description: "systemd unit name, e.g. nginx or asterisk"
  window:
    type: string
    optional: true
    description: "Time window in seconds (default: 60)"

steps:
  - internal: journalctl
    id: journal
    args:
      unit: "{{ unit }}"
      window_s: "{{ window }}"
```

The AI can now call `service_snapshot` with `unit: nginx` or `unit: asterisk` — no new macro needed for each service.

### When macros pay off most

Macros are worth writing when a pattern repeats across sessions: the same combination of log file + journal, the same "start here" diagnostic, the same first question for a known class of incidents. Write it once, name it well, and the AI will use it unprompted.

---

## 4 — MySQL: Database Access

### The situation

A web application stores its data in MySQL. When diagnosing issues — slow queries, missing records, unexpected state — the AI needs to inspect the schema and query the database directly. Rather than copy-pasting table definitions into the conversation, LogMCP exposes the schema as an MCP resource and executes queries on demand.

### Setup

```yaml
# /etc/logmcp/config.yaml

databases:
  - name: app
    dsn: "readonly_user:$APP_DB_PASSWORD@tcp(127.0.0.1:3306)/appdb"
```

Two environment variables are expanded at load time. The read-only database user limits blast radius — `db_query` can execute any SQL, but write operations will be rejected by the database itself.

After `sudo systemctl restart logmcp`, three tools and one resource template appear:

```
db_list    — list non-system databases on a connection
db_schema  — schema for a connection (databases → tables → columns)
db_query   — execute SQL and return columns + rows as JSON
```

And the resource `logmcp://db/app/schema` is available for AI clients that fetch resources before starting a session.

### How schema and queries work together

The AI can pull the schema once at the start of a session, then query freely without re-fetching it:

> **Me:** Users are reporting that their session expires immediately after login. Can you check the sessions table?
>
> **AI:** Let me look at the schema first.
> *(reads `logmcp://db/app/schema` as a resource — one round-trip, full schema in context)*
>
> **AI:** The sessions table has `user_id`, `token`, `created_at`, and `expires_at`. Let me check recent entries.
> *(calls `db_query` with `SELECT user_id, created_at, expires_at FROM sessions ORDER BY created_at DESC LIMIT 10`)*
>
> **AI:** I see `expires_at` is being set to the same value as `created_at` for all sessions created in the last hour. The session lifetime is effectively zero. This looks like a bug introduced in the last deployment — the `expires_at` calculation is probably using the wrong timezone or missing the TTL offset.

One schema fetch, one query, root cause identified. The AI did not need to guess column names or run exploratory queries to discover the structure.

### Cache behaviour

Schema results are cached in process memory for 5 minutes. If the schema changes (new column added, table renamed), call `db_schema` with `refresh:true` to force a reload:

```
db_schema name=app refresh=true
```

The `logmcp://db/app/schema` resource serves from the same cache — no extra round-trip to the database.

---

## Putting it together

These three extensions compose cleanly:

1. **CLITOOL + Auth** — `switchboard mcp` manages tokens and provides tools. One config entry covers both.
2. **RPC** — add `redis_addr` to make tool calls fast. No changes to auth or to Switchboard itself.
3. **Macros** — define `call_snapshot.yaml` once. The AI uses it as the natural starting point for every call investigation.

The result is an AI assistant that authenticates against your existing system, calls tools with minimal latency, and opens every investigation with a focused, combined data pull — without any custom server code.
