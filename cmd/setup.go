package cmd

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/chzyer/readline"
	"github.com/google/uuid"
	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/security"
	internaltls "github.com/kleist-dev/logmcp/internal/tls"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup wizard",
		Long:  "Guides you through creating /etc/logmcp/config.yaml and optional TLS/systemd setup.",
		RunE:  runSetup,
	}
}

func runSetup(cmd *cobra.Command, args []string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "logmcp setup requires an interactive terminal (stdin is not a TTY)")
		os.Exit(1)
	}

	rl, err := readline.NewEx(&readline.Config{
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return fmt.Errorf("initializing readline: %w", err)
	}
	defer func() { _ = rl.Close() }()

	fmt.Println("=== LogMCP Setup Wizard ===")
	fmt.Println()

	// Load existing config as defaults if present.
	cfg := config.Default()
	if existing, err := config.Load(config.DefaultConfigPath); err == nil {
		cfg = existing
		fmt.Println("Bestehende Konfiguration gefunden — Enter drücken übernimmt den aktuellen Wert.")
		fmt.Println()
	}

	// --- Server name ---
	defaultName := cfg.Name
	if defaultName == "" || defaultName == "logmcp" {
		if hn, err := os.Hostname(); err == nil && hn != "" {
			defaultName = "logmcp-" + hn
		}
	}
	fmt.Println("Server-Name (erscheint als MCP-Server-Name in Claude Code / VS Code):")
	cfg.Name = prompt(rl, "Name", defaultName)

	fmt.Println()

	// --- Modus: HTTP-Server oder stdio ---
	fmt.Println("Modus:")
	fmt.Println("  1) HTTP-Server  (empfohlen — Claude Code / Claude Desktop via HTTPS + Token)")
	fmt.Println("  2) stdio        (lokal, kein Netzwerk — nicht empfohlen, nur für Einzel-Rechner)")
	mode := prompt(rl, "Modus [1/2]", "1")
	if mode == "2" {
		return runSetupStdio(cfg)
	}

	fmt.Println()

	// --- Step 0: System user ---
	logmcpExists, err := setupSystemUser(rl)
	if err != nil {
		return err
	}

	// --- Step 1: Deployment mode ---
	behindProxy, directHost, err := setupDeploymentMode(rl, cfg)
	if err != nil {
		return err
	}

	// --- Step 2: Port ---
	if err := promptPort(rl, cfg); err != nil {
		return err
	}

	// --- Step 3: Bearer tokens ---
	if err := setupInitialToken(rl, cfg); err != nil {
		return err
	}

	// Whitelist/Blacklist werden nicht interaktiv abgefragt — Defaults aus
	// config.Default() greifen für neue Installs; bestehende Configs behalten
	// ihre Werte. Feinjustierung direkt in /etc/logmcp/config.yaml.
	if len(cfg.Logs.Whitelist) == 0 {
		cfg.Logs.Whitelist = config.DefaultWhitelist
	}

	// --- Write config ---
	fmt.Println()
	if err := os.MkdirAll("/etc/logmcp", 0o750); err != nil {
		return fmt.Errorf("creating /etc/logmcp: %w", err)
	}

	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	data := injectSecurityComments(injectWhitelistComments(raw))
	if err := os.WriteFile(config.DefaultConfigPath, data, 0o640); err != nil {
		return fmt.Errorf("writing config to %s: %w", config.DefaultConfigPath, err)
	}
	config.BackfillComments(config.DefaultConfigPath)
	fmt.Printf("Config written to %s\n", config.DefaultConfigPath)

	// --- Generate self-signed cert if needed ---
	if !behindProxy && cfg.Server.TLS.Mode == "self-signed" {
		fmt.Print("Generating self-signed certificate...")
		if err := internaltls.GenerateSelfSigned(directHost, cfg.Server.TLS.Cert, cfg.Server.TLS.Key); err != nil {
			fmt.Println(" FAILED")
			fmt.Fprintf(os.Stderr, "Warning: could not generate certificate: %v\n", err)
		} else {
			fmt.Println(" done")
			printCertFingerprint(cfg.Server.TLS.Cert)
		}
	}

	if err := fixPermissions(logmcpExists); err != nil {
		return err
	}

	// --- Caddy snippet ---
	if behindProxy && cfg.Proxy.Caddy {
		fmt.Println()
		caddyfile := "/etc/caddy/Caddyfile"
		if caddyContainsLogmcp(caddyfile, cfg.Proxy.Domain, cfg.Server.Port) {
			fmt.Printf("✓ Caddy-Konfiguration für '%s' ist bereits in %s eingetragen.\n", cfg.Proxy.Domain, caddyfile)
		} else {
			fmt.Println("=== Caddyfile snippet ===")
			fmt.Printf("Füge folgendes in %s ein:\n\n", caddyfile)
			printCaddySnippet(cfg)
		}
	}

	// --- Step 7: systemd ---
	fmt.Println()
	setupSystemd(rl)

	// --- Step 8: fail2ban ---
	fmt.Println()
	setupFail2ban(rl, cfg)

	// --- Step 9: Claude Code MCP registration ---
	fmt.Println()
	setupClaudeCodeMCP(cfg)

	fmt.Println()
	fmt.Println("=== Setup complete ===")
	fmt.Println("Start the server with: sudo logmcp serve")
	fmt.Println("Or configure client snippets with: logmcp client-config claude-code")

	return nil
}

// runSetupStdio handles the stdio-only branch of the setup wizard.
// It writes a minimal config (no tokens, no TLS) and prints the claude mcp add command.
func runSetupStdio(cfg *config.Config) error {
	fmt.Println()
	fmt.Println("=== stdio-Modus ===")
	fmt.Println("Es wird eine minimale Config ohne Token und ohne TLS angelegt.")
	fmt.Println("LogMCP läuft nur lokal — kein HTTP-Server wird gestartet.")
	fmt.Println()

	name := cfg.Name
	if name == "" {
		name = "logmcp"
	}

	whitelist := cfg.Logs.Whitelist
	if len(whitelist) == 0 {
		whitelist = config.DefaultWhitelist
	}
	whitelistYAML := ""
	for _, p := range whitelist {
		whitelistYAML += fmt.Sprintf("    - %s\n", p)
	}

	cfgYAML := fmt.Sprintf(`name: %s

auth:
  stdio:
    scopes:
      - logmcp:read

logs:
  whitelist:
%s  # Weitere Pfade freischalten (# entfernen):
  # - /var/log/nginx/**
  # - /var/log/asterisk/*
  # - /tmp/myapp/*.log
  blacklist: []
  journald: false

audit:
  syslog: true

# redis:
#   addr: 127.0.0.1:6379
#   key_prefix: logmcp
`, name, whitelistYAML)

	fmt.Println()
	if err := os.MkdirAll("/etc/logmcp", 0o755); err != nil {
		return fmt.Errorf("creating /etc/logmcp: %w", err)
	}
	if err := os.Chmod("/etc/logmcp", 0o755); err != nil {
		return fmt.Errorf("setting permissions on /etc/logmcp: %w", err)
	}
	if err := os.WriteFile(config.DefaultConfigPath, []byte(cfgYAML), 0o644); err != nil {
		return fmt.Errorf("writing config to %s: %w", config.DefaultConfigPath, err)
	}
	fmt.Printf("Config written to %s\n", config.DefaultConfigPath)

	binaryPath, _ := os.Executable()
	if binaryPath == "" {
		binaryPath = "/usr/local/bin/logmcp"
	}

	fmt.Println()
	fmt.Println("=== Setup complete ===")
	fmt.Println()
	fmt.Println("Claude Code — MCP-Server registrieren:")
	fmt.Println()
	fmt.Printf("  claude mcp add %s --type stdio -- %s stdio\n", name, binaryPath)
	fmt.Println()
	fmt.Println("Claude Desktop — claude_desktop_config.json:")
	fmt.Println()
	fmt.Printf("  { \"mcpServers\": { \"%s\": { \"command\": \"%s\", \"args\": [\"stdio\"] } } }\n", name, binaryPath)
	fmt.Println()
	fmt.Println("Scopes anpassen: auth.stdio.scopes in", config.DefaultConfigPath)

	return nil
}

// setupDeploymentMode asks whether logmcp runs directly (with TLS) or behind a
// reverse proxy, and configures cfg accordingly.
// Returns (behindProxy, directHost, error). directHost is the hostname or IP
// used as the TLS certificate SAN when running in direct mode; it is empty
// when behindProxy is true.
func setupDeploymentMode(rl *readline.Instance, cfg *config.Config) (behindProxy bool, directHost string, err error) {
	fmt.Println("Deployment mode:")
	fmt.Println("  1) Direct (logmcp handles TLS)")
	fmt.Println("  2) Behind reverse proxy (Caddy, nginx, etc.)")
	defaultMode := "1"
	if cfg.Proxy.Enabled {
		defaultMode = "2"
	}
	mode := prompt(rl, "Choose [1/2]", defaultMode)
	behindProxy = mode == "2"
	directHost = "localhost"

	if behindProxy {
		cfg.Proxy.Enabled = true
		cfg.Server.TLS.Mode = "off"
		cfg.Server.Host = "127.0.0.1"

		cfg.Proxy.Caddy = promptYN(rl, "Use Caddy as reverse proxy?", cfg.Proxy.Caddy)

		domain, domainErr := promptDomain(rl, cfg.Proxy.Domain)
		if domainErr != nil {
			return false, "", domainErr
		}
		cfg.Proxy.Domain = domain

		cfg.Proxy.PathPrefix = prompt(rl, "Subpath (leave empty for root, e.g. /logmcp)", cfg.Proxy.PathPrefix)
		cfg.Proxy.TrustedProxy = true
	} else {
		cfg.Proxy.Enabled = false
		defaultHost := cfg.Server.TLS.Cert // reuse cert SAN if set, else localhost
		if defaultHost == "" || defaultHost == "/etc/logmcp/server.crt" {
			defaultHost = "localhost"
		}
		directHost = prompt(rl, "Hostname or IP for TLS certificate SAN", defaultHost)

		fmt.Println("TLS mode:")
		fmt.Println("  1) Self-signed (auto-generated)")
		fmt.Println("  2) Custom (provide cert/key paths)")
		defaultTLSMode := "1"
		if cfg.Server.TLS.Mode == "custom" {
			defaultTLSMode = "2"
		}
		tlsMode := prompt(rl, "Choose [1/2]", defaultTLSMode)

		if tlsMode == "2" {
			cfg.Server.TLS.Mode = "custom"
			cfg.Server.TLS.Cert = prompt(rl, "Path to TLS certificate", cfg.Server.TLS.Cert)
			cfg.Server.TLS.Key = prompt(rl, "Path to TLS private key", cfg.Server.TLS.Key)
		} else {
			cfg.Server.TLS.Mode = "self-signed"
			cfg.Server.TLS.Cert = "/etc/logmcp/server.crt"
			cfg.Server.TLS.Key = "/etc/logmcp/server.key"
		}
		cfg.Server.Host = "0.0.0.0"
	}
	return behindProxy, directHost, nil
}

// promptPort asks for a port number, checks availability, and stores the result
// in cfg.Server.Port. Retries until the user accepts a valid port.
func promptPort(rl *readline.Instance, cfg *config.Config) error {
	for {
		portStr := prompt(rl, "Port", fmt.Sprintf("%d", cfg.Server.Port))
		port := cfg.Server.Port
		if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port < 1 || port > 65535 {
			fmt.Println("  Ungültige Portnummer.")
			continue
		}
		if ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err != nil {
			fmt.Printf("  ⚠ Port %d ist bereits belegt: %v\n", port, err)
			if !promptYN(rl, "Trotzdem verwenden?", false) {
				continue
			}
		} else {
			_ = ln.Close()
		}
		cfg.Server.Port = port
		break
	}
	return nil
}

// setupInitialToken either displays the existing tokens with a management hint,
// or interactively creates the first bearer token and stores it in cfg.
func setupInitialToken(rl *readline.Instance, cfg *config.Config) error {
	fmt.Println()
	if len(cfg.Auth.Tokens) > 0 {
		fmt.Println("Konfigurierte Tokens:")
		for _, t := range cfg.Auth.Tokens {
			fmt.Printf("  %-20s  scopes: %s  token: %s\n",
				t.Name, strings.Join(t.Scopes, ","), maskToken(t.Token))
		}
		fmt.Println("  → Tokens verwalten mit: logmcp token list|add|remove|renew")
	} else {
		fmt.Println("Bearer token (used by AI clients to authenticate):")
		name := prompt(rl, "Token name", "default")
		var tokenVal string
		if promptYN(rl, "Auto-generate a UUID token?", true) {
			tokenVal = uuid.NewString()
			fmt.Printf("Generated token: %s\n", tokenVal)
		} else {
			tokenVal = prompt(rl, "Enter bearer token", "")
		}
		cfg.Auth.Tokens = []config.TokenConfig{
			{Name: name, Token: tokenVal, Scopes: []string{"logmcp:read"}},
		}
	}
	return nil
}

// setupSystemUser interactively creates the logmcp system user and optionally
// adds it to relevant groups for log file access.
// Returns (true, nil) if the logmcp user/group exists (already or just created),
// (false, nil) if the user declined to create it, or (false, error) if useradd failed.
func setupSystemUser(rl *readline.Instance) (bool, error) {
	userExists := exec.Command("id", "logmcp").Run() == nil

	if userExists {
		fmt.Println("✓ System-User 'logmcp' existiert bereits.")
	} else {
		fmt.Println("Dienst-User: logmcp läuft empfohlenerweise als eigener System-User (nicht root).")
		if promptYN(rl, "System-User 'logmcp' anlegen?", true) {
			if err := exec.Command("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "logmcp").Run(); err != nil {
				return false, fmt.Errorf("useradd fehlgeschlagen: %w", err)
			}
			fmt.Println("✓ User 'logmcp' angelegt.")
			userExists = true
		} else {
			fmt.Println("  User nicht angelegt — Dienst läuft als der User, der 'logmcp serve' startet.")
		}
	}

	if !userExists {
		fmt.Println()
		return false, nil
	}

	// Offer to add logmcp to relevant groups.
	type groupSuggestion struct {
		name   string
		reason string
	}
	suggestions := []groupSuggestion{
		{"adm", "System-Logs (/var/log/syslog, auth.log)"},
		{"asterisk", "Asterisk-Logs (/var/log/asterisk/)"},
	}

	for _, sg := range suggestions {
		if !groupExists(sg.name) {
			continue
		}
		if inGroup("logmcp", sg.name) {
			fmt.Printf("✓ 'logmcp' ist bereits in Gruppe '%s'.\n", sg.name)
			continue
		}
		fmt.Printf("Gruppe '%s' gefunden — Zugriff auf %s.\n", sg.name, sg.reason)
		if promptYN(rl, fmt.Sprintf("'logmcp' zur Gruppe '%s' hinzufügen?", sg.name), true) {
			if err := exec.Command("usermod", "-aG", sg.name, "logmcp").Run(); err != nil {
				fmt.Fprintf(os.Stderr, "  Warnung: usermod fehlgeschlagen: %v\n", err)
			} else {
				fmt.Printf("✓ 'logmcp' zu Gruppe '%s' hinzugefügt.\n", sg.name)
			}
		}
	}

	// Offer to add the invoking admin user to the logmcp group so that
	// commands like "logmcp client-config" work without sudo.
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && sudoUser != "root" {
		fmt.Println()
		if inGroup(sudoUser, "logmcp") {
			fmt.Printf("✓ '%s' ist bereits in der Gruppe 'logmcp'.\n", sudoUser)
		} else {
			fmt.Printf("Admin-User '%s': Zur Gruppe 'logmcp' hinzufügen?\n", sudoUser)
			fmt.Println("  → Ermöglicht 'logmcp client-config' ohne sudo.")
			if promptYN(rl, fmt.Sprintf("'%s' zur Gruppe 'logmcp' hinzufügen?", sudoUser), true) {
				if err := exec.Command("usermod", "-aG", "logmcp", sudoUser).Run(); err != nil {
					fmt.Fprintf(os.Stderr, "  Warnung: usermod fehlgeschlagen: %v\n", err)
				} else {
					fmt.Printf("✓ '%s' zur Gruppe 'logmcp' hinzugefügt.\n", sudoUser)
					fmt.Println("  Hinweis: Neu einloggen (oder 'newgrp logmcp'), damit die Gruppe aktiv wird.")
				}
			}
		}
	}

	fmt.Println()
	return true, nil
}

// fixPermissions sets ownership and permissions on /etc/logmcp/ and all
// regular files within it. chmod is always applied; chown root:logmcp is only
// applied when logmcpExists is true.
func fixPermissions(logmcpExists bool) error {
	const dir = "/etc/logmcp"

	if err := os.Chmod(dir, 0o750); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	if logmcpExists {
		if err := exec.Command("chown", "root:logmcp", dir).Run(); err != nil {
			return fmt.Errorf("chown root:logmcp %s: %w", dir, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Chmod(path, 0o640); err != nil {
			return fmt.Errorf("chmod %s: %w", path, err)
		}
		if logmcpExists {
			if err := exec.Command("chown", "root:logmcp", path).Run(); err != nil {
				return fmt.Errorf("chown root:logmcp %s: %w", path, err)
			}
		}
	}
	return nil
}

// caddyContainsLogmcp checks if the Caddyfile already mentions the domain
// and a logmcp-related entry (configured port or the path /logmcp).
func caddyContainsLogmcp(caddyfile, domain string, port int) bool {
	data, err := os.ReadFile(caddyfile)
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, domain) &&
		(strings.Contains(content, strconv.Itoa(port)) || strings.Contains(content, "/logmcp"))
}

// setupClaudeCodeMCP prints the claude mcp add command for the client machine.
func setupClaudeCodeMCP(cfg *config.Config) {
	defaultToken := cfg.Auth.Default()
	if defaultToken == nil {
		return
	}

	var mcpURL string
	if cfg.Proxy.Enabled && cfg.Proxy.Domain != "" {
		scheme := "https"
		prefix := strings.TrimRight(cfg.Proxy.PathPrefix, "/")
		mcpURL = fmt.Sprintf("%s://%s%s/mcp", scheme, cfg.Proxy.Domain, prefix)
	} else {
		scheme := "http"
		if cfg.Server.TLS.Mode != "off" {
			scheme = "https"
		}
		mcpURL = fmt.Sprintf("%s://%s:%d/mcp", scheme, cfg.Server.Host, cfg.Server.Port)
	}

	name := cfg.Name
	if name == "" {
		name = "logmcp"
	}

	fmt.Println("Claude Code — Server auf dem Client-Rechner registrieren:")
	fmt.Println()
	fmt.Printf("  claude mcp add --transport http %s %s \\\n", name, mcpURL)
	fmt.Printf("    --header \"Authorization: Bearer %s\"\n", defaultToken.Token)
	fmt.Println()
	fmt.Println("Zum Entfernen:")
	fmt.Printf("  claude mcp remove %s\n", name)
}

// setupSystemd handles the systemd installation step in the setup wizard.
func setupSystemd(rl *readline.Instance) {
	unitExists := func() bool {
		_, err := os.Stat(systemdUnitPath)
		return err == nil
	}
	isRunning := func() bool {
		return exec.Command("systemctl", "is-active", "--quiet", "logmcp").Run() == nil
	}

	if unitExists() {
		if isRunning() {
			fmt.Println("✓ logmcp-Service läuft bereits.")
			if promptYN(rl, "Service neu starten (nach Config-Änderung)?", false) {
				run("systemctl", "restart", "logmcp")
				fmt.Println("Service neu gestartet.")
			}
		} else {
			fmt.Println("Service-Unit vorhanden, aber nicht aktiv.")
			if promptYN(rl, "Service jetzt starten?", true) {
				run("systemctl", "enable", "--now", "logmcp")
			}
		}
		return
	}

	if !promptYN(rl, "Systemd-Service jetzt installieren und starten?", false) {
		fmt.Println("Später installieren mit: sudo logmcp service install")
		return
	}

	if err := writeSystemdUnit(); err != nil {
		fmt.Fprintf(os.Stderr, "Warnung: Unit-Datei konnte nicht geschrieben werden: %v\n", err)
		fmt.Println("Manuell installieren mit: sudo logmcp service install")
		return
	}
	run("systemctl", "daemon-reload")
	run("systemctl", "enable", "--now", "logmcp")
	fmt.Println("✓ Service installiert und gestartet.")
}

// groupExists returns true if the named group exists on the system.
func groupExists(name string) bool {
	return exec.Command("getent", "group", name).Run() == nil
}

// inGroup returns true if user is already a member of the named group.
func inGroup(user, group string) bool {
	out, err := exec.Command("groups", user).Output()
	if err != nil {
		return false
	}
	return slices.Contains(strings.Fields(string(out)), group)
}

// promptDomain asks for a domain name, strips scheme/path, and validates.
// Retries until a valid domain is entered.
func promptDomain(rl *readline.Instance, current string) (string, error) {
	for {
		raw := prompt(rl, "Domain name (e.g. switchbox-dev.gpt4voice.de)", current)
		if raw == "" {
			fmt.Println("  Domain darf nicht leer sein.")
			continue
		}

		domain, err := cleanDomain(raw)
		if err != nil {
			fmt.Printf("  Ungültige Domain: %v\n", err)
			continue
		}

		if domain != raw {
			fmt.Printf("  → bereinigt zu: %s\n", domain)
		}
		return domain, nil
	}
}

// cleanDomain strips scheme (https://, http://), trailing slashes/paths,
// and port. Returns an error if the result is not a plausible domain/IP.
func cleanDomain(input string) (string, error) {
	d := strings.TrimSpace(input)
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")

	if i := strings.Index(d, "/"); i >= 0 {
		d = d[:i]
	}
	if host, _, err := net.SplitHostPort(d); err == nil {
		d = host
	}

	d = strings.TrimSpace(d)
	if d == "" {
		return "", fmt.Errorf("nach dem Bereinigen ist die Domain leer")
	}
	if net.ParseIP(d) != nil {
		return d, nil
	}
	if !strings.Contains(d, ".") {
		return "", fmt.Errorf("%q enthält keinen Punkt (kein gültiger Domainname)", d)
	}
	for _, r := range d {
		if !isValidDomainChar(r) {
			return "", fmt.Errorf("ungültiges Zeichen %q in Domain", r)
		}
	}
	return d, nil
}

func isValidDomainChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '.' || r == '-'
}

// maskToken shows only the first 8 characters of a token followed by "...".
func maskToken(token string) string {
	if len(token) <= 8 {
		return strings.Repeat("*", len(token))
	}
	return token[:8] + "..."
}

// abortIfCancelled exits cleanly when the user presses Ctrl+C or Ctrl+D.
func abortIfCancelled(err error) {
	if err == readline.ErrInterrupt || err == io.EOF {
		fmt.Println("\nSetup abgebrochen.")
		os.Exit(0)
	}
}

// prompt displays a prompt with optional default and returns the trimmed input.
func prompt(rl *readline.Instance, question, defaultVal string) string {
	if defaultVal != "" {
		rl.SetPrompt(fmt.Sprintf("%s [%s]: ", question, defaultVal))
	} else {
		rl.SetPrompt(fmt.Sprintf("%s: ", question))
	}

	line, err := rl.Readline()
	abortIfCancelled(err)
	if err != nil {
		return defaultVal
	}
	text := strings.TrimSpace(line)
	if text == "" {
		return defaultVal
	}
	return text
}

// promptYN asks a yes/no question and returns true for yes.
func promptYN(rl *readline.Instance, question string, defaultYes bool) bool {
	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	rl.SetPrompt(fmt.Sprintf("%s [%s]: ", question, hint))

	line, err := rl.Readline()
	abortIfCancelled(err)
	if err != nil {
		return defaultYes
	}
	text := strings.ToLower(strings.TrimSpace(line))
	if text == "" {
		return defaultYes
	}
	return text == "y" || text == "yes"
}

// printCertFingerprint reads the PEM cert and prints its SHA256 fingerprint.
// Best-effort: silently skip on any read/parse error.
func printCertFingerprint(certPath string) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return
	}
	fp := sha256.Sum256(cert.Raw)
	parts := make([]string, len(fp))
	for i, b := range fp {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	fmt.Printf("Certificate SHA256 fingerprint:\n  %s\n", strings.Join(parts, ":"))
}

// setupFail2ban offers to install the fail2ban filter and jail configs.
func setupFail2ban(rl *readline.Instance, cfg *config.Config) {
	if !cfg.Security.Fail2ban.Enabled {
		fmt.Println("fail2ban-Integration: in der Config deaktiviert (security.fail2ban.enabled: false).")
		return
	}
	if !security.Available() {
		fmt.Println("fail2ban nicht gefunden — Filter-Dateien können später installiert werden:")
		fmt.Println("  sudo logmcp security install-fail2ban --reload")
		return
	}
	if !promptYN(rl, "fail2ban-Filter und Jail für logmcp installieren?", true) {
		fmt.Println("Später installieren mit: sudo logmcp security install-fail2ban --reload")
		return
	}
	if err := security.InstallFail2ban(cfg.Security.Fail2ban.FilterDir, cfg.Security.Fail2ban.JailDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warnung: fail2ban-Konfiguration: %v\n", err)
		return
	}
	if err := security.ReloadFail2ban(); err != nil {
		fmt.Fprintf(os.Stderr, "Warnung: fail2ban-client reload: %v\n", err)
	} else {
		fmt.Println("✓ fail2ban konfiguriert und neu geladen.")
	}
}

// injectSecurityComments adds a commented-out rate_limit example block inside
// the security section of the marshalled YAML.
func injectSecurityComments(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	result := make([]string, 0, len(lines)+10)
	for _, line := range lines {
		result = append(result, line)
		if line == "security:" {
			result = append(result,
				"  # Rate-Limiting für Auth-Fehler (auskommentiert = deaktiviert):",
				"  # Beide Stufen sind unabhängig optional — einzelne Stufe weglassen = deaktiviert.",
				"  # Burst-Threshold höher ansetzen: MCP-Einrichtung kostet erfahrungsgemäß einige Fehlversuche.",
				"  # rate_limit:",
				"  #   burst:",
				"  #     max_failures: 20      # schnelle Burst-Bremse",
				"  #     window_seconds: 30",
				"  #   sustained:",
				"  #     max_failures: 50      # langsame Dauersperre",
				"  #     window_seconds: 600   # 10 Minuten",
			)
		}
	}
	return []byte(strings.Join(result, "\n"))
}

// injectWhitelistComments appends commented-out example paths after the
// whitelist entries in a yaml.Marshal output, so users have ready-made
// templates to uncomment.
func injectWhitelistComments(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	result := make([]string, 0, len(lines)+6)
	inWhitelist := false

	for i, line := range lines {
		result = append(result, line)
		if line == "    whitelist:" {
			inWhitelist = true
			continue
		}
		if inWhitelist {
			if strings.HasPrefix(line, "        -") {
				// Inject comments after the last whitelist item.
				next := i + 1
				if next >= len(lines) || !strings.HasPrefix(lines[next], "        -") {
					result = append(result,
						"    # Weitere Pfade freischalten (# entfernen):",
						"    # - /var/log/nginx/**",
						"    # - /var/log/asterisk/*",
						"    # - /tmp/myapp/*.log",
					)
					inWhitelist = false
				}
			} else {
				inWhitelist = false
			}
		}
	}
	return []byte(strings.Join(result, "\n"))
}
