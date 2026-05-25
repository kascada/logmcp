# check_config

Show the current LogMCP server configuration and highlight optional parameters that are at their default value.

## When to use

Use to understand how this server is configured — which logs are accessible, whether proxy mode or fail2ban are active, which tools are enabled, and which optional features are not yet configured. See `logmcp://docs/config` for the full configuration reference.

## Response

Object with two fields:

- `current` — key configuration values currently active on this server
- `defaults` — optional parameters that are at their default value, each with a short explanation of what they do
