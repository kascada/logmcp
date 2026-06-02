package database

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// CheckResult holds the outcome of a database connectivity check.
type CheckResult struct {
	OK      bool
	Version string // MySQL server version on success
	Detail  string // Human-readable error message on failure
}

// Check pings the database on pool entry name and returns a structured result
// with a precise error message when the connection fails.
func Check(pool *Pool, name string) CheckResult {
	db, err := pool.Get(name)
	if err != nil {
		return CheckResult{Detail: err.Error()}
	}

	// Use QueryRow to retrieve the server version; this exercises the full
	// handshake including authentication.
	var version string
	err = db.QueryRow("SELECT VERSION()").Scan(&version)
	if err == nil {
		return CheckResult{OK: true, Version: version}
	}

	return CheckResult{Detail: classifyError(err)}
}

// classifyError returns a short, human-readable explanation for common MySQL
// connection errors.
func classifyError(err error) string {
	if err == nil {
		return ""
	}

	// MySQL protocol errors.
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		switch myErr.Number {
		case 1045: // ER_ACCESS_DENIED_ERROR
			return "authentication failed (wrong password?)"
		case 1049: // ER_BAD_DB_ERROR
			return fmt.Sprintf("unknown database %q", extractDBName(myErr.Message))
		}
		return myErr.Error()
	}

	// Network-level errors (TCP not reachable, refused, etc.).
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return fmt.Sprintf("cannot connect to host: %v", netErr.Err)
	}

	// Fallback.
	return err.Error()
}

// extractDBName tries to pull the database name from a MySQL "Unknown database 'xyz'" message.
func extractDBName(msg string) string {
	// Message format: "Unknown database 'xyz'"
	start := strings.Index(msg, "'")
	if start < 0 {
		return "?"
	}
	end := strings.LastIndex(msg, "'")
	if end <= start {
		return "?"
	}
	return msg[start+1 : end]
}
