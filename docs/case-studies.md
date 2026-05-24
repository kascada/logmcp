# LogMCP — Case Studies

Real-world scenarios that show why read-only log access from an AI makes a tangible difference.

---

## Asterisk / VoIP Server — Paket-Kompilierung und Abhängigkeitsfehler debuggen

### Warum ist das schwierig?

Asterisk und verwandte VoIP-Pakete (DAHDI, PJSIP, codec_g729 o.ä.) liegen oft nicht fertig in den Debian-Standardrepositorien. Neue Versionen müssen aus dem Quellcode kompiliert werden — mit eigenen `./configure`-Flags, Kernel-Headern, und einem langen Tail an C-Kompilierungsfehlern, die sich über tausende Zeilen Build-Output verteilen.

Klassische Situation: Das System zieht ein neues Paket, ein Modul lässt sich nicht mehr laden, der Dienst startet nicht. Die Fehlerursache liegt irgendwo in `make` output, `dmesg`, `asterisk -rvvv` oder `journalctl` — aber wer soll das alles durchlesen?

### Wie hilft LogMCP?

LogMCP gibt dem AI-Assistenten Lesezugriff auf:

- `/var/log/asterisk/full` — vollständiges Asterisk-Log
- `journald://` — systemd-Journal für Service-Start und Kernel-Events
- Build-Logs (z.B. `/var/log/asterisk-build.log`), sofern im Whitelist

**Typischer Ablauf:**

> **Ich:** Der Asterisk-Service startet nicht nach dem letzten Paket-Update. Kannst du dir die Logs ansehen?
>
> **KI:** Ich schaue mir das Journal und das Asterisk-Log an.
> *(liest `journald://` und `/var/log/asterisk/full`)*
>
> **KI:** Im Journal sehe ich `DAHDI: Failed to open /dev/dahdi/ctl — No such file or directory`. Das Kernel-Modul `dahdi` ist nicht geladen. Gleichzeitig zeigt das Asterisk-Log `chan_dahdi.so: undefined symbol: dahdi_get_version` — das deutet darauf hin, dass `chan_dahdi` gegen eine andere DAHDI-Version kompiliert wurde als die aktuell laufende.
>
> **Ich:** Was soll ich tun?
>
> **KI:** Prüf zuerst, welche DAHDI-Version installiert ist (`dpkg -l | grep dahdi`) und welche im Kernel läuft (`modinfo dahdi`). Wenn die Versionen abweichen, muss `chan_dahdi` neu kompiliert werden. Ich kann dich durch den Prozess führen.

### Der Vorteil

Die KI hat keinen `sudo`, kein Shell-Zugang, keine Schreibrechte. Sie liest nur — und findet das Problem in Sekunden, für das ein erfahrener Admin Minuten oder Stunden gebraucht hätte. Der Admin führt die Korrektur dann selbst aus, informiert durch die Diagnose.

---

## Webserver mit Caddy Reverse Proxy — produktive Installation

### Warum diese Variante?

Für produktive Setups mit einem öffentlichen Domainnamen übernimmt Caddy das TLS-Termination (Let's Encrypt automatisch) und leitet Anfragen intern an LogMCP weiter. LogMCP läuft ohne eigenes TLS, nur auf `127.0.0.1`.

Das ist die empfohlene Variante wenn:
- Du eine öffentliche Domain hast (z.B. `logs.example.com`)
- Du Let's Encrypt-Zertifikate willst, ohne sie selbst zu verwalten
- Mehrere Dienste hinter demselben Caddy laufen

### Wie sieht das aus?

**Caddyfile:**

```caddyfile
logs.example.com {
    reverse_proxy /logmcp/* localhost:7788
}
```

LogMCP gibt dir das Snippet fertig aus:

```sh
logmcp service caddy-snippet
```

**LogMCP config (`/etc/logmcp/config.yaml`):**

```yaml
server:
  host: 127.0.0.1
  port: 7788
  tls:
    mode: off          # TLS macht Caddy

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

### Fragen und Antworten

**F: Muss ich LogMCP neu starten, wenn Caddy neu startet?**
Nein. LogMCP und Caddy sind unabhängige systemd-Services. Caddy ist nur ein vorgelagerter Proxy.

**F: Kann ich den Pfad `/logmcp/` ändern?**
Ja — in der Caddy-Config und in der `server.prefix`-Einstellung von LogMCP. Beides muss übereinstimmen.

**F: Was passiert, wenn der Token geleakt wird?**
`logmcp token renew <name>` — der Token wird sofort ungültig, ein neuer wird generiert. Laufende Verbindungen fallen ab, der Service selbst läuft weiter.

---

## Standalone-Betrieb mit eigenem TLS

### Wann passt das?

Wenn kein Reverse Proxy vorhanden ist und du LogMCP direkt mit HTTPS betreiben willst — z.B. auf einem Homelab-Server ohne öffentliche Domain, oder als schnelles Setup ohne Caddy-Abhängigkeit.

LogMCP kann:
- Ein **selbstsigniertes Zertifikat** beim ersten Start automatisch generieren
- Ein **eigenes Zertifikat** einbinden (Let's Encrypt manuell, eigene CA)

### Setup

Der Wizard fragt beim `logmcp setup` nach dem TLS-Modus:

```
TLS mode?
  [1] self-signed (generated automatically)
  [2] custom cert + key
  [3] off (behind reverse proxy)
```

Mit selbstsigniertem Cert läuft LogMCP direkt auf Port 7788 (oder einem anderen Port) mit HTTPS. Der Client muss `tlsSkipVerify: true` setzen oder das Zertifikat importieren.

### Fragen und Antworten

**F: Mein AI-Client meldet "certificate verify failed" — was tun?**

Entweder das selbstsignierte Zertifikat auf dem Client importieren, oder in der MCP-Client-Konfiguration `tlsSkipVerify: true` setzen. Für interne Netze ist das akzeptabel; für öffentliche Endpunkte lieber Caddy + Let's Encrypt.

**F: Kann ich LogMCP auch ohne root betreiben?**

Ja, wenn du auf einem Port > 1024 lauschst (z.B. 7788) und die Config-Datei im Home-Verzeichnis liegt:

```sh
logmcp serve --config ~/.logmcp/config.yaml
```

Setup-Wizard als root ist nur nötig, um in `/etc/logmcp/` zu schreiben und den systemd-Service zu installieren.

**F: Welcher Port ist Standard?**

`7788`. Kann in `config.yaml` unter `server.port` geändert werden.

---

*Weitere Szenarien geplant: Datenbank-Logs, Multi-Server-Setup, CI/CD-Integration.*
