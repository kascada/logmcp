# switchboard RPC — Redis-Protokoll

Ersetzt `switchboard mcp call` über einen Redis-basierten Request/Response-Kanal.
Nur für Aufrufe geeignet, die auf demselben Host laufen.

**Auth:** Token-Name und Scopes werden aus dem bereits aufgelösten MCP-Token-Context
entnommen und direkt als `caller`-Feld ins RPC-JSON eingebettet — kein separater
`verify`-Aufruf nötig.

## Scope: nur `mcp call`

RPC ersetzt ausschließlich `switchboard mcp call` (den heißen Pfad, pro Request).

- **`mcp list`** bleibt CLI — läuft einmalig beim logmcp-Start; Subprocess-Overhead ist dort irrelevant.
- **`mcp verify`** bleibt CLI — wird vom Authenticator-Middleware verwendet; Ergebnis wird 10 Minuten gecacht.

## Warum RPC statt CLI

`switchboard mcp call` startet einen neuen Python-Prozess (inkl. Imports, DB-Verbindung).
Beim RPC-Kanal wird der bereits laufende Worker direkt angesprochen — kein Prozess-Overhead.

## Key-Schema

| Key | Typ | Beschreibung |
|---|---|---|
| `sb:rpc:req` | List | Request-Queue (LPUSH → BRPOP) |
| `sb:rpc:reply:<uuid>` | List | Antwort-Kanal (temporär, EXPIRE 30 s) |

## Request-Format

```json
{
  "tool": "switchboard_status",
  "params": {},
  "caller": { "name": "logmcp-dev", "scopes": ["switchboard:read"] },
  "reply_key": "sb:rpc:reply:550e8400-e29b-41d4-a716",
  "expires_at": 1716900000.5
}
```

| Feld | Typ | Beschreibung |
|---|---|---|
| `tool` | string | Tool-Name (identisch zu `mcp call`) |
| `params` | object | Tool-Parameter (kann `{}` sein) |
| `caller` | object | Vorher per `mcp verify` aufgelöst — wird nicht erneut gegen DB geprüft |
| `reply_key` | string | `sb:rpc:reply:<uuid>` — UUID vom Sender generiert |
| `expires_at` | float | Unix-Timestamp — Switchboard verwirft Requests nach diesem Zeitpunkt |

## Response-Format

```json
{ "ok": true, "result": { ... } }
```

```json
{ "ok": false, "error": "Fehlermeldung", "code": "tool_not_found" }
```

Fehlercodes: `tool_not_found`, `scope_denied`, `expired`, `execution_error`

## Ablauf

```
Sender (logmcp)                         Switchboard Worker
  │                                             │
  ├─ LPUSH sb:rpc:req  <request-json>           │
  │                                             │
  ├─ BLPOP sb:rpc:reply:<uuid>  [timeout 5s]   │
  │    wartet ...                               ├─ BRPOP sb:rpc:req
  │                                             ├─ expires_at prüfen → ggf. verwerfen
  │                                             ├─ Tool ausführen
  │                                             ├─ LPUSH sb:rpc:reply:<uuid>  <response>
  │                                             └─ EXPIRE sb:rpc:reply:<uuid>  30
  │
  ├─ Antwort empfangen
  └─ Bei BLPOP-Timeout: Fehler "service unavailable" zurückgeben
```

## TTL-Verhalten

- **Request-Expiry:** Sender setzt `expires_at = jetzt + 5 s`. Switchboard prüft nach dem BRPOP — abgelaufene Requests werden still verworfen (kein Reply).
- **Reply-Expiry:** Switchboard setzt EXPIRE 30 s auf den Reply-Key. Falls Sender bereits aufgegeben hat, räumt Redis die Antwort selbst weg.
- **Sender-Timeout:** BLPOP mit 5 s Timeout. Bei Ablauf → Fehler an den Aufrufer, kein Ansammeln im Reply-Key.

**Verhalten bei Switchboard-Neustart:** Alte Requests werden der Reihe nach gepoppt und wegen abgelaufener `expires_at` verworfen. Kein Ansammeln.

## Verfügbare Tools

Dieselben Tools wie bei `switchboard mcp call` — einsehen mit:

```bash
ssh user@switchbox-dev.gpt4voice.de "switchboard mcp list"
```

## Implementierungshinweise (Go / logmcp)

**Caller-Info-Zusammenstellung:**
- `callerName` wird mit `auth.TokenNameFromCtx(ctx)` aus dem bereits validierten MCP-Token-Context gelesen — immer verfügbar.
- `callerScopes` werden mit `cfg.Auth.Find(callerName)` nachgeschlagen (statische Token-Liste). Für dynamisch authentifizierte Token (Authenticator-Modus) wird ein leeres Slice übergeben.

**Einzelner Aufrufpunkt pro Tool-Invocation:**
```go
rpc.Call(ctx, redisAddr, toolName, callerName, callerScopes, params, timeout)
```

`rpc.Call` übernimmt UUID-Generierung, Serialisierung, LPUSH, BLPOP und Response-Parsing. Rückgabetyp ist `*clitool.CallResult` — identisch zum CLI-Pfad, die nachgelagerte Fehlerbehandlung in `server.go` ist unverändert.

**Redis-Verbindung:** `127.0.0.1:6379`, keine Auth (lokaler Redis). Konfigurierbar per `redis_addr` in der Extension-Config.
