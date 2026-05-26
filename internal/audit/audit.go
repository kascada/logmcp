package audit

import (
	"fmt"
	"log/syslog"
	"os"
	"sync"
)

// Logger kapselt den Syslog-Writer und seinen Initialisierungszustand.
type Logger struct {
	mu     sync.Mutex
	writer *syslog.Writer
	once   sync.Once
}

// New returns a new Logger. The syslog connection is opened lazily on first use.
func New() *Logger {
	return &Logger{}
}

var defaultLogger = &Logger{}

// open initializes the syslog connection on first use.
func (l *Logger) open() {
	l.once.Do(func() {
		w, err := syslog.New(syslog.LOG_INFO|syslog.LOG_DAEMON, "logmcp")
		if err == nil {
			l.mu.Lock()
			l.writer = w
			l.mu.Unlock()
		}
	})
}

// logLine writes a line to syslog if available, otherwise to stderr.
func (l *Logger) logLine(line string) error {
	l.open()
	l.mu.Lock()
	w := l.writer
	l.mu.Unlock()
	if w != nil {
		return w.Info(line)
	}
	// Fallback: write to stderr with PID prefix to match syslog format.
	_, err := fmt.Fprintf(os.Stderr, "logmcp[%d]: %s\n", os.Getpid(), line)
	return err
}

// LogAuthFailed records a failed authentication attempt.
func (l *Logger) LogAuthFailed(clientIP, reason string) error {
	return l.logLine(fmt.Sprintf("auth_failed client=%s reason=%s", clientIP, reason))
}

// Log records a successful tool access.
func (l *Logger) Log(tool, path, clientIP string) error {
	return l.logLine(fmt.Sprintf("access tool=%s path=%s client=%s", tool, path, clientIP))
}

// LogDenied records a denied access attempt.
func (l *Logger) LogDenied(path, clientIP, reason string) error {
	return l.logLine(fmt.Sprintf("denied path=%s client=%s reason=%s", path, clientIP, reason))
}

// LogSearch records a search access (pattern is redacted).
func (l *Logger) LogSearch(path, clientIP string) error {
	return l.logLine(fmt.Sprintf("access tool=search_log path=%s pattern=<redacted> client=%s", path, clientIP))
}

// Package-level convenience wrappers that delegate to defaultLogger.

// LogAuthFailed records a failed authentication attempt.
func LogAuthFailed(clientIP, reason string) error {
	return defaultLogger.LogAuthFailed(clientIP, reason)
}

// Log records a successful tool access.
func Log(tool, path, clientIP string) error {
	return defaultLogger.Log(tool, path, clientIP)
}

// LogDenied records a denied access attempt.
func LogDenied(path, clientIP, reason string) error {
	return defaultLogger.LogDenied(path, clientIP, reason)
}

// LogSearch records a search access (pattern is redacted).
func LogSearch(path, clientIP string) error {
	return defaultLogger.LogSearch(path, clientIP)
}
