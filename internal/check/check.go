package check

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kleist-dev/logmcp/internal/config"
)

// Result is the JSON-serialisable output of Run.
type Result struct {
	OK     bool   `json:"ok"`
	Checks []Item `json:"checks"`
}

// Item represents a single check.
type Item struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Options controls which checks Run performs.
type Options struct {
	// ConfigPath is shown in the "Config file" item for display purposes.
	ConfigPath string
	// IncludePort checks whether the configured port is free.
	// Set to false when called from a running server (port is in use by design).
	IncludePort bool
}

// Run performs all environment checks and returns a structured result.
// cfg must already be loaded by the caller.
func Run(cfg *config.Config, opts Options) Result {
	r := Result{OK: true}

	add := func(name string, ok bool, detail string) {
		if !ok {
			r.OK = false
		}
		r.Checks = append(r.Checks, Item{Name: name, OK: ok, Detail: detail})
	}

	// Config loaded (always ok since caller provides a valid cfg).
	add("Config file", true, opts.ConfigPath)

	// At least one token configured.
	add("auth.tokens configured", len(cfg.Auth.Tokens) > 0, fmt.Sprintf("%d token(s)", len(cfg.Auth.Tokens)))

	// At least one whitelist entry.
	add("logs.whitelist has at least one entry", len(cfg.Logs.Whitelist) > 0, "")

	// TLS cert (if not mode=off).
	if cfg.Server.TLS.Mode != "off" {
		ok, detail := checkCert(cfg.Server.TLS.Cert, cfg.Server.TLS.Key)
		add("TLS certificate valid and not expired", ok, detail)
	} else {
		add("TLS mode is off (no cert check)", true, "")
	}

	// Proxy domain set if proxy enabled.
	if cfg.Proxy.Enabled {
		add("proxy.domain is set", cfg.Proxy.Domain != "", "")
	}

	// Whitelist paths exist and are readable.
	for _, pattern := range cfg.Logs.Whitelist {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			add(fmt.Sprintf("glob %q", pattern), false, err.Error())
			continue
		}
		if len(matches) == 0 {
			add(fmt.Sprintf("glob %q", pattern), false, "no files matched")
			continue
		}
		allReadable := true
		for _, m := range matches {
			f, err := os.Open(m)
			if err != nil {
				allReadable = false
				break
			}
			f.Close()
		}
		add(fmt.Sprintf("glob %q (%d files)", pattern, len(matches)), allReadable, "")
	}

	// Port reachable (service is running and accepting connections).
	if opts.IncludePort {
		addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		reachable := err == nil
		if reachable {
			conn.Close()
		}
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		add(fmt.Sprintf("Port %d reachable", cfg.Server.Port), reachable, detail)
	}

	// systemd service status (skipped if systemctl is not available).
	for _, item := range checkSystemdService("logmcp") {
		if !item.OK {
			r.OK = false
		}
		r.Checks = append(r.Checks, item)
	}

	// Syslog reachable.
	add("Syslog reachable", checkSyslog(), "")

	// MySQL servers (if configured).
	for _, db := range cfg.Extensions.Databases.MySQL {
		addr, err := mysqlAddr(db.DSN)
		if err != nil {
			add(fmt.Sprintf("MySQL %s", db.Name), false, fmt.Sprintf("could not parse DSN: %v", err))
			continue
		}
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			add(fmt.Sprintf("MySQL %s (%s)", db.Name, addr), false, err.Error())
		} else {
			conn.Close()
			add(fmt.Sprintf("MySQL %s (%s)", db.Name, addr), true, "")
		}
	}

	return r
}

func checkCert(certPath, keyPath string) (bool, string) {
	_, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return false, err.Error()
	}

	data, err := os.ReadFile(certPath)
	if err != nil {
		return false, err.Error()
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false, "could not decode PEM"
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err.Error()
	}

	if time.Now().After(cert.NotAfter) {
		return false, fmt.Sprintf("expired on %s", cert.NotAfter.Format(time.RFC3339))
	}
	return true, fmt.Sprintf("expires %s", cert.NotAfter.Format("2006-01-02"))
}

func checkSyslog() bool {
	if _, err := os.Stat("/dev/log"); err == nil {
		conn, err := net.Dial("unixgram", "/dev/log")
		if err == nil {
			conn.Close()
			return true
		}
	}
	conn, err := net.Dial("udp", "127.0.0.1:514")
	if err == nil {
		conn.Close()
		return true
	}
	return false
}

// checkSystemdService returns active/enabled items for a systemd unit.
// Returns nil if systemctl is not available (non-systemd systems are silently skipped).
func checkSystemdService(name string) []Item {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil
	}
	activeOut, _ := exec.Command("systemctl", "is-active", name).Output()
	activeState := strings.TrimSpace(string(activeOut))
	if activeState == "" {
		activeState = "unknown"
	}

	enabledOut, _ := exec.Command("systemctl", "is-enabled", name).Output()
	enabledState := strings.TrimSpace(string(enabledOut))
	if enabledState == "" {
		enabledState = "unknown"
	}

	return []Item{
		{Name: fmt.Sprintf("systemd %s active", name), OK: activeState == "active", Detail: activeState},
		{Name: fmt.Sprintf("systemd %s enabled", name), OK: enabledState == "enabled", Detail: enabledState},
	}
}

// mysqlAddr extracts a "host:port" address from a Go sql-driver DSN.
// Supported formats:
//   - user:pass@tcp(host:port)/dbname
//   - user:pass@tcp(host)/dbname  → port 3306
//   - user:pass@host:port/dbname
func mysqlAddr(dsn string) (string, error) {
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return "", fmt.Errorf("missing @ in DSN")
	}
	rest := dsn[at+1:]
	if strings.HasPrefix(rest, "tcp(") {
		end := strings.Index(rest, ")")
		if end < 0 {
			return "", fmt.Errorf("missing closing ) in tcp() DSN")
		}
		addr := rest[4:end]
		if !strings.Contains(addr, ":") {
			addr += ":3306"
		}
		return addr, nil
	}
	if slash := strings.Index(rest, "/"); slash >= 0 {
		rest = rest[:slash]
	}
	if !strings.Contains(rest, ":") {
		rest += ":3306"
	}
	return rest, nil
}
