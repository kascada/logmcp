# read_log

Read lines from a log file. Supports reading from the beginning or end, with optional time-based filtering.

## When to use

Use to inspect a specific portion of a log file. Use `tail: true` for the most recent entries. Use `since`/`until` to narrow to a specific time window. For targeted pattern matching, prefer `search_log` instead.

## Parameters

### path
Absolute path to the log file. Obtain valid paths from `list_logs`.

### lines
Number of lines to return. Defaults to 100.

### tail
If true, return the last N lines instead of the first N. Useful for checking recent log activity. Default: false.

### offset
Skip this many lines from the start (or from the end if `tail=true`). Use for pagination through large files.

### since
Return only lines after this point in time. Accepts RFC3339 timestamps (`2024-01-15T10:00:00Z`) or relative durations (`1h`, `30m`, `2h30m`). Relative durations are resolved against the current server time.

### until
Return only lines before this point in time. Same format as `since`.

## Response

- `path` — file path that was read
- `lines` — array of log lines (strings)
- `count` — number of lines returned
