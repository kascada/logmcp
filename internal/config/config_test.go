package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- validate ---

func TestValidate(t *testing.T) {
	valid := &Config{
		Auth: AuthConfig{Tokens: []TokenConfig{
			{Name: "t1", Token: "abc", Scopes: []string{"logmcp:read"}},
		}},
		Server: ServerConfig{TLS: TLSConfig{Mode: "self-signed"}},
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"valid config", func(*Config) {}, ""},
		{"no tokens", func(c *Config) { c.Auth.Tokens = nil }, "auth.tokens must contain"},
		{"empty token name", func(c *Config) { c.Auth.Tokens[0].Name = "" }, "name must not be empty"},
		{"empty token value", func(c *Config) { c.Auth.Tokens[0].Token = "" }, "token must not be empty"},
		{"empty scopes", func(c *Config) { c.Auth.Tokens[0].Scopes = nil }, "scopes must not be empty"},
		{"invalid TLS mode", func(c *Config) { c.Server.TLS.Mode = "none" }, "invalid"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := *valid
			cfg.Auth.Tokens = []TokenConfig{{
				Name:   valid.Auth.Tokens[0].Name,
				Token:  valid.Auth.Tokens[0].Token,
				Scopes: append([]string{}, valid.Auth.Tokens[0].Scopes...),
			}}
			tc.mutate(&cfg)

			err := validate(&cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tc.wantErr)
				}
			}
		})
	}
}

// TestLoad_LegacyMigration verifies that the old auth.token field is migrated
// to auth.tokens[0] when no tokens list is present.
func TestLoad_LegacyMigration(t *testing.T) {
	yaml := `
server:
  tls:
    mode: off
auth:
  token: "old-token"
logs:
  whitelist:
    - /var/log/*
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.Auth.Tokens) != 1 {
		t.Fatalf("expected 1 token after migration, got %d", len(cfg.Auth.Tokens))
	}
	if cfg.Auth.Tokens[0].Token != "old-token" {
		t.Errorf("token value = %q, want %q", cfg.Auth.Tokens[0].Token, "old-token")
	}
}

// TestLoad_EnvExpansion verifies that $VAR references in the config are expanded.
func TestLoad_EnvExpansion(t *testing.T) {
	t.Setenv("TEST_LOGMCP_TOKEN", "env-expanded-value")

	yaml := `
server:
  tls:
    mode: off
auth:
  tokens:
    - name: test
      token: $TEST_LOGMCP_TOKEN
      scopes: [read]
logs:
  whitelist:
    - /var/log/*
`
	path := writeTempYAML(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Auth.Tokens[0].Token != "env-expanded-value" {
		t.Errorf("token = %q, want %q", cfg.Auth.Tokens[0].Token, "env-expanded-value")
	}
}

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
