# log_info

Return metadata for a single log file: size, line count, and last-modified timestamp.

## When to use

Use to check whether a log file has changed recently, or to determine its total size and line count before deciding how many lines to read with `read_log`.

## Parameters

### path
Absolute path to the log file. Obtain valid paths from `list_logs`.

## Response

- `path` — file path
- `size_bytes` — file size in bytes
- `line_count` — total number of lines in the file
- `last_modified` — last-modified timestamp (RFC3339)
- `readable` — whether the file is accessible by the server process
