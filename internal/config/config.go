package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

var validExtensionName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

const DefaultConfigPath = "/etc/logmcp/config.yaml"
const DefaultPort = 7788

var DefaultWhitelist = []string{"/var/log/*"}

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
	RAG        *RAGConfig       `yaml:"rag,omitempty"`
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

// AuthenticatorConfig configures an external authenticator program.
// See docs/CLITOOL.md for the verify interface convention.
type AuthenticatorConfig struct {
	Command        string `yaml:"command"`
	TimeoutSeconds int    `yaml:"timeout_seconds,omitempty"`
}

// AuthConfig holds bearer-token authentication settings.
type AuthConfig struct {
	Tokens        []TokenConfig        `yaml:"tokens,omitempty"`
	Authenticator *AuthenticatorConfig `yaml:"authenticator,omitempty"`
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
	Clitool []CltoolExtension `yaml:"clitool"`
	Macros  MacroConfig       `yaml:"macros"`
}

// RAGConfig holds settings for the optional RAG feature.
// When nil (absent from config), no RAG tools are registered.
type RAGConfig struct {
	// Builtin controls whether the embedded LogMCP docs are indexed as source "logmcp".
	// Defaults to true when omitted.
	Builtin        *bool       `yaml:"builtin,omitempty"`
	OllamaURL      string      `yaml:"ollama_url"`
	EmbeddingModel string      `yaml:"embedding_model"`
	RedisAddr      string      `yaml:"redis_addr"`
	Sources        []RAGSource `yaml:"sources"`
}

// BuiltinEnabled reports whether the built-in LogMCP docs should be indexed.
func (r *RAGConfig) BuiltinEnabled() bool {
	return r.Builtin == nil || *r.Builtin
}

// RAGSource describes a directory of documents to index.
type RAGSource struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// CltoolExtension configures a single clitool-based MCP extension.
// See docs/CLITOOL.md for the interface convention.
type CltoolExtension struct {
	// Name is used as a prefix for all tools exposed by this extension (e.g. "switchboard").
	// Must match [a-z][a-z0-9_]*.
	Name string `yaml:"name"`
	// Command is the full path to the clitool executable.
	Command string `yaml:"command"`
	// TimeoutSeconds is the per-call timeout (default: 10).
	TimeoutSeconds int `yaml:"timeout_seconds,omitempty"`
	// Mode selects the call transport: "cli" (default) spawns a subprocess per call;
	// "rpc" uses a Redis-based request/response channel to avoid process-startup overhead.
	Mode string `yaml:"mode,omitempty"`
	// RedisAddr is the Redis server address used when Mode is "rpc" (default: "127.0.0.1:6379").
	RedisAddr string `yaml:"redis_addr,omitempty"`
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		Name: "",
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: DefaultPort,
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
			Whitelist: DefaultWhitelist,
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
		Extensions: ExtensionsConfig{},
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
				{Name: "default", Token: legacy.Auth.Token, Scopes: []string{"logmcp:read"}},
			}
		}
	}

	if len(cfg.Logs.Whitelist) == 0 {
		cfg.Logs.Whitelist = DefaultWhitelist
	}

	if cfg.RAG != nil {
		if cfg.RAG.OllamaURL == "" {
			cfg.RAG.OllamaURL = "http://127.0.0.1:11434"
		}
		if cfg.RAG.EmbeddingModel == "" {
			cfg.RAG.EmbeddingModel = "nomic-embed-text"
		}
		if cfg.RAG.RedisAddr == "" {
			cfg.RAG.RedisAddr = "127.0.0.1:6379"
		}
	}

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// validate checks that required fields are present and values are valid.
func validate(cfg *Config) error {
	if cfg.Auth.Authenticator != nil {
		if cfg.Auth.Authenticator.Command == "" {
			return fmt.Errorf("auth.authenticator.command must not be empty")
		}
	} else {
		if len(cfg.Auth.Tokens) == 0 {
			return fmt.Errorf("auth.tokens must contain at least one token, or auth.authenticator must be configured")
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
	}
	switch cfg.Server.TLS.Mode {
	case "self-signed", "custom", "off":
		// valid
	default:
		return fmt.Errorf("server.tls.mode %q is invalid; expected self-signed, custom, or off", cfg.Server.TLS.Mode)
	}
	if cfg.RAG != nil {
		seen := make(map[string]bool)
		for i, s := range cfg.RAG.Sources {
			if s.Name == "" {
				return fmt.Errorf("rag.sources[%d]: name must not be empty", i)
			}
			if s.Path == "" {
				return fmt.Errorf("rag.sources[%d] (%s): path must not be empty", i, s.Name)
			}
			if s.Name == "logmcp" {
				return fmt.Errorf("rag.sources[%d]: name %q is reserved for built-in docs", i, s.Name)
			}
			if seen[s.Name] {
				return fmt.Errorf("rag.sources: duplicate name %q", s.Name)
			}
			seen[s.Name] = true
		}
	}

	seenExtNames := make(map[string]bool)
	for i, ext := range cfg.Extensions.Clitool {
		if !validExtensionName.MatchString(ext.Name) {
			return fmt.Errorf("extensions.clitool[%d]: name %q must match [a-z][a-z0-9_]*", i, ext.Name)
		}
		if ext.Command == "" {
			return fmt.Errorf("extensions.clitool[%d] (%s): command must not be empty", i, ext.Name)
		}
		if seenExtNames[ext.Name] {
			return fmt.Errorf("extensions.clitool: duplicate name %q", ext.Name)
		}
		seenExtNames[ext.Name] = true
		switch ext.Mode {
		case "", "cli", "rpc":
			// valid
		default:
			return fmt.Errorf("extensions.clitool[%d] (%s): mode %q is invalid; expected cli or rpc", i, ext.Name, ext.Mode)
		}
	}
	return nil
}
