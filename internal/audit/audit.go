package audit

import (
	"fmt"
	"log/syslog"
	"os"
	"sync"
)

var (
	writer   *syslog.Writer
	initOnce sync.Once
)

func openSyslog() {
	initOnce.Do(func() {
		w, err := syslog.New(syslog.LOG_INFO|syslog.LOG_DAEMON, "logmcp")
		if err == nil {
			writer = w
		}
	})
}

// logLine writes a line to syslog if available, otherwise to stderr.
func logLine(line string) error {
	openSyslog()
	if writer != nil {
		return writer.Info(line)
	}
	// Fallback: write to stderr with PID prefix to match syslog format.
	_, err := fmt.Fprintf(os.Stderr, "logmcp[%d]: %s\n", os.Getpid(), line)
	return err
}

// LogAuthFailed records a failed authentication attempt.
func LogAuthFailed(clientIP, reason string) error {
	return logLine(fmt.Sprintf("auth_failed client=%s reason=%s", clientIP, reason))
}

// Log records a successful tool access.
func Log(tool, path, clientIP string) error {
	return logLine(fmt.Sprintf("access tool=%s path=%s client=%s", tool, path, clientIP))
}

// LogDenied records a denied access attempt.
func LogDenied(path, clientIP, reason string) error {
	return logLine(fmt.Sprintf("denied path=%s client=%s reason=%s", path, clientIP, reason))
}

// LogSearch records a search access (pattern is redacted).
func LogSearch(path, clientIP string) error {
	return logLine(fmt.Sprintf("access tool=search_log path=%s pattern=<redacted> client=%s", path, clientIP))
}
