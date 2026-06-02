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
	"strconv"
	"strings"
	"time"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/database"
	"github.com/kleist-dev/logmcp/internal/extensions/clitool"
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
	// DBPool, when non-nil, is used to check configured database connections.
	// Pass nil to skip database checks (e.g. from the CLI before the server starts).
	DBPool *database.Pool
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

	// Auth configured: either static tokens or an external authenticator.
	if cfg.Auth.Authenticator != nil {
		add("auth configured", true, fmt.Sprintf("authenticator: %s", cfg.Auth.Authenticator.Command))
	} else {
		add("auth configured", len(cfg.Auth.Tokens) > 0, fmt.Sprintf("%d token(s)", len(cfg.Auth.Tokens)))
	}

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
		readable := 0
		for _, m := range matches {
			f, err := os.Open(m)
			if err == nil {
				_ = f.Close()
				readable++
			}
		}
		isWildcard := strings.ContainsAny(pattern, "*?[")
		var ok bool
		var detail string
		if isWildcard {
			// For wildcard patterns some files (e.g. binary btmp, root-only logs) may
			// not be readable; the server skips them at runtime. Pass if at least one
			// file is accessible.
			ok = readable > 0
			detail = fmt.Sprintf("%d/%d readable", readable, len(matches))
		} else {
			ok = readable == len(matches)
		}
		add(fmt.Sprintf("glob %q (%d files)", pattern, len(matches)), ok, detail)
	}

	// Port reachable (service is running and accepting connections).
	if opts.IncludePort {
		addr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		reachable := err == nil
		if reachable {
			_ = conn.Close()
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

	// Clitool extensions (if configured).
	for _, ext := range cfg.Extensions.Clitool {
		tools, err := clitool.List(ext.Command, 5*time.Second)
		if err != nil {
			add(fmt.Sprintf("extension %q accessible", ext.Name), false, err.Error())
		} else {
			add(fmt.Sprintf("extension %q accessible", ext.Name), true, fmt.Sprintf("%d tool(s)", len(tools)))
		}
	}

	// Database connections (if configured and pool is available).
	if opts.DBPool != nil {
		for _, db := range cfg.Databases {
			result := database.Check(opts.DBPool, db.Name)
			checkName := fmt.Sprintf("database %q reachable", db.Name)
			if result.OK {
				add(checkName, true, result.Version)
			} else {
				add(checkName, false, result.Detail)
			}
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
			_ = conn.Close()
			return true
		}
	}
	conn, err := net.Dial("udp", "127.0.0.1:514")
	if err == nil {
		_ = conn.Close()
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

