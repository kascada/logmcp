package cmd

import (
	"fmt"
	"os"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/logs"
	internalmcp "github.com/kleist-dev/logmcp/internal/mcp"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "logmcp",
	Short: "LogMCP — AI Log Access Server",
	Long: `LogMCP exposes local log files to AI clients (Claude Code, VS Code, Claude Desktop)
over HTTPS + Bearer Token using the Model Context Protocol (MCP).`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(newServeCmd())
	rootCmd.AddCommand(newSetupCmd())
	rootCmd.AddCommand(newCheckCmd())
	rootCmd.AddCommand(newServiceCmd())
	rootCmd.AddCommand(newClientConfigCmd())
	rootCmd.AddCommand(newLogsCmd())
	rootCmd.AddCommand(newTokenCmd())
}

// newServeCmd returns the explicit `serve` subcommand.
func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the LogMCP MCP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return serve()
		},
	}
}

func serve() error {
	if _, err := os.Stat(config.DefaultConfigPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr,
			"Config file not found at %s.\nRun 'logmcp setup' to create it.\n",
			config.DefaultConfigPath,
		)
		os.Exit(1)
	}

	cfg, err := config.Load(config.DefaultConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	config.BackfillComments(config.DefaultConfigPath)

	logMgr := logs.NewManager(cfg.Logs.Whitelist, cfg.Logs.Blacklist, cfg.Logs.Journald)

	srv, err := internalmcp.New(cfg, logMgr)
	if err != nil {
		return fmt.Errorf("creating MCP server: %w", err)
	}

	return srv.Start()
}
