package cmd

import (
	"testing"

	"github.com/kleist-dev/logmcp/internal/config"
)

func TestDeriveURL(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{
			name: "self-signed TLS, 0.0.0.0 becomes localhost",
			cfg: config.Config{
				Server: config.ServerConfig{Host: "0.0.0.0", Port: 7788, TLS: config.TLSConfig{Mode: "self-signed"}},
			},
			want: "https://localhost:7788/mcp",
		},
		{
			name: "custom TLS, named host",
			cfg: config.Config{
				Server: config.ServerConfig{Host: "myserver", Port: 443, TLS: config.TLSConfig{Mode: "custom"}},
			},
			want: "https://myserver:443/mcp",
		},
		{
			name: "TLS off, no proxy",
			cfg: config.Config{
				Server: config.ServerConfig{Host: "0.0.0.0", Port: 7788, TLS: config.TLSConfig{Mode: "off"}},
				Proxy:  config.ProxyConfig{Enabled: false},
			},
			want: "http://localhost:7788/mcp",
		},
		{
			name: "TLS off, proxy+caddy, no path prefix",
			cfg: config.Config{
				Server: config.ServerConfig{Host: "0.0.0.0", Port: 7788, TLS: config.TLSConfig{Mode: "off"}},
				Proxy:  config.ProxyConfig{Enabled: true, Caddy: true, Domain: "logs.example.com"},
			},
			want: "https://logs.example.com/mcp",
		},
		{
			name: "TLS off, proxy+caddy, with path prefix",
			cfg: config.Config{
				Server: config.ServerConfig{Host: "0.0.0.0", Port: 7788, TLS: config.TLSConfig{Mode: "off"}},
				Proxy:  config.ProxyConfig{Enabled: true, Caddy: true, Domain: "logs.example.com", PathPrefix: "/logmcp"},
			},
			want: "https://logs.example.com/logmcp/mcp",
		},
		{
			name: "path prefix trailing slash is stripped",
			cfg: config.Config{
				Server: config.ServerConfig{Host: "0.0.0.0", Port: 7788, TLS: config.TLSConfig{Mode: "off"}},
				Proxy:  config.ProxyConfig{Enabled: true, Caddy: true, Domain: "logs.example.com", PathPrefix: "/logmcp/"},
			},
			want: "https://logs.example.com/logmcp/mcp",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveURL(&tc.cfg)
			if got != tc.want {
				t.Errorf("deriveURL() = %q, want %q", got, tc.want)
			}
		})
	}
}
