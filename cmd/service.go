package cmd

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/spf13/cobra"
)

const systemdUnitPath = "/etc/systemd/system/logmcp.service"

//go:embed assets/logmcp.service
var systemdUnitContent string

func newServiceCmd() *cobra.Command {
	svcCmd := &cobra.Command{
		Use:   "service",
		Short: "Manage the LogMCP systemd service",
	}
	svcCmd.AddCommand(newServiceInstallCmd())
	svcCmd.AddCommand(newServiceRemoveCmd())
	svcCmd.AddCommand(newServiceStatusCmd())
	svcCmd.AddCommand(newServiceCaddySnippetCmd())
	return svcCmd
}

func newServiceInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the systemd service unit",
		RunE:  runServiceInstall,
	}
}

func runServiceInstall(cmd *cobra.Command, args []string) error {
	isRoot := os.Getuid() == 0

	if !isRoot {
		fmt.Println("Not running as root. Here is the systemd unit file — install it manually:")
		fmt.Println()
		fmt.Print(systemdUnitContent)
		fmt.Println("Then run:")
		fmt.Println("  sudo cp logmcp.service /etc/systemd/system/")
		fmt.Println("  sudo systemctl daemon-reload")
		fmt.Println("  sudo systemctl enable --now logmcp")
		return nil
	}

	if err := os.WriteFile(systemdUnitPath, []byte(systemdUnitContent), 0o644); err != nil {
		return fmt.Errorf("writing unit file to %s: %w", systemdUnitPath, err)
	}
	fmt.Printf("Unit file written to %s\n", systemdUnitPath)

	run("systemctl", "daemon-reload")
	run("systemctl", "enable", "logmcp")
	startOut, startErr := exec.Command("systemctl", "start", "logmcp").CombinedOutput()
	if len(startOut) > 0 {
		fmt.Print(string(startOut))
	}
	if startErr != nil {
		fmt.Fprintln(os.Stderr, "Warning: service failed to start. Check: journalctl -u logmcp -n 50")
	} else {
		fmt.Println("Service enabled and started.")
	}
	return nil
}

func newServiceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove the systemd service unit",
		RunE:  runServiceRemove,
	}
}

func runServiceRemove(cmd *cobra.Command, args []string) error {
	if os.Getuid() != 0 {
		fmt.Fprintln(os.Stderr, "This command requires root. Run with sudo.")
		os.Exit(1)
	}

	run("systemctl", "stop", "logmcp")
	run("systemctl", "disable", "logmcp")

	if err := os.Remove(systemdUnitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing unit file: %w", err)
	}
	run("systemctl", "daemon-reload")
	fmt.Println("Service removed.")
	return nil
}

func newServiceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show systemd service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := exec.Command("systemctl", "status", "logmcp").CombinedOutput()
			fmt.Print(string(out))
			if err != nil {
				// systemctl exits 3 for inactive; not a fatal error for display.
				_ = err
			}
			return nil
		},
	}
}

func newServiceCaddySnippetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "caddy-snippet",
		Short: "Print a Caddyfile snippet for reverse proxy configuration",
		RunE:  runServiceCaddySnippet,
	}
}

func runServiceCaddySnippet(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(config.DefaultConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	printCaddySnippet(cfg)
	return nil
}

// printCaddySnippet prints Caddy reverse proxy configuration.
func printCaddySnippet(cfg *config.Config) {
	port := cfg.Server.Port
	domain := cfg.Proxy.Domain
	if domain == "" {
		domain = "logs.example.com"
	}

	backendAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	prefix := strings.TrimRight(cfg.Proxy.PathPrefix, "/")

	fmt.Println("Place the following in your Caddyfile, then run: sudo systemctl reload caddy")
	fmt.Println()

	if prefix == "" {
		// Subdomain variant.
		fmt.Printf("%s {\n", domain)
		fmt.Printf("    reverse_proxy /mcp* %s\n", backendAddr)
		fmt.Println("}")
	} else {
		// Subpath variant.
		fmt.Printf("%s {\n", domain)
		fmt.Printf("    reverse_proxy %s/mcp* %s\n", prefix, backendAddr)
		fmt.Println("}")
	}

	fmt.Println()
	fmt.Println("Note: Caddy will automatically obtain and renew a TLS certificate via Let's Encrypt.")
}

// run executes a command and prints its output. Ignores errors (best-effort).
func run(name string, args ...string) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if len(out) > 0 {
		fmt.Print(string(out))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s %v: %v\n", name, args, err)
	}
}

// writeSystemdUnit writes the unit file (used from setup.go).
func writeSystemdUnit() error {
	if os.Getuid() != 0 {
		return fmt.Errorf("not running as root")
	}
	return os.WriteFile(systemdUnitPath, []byte(systemdUnitContent), 0o644)
}
