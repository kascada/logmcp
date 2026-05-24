# list_logs

List all log files the server has been configured to expose. Returns an array of file entries ordered by path.

## When to use

Call this first to discover which log files are available before calling `read_log`, `search_log`, or `log_info`. If you do not know the exact path of a log file, always call `list_logs` first.

## Response

Array of objects, each with:

- `path` — absolute path on the server
- `size` — file size in bytes
- `modified` — last-modified timestamp (RFC3339)
