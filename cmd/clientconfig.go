package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/spf13/cobra"
)

func newClientConfigCmd() *cobra.Command {
	var showAll bool
	ccCmd := &cobra.Command{
		Use:   "client-config [claude-code|vscode]",
		Short: "Print MCP client configuration snippets",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfigForClient()
			if err != nil {
				return err
			}
			url := deriveURL(cfg)
			showAll = resolveShowAll(cfg, showAll)
			printClaudeCodeConfig(cfg, url, showAll)
			return nil
		},
	}
	ccCmd.PersistentFlags().BoolVar(&showAll, "all", false, "Show configuration for all tokens")
	ccCmd.AddCommand(newClientConfigSubCmd("claude-code", printClaudeCodeConfig, &showAll))
	ccCmd.AddCommand(newClientConfigSubCmd("vscode", printVSCodeConfig, &showAll))
	ccCmd.AddCommand(newClientConfigSubCmd("claude-desktop", printClaudeDesktopConfig, &showAll))
	return ccCmd
}

func newClientConfigSubCmd(name string, fn func(*config.Config, string, bool), showAll *bool) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: fmt.Sprintf("Print %s MCP configuration snippet", name),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfigForClient()
			if err != nil {
				return err
			}
			url := deriveURL(cfg)
			fn(cfg, url, resolveShowAll(cfg, *showAll))
			return nil
		},
	}
}

func resolveShowAll(cfg *config.Config, showAll bool) bool {
	if showAll || len(cfg.Auth.Tokens) <= 1 {
		return showAll
	}
	return promptShowAll()
}

// hasPlaceholderToken returns true when visibleTokens will show a synthetic placeholder.
func hasPlaceholderToken(cfg *config.Config) bool {
	return len(cfg.Auth.Tokens) == 0
}

func loadConfigForClient() (*config.Config, error) {
	cfg, err := config.Load(config.DefaultConfigPath)
	if err != nil {
		return config.Default(), nil
	}
	return cfg, nil
}

// deriveURL builds the MCP endpoint URL from config.
func deriveURL(cfg *config.Config) string {
	port := cfg.Server.Port
	host := cfg.Server.Host
	if host == "0.0.0.0" || host == "" {
		host = "localhost"
	}

	switch {
	case cfg.Server.TLS.Mode == "self-signed" || cfg.Server.TLS.Mode == "custom":
		return fmt.Sprintf("https://%s:%d/mcp", host, port)

	case cfg.Server.TLS.Mode == "off" && !cfg.Proxy.Enabled:
		return fmt.Sprintf("http://%s:%d/mcp", host, port)

	case cfg.Server.TLS.Mode == "off" && cfg.Proxy.Enabled && cfg.Proxy.Caddy:
		domain := cfg.Proxy.Domain
		prefix := strings.TrimRight(cfg.Proxy.PathPrefix, "/")
		if prefix == "" {
			return fmt.Sprintf("https://%s/mcp", domain)
		}
		return fmt.Sprintf("https://%s%s/mcp", domain, prefix)

	default:
		return fmt.Sprintf("http://%s:%d/mcp", host, port)
	}
}

// mcpEntry is the JSON structure for an MCP server entry.
type mcpEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func buildEntry(url string, tokenVal string) mcpEntry {
	return mcpEntry{
		Type: "http",
		URL:  url,
		Headers: map[string]string{
			"Authorization": "Bearer " + tokenVal,
		},
	}
}

func serverName(cfg *config.Config) string {
	if cfg.Name != "" {
		return cfg.Name
	}
	if hn, err := os.Hostname(); err == nil && hn != "" {
		return "logmcp-" + hn
	}
	return "logmcp"
}

// mcpEntryName returns the Claude Code MCP server name for a token.
// The default token uses the bare server name; all others get a "-<tokenname>" suffix.
func mcpEntryName(srv string, t config.TokenConfig) string {
	if t.Name == "default" {
		return srv
	}
	return srv + "-" + t.Name
}

// tokenDisplayValue returns the token value to print.
// Tokens named "dummy" are shown as a placeholder.
func tokenDisplayValue(t config.TokenConfig) string {
	if t.Name == "dummy" {
		return "<DEIN-TOKEN>"
	}
	return t.Token
}

// visibleTokens returns the slice of tokens to display.
// When no static tokens are configured (authenticator mode), a placeholder is returned.
func visibleTokens(cfg *config.Config, showAll bool) []config.TokenConfig {
	if len(cfg.Auth.Tokens) == 0 {
		return []config.TokenConfig{{Name: "default", Token: "<TOKEN>"}}
	}
	if showAll {
		return cfg.Auth.Tokens
	}
	return cfg.Auth.Tokens[:1]
}

func printClaudeCodeConfig(cfg *config.Config, url string, showAll bool) {
	name := serverName(cfg)
	tokens := visibleTokens(cfg, showAll)

	fmt.Println("=== Claude Code ===")
	fmt.Println()
	if hasPlaceholderToken(cfg) {
		fmt.Println("Hinweis: <TOKEN> durch das Bearer-Token aus deiner Auth-Quelle ersetzen.")
		fmt.Println()
	}
	fmt.Println("Registrieren:")

	for _, t := range tokens {
		fmt.Printf("  claude mcp add --transport http %s %s \\\n", mcpEntryName(name, t), url)
		fmt.Printf("    --header \"Authorization: Bearer %s\"\n", tokenDisplayValue(t))
		fmt.Println()
	}

	fmt.Println("  Tipp: Diesen Befehl im Projektverzeichnis ausführen, in dem der Server gelten soll.")
	fmt.Println("        Claude Code speichert die Konfiguration projektbezogen in .claude/settings.json.")
	fmt.Println("        Für globale Registrierung: --scope user anhängen.")
	fmt.Println()
	fmt.Println("Entfernen:")
	for _, t := range tokens {
		fmt.Printf("  claude mcp remove %s\n", mcpEntryName(name, t))
	}
	printTLSNote(cfg)
}

func printVSCodeConfig(cfg *config.Config, url string, showAll bool) {
	tokens := visibleTokens(cfg, showAll)
	name := serverName(cfg)

	fmt.Println("=== VS Code (.vscode/settings.json oder User-Settings → mcp.servers) ===")
	fmt.Println()
	if hasPlaceholderToken(cfg) {
		fmt.Println("Hinweis: <TOKEN> durch das Bearer-Token aus deiner Auth-Quelle ersetzen.")
		fmt.Println()
	}
	fmt.Println(`Eintragen unter "mcp":`)
	fmt.Println()

	entries := make(map[string]json.RawMessage, len(tokens))
	for _, t := range tokens {
		data, _ := json.MarshalIndent(buildEntry(url, tokenDisplayValue(t)), "      ", "  ")
		entries[mcpEntryName(name, t)] = data
	}
	serversJSON, _ := json.MarshalIndent(entries, "    ", "  ")

	fmt.Println(`{`)
	fmt.Println(`  "mcp": {`)
	fmt.Printf(`    "servers": %s`, string(serversJSON))
	fmt.Println()
	fmt.Println(`  }`)
	fmt.Println(`}`)
	fmt.Println()

	fmt.Println("Entfernen: Entsprechende Einträge aus den Settings löschen.")
	fmt.Println()
	fmt.Println("# ACHTUNG: Tokens sind sensitiv — nicht in öffentliche Repos einchecken")
	printTLSNote(cfg)
}

func printClaudeDesktopConfig(cfg *config.Config, url string, showAll bool) {
	fmt.Println("=== Claude Desktop ===")
	fmt.Println()
	fmt.Println("Claude Desktop supports only OAuth-based MCP connections.")
	fmt.Println("Bearer token authentication as used by logmcp is not supported.")
	fmt.Println()
	fmt.Println("Use Claude Code or VS Code to connect to this server.")
}

func printTLSNote(cfg *config.Config) {
	if cfg.Server.TLS.Mode == "self-signed" {
		fmt.Println()
		fmt.Println("Note: This server uses a self-signed TLS certificate.")
		fmt.Println("Your AI client may need to be configured to trust it.")
		fmt.Println("Run 'logmcp check' to display the certificate fingerprint.")
	}
}

func promptShowAll() bool {
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) == 0 {
		return false
	}
	fmt.Fprint(os.Stderr, "Für alle Token anzeigen? [j/N] ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.EqualFold(strings.TrimSpace(scanner.Text()), "j")
	}
	return false
}
