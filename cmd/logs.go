package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/logs"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	logsCmd := &cobra.Command{
		Use:   "logs",
		Short: "Interact with log files directly (mirrors MCP tools)",
	}
	logsCmd.AddCommand(newLogsListCmd())
	logsCmd.AddCommand(newLogsReadCmd())
	logsCmd.AddCommand(newLogsSearchCmd())
	logsCmd.AddCommand(newLogsInfoCmd())
	return logsCmd
}

func loadLogsManager() (*logs.Manager, error) {
	cfg, err := config.Load(config.DefaultConfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return logs.NewManager(cfg.Logs.Whitelist, cfg.Logs.Blacklist, cfg.Logs.Journald), nil
}

// newLogsListCmd returns the `logs list` subcommand.
func newLogsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all accessible log files",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := loadLogsManager()
			if err != nil {
				return err
			}
			files, err := mgr.ListAccessible()
			if err != nil {
				return fmt.Errorf("listing files: %w", err)
			}
			if len(files) == 0 {
				fmt.Println("No accessible log files found.")
				return nil
			}
			for _, f := range files {
				readable := "readable"
				if !f.Readable {
					readable = "not readable"
				}
				fmt.Printf("%-60s  %8d bytes  %s  %s\n",
					f.Path, f.SizeBytes,
					f.LastModified.Format("2006-01-02 15:04:05"),
					readable,
				)
			}
			return nil
		},
	}
}

// newLogsReadCmd returns the `logs read` subcommand.
func newLogsReadCmd() *cobra.Command {
	var (
		lines  int
		tail   bool
		offset int
		since  string
		until  string
	)

	cmd := &cobra.Command{
		Use:   "read <file>",
		Short: "Read lines from a log file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			mgr, err := loadLogsManager()
			if err != nil {
				return err
			}
			if !mgr.IsAllowed(path) {
				fmt.Fprintf(os.Stderr, "Access denied: %s is not in the whitelist.\n", path)
				os.Exit(1)
			}

			opts := logs.ReadOptions{
				Lines:  lines,
				Tail:   tail,
				Offset: offset,
			}
			if since != "" {
				t, err := logs.ParseTimeOrDuration(since)
				if err != nil {
					return fmt.Errorf("invalid --since: %w", err)
				}
				opts.Since = &t
			}
			if until != "" {
				t, err := logs.ParseTimeOrDuration(until)
				if err != nil {
					return fmt.Errorf("invalid --until: %w", err)
				}
				opts.Until = &t
			}

			result, err := mgr.ReadFile(context.Background(), path, opts)
			if err != nil {
				return fmt.Errorf("reading file: %w", err)
			}
			for _, line := range result {
				fmt.Println(line)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&lines, "lines", 100, "Number of lines to return")
	cmd.Flags().BoolVar(&tail, "tail", false, "Return last N lines instead of first N")
	cmd.Flags().IntVar(&offset, "offset", 0, "Skip this many lines from the start")
	cmd.Flags().StringVar(&since, "since", "", "Return lines after this time (RFC3339 or duration)")
	cmd.Flags().StringVar(&until, "until", "", "Return lines before this time (RFC3339 or duration)")

	return cmd
}

// newLogsSearchCmd returns the `logs search` subcommand.
func newLogsSearchCmd() *cobra.Command {
	var (
		pattern      string
		since        string
		until        string
		maxResults   int
		contextLines int
	)

	cmd := &cobra.Command{
		Use:   "search <file>",
		Short: "Search a log file for lines matching a pattern",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			if pattern == "" {
				return fmt.Errorf("--pattern is required")
			}
			mgr, err := loadLogsManager()
			if err != nil {
				return err
			}
			if !mgr.IsAllowed(path) {
				fmt.Fprintf(os.Stderr, "Access denied: %s is not in the whitelist.\n", path)
				os.Exit(1)
			}

			opts := logs.SearchOptions{
				Pattern:      pattern,
				MaxResults:   maxResults,
				ContextLines: contextLines,
			}
			if since != "" {
				t, err := logs.ParseTimeOrDuration(since)
				if err != nil {
					return fmt.Errorf("invalid --since: %w", err)
				}
				opts.Since = &t
			}
			if until != "" {
				t, err := logs.ParseTimeOrDuration(until)
				if err != nil {
					return fmt.Errorf("invalid --until: %w", err)
				}
				opts.Until = &t
			}

			matches, err := mgr.SearchFile(context.Background(), path, opts)
			if err != nil {
				return fmt.Errorf("searching file: %w", err)
			}
			if len(matches) == 0 {
				fmt.Println("No matches found.")
				return nil
			}
			for _, m := range matches {
				for _, b := range m.Before {
					fmt.Printf("  %s\n", b)
				}
				fmt.Printf("> [%d] %s\n", m.LineNumber, m.Line)
				for _, a := range m.After {
					fmt.Printf("  %s\n", a)
				}
			}
			fmt.Printf("\n%d match(es) found.\n", len(matches))
			return nil
		},
	}

	cmd.Flags().StringVar(&pattern, "pattern", "", "Regular expression to search for (required)")
	cmd.Flags().StringVar(&since, "since", "", "Restrict to lines after this time (RFC3339 or duration)")
	cmd.Flags().StringVar(&until, "until", "", "Restrict to lines before this time (RFC3339 or duration)")
	cmd.Flags().IntVar(&maxResults, "max", 200, "Maximum number of results")
	cmd.Flags().IntVar(&contextLines, "context", 0, "Lines of context around each match")

	return cmd
}

// newLogsInfoCmd returns the `logs info` subcommand.
func newLogsInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <file>",
		Short: "Show metadata for a log file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			mgr, err := loadLogsManager()
			if err != nil {
				return err
			}
			if !mgr.IsAllowed(path) {
				fmt.Fprintf(os.Stderr, "Access denied: %s is not in the whitelist.\n", path)
				os.Exit(1)
			}

			fi, err := mgr.FileInfo(path)
			if err != nil {
				return fmt.Errorf("getting file info: %w", err)
			}

			data, err := json.MarshalIndent(fi, "", "  ")
			if err != nil {
				return fmt.Errorf("serialising info: %w", err)
			}
			fmt.Println(string(data))
			return nil
		},
	}
}
