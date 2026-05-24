package switchboard

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/kleist-dev/logmcp/internal/config"
)

const (
	asteriskLogPath  = "/var/log/asterisk/messages.log"
	asteriskLogTail  = 300
	journalUnit      = "switchboard"
	appLogLimit      = 100
)

// DebugResult is the combined snapshot returned by Debug.
type DebugResult struct {
	CallID      string        `json:"call_id"`
	CDR         *CDR          `json:"cdr"`
	AppLog      []AppLogEntry `json:"app_log"`
	AsteriskLog LogBlock      `json:"asterisk_log"`
	ServiceLog  LogBlock      `json:"service_log"`
}

// CDR mirrors the call_records table row.
type CDR struct {
	CallID    string          `json:"call_id"`
	Called    string          `json:"called"`
	Caller    string          `json:"caller"`
	StartedAt *time.Time      `json:"started_at"`
	EndedAt   *time.Time      `json:"ended_at"`
	DurationS *float64        `json:"duration_s"`
	Plan      string          `json:"plan"`
	Tenant    *string         `json:"tenant"`
	Server    *string         `json:"server"`
	Data      json.RawMessage `json:"data"`
}

// AppLogEntry mirrors one row from the app_log table.
type AppLogEntry struct {
	ID     int             `json:"id"`
	TS     *time.Time      `json:"ts"`
	Level  string          `json:"level"`
	Area   string          `json:"area"`
	Event  string          `json:"event"`
	CallID *string         `json:"call_id"`
	User   *string         `json:"user"`
	Fields json.RawMessage `json:"fields"`
}

// LogBlock is the common envelope for file/command log sources.
type LogBlock struct {
	Lines  []string `json:"lines"`
	Source string   `json:"source,omitempty"`
	Error  *string  `json:"error"`
}

// Debug fetches all five debug sources for the given call_id.
// If callID is empty, the most recent call in call_records is used.
func Debug(cfg *config.Config, callID string) (*DebugResult, error) {
	dsn := findDSN(cfg)
	if dsn == "" {
		return nil, fmt.Errorf("no MySQL server configured under extensions.databases.mysql")
	}

	db, err := sql.Open("mysql", ensureParseTime(dsn))
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()
	db.SetConnMaxLifetime(10 * time.Second)
	db.SetMaxOpenConns(1)

	if callID == "" {
		row := db.QueryRow("SELECT call_id FROM call_records ORDER BY started_at DESC LIMIT 1")
		if err := row.Scan(&callID); err != nil {
			return nil, fmt.Errorf("finding latest call: %w", err)
		}
	}

	result := &DebugResult{CallID: callID}

	cdr, err := queryCDR(db, callID)
	if err != nil {
		return nil, fmt.Errorf("querying CDR: %w", err)
	}
	result.CDR = cdr
	result.AppLog, err = queryAppLog(db, callID)
	if err != nil {
		return nil, fmt.Errorf("querying app_log: %w", err)
	}

	result.AsteriskLog = readAsteriskLog()
	result.ServiceLog = readServiceLog(cdr)

	return result, nil
}

func findDSN(cfg *config.Config) string {
	for _, db := range cfg.Extensions.Databases.MySQL {
		if db.Name == "switchboard" {
			return db.DSN
		}
	}
	if len(cfg.Extensions.Databases.MySQL) > 0 {
		return cfg.Extensions.Databases.MySQL[0].DSN
	}
	return ""
}

func ensureParseTime(dsn string) string {
	if strings.Contains(dsn, "parseTime") {
		return dsn
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&parseTime=true"
	}
	return dsn + "?parseTime=true"
}

func queryCDR(db *sql.DB, callID string) (*CDR, error) {
	row := db.QueryRow(`
		SELECT call_id, called, caller, started_at, ended_at, duration_s, plan, tenant, server, data
		FROM call_records WHERE call_id = ?`, callID)

	var cdr CDR
	var startedAt, endedAt sql.NullTime
	var durationS sql.NullFloat64
	var tenant, srv sql.NullString
	var dataStr string

	err := row.Scan(
		&cdr.CallID, &cdr.Called, &cdr.Caller,
		&startedAt, &endedAt, &durationS,
		&cdr.Plan, &tenant, &srv, &dataStr,
	)
	if err != nil {
		return nil, err
	}

	if startedAt.Valid {
		cdr.StartedAt = &startedAt.Time
	}
	if endedAt.Valid {
		cdr.EndedAt = &endedAt.Time
	}
	if durationS.Valid {
		cdr.DurationS = &durationS.Float64
	}
	if tenant.Valid {
		cdr.Tenant = &tenant.String
	}
	if srv.Valid {
		cdr.Server = &srv.String
	}
	if json.Valid([]byte(dataStr)) {
		cdr.Data = json.RawMessage(dataStr)
	} else {
		cdr.Data, _ = json.Marshal(dataStr)
	}

	return &cdr, nil
}

func queryAppLog(db *sql.DB, callID string) ([]AppLogEntry, error) {
	rows, err := db.Query(`
		SELECT id, ts, level, area, event, call_id, user, fields
		FROM app_log WHERE call_id = ?
		ORDER BY ts DESC LIMIT ?`, callID, appLogLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AppLogEntry
	for rows.Next() {
		var e AppLogEntry
		var ts sql.NullTime
		var callIDNull, userNull sql.NullString
		var fieldsStr string

		if err := rows.Scan(&e.ID, &ts, &e.Level, &e.Area, &e.Event, &callIDNull, &userNull, &fieldsStr); err != nil {
			return nil, err
		}
		if ts.Valid {
			e.TS = &ts.Time
		}
		if callIDNull.Valid {
			e.CallID = &callIDNull.String
		}
		if userNull.Valid {
			e.User = &userNull.String
		}
		if json.Valid([]byte(fieldsStr)) {
			e.Fields = json.RawMessage(fieldsStr)
		} else {
			e.Fields = json.RawMessage(`{}`)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func readAsteriskLog() LogBlock {
	block := LogBlock{Source: asteriskLogPath}
	data, err := os.ReadFile(asteriskLogPath)
	if err != nil {
		msg := err.Error()
		block.Error = &msg
		block.Lines = []string{}
		return block
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > asteriskLogTail {
		lines = lines[len(lines)-asteriskLogTail:]
	}
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, "\r")
	}
	block.Lines = lines
	return block
}

func readServiceLog(cdr *CDR) LogBlock {
	var block LogBlock
	args := []string{"-u", journalUnit, "--no-pager", "-o", "short"}

	if cdr != nil && cdr.StartedAt != nil && cdr.EndedAt != nil {
		since := cdr.StartedAt.Add(-30 * time.Second).Format("2006-01-02 15:04:05")
		until := cdr.EndedAt.Add(30 * time.Second).Format("2006-01-02 15:04:05")
		args = append(args, "--since", since, "--until", until)
	} else {
		args = append(args, "-n", "200")
	}

	out, err := exec.Command("journalctl", args...).Output()
	if err != nil {
		msg := err.Error()
		block.Error = &msg
		block.Lines = []string{}
		return block
	}
	block.Lines = strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	return block
}
