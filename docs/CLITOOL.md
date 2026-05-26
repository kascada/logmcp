# clitool — MCP-Tool-Interface für CLI-Programme

`clitool` ist eine Konvention für CLI-Programme, die als Backend für MCP-Server dienen.
Das Programm stellt seine Funktionen als typisierte Tools mit Beschreibung und JSON-Schema bereit.
Ein MCP-Server ruft das CLI auf — er enthält selbst keine Logik.

## Konzept

```
MCP-Client  ──►  MCP-Server (thin)  ──►  clitool-CLI  ──►  Programm-Logik
```

Der MCP-Server macht beim Start `<cmd> list`, registriert die Tools und leitet dann jeden
Tool-Call per `<cmd> call <tool> ...` weiter. Der Server ist austauschbar — die Logik bleibt im CLI.

`list` braucht kein Token — die Tool-Liste ist öffentlich. Der MCP-Server stellt aber sicher,
dass ein gültiges Token vorhanden ist, bevor er die Liste an einen Client ausliefert. Auth findet
also auf MCP-Ebene statt, nicht im CLI.

## Authenticator

Ein clitool-Programm kann als **Authenticator** für LogMCP dienen. Ist in der Config
`auth.authenticator` gesetzt, ruft LogMCP bei jedem eingehenden Request `<cmd> verify` auf
und verwendet das Ergebnis zur Authentifizierung — die interne `auth.tokens`-Liste wird dann
komplett ignoriert.

Ist kein Authenticator konfiguriert, greift die interne Token-Tabelle wie gewohnt.

```yaml
auth:
  authenticator:
    command: switchboard mcp
```

Der Authenticator ist unabhängig von den Extensions unter `extensions.clitool`. Es kann
dasselbe CLI-Programm sein (wie im Beispiel oben) oder ein völlig anderes.

## Prefix

Jede Extension wird in der MCP-Server-Config mit einem **Namen** registriert. Dieser Name wird
als Prefix vor alle Tool-Namen gestellt (mit `_` als Trenner). Aus `status` wird damit z.B.
`switchboard_status`, aus `routing_list_plans` wird `switchboard_routing_list_plans`.

Der Prefix sorgt dafür, dass Tools verschiedener Extensions sich nicht überschneiden. Das CLI
selbst kennt keinen Prefix — es liefert und empfängt Tool-Namen ohne Prefix. Der MCP-Server
fügt ihn beim Registrieren hinzu und entfernt ihn wieder vor dem Weiterleiten an das CLI.

Beispiel-Config:

```yaml
extensions:
  clitool:
    - name: switchboard
      command: switchboard mcp
    - name: asterisk
      command: /usr/local/bin/asterisk-ctl
```

## Drei Befehle

### `<cmd> list`

Gibt alle verfügbaren Tools als JSON-Array aus. Kein Token nötig — die Liste ist vollständig
und unabhängig von Scopes. Scope-Prüfung findet erst beim `call` statt.

```bash
<cmd> list
```

Antwort (stdout, exit 0):

```json
[
  {
    "name": "status",
    "description": "ARI-, Storage- und System-Status dieses Servers.",
    "inputSchema": {
      "type": "object",
      "properties": {}
    },
    "requiredScope": "status:read"
  },
  {
    "name": "routing_get_plan",
    "description": "Liest alle Felder eines Routing-Plans anhand seines Namens.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "name": {
          "type": "string",
          "description": "Name des Routing-Plans"
        }
      },
      "required": ["name"]
    },
    "requiredScope": "config:read"
  }
]
```

Felder je Tool:

| Feld | Typ | Beschreibung |
|---|---|---|
| `name` | string | Tool-Name, nur `[a-z0-9_]`, eindeutig |
| `description` | string | Kurzbeschreibung für den MCP-Client |
| `inputSchema` | object | JSON Schema der Parameter |
| `requiredScope` | string | Scope der geprüft wird (z.B. `status:read`) |

### `<cmd> verify`

Prüft ein Bearer-Token und gibt Name und Scopes zurück. Wird vom LogMCP-Authenticator bei
jedem Request aufgerufen. Nur relevant wenn das Programm als `auth.authenticator` konfiguriert
ist — Extensions unter `extensions.clitool` müssen `verify` nicht implementieren.

Das Token wird via stdin übergeben (kein CLI-Argument — wäre in `ps` sichtbar).

```bash
echo "<token>" | <cmd> verify
```

Antwort bei gültigem Token (stdout, exit 0):

```json
{
  "authenticated": true,
  "name": "admin",
  "scopes": ["status:read", "config:read", "logmcp:read"]
}
```

Antwort bei ungültigem Token (stdout, exit 0):

```json
{
  "authenticated": false
}
```

Exit 0 in beiden Fällen — exit 1 nur bei Programmfehler (z.B. Datenbankfehler).

**LogMCP-Scope:** Damit ein Token Zugriff auf LogMCP-Tools erhält, muss der `verify`-Aufruf
mindestens den Scope `logmcp:read` zurückgeben. Tokens ohne diesen Scope werden abgewiesen.

### `<cmd> call <tool-name>`

Führt ein Tool aus. Erwartet ein Token (für Auth + Scope) und optionale Parameter als JSON.
Das Token wird via stdin übergeben.

```bash
# Token via stdin
echo "<token>" | <cmd> call status --token-stdin

# Mit Parametern
echo "<token>" | <cmd> call routing_get_plan --token-stdin --params '{"name": "default"}'

# Parameter aus Datei
echo "<token>" | <cmd> call routing_get_plan --token-stdin --params @params.json
```

## Auth-Flow

Der erste Schritt bei jedem `call` ist die Token-Prüfung — noch vor der Ausführung.

1. Token aus `--token-stdin` lesen
2. Token gegen die Client-Datenbank prüfen (z.B. `clients.yaml`)
3. Scope des Tokens gegen `requiredScope` des Tools prüfen
4. Nur bei Erfolg: Tool ausführen

## Antwort-Format

Alle Antworten gehen auf stdout als JSON. Exit-Code signalisiert Erfolg oder Fehler.

### Erfolg (exit 0)

```json
{
  "ok": true,
  "result": {}
}
```

`result` enthält die Tool-Antwort — ein beliebiges JSON-Objekt, Array oder primitiver Wert.

### Fehler (exit 1)

```json
{
  "ok": false,
  "error": "Beschreibung des Fehlers",
  "code": "error_code"
}
```

Definierte Fehlercodes:

| Code | Bedeutung |
|---|---|
| `auth_failed` | Token ungültig oder nicht gefunden |
| `scope_denied` | Token gültig, aber fehlender Scope |
| `tool_not_found` | Tool-Name existiert nicht |
| `invalid_params` | Parameter entsprechen nicht dem Schema |
| `execution_error` | Tool-Ausführung fehlgeschlagen |

## Vollständiges Beispiel

```bash
# Tools entdecken
$ switchboard mcp list
[
  { "name": "status", "description": "...", "inputSchema": {...}, "requiredScope": "status:read" },
  { "name": "routing_get_plan", "description": "...", "inputSchema": {...}, "requiredScope": "config:read" }
]

# Token verifizieren
$ echo "validtoken" | switchboard mcp verify
{"authenticated": true, "name": "admin", "scopes": ["status:read", "config:read", "logmcp:read"]}

# Token verifizieren — ungültig
$ echo "wrongtoken" | switchboard mcp verify
{"authenticated": false}

# Tool aufrufen — Auth-Fehler
$ echo "wrongtoken" | switchboard mcp call status --token-stdin
{"ok": false, "error": "Token nicht gefunden", "code": "auth_failed"}
# exit 1

# Tool aufrufen — fehlender Scope
$ echo "readonlytoken" | switchboard mcp call status --token-stdin
{"ok": false, "error": "Scope 'status:read' fehlt", "code": "scope_denied"}
# exit 1

# Tool aufrufen — Erfolg
$ echo "validtoken" | switchboard mcp call status --token-stdin
{"ok": true, "result": {"ari": {"ok": true}, "db": {"ok": true}}}
# exit 0
```

## Implementierungshinweise

- `list` braucht kein Token und gibt immer exit 0 zurück
- `verify` liest Token von stdin (erste Zeile, Zeilenumbruch wird abgeschnitten); gibt immer exit 0 zurück außer bei Programmfehler
- `call` liest Token von stdin via `--token-stdin`
- Fehlermeldungen gehen auf stdout (als JSON), nicht auf stderr — damit der MCP-Server sie parsen kann
- Der MCP-Server übergibt das Token bei `call` transparent: er prüft Scope nicht selbst
- Tool-Namen: nur `[a-z][a-z0-9_]*`, Gruppen mit Unterstrich (`routing_get_plan`)
