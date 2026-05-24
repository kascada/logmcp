package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/spf13/cobra"
)

func newClientConfigCmd() *cobra.Command {
	ccCmd := &cobra.Command{
		Use:   "client-config [claude-code|vscode]",
		Short: "Print MCP client configuration snippets",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfigForClient()
			if err != nil {
				return err
			}
			url := deriveURL(cfg)
			printClaudeCodeConfig(cfg, url)
			return nil
		},
	}
	ccCmd.AddCommand(newClientConfigSubCmd("claude-code", printClaudeCodeConfig))
	ccCmd.AddCommand(newClientConfigSubCmd("vscode", printVSCodeConfig))
	return ccCmd
}

func newClientConfigSubCmd(name string, fn func(*config.Config, string)) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: fmt.Sprintf("Print %s MCP configuration snippet", name),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfigForClient()
			if err != nil {
				return err
			}
			url := deriveURL(cfg)
			fn(cfg, url)
			return nil
		},
	}
}

func loadConfigForClient() (*config.Config, error) {
	cfg, err := config.Load(config.DefaultConfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
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

func buildEntry(cfg *config.Config, url string) mcpEntry {
	token := ""
	if t := cfg.Auth.Default(); t != nil {
		token = t.Token
	}
	return mcpEntry{
		Type: "http",
		URL:  url,
		Headers: map[string]string{
			"Authorization": "Bearer " + token,
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

func printClaudeCodeConfig(cfg *config.Config, url string) {
	name := serverName(cfg)
	token := ""
	if t := cfg.Auth.Default(); t != nil {
		token = t.Token
	}

	fmt.Println("=== Claude Code ===")
	fmt.Println()
	fmt.Println("Registrieren:")
	fmt.Printf("  claude mcp add --transport http %s %s \\\n", name, url)
	fmt.Printf("    --header \"Authorization: Bearer %s\"\n", token)
	fmt.Println()
	fmt.Println("  Tipp: Diesen Befehl im Projektverzeichnis ausführen, in dem der Server gelten soll.")
	fmt.Println("        Claude Code speichert die Konfiguration projektbezogen in .claude/settings.json.")
	fmt.Println("        Für globale Registrierung: --scope user anhängen.")
	fmt.Println()
	fmt.Println("Entfernen:")
	fmt.Printf("  claude mcp remove %s\n", name)
	printTLSNote(cfg)
}

func printVSCodeConfig(cfg *config.Config, url string) {
	entry := buildEntry(cfg, url)
	data, _ := json.MarshalIndent(entry, "    ", "  ")
	name := serverName(cfg)

	fmt.Println("=== VS Code (.vscode/settings.json oder User-Settings → mcp.servers) ===")
	fmt.Println()
	fmt.Println(`Eintragen unter "mcp":`)
	fmt.Println()
	fmt.Println(`{`)
	fmt.Println(`  "mcp": {`)
	fmt.Println(`    "servers": {`)
	fmt.Printf(`      "%s": %s`, name, string(data))
	fmt.Println()
	fmt.Println(`    }`)
	fmt.Println(`  }`)
	fmt.Println(`}`)
	fmt.Println()
	fmt.Printf("Entfernen: Eintrag \"%s\" aus den Settings löschen.\n", name)
	fmt.Println()
	fmt.Println("# ACHTUNG: Dieses Token ist sensitiv — nicht in öffentliche Repos einchecken")
	printTLSNote(cfg)
}

func printTLSNote(cfg *config.Config) {
	if cfg.Server.TLS.Mode == "self-signed" {
		fmt.Println()
		fmt.Println("Note: This server uses a self-signed TLS certificate.")
		fmt.Println("Your AI client may need to be configured to trust it.")
		fmt.Println("Run 'logmcp check' to display the certificate fingerprint.")
	}
}
