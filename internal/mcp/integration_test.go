//go:build integration

package mcp

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/logs"
)

// mcpToolCallRequest builds a JSON-RPC 2.0 tools/call request body.
func mcpToolCallRequest(id int, toolName string, params map[string]any) []byte {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": params,
		},
	}
	body, _ := json.Marshal(req)
	return body
}

// mcpInitRequest builds a JSON-RPC 2.0 initialize request body.
func mcpInitRequest(id int) []byte {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"clientInfo": map[string]any{
				"name":    "integration-test",
				"version": "0.0.1",
			},
		},
	}
	body, _ := json.Marshal(req)
	return body
}

// doMCPPost sends a POST to the MCP endpoint with the given bearer token, optional
// session ID, and body. Returns the HTTP response (caller must close the body).
func doMCPPost(client *http.Client, url, token, sessionID string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	return client.Do(req)
}

// mcpInitialize performs the MCP initialize handshake and returns the session ID.
// It fails the test if the handshake fails.
func mcpInitialize(t *testing.T, client *http.Client, mcpURL, token string) string {
	t.Helper()
	resp, err := doMCPPost(client, mcpURL, token, "", mcpInitRequest(1))
	if err != nil {
		t.Fatalf("initialize request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("initialize: expected HTTP 200, got %d — body: %s", resp.StatusCode, string(body))
	}
	sessionID := resp.Header.Get("Mcp-Session-Id")
	// Drain body.
	_, _ = io.ReadAll(resp.Body)
	return sessionID
}

// newIntegrationServer creates a Server with a real httptest.Server.
// It returns the Server (for Reload), the httptest server, and the MCP endpoint URL.
func newIntegrationServer(t *testing.T, cfg *config.Config, logMgr *logs.Manager) (*Server, *httptest.Server, string) {
	t.Helper()
	srv, err := New(cfg, logMgr, embed.FS{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	handler := srv.buildHandler()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return srv, ts, ts.URL + "/mcp"
}

// writeConfigFile writes a minimal YAML config to a file in dir and returns the path.
func writeConfigFile(t *testing.T, dir, token string, whitelist []string) string {
	t.Helper()
	wlEntries := make([]string, len(whitelist))
	for i, w := range whitelist {
		wlEntries[i] = fmt.Sprintf("  - %s", w)
	}
	yamlContent := fmt.Sprintf(`name: integration-test
server:
  host: 127.0.0.1
  port: 17799
  tls:
    mode: off
auth:
  tokens:
    - name: testuser
      token: %s
      scopes: [logmcp:read]
logs:
  whitelist:
%s
`, token, strings.Join(wlEntries, "\n"))
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("writeConfigFile: %v", err)
	}
	return path
}

// extractResultText extracts the text from the first TextContent item in an MCP result.
func extractResultText(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("extractResultText: unmarshal failed: %v — body: %s", err, string(body))
	}
	for _, c := range resp.Result.Content {
		if c.Type == "text" {
			return c.Text
		}
	}
	t.Fatalf("extractResultText: no text content in result — body: %s", string(body))
	return ""
}

// --- TestIntegration_AuthOKAndListLogs ---

// TestIntegration_AuthOKAndListLogs verifies that a valid token + tools/call list_logs
// returns HTTP 200 with a JSON result containing at least one accessible log file.
func TestIntegration_AuthOKAndListLogs(t *testing.T) {
	dir := t.TempDir()
	logPath := writeTempLog(t, dir, "app.log", []string{"info: server started", "debug: connection accepted"})

	cfg := config.Default()
	cfg.Server.TLS.Mode = "off"
	cfg.Auth.Tokens = []config.TokenConfig{
		{Name: "testuser", Token: "valid-token-abc", Scopes: []string{"logmcp:read"}},
	}
	cfg.Logs.Whitelist = []string{dir + "/*"}
	cfg.Logs.Blacklist = nil
	cfg.Logs.Journald = false

	mgr := logs.NewManager([]string{dir + "/*"}, nil, false)
	// Refresh the cache so the temp dir is scanned.
	mgr.Update([]string{dir + "/*"}, nil, false)

	_, _, mcpURL := newIntegrationServer(t, cfg, mgr)

	// Initialize the MCP session first.
	sessionID := mcpInitialize(t, http.DefaultClient, mcpURL, "valid-token-abc")

	reqBody := mcpToolCallRequest(2, "list_logs", nil)
	resp, err := doMCPPost(http.DefaultClient, mcpURL, "valid-token-abc", sessionID, reqBody)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected HTTP 200, got %d — body: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	text := extractResultText(t, body)

	var files []logs.FileInfo
	if err := json.Unmarshal([]byte(text), &files); err != nil {
		t.Fatalf("unmarshal list_logs result: %v — text: %s", err, text)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one log file in list_logs result, got empty list")
	}
	found := false
	for _, fi := range files {
		if fi.Path == logPath {
			found = true
			if !fi.Readable {
				t.Errorf("expected log file %s to be readable", logPath)
			}
		}
	}
	if !found {
		t.Errorf("expected %s in list_logs result, got: %v", logPath, files)
	}
}

// --- TestIntegration_WrongToken ---

// TestIntegration_WrongToken verifies that an invalid Bearer token returns HTTP 401.
func TestIntegration_WrongToken(t *testing.T) {
	dir := t.TempDir()
	writeTempLog(t, dir, "app.log", []string{"line1"})

	cfg := config.Default()
	cfg.Server.TLS.Mode = "off"
	cfg.Auth.Tokens = []config.TokenConfig{
		{Name: "testuser", Token: "correct-token", Scopes: []string{"logmcp:read"}},
	}
	cfg.Logs.Whitelist = []string{dir + "/*"}
	cfg.Logs.Blacklist = nil
	cfg.Logs.Journald = false

	mgr := logs.NewManager([]string{dir + "/*"}, nil, false)
	_, _, mcpURL := newIntegrationServer(t, cfg, mgr)

	// Sending a tools/call with wrong token — no session needed since auth check
	// happens before MCP session validation.
	reqBody := mcpToolCallRequest(1, "list_logs", nil)
	resp, err := doMCPPost(http.DefaultClient, mcpURL, "wrong-token", "", reqBody)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected HTTP 401, got %d — body: %s", resp.StatusCode, string(body))
	}
}

// --- TestIntegration_MissingAuthHeader ---

// TestIntegration_MissingAuthHeader verifies that a request with no Authorization header
// returns HTTP 401.
func TestIntegration_MissingAuthHeader(t *testing.T) {
	dir := t.TempDir()
	writeTempLog(t, dir, "app.log", []string{"line1"})

	cfg := config.Default()
	cfg.Server.TLS.Mode = "off"
	cfg.Auth.Tokens = []config.TokenConfig{
		{Name: "testuser", Token: "some-token", Scopes: []string{"logmcp:read"}},
	}
	cfg.Logs.Whitelist = []string{dir + "/*"}
	cfg.Logs.Blacklist = nil
	cfg.Logs.Journald = false

	mgr := logs.NewManager([]string{dir + "/*"}, nil, false)
	_, _, mcpURL := newIntegrationServer(t, cfg, mgr)

	// No token → no Authorization header. Auth check runs before MCP routing.
	reqBody := mcpToolCallRequest(1, "list_logs", nil)
	resp, err := doMCPPost(http.DefaultClient, mcpURL, "", "", reqBody)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected HTTP 401, got %d — body: %s", resp.StatusCode, string(body))
	}
}

// --- TestIntegration_ConfigReload ---

// TestIntegration_ConfigReload verifies that after srv.Reload(path) with a new token,
// the new token is accepted and the old token is rejected.
func TestIntegration_ConfigReload(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	writeTempLog(t, logDir, "app.log", []string{"line1"})

	const oldToken = "token-before-reload"
	const newToken = "token-after-reload"

	// Write initial config file.
	cfgPath := writeConfigFile(t, dir, oldToken, []string{logDir + "/*"})

	// Load config from file (so Reload can reload from the same path).
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	mgr := logs.NewManager(cfg.Logs.Whitelist, cfg.Logs.Blacklist, cfg.Logs.Journald)
	srv, _, mcpURL := newIntegrationServer(t, cfg, mgr)

	// Before reload: old token should work for initialize.
	sessionID := mcpInitialize(t, http.DefaultClient, mcpURL, oldToken)
	if sessionID == "" {
		t.Log("note: server returned no session ID (stateless mode)")
	}

	reqBody := mcpToolCallRequest(2, "list_logs", nil)

	// Before reload: old token + session should work.
	resp, err := doMCPPost(http.DefaultClient, mcpURL, oldToken, sessionID, reqBody)
	if err != nil {
		t.Fatalf("pre-reload request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pre-reload: expected 200 with old token, got %d", resp.StatusCode)
	}

	// Before reload: new token for initialize should fail with 401.
	resp, err = doMCPPost(http.DefaultClient, mcpURL, newToken, "", mcpInitRequest(1))
	if err != nil {
		t.Fatalf("pre-reload (new token init) request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pre-reload: expected 401 for new token initialize, got %d", resp.StatusCode)
	}

	// Rewrite config file with new token and trigger reload.
	cfgPath = writeConfigFile(t, dir, newToken, []string{logDir + "/*"})
	if err := srv.Reload(cfgPath); err != nil {
		t.Fatalf("srv.Reload: %v", err)
	}

	// After reload: new token should work for initialize.
	newSessionID := mcpInitialize(t, http.DefaultClient, mcpURL, newToken)

	// After reload: new token + new session should work for tool calls.
	resp, err = doMCPPost(http.DefaultClient, mcpURL, newToken, newSessionID, reqBody)
	if err != nil {
		t.Fatalf("post-reload (new token) request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-reload: expected 200 with new token, got %d", resp.StatusCode)
	}

	// After reload: old token should be rejected.
	resp, err = doMCPPost(http.DefaultClient, mcpURL, oldToken, "", mcpInitRequest(1))
	if err != nil {
		t.Fatalf("post-reload (old token) request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-reload: expected 401 with old token, got %d", resp.StatusCode)
	}
}
