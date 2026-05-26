package cmd

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"

	"github.com/chzyer/readline"
	"github.com/google/uuid"
	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/logs"
	internalmcp "github.com/kleist-dev/logmcp/internal/mcp"
	internaltls "github.com/kleist-dev/logmcp/internal/tls"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newQuickstartCmd() *cobra.Command {
	var port int
	var token string
	var runAsUser string

	c := &cobra.Command{
		Use:   "quickstart",
		Short: "Start LogMCP instantly without a config file",
		Long: `Starts a temporary LogMCP server without writing a config file.
The bearer token changes on every start unless passed via --token.
For persistent configuration use 'logmcp setup'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQuickstart(port, token, runAsUser)
		},
	}

	defaults := config.Default()
	c.Flags().IntVar(&port, "port", defaults.Server.Port, "Port to listen on")
	c.Flags().StringVar(&token, "token", "", "Bearer token (prompted or generated if not set)")
	c.Flags().StringVar(&runAsUser, "user", "", "User to run as (root only)")
	return c
}

func runQuickstart(port int, tokenFlag, userFlag string) error {
	isRoot := os.Getuid() == 0

	rl, err := readline.NewEx(&readline.Config{
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return fmt.Errorf("initializing readline: %w", err)
	}
	defer rl.Close()

	interactive := term.IsTerminal(int(os.Stdin.Fd()))

	fmt.Println("=== LogMCP Quickstart ===")
	fmt.Println()

	// --- Root: determine target user ---
	targetUser := userFlag
	if isRoot && targetUser == "" && interactive {
		defaultUser := os.Getenv("SUDO_USER")
		if defaultUser == "" {
			defaultUser = "nobody"
		}
		targetUser = prompt(rl, "Als welcher User soll LogMCP laufen?", defaultUser)
	}

	// --- Group check ---
	checkUser := targetUser
	if checkUser == "" {
		if u, err := user.Current(); err == nil {
			checkUser = u.Username
		}
	}
	quickstartCheckGroups(isRoot, checkUser)

	// --- Root: re-exec as target user ---
	if isRoot && targetUser != "" {
		return reexecAsUser(targetUser, tokenFlag, port)
	}

	// --- Token ---
	token := tokenFlag
	if token == "" {
		if interactive {
			fmt.Println("Bearer Token:")
			fmt.Println("  Ohne Config-Datei ändert sich der Token bei jedem Start.")
			fmt.Println("  Für permanente Nutzung: 'logmcp setup'.")
			fmt.Println()
			if promptYN(rl, "Eigenen Token angeben (sonst wird ein zufälliger erzeugt)?", false) {
				token = prompt(rl, "Token eingeben", "")
			}
		}
		if token == "" {
			token = uuid.NewString()
		}
	}

	// --- Build in-memory config ---
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "localhost"
	}

	cfg := config.Default()
	cfg.Name = "logmcp-quickstart"
	cfg.Server.Port = port
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.TLS.Mode = "self-signed"
	cfg.Auth.Tokens = []config.TokenConfig{
		{Name: "quickstart", Token: token, Scopes: []string{"logmcp:read"}},
	}
	cfg.Logs.Whitelist = config.DefaultWhitelist
	cfg.Logs.Journald = true
	cfg.Security.RateLimit = &config.TwoTierRateLimitConfig{
		Burst: &config.RateLimitTierConfig{
			MaxFailures:   20,
			WindowSeconds: 30,
		},
		Sustained: &config.RateLimitTierConfig{
			MaxFailures:   50,
			WindowSeconds: 600,
		},
	}

	// --- TLS cert in temp dir (ephemeral) ---
	tmpDir, err := os.MkdirTemp("", "logmcp-qs-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	certPath := tmpDir + "/server.crt"
	keyPath := tmpDir + "/server.key"

	fmt.Print("Generating TLS certificate... ")
	if err := internaltls.GenerateSelfSigned(hostname, certPath, keyPath); err != nil {
		return fmt.Errorf("generating TLS cert: %w", err)
	}
	fmt.Println("done")

	cfg.Server.TLS.Cert = certPath
	cfg.Server.TLS.Key = keyPath

	// --- Print connection info ---
	mcpURL := fmt.Sprintf("https://%s:%d/mcp", hostname, port)

	fmt.Println()
	fmt.Println("────────────────────────────────────────────────────────────────────")
	fmt.Printf("  Token:  %s\n", token)
	fmt.Printf("  URL:    %s\n", mcpURL)
	fmt.Println()
	printCertFingerprint(certPath)
	fmt.Println()
	fmt.Println("  Claude Code — Server hinzufügen:")
	fmt.Printf("    claude mcp add --transport http logmcp-quickstart %s \\\n", mcpURL)
	fmt.Printf("      --header \"Authorization: Bearer %s\"\n", token)
	fmt.Println()
	fmt.Println("  Nicht vergessen — danach wieder entfernen:")
	fmt.Println("    claude mcp remove logmcp-quickstart")
	fmt.Println("────────────────────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("Server läuft. Beenden mit Ctrl+C.")
	fmt.Println()

	// --- Start server ---
	logMgr := logs.NewManager(cfg.Logs.Whitelist, cfg.Logs.Blacklist, cfg.Logs.Journald)
	srv, err := internalmcp.New(cfg, logMgr, embed.FS{})
	if err != nil {
		return fmt.Errorf("creating MCP server: %w", err)
	}
	return srv.Start()
}

// quickstartCheckGroups shows which log-access groups are present or missing.
// Root: adds missing groups via usermod immediately.
// Non-root: prints sudo hints for missing groups.
func quickstartCheckGroups(isRoot bool, username string) {
	type groupInfo struct {
		name   string
		reason string
	}
	required := []groupInfo{
		{"adm", "System-Logs (/var/log/syslog, auth.log)"},
		{"systemd-journal", "Journald-Logs"},
		{"asterisk", "Asterisk-Logs"},
	}

	var missing []string
	for _, g := range required {
		if !groupExists(g.name) {
			continue
		}
		if inGroup(username, g.name) {
			fmt.Printf("✓ Gruppe '%-18s vorhanden\n", g.name+"'")
		} else {
			fmt.Printf("✗ Gruppe '%-18s fehlt  (%s)\n", g.name+"'", g.reason)
			if isRoot {
				if err := exec.Command("usermod", "-aG", g.name, username).Run(); err != nil {
					fmt.Fprintf(os.Stderr, "  Warnung: usermod fehlgeschlagen: %v\n", err)
				} else {
					fmt.Printf("  → '%s' zur Gruppe '%s' hinzugefügt.\n", username, g.name)
				}
			} else {
				missing = append(missing, g.name)
			}
		}
	}

	if len(missing) > 0 {
		fmt.Println()
		fmt.Println("Fehlende Gruppen — einmalig als Root setzen:")
		for _, g := range missing {
			fmt.Printf("  sudo adduser %s %s\n", username, g)
		}
		fmt.Println()
		fmt.Println("Danach neu einloggen (oder 'newgrp <gruppe>') und 'logmcp quickstart' erneut starten.")
	}
	fmt.Println()
}

// reexecAsUser spawns the current binary as targetUser, passing through port
// and token flags. The parent (root) waits for the child and returns its exit
// status. This avoids Go's multi-threaded setuid limitations.
func reexecAsUser(targetUser, token string, port int) error {
	u, err := user.Lookup(targetUser)
	if err != nil {
		return fmt.Errorf("user %q not found: %w", targetUser, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("invalid UID for %q: %w", targetUser, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("invalid GID for %q: %w", targetUser, err)
	}

	groupIDs, _ := u.GroupIds()
	gids := make([]uint32, 0, len(groupIDs))
	for _, g := range groupIDs {
		if id, err := strconv.Atoi(g); err == nil {
			gids = append(gids, uint32(id))
		}
	}

	args := []string{"quickstart", fmt.Sprintf("--port=%d", port)}
	if token != "" {
		args = append(args, "--token="+token)
	}

	fmt.Printf("Starte LogMCP als User '%s'. Root ist für zukünftige Starts nicht mehr nötig.\n\n", targetUser)

	cmd := exec.Command(os.Args[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid:    uint32(uid),
			Gid:    uint32(gid),
			Groups: gids,
		},
	}
	return cmd.Run()
}
