# switchboard_debug

Fetch a combined debug snapshot for a single Switchboard call. Collects data from five independent sources in one request and returns them as a single JSON object.

## When to use

Use when a Switchboard call has failed or behaved unexpectedly and you need to diagnose the cause. Provide the `call_id` if known. If omitted, the most recently started call in the database is used — useful for investigating the last call without needing to look up its ID first.

## Parameters

### call_id
The Switchboard call ID to inspect. Optional — if omitted, the call with the most recent `started_at` timestamp in `call_records` is used.

## Response

- `call_id` — the call ID that was inspected

- `cdr` — Call Detail Record from the `call_records` table:
  - `called` / `caller` — phone numbers involved in the call
  - `started_at` / `ended_at` / `duration_s` — call timing (null if the call has not ended)
  - `plan` — name of the call plan that was executed
  - `tenant` / `server` — routing context (may be null)
  - `data` — arbitrary JSON attached to the call by the plan; structure depends on the plan

- `app_log` — up to 100 log entries from the `app_log` table for this call, ordered newest first:
  - `ts` — timestamp
  - `level` — log level (e.g. `info`, `warn`, `error`)
  - `area` — subsystem that produced the entry
  - `event` — event name or short description
  - `fields` — structured JSON payload with additional context

- `asterisk_log` — last 300 lines of `/var/log/asterisk/messages.log` at the time of the request:
  - `lines` — array of log line strings
  - `error` — non-null string with the read error if the file could not be accessed

- `service_log` — systemd journal for the `switchboard` unit, time-windowed to ±30 seconds around the call (or the last 200 lines if call timing is unavailable):
  - `lines` — array of journal line strings
  - `error` — non-null string with the error if `journalctl` failed

## Notes

- `asterisk_log` and `service_log` reflect the live state at request time, not a stored snapshot. For very recent calls, journal entries may not yet be complete.
- `cdr.data` is raw JSON; its structure is defined by the call plan and varies per plan.
- If `cdr` is null, no call record was found for the given ID.
