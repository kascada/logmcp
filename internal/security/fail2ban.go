package security

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed assets/filter.conf
var filterConf []byte

//go:embed assets/jail.conf
var jailConf []byte

const (
	defaultFilterDir = "/etc/fail2ban/filter.d"
	defaultJailDir   = "/etc/fail2ban/jail.d"
)

// InstallFail2ban writes the embedded filter and jail configs to the given
// directories. Empty strings use the standard fail2ban paths.
func InstallFail2ban(filterDir, jailDir string) error {
	if filterDir == "" {
		filterDir = defaultFilterDir
	}
	if jailDir == "" {
		jailDir = defaultJailDir
	}

	for _, dir := range []string{filterDir, jailDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	if err := os.WriteFile(filepath.Join(filterDir, "logmcp.conf"), filterConf, 0o644); err != nil {
		return fmt.Errorf("writing filter config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(jailDir, "logmcp.conf"), jailConf, 0o644); err != nil {
		return fmt.Errorf("writing jail config: %w", err)
	}
	return nil
}

// ReloadFail2ban runs fail2ban-client reload.
func ReloadFail2ban() error {
	return exec.Command("fail2ban-client", "reload").Run()
}

// Available reports whether fail2ban-client is installed.
func Available() bool {
	_, err := exec.LookPath("fail2ban-client")
	return err == nil
}
