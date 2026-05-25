package macro

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/logs"
)

const stepTimeout = 30 * time.Second

// stepContext returns a child context with the standard step timeout applied.
func stepContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, stepTimeout)
}

// execDBQuery executes a db_query step.
// The SQL query template is resolved for bind parameters (not string substitution).
// Returns a []map[string]any with all rows.
func execDBQuery(ctx context.Context, step StepDef, params map[string]string, stepResults map[string]any, cfg *config.Config) (any, error) {
	ctx, cancel := stepContext(ctx)
	defer cancel()

	// Resolve database reference.
	dbName, _ := step.Args["database"].(string)
	dsn := findDSN(cfg, dbName)
	if dsn == "" {
		return nil, fmt.Errorf("no MySQL connection found for database %q", dbName)
	}

	// Resolve SQL with bind parameters.
	sqlTmpl, _ := step.Args["sql"].(string)
	if sqlTmpl == "" {
		return nil, fmt.Errorf("db_query step %q: missing 'sql' arg", step.ID)
	}
	query, bindArgs := interpolateForSQL(sqlTmpl, params, stepResults)

	db, err := sql.Open("mysql", ensureParseTime(dsn))
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()
	db.SetConnMaxLifetime(10 * time.Second)
	db.SetMaxOpenConns(1)

	rows, err := db.QueryContext(ctx, query, bindArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying database: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("reading columns: %w", err)
	}

	var result []map[string]any
	for rows.Next() {
		// Create a slice of any pointers for scanning.
		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		row := make(map[string]any, len(cols))
		for i, col := range cols {
			val := values[i]
			// MySQL driver returns []byte for text columns; convert to string.
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			row[col] = val
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	if result == nil {
		result = []map[string]any{}
	}
	return result, nil
}

// execReadFile executes a read_file step, respecting logs.Manager access control.
func execReadFile(ctx context.Context, step StepDef, params map[string]string, stepResults map[string]any, logMgr *logs.Manager) (any, error) {
	ctx, cancel := stepContext(ctx)
	defer cancel()

	resolvedArgs := interpolateArgs(step.Args, params, stepResults)

	path, _ := resolvedArgs["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("read_file step %q: missing 'path' arg", step.ID)
	}

	// Access control: same rules as the read_log MCP tool.
	if !logMgr.IsAllowed(path) {
		return nil, fmt.Errorf("access denied: %s is not in the whitelist", path)
	}

	opts := logs.ReadOptions{}

	if tail, ok := resolvedArgs["tail"]; ok {
		switch v := tail.(type) {
		case bool:
			opts.Tail = v
		case string:
			opts.Tail = v == "true"
		}
	}

	if linesVal, ok := resolvedArgs["lines"]; ok {
		switch v := linesVal.(type) {
		case int:
			opts.Lines = v
		case float64:
			opts.Lines = int(v)
		case string:
			var n int
			if _, err := fmt.Sscan(v, &n); err == nil {
				opts.Lines = n
			}
		}
	}
	if opts.Lines <= 0 {
		opts.Lines = 100
	}

	lines, err := logMgr.ReadFile(ctx, path, opts)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	return map[string]any{
		"path":  path,
		"lines": lines,
		"count": len(lines),
	}, nil
}

// execJournalctl executes a journalctl step.
func execJournalctl(ctx context.Context, step StepDef, params map[string]string, stepResults map[string]any) (any, error) {
	ctx, cancel := stepContext(ctx)
	defer cancel()

	resolvedArgs := interpolateArgs(step.Args, params, stepResults)

	unit, _ := resolvedArgs["unit"].(string)

	args := []string{"--no-pager", "--output=short-iso"}
	if unit != "" {
		args = append(args, "--unit="+unit)
	}

	// around + window_s: compute since/until as [around - window_s, around + window_s].
	if aroundStr, ok := resolvedArgs["around"].(string); ok && aroundStr != "" {
		aroundTime, err := parseFlexibleTime(aroundStr)
		if err == nil {
			windowS := 30.0
			if ws, ok := resolvedArgs["window_s"]; ok {
				switch v := ws.(type) {
				case float64:
					windowS = v
				case int:
					windowS = float64(v)
				case string:
					var n float64
					if _, err := fmt.Sscan(v, &n); err == nil {
						windowS = n
					}
				}
			}
			window := time.Duration(windowS) * time.Second
			since := aroundTime.Add(-window)
			until := aroundTime.Add(window)
			args = append(args,
				"--since="+since.Format("2006-01-02 15:04:05"),
				"--until="+until.Format("2006-01-02 15:04:05"),
			)
		}
	} else {
		// No around: fall back to since/until or tail.
		if sinceStr, ok := resolvedArgs["since"].(string); ok && sinceStr != "" {
			args = append(args, "--since="+sinceStr)
		}
		if untilStr, ok := resolvedArgs["until"].(string); ok && untilStr != "" {
			args = append(args, "--until="+untilStr)
		}
		if _, hasAround := resolvedArgs["around"]; !hasAround {
			// Default: last 200 lines.
			args = append(args, "-n", "200")
		}
	}

	out, err := exec.CommandContext(ctx, "journalctl", args...).Output()
	if err != nil {
		// journalctl exits non-zero when no entries match; treat as empty.
		errMsg := err.Error()
		return map[string]any{
			"lines":  []string{},
			"error":  errMsg,
			"source": "journald://" + unit,
		}, nil
	}

	lines := splitLines(out)
	return map[string]any{
		"lines":  lines,
		"source": "journald://" + unit,
	}, nil
}

// findDSN returns the DSN for the named MySQL connection.
// Falls back to the first configured connection when name is empty or not found
// (if there is exactly one connection configured).
func findDSN(cfg *config.Config, name string) string {
	if name != "" {
		for _, db := range cfg.Extensions.Databases.MySQL {
			if db.Name == name {
				return db.DSN
			}
		}
	}
	// Fallback: use first entry if only one is configured.
	if len(cfg.Extensions.Databases.MySQL) == 1 {
		return cfg.Extensions.Databases.MySQL[0].DSN
	}
	if name == "" && len(cfg.Extensions.Databases.MySQL) > 0 {
		return cfg.Extensions.Databases.MySQL[0].DSN
	}
	return ""
}

// ensureParseTime appends parseTime=true to the DSN if not already present.
func ensureParseTime(dsn string) string {
	if strings.Contains(dsn, "parseTime") {
		return dsn
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&parseTime=true"
	}
	return dsn + "?parseTime=true"
}

// splitLines splits command output into trimmed lines, dropping a trailing blank.
func splitLines(out []byte) []string {
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

// parseFlexibleTime tries to parse a time string in multiple formats.
// Accepts RFC3339, "2006-01-02 15:04:05", and relative durations.
func parseFlexibleTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}
	// Try logs.ParseTimeOrDuration first (RFC3339 + relative).
	if t, err := logs.ParseTimeOrDuration(s); err == nil {
		return t, nil
	}
	// Try MySQL-style datetime.
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}
