package cmd

import (
	"fmt"
	"os"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/security"
	"github.com/spf13/cobra"
)

func newSecurityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "security",
		Short: "Security utilities",
	}
	cmd.AddCommand(newInstallFail2banCmd())
	return cmd
}

func newInstallFail2banCmd() *cobra.Command {
	var filterDir, jailDir string
	var reload bool

	c := &cobra.Command{
		Use:   "install-fail2ban",
		Short: "Install fail2ban filter and jail config for logmcp",
		Long: `Writes the embedded fail2ban filter and jail configuration files to the
fail2ban config directories. Run this once after installing logmcp to enable
automatic IP banning on repeated authentication failures.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Read config for custom dirs, but flags take precedence.
			if cfg, err := config.Load(config.DefaultConfigPath); err == nil {
				if !cfg.Security.Fail2ban.Enabled {
					fmt.Fprintln(os.Stderr, "fail2ban integration is disabled in config (security.fail2ban.enabled: false)")
					return nil
				}
				if filterDir == "" {
					filterDir = cfg.Security.Fail2ban.FilterDir
				}
				if jailDir == "" {
					jailDir = cfg.Security.Fail2ban.JailDir
				}
			}

			if err := security.InstallFail2ban(filterDir, jailDir); err != nil {
				return err
			}

			fd := filterDir
			if fd == "" {
				fd = "/etc/fail2ban/filter.d"
			}
			jd := jailDir
			if jd == "" {
				jd = "/etc/fail2ban/jail.d"
			}
			fmt.Printf("✓ %s/logmcp.conf written\n", fd)
			fmt.Printf("✓ %s/logmcp.conf written\n", jd)

			if reload {
				if !security.Available() {
					fmt.Fprintln(os.Stderr, "Warning: fail2ban-client not found, skipping reload")
					return nil
				}
				if err := security.ReloadFail2ban(); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: fail2ban-client reload: %v\n", err)
				} else {
					fmt.Println("✓ fail2ban reloaded")
				}
			}
			return nil
		},
	}

	c.Flags().StringVar(&filterDir, "filter-dir", "", "fail2ban filter.d directory (default: /etc/fail2ban/filter.d)")
	c.Flags().StringVar(&jailDir, "jail-dir", "", "fail2ban jail.d directory (default: /etc/fail2ban/jail.d)")
	c.Flags().BoolVar(&reload, "reload", false, "Run fail2ban-client reload after installing")
	return c
}
