package cmd

import (
	"context"
	"embed"
	"fmt"
	"os"

	"github.com/kleist-dev/logmcp/internal/auth"
	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/logs"
	internalmcp "github.com/kleist-dev/logmcp/internal/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

// newStdioCmd returns the `logmcp stdio` subcommand.
func newStdioCmd(docsFS embed.FS) *cobra.Command {
	return &cobra.Command{
		Use:   "stdio",
		Short: "Start LogMCP as a local MCP server via stdio (no HTTP, no tokens)",
		Long: `Start LogMCP as a local MCP server using the stdio transport.

No HTTP server is started. The MCP protocol is spoken over stdin/stdout,
making this suitable for use with Claude Desktop (mcpServers in
claude_desktop_config.json) or Claude Code (.claude/settings.json) running
on the same machine.

No bearer token is required. Access is controlled by the scopes configured
under auth.stdio.scopes in the config file (default: ["logmcp:read"]).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return serveStdio(docsFS)
		},
	}
}

func serveStdio(docsFS embed.FS) error {
	if _, err := os.Stat(config.DefaultConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("config file not found at %s — run 'logmcp setup' to create it", config.DefaultConfigPath)
	}

	cfg, err := config.Load(config.DefaultConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	scopes := cfg.Auth.Stdio.Scopes
	if len(scopes) == 0 {
		scopes = []string{"logmcp:read"}
	}

	logMgr := logs.NewManager(cfg.Logs.Whitelist, cfg.Logs.Blacklist, cfg.Logs.Journald)

	srv, err := internalmcp.New(cfg, logMgr, docsFS)
	if err != nil {
		return fmt.Errorf("creating MCP server: %w", err)
	}

	// ctxFunc injects the stdio identity into every request context so that
	// tool handlers can read caller name and scopes via auth.TokenNameFromCtx /
	// auth.TokenScopesFromCtx. The name "stdio" appears in audit log entries.
	capturedScopes := scopes
	ctxFunc := func(ctx context.Context) context.Context {
		return auth.InjectStdioIdentity(ctx, "stdio", capturedScopes)
	}

	return server.ServeStdio(
		srv.MCPServer(),
		server.WithStdioContextFunc(ctxFunc),
	)
}
