# search_log

Search a log file for lines matching a regular expression. Returns matching lines with optional surrounding context.

## When to use

Use when you need to find specific events, errors, or patterns in a log file without reading the whole file. More efficient than `read_log` for targeted searches. Combine with `since`/`until` to scope the search to a time window.

## Parameters

### path
Absolute path to the log file. Obtain valid paths from `list_logs`.

### pattern
Regular expression to search for. Uses Go regexp syntax. The pattern is not echoed back in the response.

### since
Restrict the search to lines after this point in time. Accepts RFC3339 timestamps or relative durations (`1h`, `30m`).

### until
Restrict the search to lines before this point in time. Same format as `since`.

### max_results
Maximum number of matching lines to return. Default: 200.

### context_lines
Number of surrounding lines to include before and after each match. Default: 0 (match lines only).

## Response

- `path` — file path that was searched
- `pattern_redacted` — always `"<redacted>"` (the search pattern is not echoed back for security reasons)
- `matches` — array of match objects, each with:
  - `line` — the matching log line
  - `line_number` — 1-based line number in the file
  - `context_before` / `context_after` — surrounding lines (only present if `context_lines > 0`)
- `count` — total number of matches returned
