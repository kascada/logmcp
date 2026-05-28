# LogMCP — Macros

A **macro** is a YAML file that defines a new MCP tool by combining one or more server-side operations into a single call. The result is a JSON object keyed by step ID.

Macros replace ad-hoc scripting for common log queries: write a YAML file, place it in the macros directory, restart LogMCP — and the tool appears in the MCP tool list automatically.

---

## Enabling macros

Add the macros directory to your config:

```yaml
extensions:
  macros:
    dir: /etc/logmcp/macros
```

LogMCP loads all `*.yaml` / `*.yml` files from that directory at startup. Files that fail to parse are skipped with a warning in the log.

> **Note:** Macros are loaded at startup only. After adding or changing files, run `sudo systemctl restart logmcp`. A plain `reload` (SIGHUP) is not sufficient.

---

## Macro file structure

```yaml
name: my_tool           # MCP tool name (required)
description: |          # Shown to the AI client (required)
  Short description of what this macro does.

parameters:             # Optional: named inputs from the AI client
  service:
    type: string
    description: "systemd unit name to inspect"
  since:
    type: string
    optional: true
    description: "Start of time range (RFC3339 or relative, e.g. 1h)"

steps:                  # One or more steps (required)
  - internal: journalctl
    id: service_log
    args:
      unit: "{{ service }}"
      since: "{{ since }}"
```

---

## Top-level fields

| Field | Required | Description |
|---|---|---|
| `name` | yes | Tool name as registered with MCP. Must be unique across all tools. |
| `description` | yes | Human-readable description shown to the AI client. |
| `parameters` | no | Named string parameters the AI client can pass in. |
| `steps` | yes | List of steps to execute, in order. |
| `timeout_seconds` | no | Per-step timeout in seconds. Applies to every step in this macro. Default: `30`. |

### Parameter definition

Each entry under `parameters` is a key → object:

| Field | Required | Description |
|---|---|---|
| `type` | no | Type hint (currently always `string`). |
| `description` | no | Shown to the AI client in the tool schema. |
| `optional` | no | If `true`, the parameter is not marked as required in the tool schema (default: `false`). |

---

## Steps

Each step has:

| Field | Required | Description |
|---|---|---|
| `internal` | yes | Step type: `read_file` or `journalctl`. |
| `id` | yes | Unique key in the result object. Used for cross-step references. |
| `args` | no | Step arguments. String values may contain `{{ }}` placeholders. |

Steps execute sequentially. If a step fails, execution stops and the partial result (including an `error` key for the failed step) is returned.

Each step has a timeout; the default is **30 seconds**. Override it for the whole macro via `timeout_seconds` (see [Top-level fields](#top-level-fields)).

---

## Step types

### `read_file`

Reads lines from a whitelisted file. Uses the same access control as the `read_log` MCP tool — paths not on the server whitelist are rejected.

| Arg | Type | Default | Description |
|---|---|---|---|
| `path` | string | — | File path (required). Must be on the whitelist. |
| `tail` | bool | `false` | If `true`, read from the end of the file. |
| `lines` | int | `100` | Number of lines to return. |

**Result fields:** `path`, `lines` (array of strings), `count`.

---

### `journalctl`

Reads the systemd journal for a named unit via `journalctl`.

| Arg | Type | Default | Description |
|---|---|---|---|
| `unit` | string | — | systemd unit name (e.g. `myapp.service`). |
| `around` | string | — | Central timestamp. Combined with `window_s` to derive `since`/`until`. |
| `window_s` | number | `30` | Half-window in seconds around `around`. |
| `since` | string | — | Start time (RFC3339 or `"2006-01-02 15:04:05"`). Used when `around` is absent. |
| `until` | string | — | End time. Used when `around` is absent. |

When neither `around` nor `since`/`until` are given, the last 200 journal lines are returned.

**Result fields:** `lines` (array of strings), `source` (`journald://<unit>`).

---

## Template syntax

String args may contain placeholders that are resolved before each step runs.

### `{{ param_name }}`

Replaced with the value of the named macro parameter. If the parameter was not supplied, replaced with an empty string.

### `{{ step_id.field }}`

Replaced with a field from the result of a previous step. If the step result contains an array, the first element is used.

Example — pass the `path` from one step into another:

```yaml
steps:
  - internal: read_file
    id: app
    args:
      path: /var/log/myapp/app.log
      tail: true
      lines: 50
  - internal: journalctl
    id: svc
    args:
      unit: myapp
      around: "{{ app.lines }}"   # illustrative; normally you'd use a timestamp field
```

---

## Output format

The macro result is a JSON object. Each key is a step `id`; each value is the step's output.

```json
{
  "asterisk_log": {
    "path": "/var/log/asterisk/messages.log",
    "lines": ["..."],
    "count": 300
  },
  "service_log": {
    "lines": ["..."],
    "source": "journald://switchboard"
  }
}
```

If a step fails, its value contains an `"error"` key and all subsequent steps are omitted.

---

## Disabling a macro tool

A macro tool can be hidden from AI clients without removing the file:

```yaml
tools:
  disabled:
    - my_tool
```

---

## Example — `call_snapshot.yaml`

```yaml
name: call_snapshot
description: |
  Combined diagnostic snapshot for a Switchboard instance.
  Collects asterisk log and service log in one call.
steps:
  - internal: read_file
    id: asterisk_log
    args:
      path: /var/log/asterisk/messages.log
      tail: true
      lines: 300
  - internal: journalctl
    id: service_log
    args:
      unit: switchboard
      window_s: 30
```

This macro is called without parameters. The AI client sees it as a single tool named `call_snapshot`.

---

## Example — parameterised macro

```yaml
name: service_snapshot
description: |
  Diagnostic snapshot for a named systemd service.
  Collects the last 200 journal lines for the given unit.
parameters:
  unit:
    type: string
    description: "systemd unit name (e.g. nginx)"
  window:
    type: string
    optional: true
    description: "Half-window in seconds around current time (default: 60)"
steps:
  - internal: journalctl
    id: journal
    args:
      unit: "{{ unit }}"
      window_s: "{{ window }}"
```
