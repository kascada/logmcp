# LogMCP — Logging Reference

LogMCP schreibt alle sicherheitsrelevanten Ereignisse über syslog (Facility `daemon`, Tag `logmcp`). Auf Systemen mit systemd landen die Einträge im Journal:

```bash
journalctl -t logmcp
grep logmcp /var/log/syslog
```

---

## Ereignisse

### Auth-Fehler

Jeder abgewiesene HTTP-Request wegen fehlender oder ungültiger Authentifizierung:

```
logmcp[PID]: auth_failed client=<IP> reason=<Grund>
```

| `reason` | Bedeutung |
|---|---|
| `missing_header` | Kein `Authorization`-Header |
| `bad_scheme` | Header nicht im `Bearer`-Schema |
| `invalid_token` | Token unbekannt |

Diese Einträge sind der Trigger für fail2ban. Das Suchmuster ist auf genau dieses Format abgestimmt — Änderungen am Format erfordern ein Update der fail2ban-Filter-Datei.

### Zugriff verweigert (Whitelist)

Request mit gültigem Token auf einen Pfad außerhalb der Whitelist:

```
logmcp[PID]: denied path=<Pfad> client=<IP> reason=not_in_whitelist
```

### Erfolgreicher Tool-Aufruf

```
logmcp[PID]: access tool=<Tool> path=<Pfad> client=<IP>
logmcp[PID]: access tool=search_log path=<Pfad> pattern=<redacted> client=<IP>
```

Mögliche `tool`-Werte: `list_logs`, `read_log`, `search_log`, `log_info`, `check_environment`.
Das Suchmuster bei `search_log` wird nicht geloggt.

---

## In-Process Rate Limiting

Unabhängig von fail2ban begrenzt LogMCP Auth-Fehler pro IP intern auf zwei Stufen. Sobald eine IP ein Limit überschreitet, antwortet der Server sofort mit `429 Too Many Requests` — ohne den Token zu prüfen und ohne weiteren Syslog-Eintrag.

**Burst-Stufe** — reagiert schnell auf kurze Angriffsbursts (kleines Zeitfenster, höherer Threshold).  
**Sustained-Stufe** — sperrt IPs bei anhaltenden, langsamen Versuchen (großes Zeitfenster).

Fehler werden in beide aktiven Stufen eingetragen. Jede Stufe sperrt unabhängig.

Konfiguration in `/etc/logmcp/config.yaml`:

```yaml
security:
  rate_limit:
    burst:
      max_failures: 20       # höherer Threshold — MCP-Einrichtung kostet Versuche
      window_seconds: 30
    sustained:
      max_failures: 50
      window_seconds: 600    # 10 Minuten
```

Ohne `rate_limit`-Block ist das Feature deaktiviert. Beim `quickstart`-Modus sind beide Stufen mit Defaults immer aktiv.

---

## fail2ban-Integration

LogMCP liefert fertige fail2ban-Konfiguration direkt im Binary mit. Installation:

```bash
sudo logmcp security install-fail2ban --reload
```

Das schreibt:
- `/etc/fail2ban/filter.d/logmcp.conf` — Regex-Filter für Auth-Fehler
- `/etc/fail2ban/jail.d/logmcp.conf` — Jail-Defaults

Der `logmcp setup`-Wizard bietet diesen Schritt interaktiv an.

### Aktivierung steuern

In `/etc/logmcp/config.yaml`:

```yaml
security:
  fail2ban:
    enabled: true          # false = setup und install-fail2ban tun nichts
    # filter_dir: /etc/fail2ban/filter.d   # Standard, selten nötig
    # jail_dir:   /etc/fail2ban/jail.d
```

### Filter-Pattern

```ini
failregex = logmcp\[\d+\]: auth_failed client=<HOST> reason=\S+
```

### Jail-Defaults

| Parameter | Wert | Bedeutung |
|---|---|---|
| `maxretry` | 5 | Fehlversuche vor Ban |
| `findtime` | 60 s | Zeitfenster |
| `bantime` | 3600 s | Ban-Dauer (1 h) |

Werte können in `/etc/fail2ban/jail.d/logmcp.conf` überschrieben werden.

### Wenn fail2ban bereits läuft

`install-fail2ban` schreibt die Dateien unabhängig davon, ob fail2ban läuft oder nicht. Mit `--reload` wird `fail2ban-client reload` aufgerufen, das neue Jails additiv einliest ohne laufende Bans zu unterbrechen.

---

## Log-Rotation

LogMCP schreibt selbst keine Dateien — alle Ausgaben gehen über syslog. Rotation wird durch `rsyslog`/`syslog-ng` oder das systemd-Journal gesteuert.
