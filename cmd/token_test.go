package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kleist-dev/logmcp/internal/config"
	"gopkg.in/yaml.v3"
)

// useConfigFile temporarily redirects configPath to a file in a temp dir and
// restores it when the test finishes.
func useConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := configPath
	configPath = path
	t.Cleanup(func() { configPath = orig })
	return path
}

const minimalConfigYAML = `
server:
  tls:
    mode: off
auth:
  tokens:
    - name: default
      token: test-token-abc
      scopes:
        - logmcp:read
logs:
  whitelist:
    - /var/log/*
`

func TestLoadConfigRaw(t *testing.T) {
	useConfigFile(t, minimalConfigYAML)

	cfg, err := loadConfigRaw()
	if err != nil {
		t.Fatalf("loadConfigRaw() error = %v", err)
	}

	if len(cfg.Auth.Tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(cfg.Auth.Tokens))
	}
	tok := cfg.Auth.Tokens[0]
	if tok.Name != "default" {
		t.Errorf("token name = %q, want %q", tok.Name, "default")
	}
	if tok.Token != "test-token-abc" {
		t.Errorf("token value = %q, want %q", tok.Token, "test-token-abc")
	}
	if len(tok.Scopes) != 1 || tok.Scopes[0] != "logmcp:read" {
		t.Errorf("token scopes = %v, want [logmcp:read]", tok.Scopes)
	}
	if cfg.Server.TLS.Mode != "off" {
		t.Errorf("tls mode = %q, want %q", cfg.Server.TLS.Mode, "off")
	}
}

func TestLoadConfigRaw_MissingFile(t *testing.T) {
	orig := configPath
	configPath = filepath.Join(t.TempDir(), "does-not-exist.yaml")
	t.Cleanup(func() { configPath = orig })

	_, err := loadConfigRaw()
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestSaveConfig(t *testing.T) {
	path := useConfigFile(t, minimalConfigYAML)

	// Load the original config, add a second token, save, reload, and verify.
	cfg, err := loadConfigRaw()
	if err != nil {
		t.Fatalf("loadConfigRaw() error = %v", err)
	}

	cfg.Auth.Tokens = append(cfg.Auth.Tokens, config.TokenConfig{
		Name:   "second",
		Token:  "second-token-xyz",
		Scopes: []string{"logmcp:read", "logmcp:admin"},
	})
	cfg.Name = "test-server"

	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}

	// Read back the file directly to verify it is valid YAML with our data.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	var roundtrip config.Config
	if err := yaml.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("parsing saved YAML: %v", err)
	}

	if roundtrip.Name != "test-server" {
		t.Errorf("name = %q, want %q", roundtrip.Name, "test-server")
	}
	if len(roundtrip.Auth.Tokens) != 2 {
		t.Fatalf("expected 2 tokens after save, got %d", len(roundtrip.Auth.Tokens))
	}
	second := roundtrip.Auth.Tokens[1]
	if second.Name != "second" {
		t.Errorf("second token name = %q, want %q", second.Name, "second")
	}
	if second.Token != "second-token-xyz" {
		t.Errorf("second token value = %q, want %q", second.Token, "second-token-xyz")
	}
	if len(second.Scopes) != 2 || second.Scopes[0] != "logmcp:read" || second.Scopes[1] != "logmcp:admin" {
		t.Errorf("second token scopes = %v, want [logmcp:read logmcp:admin]", second.Scopes)
	}
}

func TestParseScopes(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{
			input: "logmcp:read",
			want:  []string{"logmcp:read"},
		},
		{
			input: "logmcp:read,logmcp:admin",
			want:  []string{"logmcp:read", "logmcp:admin"},
		},
		{
			input: " logmcp:read , logmcp:admin ",
			want:  []string{"logmcp:read", "logmcp:admin"},
		},
		{
			input: "",
			want:  nil,
		},
		{
			input: "  ,  ",
			want:  nil,
		},
		{
			input: "a, , b",
			want:  []string{"a", "b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := parseScopes(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("parseScopes(%q) = %v, want %v", tc.input, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("parseScopes(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
				}
			}
		})
	}
}
