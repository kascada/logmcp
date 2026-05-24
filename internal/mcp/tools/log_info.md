# log_info

Return metadata for a single log file: size, line count, and last-modified timestamp.

## When to use

Use to check whether a log file has changed recently, or to determine its total size and line count before deciding how many lines to read with `read_log`.

## Parameters

### path
Absolute path to the log file. Obtain valid paths from `list_logs`.

## Response

- `path` — file path
- `size` — file size in bytes
- `lines` — total number of lines in the file
- `modified` — last-modified timestamp (RFC3339)
