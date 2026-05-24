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
	if _, err := os.Stat(config.DefaultConfigPath); err != nil {
		fmt.Printf("  ✗  Config file exists — %s\n\n", config.DefaultConfigPath)
		fmt.Println("Cannot proceed without a config file. Run: logmcp setup")
		os.Exit(1)
	}

	cfg, err := config.Load(config.DefaultConfigPath)
	if err != nil {
		fmt.Printf("  ✗  Config is valid YAML — %s\n\n", err.Error())
		fmt.Println("Fix config errors before continuing.")
		os.Exit(1)
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
	fmt.Println("One or more checks failed.")
	os.Exit(1)
	return nil
}
