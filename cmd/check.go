package cmd

import (
	"fmt"
	"os"

	"github.com/kleist-dev/logmcp/internal/check"
	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/spf13/cobra"
)

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Verify configuration and environment",
		Long:  "Checks that the config file is valid and the environment is ready to run LogMCP.",
		RunE:  runCheck,
	}
}

func runCheck(cmd *cobra.Command, args []string) error {
	if os.Getuid() == 0 {
		fmt.Println("WARNING: running as root — permission checks may not reflect actual service behaviour.")
		fmt.Println("         Run as the service user for accurate results: sudo su -s /bin/sh logmcp -c 'logmcp check'")
		fmt.Println()
	}
	if _, err := os.Stat(config.DefaultConfigPath); err != nil {
		return fmt.Errorf("config file not found at %s — run: logmcp setup", config.DefaultConfigPath)
	}

	cfg, err := config.Load(config.DefaultConfigPath)
	if err != nil {
		return fmt.Errorf("config is not valid YAML: %w", err)
	}

	result := check.Run(cfg, check.Options{
		ConfigPath:  config.DefaultConfigPath,
		IncludePort: true,
	})

	fmt.Println("LogMCP environment check")
	fmt.Println()
	for _, item := range result.Checks {
		symbol := "✓"
		if !item.OK {
			symbol = "✗"
		}
		if item.Detail != "" {
			fmt.Printf("  %s  %s — %s\n", symbol, item.Name, item.Detail)
		} else {
			fmt.Printf("  %s  %s\n", symbol, item.Name)
		}
	}
	fmt.Println()

	if result.OK {
		fmt.Println("All checks passed.")
		return nil
	}
	return fmt.Errorf("one or more checks failed")
}
