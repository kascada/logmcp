# Redis RPC — Protokoll für Co-located Extensions

Alternatives Transport-Protokoll für clitool-Extensions, die auf demselben Host laufen.
Ersetzt den per-Call-Subprocess durch einen Redis-basierten Request/Response-Kanal.

**Auth:** Token-Name und Scopes werden aus dem bereits aufgelösten MCP-Token-Context
entnommen und direkt als `caller`-Feld ins RPC-JSON eingebettet — kein separater
`verify`-Aufruf nötig.

## Scope: nur `call`

RPC ersetzt ausschließlich den `call`-Pfad (den heißen Pfad, pro Request).

- **`list`** bleibt CLI — läuft einmalig beim logmcp-Start; Subprocess-Overhead ist dort irrelevant.
- **`verify`** bleibt CLI — wird vom Authenticator-Middleware verwendet; Ergebnis wird 10 Minuten gecacht.

## Warum RPC statt CLI

`<command> call` startet einen neuen Prozess pro Aufruf (inkl. Interpreter-Start, Imports,
ggf. Datenbankverbindung). Beim RPC-Kanal wird der bereits laufende Worker direkt
angesprochen — kein Prozess-Overhead.

## Key-Schema

| Key | Typ | Beschreibung |
|---|---|---|
| `sb:rpc:req` | List | Request-Queue (LPUSH → BRPOP) |
| `sb:rpc:reply:<uuid>` | List | Antwort-Kanal (temporär, EXPIRE 30 s) |

## Request-Format

```json
{
  "tool": "myapp_status",
  "params": {},
  "caller": { "name": "logmcp", "scopes": ["myapp:read"] },
  "reply_key": "sb:rpc:reply:550e8400-e29b-41d4-a716",
  "expires_at": 1716900000.5
}
```

| Feld | Typ | Beschreibung |
|---|---|---|
| `tool` | string | Tool-Name (identisch zu `call`) |
| `params` | object | Tool-Parameter (kann `{}` sein) |
| `caller` | object | Aus dem MCP-Token-Context aufgelöst — wird nicht erneut gegen DB geprüft |
| `reply_key` | string | `sb:rpc:reply:<uuid>` — UUID vom Sender generiert |
| `expires_at` | float | Unix-Timestamp — Worker verwirft Requests nach diesem Zeitpunkt |

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
Sender (logmcp)                         Worker
  │                                        │
  ├─ LPUSH sb:rpc:req  <request-json>      │
  │                                        │
  ├─ BLPOP sb:rpc:reply:<uuid>  [timeout]  │
  │    wartet ...                          ├─ BRPOP sb:rpc:req
  │                                        ├─ expires_at prüfen → ggf. verwerfen
  │                                        ├─ Tool ausführen
  │                                        ├─ LPUSH sb:rpc:reply:<uuid>  <response>
  │                                        └─ EXPIRE sb:rpc:reply:<uuid>  30
  │
  ├─ Antwort empfangen
  └─ Bei BLPOP-Timeout: Fehler "service unavailable" zurückgeben
```

## TTL-Verhalten

- **Request-Expiry:** Sender setzt `expires_at = jetzt + 5 s`. Worker prüft nach dem BRPOP — abgelaufene Requests werden still verworfen (kein Reply).
- **Reply-Expiry:** Worker setzt EXPIRE 30 s auf den Reply-Key. Falls Sender bereits aufgegeben hat, räumt Redis die Antwort selbst weg.
- **Sender-Timeout:** BLPOP mit 5 s Timeout. Bei Ablauf → Fehler an den Aufrufer, kein Ansammeln im Reply-Key.

**Verhalten bei Worker-Neustart:** Alte Requests werden der Reihe nach gepoppt und wegen abgelaufener `expires_at` verworfen. Kein Ansammeln.

## Verfügbare Tools

Dieselben Tools wie beim CLI-`call` — einsehbar mit `<command> list`.

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
