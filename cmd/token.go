package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// configPath is the path to the config file used by loadConfigRaw and saveConfig.
// It is a variable (not a constant) so that tests can redirect writes to a temp file.
var configPath = config.DefaultConfigPath

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage bearer tokens",
	}
	cmd.AddCommand(newTokenListCmd())
	cmd.AddCommand(newTokenAddCmd())
	cmd.AddCommand(newTokenRemoveCmd())
	cmd.AddCommand(newTokenRenewCmd())
	return cmd
}

func newTokenListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configured tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(config.DefaultConfigPath)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSCOPES\tTOKEN")
			for _, t := range cfg.Auth.Tokens {
				fmt.Fprintf(w, "%s\t%s\t%s\n", t.Name, strings.Join(t.Scopes, ","), maskToken(t.Token))
			}
			return w.Flush()
		},
	}
}

func newTokenAddCmd() *cobra.Command {
	var name, scopesFlag string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new bearer token",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			scopes := parseScopes(scopesFlag)
			if len(scopes) == 0 {
				scopes = []string{"logmcp:read"}
			}

			cfg, err := loadConfigRaw()
			if err != nil {
				return err
			}

			if cfg.Auth.Find(name) != nil {
				return fmt.Errorf("token %q already exists; use 'logmcp token renew %s' to replace it", name, name)
			}

			tokenVal := uuid.NewString()
			cfg.Auth.Tokens = append(cfg.Auth.Tokens, config.TokenConfig{
				Name:   name,
				Token:  tokenVal,
				Scopes: scopes,
			})

			if err := saveConfig(cfg); err != nil {
				return err
			}

			fmt.Printf("Token added.\n\n")
			fmt.Printf("  Name:   %s\n", name)
			fmt.Printf("  Scopes: %s\n", strings.Join(scopes, ","))
			fmt.Printf("  Token:  %s\n\n", tokenVal)
			fmt.Println("Store this token securely — it will not be shown again.")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Token name (required)")
	cmd.Flags().StringVar(&scopesFlag, "scopes", "logmcp:read", "Comma-separated scopes (e.g. logmcp:read,logmcp:admin)")
	return cmd
}

func newTokenRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a token by name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := loadConfigRaw()
			if err != nil {
				return err
			}

			before := len(cfg.Auth.Tokens)
			filtered := cfg.Auth.Tokens[:0]
			for _, t := range cfg.Auth.Tokens {
				if t.Name != name {
					filtered = append(filtered, t)
				}
			}
			if len(filtered) == before {
				return fmt.Errorf("token %q not found", name)
			}
			if len(filtered) == 0 {
				return fmt.Errorf("cannot remove the last token — add a new one first")
			}
			cfg.Auth.Tokens = filtered

			if err := saveConfig(cfg); err != nil {
				return err
			}
			fmt.Printf("Token %q removed.\n", name)
			return nil
		},
	}
}

func newTokenRenewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "renew <name>",
		Short: "Generate a new token value for an existing entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := loadConfigRaw()
			if err != nil {
				return err
			}

			t := cfg.Auth.Find(name)
			if t == nil {
				return fmt.Errorf("token %q not found", name)
			}

			tokenVal := uuid.NewString()
			t.Token = tokenVal

			if err := saveConfig(cfg); err != nil {
				return err
			}

			fmt.Printf("Token %q renewed.\n\n", name)
			fmt.Printf("  New token: %s\n\n", tokenVal)
			fmt.Println("Store this token securely — it will not be shown again.")
			fmt.Println("Update any clients that use this token.")
			return nil
		},
	}
}

// loadConfigRaw loads the config without validation so we can modify it freely.
func loadConfigRaw() (*config.Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	cfg := config.Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

// saveConfig writes cfg back to the default config path with correct permissions.
func saveConfig(cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0o640); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	_ = exec.Command("chown", "root:logmcp", configPath).Run()
	config.BackfillComments(configPath)
	return nil
}

// parseScopes splits a comma-separated scope string and trims whitespace.
func parseScopes(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
