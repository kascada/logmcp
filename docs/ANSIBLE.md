# LogMCP — Ansible Role

An Ansible role for installing and configuring LogMCP via the official Debian package.

## Requirements

- Debian/Ubuntu target host
- Ansible 2.10+
- `logmcp_token` and `logmcp_proxy_domain` must be set (see Variables)

## Role Structure

```
ansible/roles/logmcp/
  tasks/main.yml          Download, install, configure, enable
  handlers/main.yml       Restart handler
  defaults/main.yml       Default variable values
  templates/config.yaml.j2
```

## Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `logmcp_version` | No | `0.1.0` | Package version to install |
| `logmcp_token` | **Yes** | — | Bearer token for MCP access (use Ansible Vault) |
| `logmcp_proxy_domain` | **Yes** | — | Public hostname (behind reverse proxy) |
| `logmcp_name` | No | `logmcp-<hostname>` | MCP server name |
| `logmcp_port` | No | `7788` | Local listen port |
| `logmcp_path_prefix` | No | `/logmcp` | URL subpath |
| `logmcp_proxy_enabled` | No | `true` | Enable reverse proxy mode |
| `logmcp_token_name` | No | `default` | Display name for the token |
| `logmcp_whitelist` | No | `["/var/log/*"]` | Glob patterns for accessible log files |
| `logmcp_fail2ban_enabled` | No | `true` | Install fail2ban filter/jail for logmcp |
| `logmcp_macros_dir` | No | `/etc/logmcp/macros` | Directory for macro files; set to `""` to disable |
| `logmcp_rate_limit_max_failures` | No | — (disabled) | Max auth failures in the sliding window before 429 |
| `logmcp_rate_limit_window_seconds` | No | `60` | Sliding window length in seconds |

Store `logmcp_token` in Ansible Vault:

```bash
ansible-vault encrypt_string 'your-token-here' --name logmcp_token
```

## Usage

```yaml
# playbook.yml
- hosts: myserver
  roles:
    - role: logmcp
      vars:
        logmcp_version: "0.1.0"
        logmcp_proxy_domain: "example.com"
        logmcp_token: "{{ vault_logmcp_token }}"
```

## What the Role Does

1. Downloads the `.deb` from the GitHub release
2. Installs via `apt` — this runs `postinst`, which creates the `logmcp` system user, sets up group memberships and directory permissions, and runs `systemctl daemon-reload`
3. Creates `logmcp_macros_dir` (default `/etc/logmcp/macros`) if the variable is set and non-empty
4. Writes `/etc/logmcp/config.yaml` from the template
5. Enables and starts the `logmcp` systemd service
6. Runs `logmcp security install-fail2ban` if `logmcp_fail2ban_enabled: true` (default), then reloads fail2ban

The service is only started after the config is in place — `logmcp serve` exits immediately without a config file.

### fail2ban

The role installs logmcp's embedded fail2ban filter and jail configs via `logmcp security install-fail2ban`. This works whether fail2ban is already running or not — the files are written and `fail2ban-client reload` picks them up additively. Existing bans and jails are not affected.

To disable: set `logmcp_fail2ban_enabled: false`.

### Rate Limiting

In-process rate limiting. Enable by defining `logmcp_rate_limit_max_failures`:

```yaml
logmcp_rate_limit_max_failures: 10
logmcp_rate_limit_window_seconds: 60   # optional, default 60
```

Without these variables, rate limiting is disabled (fail2ban alone handles brute force).

## Releases

Packages are available at:
`https://github.com/kascada/logmcp/releases`
