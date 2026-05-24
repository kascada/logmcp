package config

import (
	"os"
	"strings"
)

// sectionParams describes optional YAML parameters grouped by their section.
// Parameters listed here are appended (commented out) to the config file when
// they are absent — neither active nor already commented.
var knownOptionalSections = []struct {
	sectionKey string // e.g. "logs:"
	params     []struct {
		key  string // e.g. "journald:"
		line string // full commented YAML line, indented
	}
}{
	{
		sectionKey: "logs:",
		params: []struct {
			key  string
			line string
		}{
			{"blacklist:", "#   blacklist: []          # Pfade/Globs die explizit gesperrt sind"},
			{"journald:", "#   journald: false        # systemd-Journal als virtuelle Log-Quelle (journald://)"},
		},
	},
	{
		sectionKey: "audit:",
		params: []struct {
			key  string
			line string
		}{
			{"syslog:", "#   syslog: true           # Audit-Events ins System-Syslog schreiben"},
		},
	},
	{
		sectionKey: "proxy:",
		params: []struct {
			key  string
			line string
		}{
			{"trusted_proxy:", "#   trusted_proxy: false   # X-Forwarded-For des Reverse-Proxy vertrauen"},
			{"path_prefix:", "#   path_prefix: \"\"        # Subpfad hinter dem Reverse-Proxy (z.B. /logmcp)"},
			{"caddy:", "#   caddy: true             # Caddy-Snippet beim Setup anzeigen"},
		},
	},
	{
		sectionKey: "tools:",
		params: []struct {
			key  string
			line string
		}{
			{"disabled:", "#   disabled: []          # Tools deaktivieren: list_logs, read_log, search_log, log_info, check_environment"},
		},
	},
	{
		sectionKey: "server:",
		params: []struct {
			key  string
			line string
		}{
			{"host:", "#   host: 0.0.0.0           # Bind-Adresse"},
			{"port:", "#   port: 7788              # Port"},
		},
	},
}

// BackfillComments reads the config file at path and appends any known optional
// parameters that are completely absent (neither active nor commented out) as
// commented-out YAML lines. Errors are silently ignored — backfilling is
// best-effort and must not block the server from starting.
func BackfillComments(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)

	var sb strings.Builder

	for _, sec := range knownOptionalSections {
		var missingLines []string
		for _, p := range sec.params {
			// Present if the bare key appears anywhere (active or commented).
			if strings.Contains(content, p.key) {
				continue
			}
			missingLines = append(missingLines, p.line)
		}
		if len(missingLines) == 0 {
			continue
		}
		// Write a commented section header + missing params.
		sb.WriteString("# " + sec.sectionKey + "\n")
		for _, l := range missingLines {
			sb.WriteString(l + "\n")
		}
	}

	if sb.Len() == 0 {
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer f.Close()

	_, _ = f.WriteString("\n# --- weitere verfügbare Parameter ---\n")
	_, _ = f.WriteString(sb.String())
}
