package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const DefaultConfigPath = "/etc/logmcp/config.yaml"

// Config is the top-level configuration structure for LogMCP.
type Config struct {
	Name       string           `yaml:"name"`
	Server     ServerConfig     `yaml:"server"`
	Proxy      ProxyConfig      `yaml:"proxy"`
	Auth       AuthConfig       `yaml:"auth"`
	Logs       LogsConfig       `yaml:"logs"`
	Audit      AuditConfig      `yaml:"audit"`
	Security   SecurityConfig   `yaml:"security"`
	Tools      ToolsConfig      `yaml:"tools,omitempty"`
	Extensions ExtensionsConfig `yaml:"extensions"`
}

// ServerConfig holds HTTP/TLS server settings.
type ServerConfig struct {
	Host string    `yaml:"host"`
	Port int       `yaml:"port"`
	TLS  TLSConfig `yaml:"tls"`
}

// TLSConfig describes TLS mode and certificate paths.
type TLSConfig struct {
	// Mode is one of: self-signed, custom, off
	Mode string `yaml:"mode"`
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// ProxyConfig describes reverse-proxy settings.
type ProxyConfig struct {
	Enabled       bool   `yaml:"enabled"`
	TrustedProxy  bool   `yaml:"trusted_proxy"`
	Domain        string `yaml:"domain"`
	PathPrefix    string `yaml:"path_prefix"`
	Caddy         bool   `yaml:"caddy"`
}

// TokenConfig describes a single bearer token with its name and scopes.
type TokenConfig struct {
	Name   string   `yaml:"name"`
	Token  string   `yaml:"token"`
	Scopes []string `yaml:"scopes"`
}

// AuthConfig holds bearer-token authentication settings.
type AuthConfig struct {
	Tokens []TokenConfig `yaml:"tokens"`
}

// Default returns the first token, or nil if none are configured.
func (a *AuthConfig) Default() *TokenConfig {
	if len(a.Tokens) == 0 {
		return nil
	}
	return &a.Tokens[0]
}

// Find returns the token with the given name, or nil.
func (a *AuthConfig) Find(name string) *TokenConfig {
	for i := range a.Tokens {
		if a.Tokens[i].Name == name {
			return &a.Tokens[i]
		}
	}
	return nil
}

// LogsConfig holds log-file access control settings.
type LogsConfig struct {
	Whitelist []string `yaml:"whitelist"`
	Blacklist []string `yaml:"blacklist"`
	// Journald enables the virtual journald:// log source backed by journalctl.
	Journald bool `yaml:"journald"`
}

// AuditConfig holds audit settings.
type AuditConfig struct {
	Syslog bool `yaml:"syslog"`
}

// SecurityConfig holds rate limiting and fail2ban integration settings.
type SecurityConfig struct {
	RateLimit *TwoTierRateLimitConfig `yaml:"rate_limit,omitempty"`
	Fail2ban  Fail2banConfig          `yaml:"fail2ban"`
}

// TwoTierRateLimitConfig holds independent burst and sustained rate limit tiers.
// When nil (omitted from config), rate limiting is disabled.
// Each tier is individually optional — omit a sub-block to disable that tier.
type TwoTierRateLimitConfig struct {
	Burst     *RateLimitTierConfig `yaml:"burst,omitempty"`
	Sustained *RateLimitTierConfig `yaml:"sustained,omitempty"`
}

// RateLimitTierConfig limits failed authentication attempts per source IP
// within a sliding window.
type RateLimitTierConfig struct {
	MaxFailures   int `yaml:"max_failures"`
	WindowSeconds int `yaml:"window_seconds"`
}

// Fail2banConfig controls the fail2ban filter/jail installation.
type Fail2banConfig struct {
	Enabled   bool   `yaml:"enabled"`
	FilterDir string `yaml:"filter_dir,omitempty"`
	JailDir   string `yaml:"jail_dir,omitempty"`
}

// ToolsConfig controls which MCP tools are exposed.
type ToolsConfig struct {
	Disabled []string `yaml:"disabled,omitempty"`
}

// MacroConfig holds configuration for the macro engine.
type MacroConfig struct {
	// Dir is the directory that is scanned for *.yaml macro files at startup.
	// Relative paths are resolved relative to the working directory.
	// An empty string disables macro loading.
	Dir string `yaml:"dir"`
}

// ExtensionsConfig holds optional extension settings.
type ExtensionsConfig struct {
	Switchboard SwitchboardConfig `yaml:"switchboard"`
	Databases   DatabasesConfig   `yaml:"databases"`
	Macros      MacroConfig       `yaml:"macros"`
}

// SwitchboardConfig is the (currently disabled) switchboard extension.
type SwitchboardConfig struct {
	Enabled        bool   `yaml:"enabled"`
	CallsDir       string `yaml:"calls_dir"`
	SimDir         string `yaml:"sim_dir"`
	TranscriptsDir string `yaml:"transcripts_dir"`
}

// DatabasesConfig holds optional database connection configurations.
type DatabasesConfig struct {
	MySQL []MySQLConfig `yaml:"mysql"`
}

// MySQLConfig describes a single MySQL server connection.
type MySQLConfig struct {
	// Name is a human-readable label shown in `logmcp check` output.
	Name string `yaml:"name"`
	// DSN is the Go sql-driver DSN: user:pass@tcp(host:port)/dbname
	DSN string `yaml:"dsn"`
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		Name: "",
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 7788,
			TLS: TLSConfig{
				Mode: "self-signed",
				Cert: "/etc/logmcp/server.crt",
				Key:  "/etc/logmcp/server.key",
			},
		},
		Proxy: ProxyConfig{
			Enabled:      false,
			TrustedProxy: false,
			Domain:       "",
			PathPrefix:   "",
			Caddy:        true,
		},
		Auth: AuthConfig{},
		Logs: LogsConfig{
			Whitelist: []string{"/var/log/*"},
			Blacklist: []string{},
		},
		Audit: AuditConfig{
			Syslog: true,
		},
		Security: SecurityConfig{
			Fail2ban: Fail2banConfig{
				Enabled: true,
			},
		},
		Extensions: ExtensionsConfig{
			Switchboard: SwitchboardConfig{
				Enabled: false,
			},
		},
	}
}

// legacyAuth is used to detect and migrate the old single-token format.
type legacyAuth struct {
	Auth struct {
		Token string `yaml:"token"`
	} `yaml:"auth"`
}

// Load reads the YAML config file at path and returns a validated Config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", path, err)
	}

	// ExpandEnv allows $VAR references in the config (e.g. tokens from env).
	// Side effect: any $WORD in comments or string values is also expanded;
	// use $$ to write a literal dollar sign.
	expanded := os.ExpandEnv(string(data))

	cfg := Default()
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %q: %w", path, err)
	}

	// Migrate old single-token format: auth.token → auth.tokens[0].
	if len(cfg.Auth.Tokens) == 0 {
		var legacy legacyAuth
		if err := yaml.Unmarshal([]byte(expanded), &legacy); err == nil && legacy.Auth.Token != "" {
			fmt.Fprintf(os.Stderr, "logmcp: deprecated config format: 'auth.token' should be migrated to 'auth.tokens' list\n")
			cfg.Auth.Tokens = []TokenConfig{
				{Name: "default", Token: legacy.Auth.Token, Scopes: []string{"read"}},
			}
		}
	}

	if len(cfg.Logs.Whitelist) == 0 {
		cfg.Logs.Whitelist = []string{"/var/log/*"}
	}

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// validate checks that required fields are present and values are valid.
func validate(cfg *Config) error {
	if len(cfg.Auth.Tokens) == 0 {
		return fmt.Errorf("auth.tokens must contain at least one token")
	}
	for i, t := range cfg.Auth.Tokens {
		if t.Name == "" {
			return fmt.Errorf("auth.tokens[%d]: name must not be empty", i)
		}
		if t.Token == "" {
			return fmt.Errorf("auth.tokens[%d] (%s): token must not be empty", i, t.Name)
		}
		if len(t.Scopes) == 0 {
			return fmt.Errorf("auth.tokens[%d] (%s): scopes must not be empty", i, t.Name)
		}
	}
	switch cfg.Server.TLS.Mode {
	case "self-signed", "custom", "off":
		// valid
	default:
		return fmt.Errorf("server.tls.mode %q is invalid; expected self-signed, custom, or off", cfg.Server.TLS.Mode)
	}
	return nil
}
