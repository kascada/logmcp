package mcp

import (
	"context"
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/logs"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// newTestServer builds a minimal Server backed by a real Manager.
// whitelist is a slice of glob patterns; the returned Server has no HTTP stack.
func newTestServer(t *testing.T, whitelist []string) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.Server.TLS.Mode = "off"
	cfg.Auth.Tokens = []config.TokenConfig{
		{Name: "test", Token: "test-token", Scopes: []string{"read"}},
	}
	cfg.Logs.Whitelist = whitelist
	cfg.Logs.Blacklist = nil
	cfg.Logs.Journald = false

	mgr := logs.NewManager(whitelist, nil, false)
	srv, err := New(cfg, mgr, embed.FS{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

// makeReq builds a CallToolRequest with the given arguments map.
func makeReq(args map[string]any) mcplib.CallToolRequest {
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// textFromResult returns the first TextContent.Text from a CallToolResult.
// It fails the test if the result has no text content.
func textFromResult(t *testing.T, res *mcplib.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("result is nil")
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatalf("no TextContent in result: %+v", res)
	return ""
}

// writeTempLog writes lines to a new file in dir and returns its path.
func writeTempLog(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("writeTempLog: %v", err)
	}
	return path
}

// --- TestHandleListLogs ---

func TestHandleListLogs(t *testing.T) {
	dir := t.TempDir()
	path := writeTempLog(t, dir, "app.log", []string{"line1", "line2", "line3"})

	srv := newTestServer(t, []string{dir + "/*"})
	// Invalidate cache so the fresh tempdir is scanned.
	srv.logMgr.Update([]string{dir + "/*"}, nil, false)

	res, err := srv.handleListLogs(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleListLogs returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("handleListLogs returned tool error: %s", textFromResult(t, res))
	}

	text := textFromResult(t, res)
	var files []logs.FileInfo
	if err := json.Unmarshal([]byte(text), &files); err != nil {
		t.Fatalf("unmarshal: %v — raw: %s", err, text)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one file in list")
	}
	found := false
	for _, fi := range files {
		if fi.Path == path {
			found = true
			if !fi.Readable {
				t.Errorf("file %s should be readable", path)
			}
		}
	}
	if !found {
		t.Errorf("expected %s in list, got: %v", path, files)
	}
}

// --- TestHandleReadLog ---

func TestHandleReadLog(t *testing.T) {
	dir := t.TempDir()
	path := writeTempLog(t, dir, "app.log", []string{"alpha", "beta", "gamma", "delta", "epsilon"})
	srv := newTestServer(t, []string{dir + "/*"})

	t.Run("happy_path", func(t *testing.T) {
		res, err := srv.handleReadLog(context.Background(), makeReq(map[string]any{
			"path":  path,
			"lines": float64(3),
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", textFromResult(t, res))
		}
		text := textFromResult(t, res)
		var result struct {
			Path  string   `json:"path"`
			Lines []string `json:"lines"`
			Count int      `json:"count"`
		}
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if result.Count != 3 {
			t.Errorf("expected count=3, got %d", result.Count)
		}
		if result.Lines[0] != "alpha" {
			t.Errorf("expected first line 'alpha', got %q", result.Lines[0])
		}
	})

	t.Run("tail_mode", func(t *testing.T) {
		res, err := srv.handleReadLog(context.Background(), makeReq(map[string]any{
			"path":  path,
			"lines": float64(2),
			"tail":  true,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", textFromResult(t, res))
		}
		text := textFromResult(t, res)
		var result struct {
			Lines []string `json:"lines"`
			Count int      `json:"count"`
		}
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if result.Count != 2 {
			t.Errorf("expected count=2, got %d", result.Count)
		}
		// Last 2 lines should be "delta" and "epsilon"
		if result.Lines[0] != "delta" || result.Lines[1] != "epsilon" {
			t.Errorf("expected [delta epsilon], got %v", result.Lines)
		}
	})

	t.Run("acl_denied", func(t *testing.T) {
		outsidePath := filepath.Join(t.TempDir(), "secret.log")
		os.WriteFile(outsidePath, []byte("secret\n"), 0644)

		res, err := srv.handleReadLog(context.Background(), makeReq(map[string]any{
			"path": outsidePath,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsError {
			t.Error("expected tool error for path not in whitelist")
		}
		text := textFromResult(t, res)
		if !strings.Contains(text, "access denied") {
			t.Errorf("expected 'access denied' in error message, got: %s", text)
		}
	})

	t.Run("missing_path_param", func(t *testing.T) {
		res, err := srv.handleReadLog(context.Background(), makeReq(map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsError {
			t.Error("expected tool error for missing path")
		}
	})

	t.Run("invalid_since", func(t *testing.T) {
		res, err := srv.handleReadLog(context.Background(), makeReq(map[string]any{
			"path":  path,
			"since": "not-a-time",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsError {
			t.Error("expected tool error for invalid since value")
		}
	})
}

// --- TestHandleSearchLog ---

func TestHandleSearchLog(t *testing.T) {
	dir := t.TempDir()
	path := writeTempLog(t, dir, "search.log", []string{
		"INFO  startup complete",
		"DEBUG processing request",
		"ERROR connection refused",
		"INFO  request handled",
		"ERROR timeout exceeded",
	})
	srv := newTestServer(t, []string{dir + "/*"})

	t.Run("matches_found", func(t *testing.T) {
		res, err := srv.handleSearchLog(context.Background(), makeReq(map[string]any{
			"path":    path,
			"pattern": "ERROR",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", textFromResult(t, res))
		}
		text := textFromResult(t, res)
		var result struct {
			Count   int          `json:"count"`
			Matches []logs.Match `json:"matches"`
		}
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if result.Count != 2 {
			t.Errorf("expected 2 ERROR matches, got %d", result.Count)
		}
	})

	t.Run("no_matches", func(t *testing.T) {
		res, err := srv.handleSearchLog(context.Background(), makeReq(map[string]any{
			"path":    path,
			"pattern": "CRITICAL",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", textFromResult(t, res))
		}
		text := textFromResult(t, res)
		var result struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if result.Count != 0 {
			t.Errorf("expected 0 matches, got %d", result.Count)
		}
	})

	t.Run("acl_denied", func(t *testing.T) {
		outsidePath := filepath.Join(t.TempDir(), "private.log")
		os.WriteFile(outsidePath, []byte("secret\n"), 0644)

		res, err := srv.handleSearchLog(context.Background(), makeReq(map[string]any{
			"path":    outsidePath,
			"pattern": "secret",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsError {
			t.Error("expected tool error for path not in whitelist")
		}
		text := textFromResult(t, res)
		if !strings.Contains(text, "access denied") {
			t.Errorf("expected 'access denied' in error, got: %s", text)
		}
	})

	t.Run("missing_path_param", func(t *testing.T) {
		res, err := srv.handleSearchLog(context.Background(), makeReq(map[string]any{
			"pattern": "ERROR",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsError {
			t.Error("expected tool error for missing path")
		}
	})

	t.Run("missing_pattern_param", func(t *testing.T) {
		res, err := srv.handleSearchLog(context.Background(), makeReq(map[string]any{
			"path": path,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsError {
			t.Error("expected tool error for missing pattern")
		}
	})

	t.Run("pattern_redacted_in_response", func(t *testing.T) {
		res, err := srv.handleSearchLog(context.Background(), makeReq(map[string]any{
			"path":    path,
			"pattern": "ERROR",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := textFromResult(t, res)
		// The raw JSON must not contain the actual search pattern.
		if strings.Contains(text, `"ERROR"`) {
			t.Error("search pattern must not appear verbatim in response (should be redacted)")
		}
		// Unmarshal to check the pattern_redacted field contains "<redacted>".
		var result struct {
			Pattern string `json:"pattern_redacted"`
		}
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if result.Pattern != "<redacted>" {
			t.Errorf("expected pattern_redacted='<redacted>', got %q", result.Pattern)
		}
	})
}

// --- TestHandleLogInfo ---

func TestHandleLogInfo(t *testing.T) {
	dir := t.TempDir()
	path := writeTempLog(t, dir, "info.log", []string{"line1", "line2"})
	srv := newTestServer(t, []string{dir + "/*"})

	t.Run("existing_file", func(t *testing.T) {
		res, err := srv.handleLogInfo(context.Background(), makeReq(map[string]any{
			"path": path,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected tool error: %s", textFromResult(t, res))
		}
		text := textFromResult(t, res)
		var fi logs.FileInfo
		if err := json.Unmarshal([]byte(text), &fi); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if fi.Path != path {
			t.Errorf("expected path %s, got %s", path, fi.Path)
		}
		if !fi.Readable {
			t.Error("expected file to be readable")
		}
		if fi.LineCount != 2 {
			t.Errorf("expected 2 lines, got %d", fi.LineCount)
		}
	})

	t.Run("nonexistent_file_in_whitelist", func(t *testing.T) {
		missingPath := filepath.Join(dir, "missing.log")
		// File doesn't exist — but the pattern dir/* would match it if it did.
		// Since IsAllowed checks the pattern (not existence), and the path matches
		// the glob pattern, we expect a stat error from the handler.
		res, err := srv.handleLogInfo(context.Background(), makeReq(map[string]any{
			"path": missingPath,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// IsAllowed uses glob matching, not file existence. The pattern dir/* does
		// not expand to a non-existent file via filepath.Match, so IsAllowed returns
		// false — resulting in an access denied error.
		if !res.IsError {
			t.Error("expected tool error for non-existent file")
		}
	})

	t.Run("acl_denied", func(t *testing.T) {
		outsidePath := filepath.Join(t.TempDir(), "outside.log")
		os.WriteFile(outsidePath, []byte("data\n"), 0644)

		res, err := srv.handleLogInfo(context.Background(), makeReq(map[string]any{
			"path": outsidePath,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsError {
			t.Error("expected access denied for path not in whitelist")
		}
	})

	t.Run("missing_path_param", func(t *testing.T) {
		res, err := srv.handleLogInfo(context.Background(), makeReq(map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsError {
			t.Error("expected tool error for missing path")
		}
	})
}

// --- TestHandleCheckEnvironment ---

func TestHandleCheckEnvironment(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, []string{dir + "/*"})

	res, err := srv.handleCheckEnvironment(context.Background(), makeReq(nil))
	if err != nil {
		t.Fatalf("handleCheckEnvironment returned error: %v", err)
	}
	if res == nil {
		t.Fatal("result is nil")
	}
	text := textFromResult(t, res)
	// Verify the response is valid JSON with expected structure.
	var result struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("unmarshal: %v — raw: %s", err, text)
	}
	if len(result.Checks) == 0 {
		t.Error("expected at least one check item in result")
	}
}

// --- TestMatchGlobDoublestar ---

func TestMatchGlobDoublestar(t *testing.T) {
	// Access matchGlob via the logs package — call via exported IsAllowed or
	// directly since we are in the same module.
	// Since matchGlob is unexported from package logs, we test it indirectly
	// through logs.Manager.IsAllowed, which exercises the same code path.

	dir := t.TempDir()

	// Create a nested directory structure.
	sub := filepath.Join(dir, "sub")
	deep := filepath.Join(sub, "deep")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	shallow := filepath.Join(dir, "shallow.log")
	nested := filepath.Join(sub, "nested.log")
	veryDeep := filepath.Join(deep, "verydeep.log")
	for _, f := range []string{shallow, nested, veryDeep} {
		os.WriteFile(f, []byte("data\n"), 0644)
	}

	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		// ** at end — matches any depth.
		{"doublestar_end_shallow", dir + "/**", shallow, true},
		{"doublestar_end_nested", dir + "/**", nested, true},
		{"doublestar_end_verydeep", dir + "/**", veryDeep, true},
		{"doublestar_end_outside", dir + "/**", "/etc/passwd", false},

		// ** in the middle.
		{"doublestar_middle_matches_nested", dir + "/**/nested.log", nested, true},
		{"doublestar_middle_matches_verydeep", dir + "/**/verydeep.log", veryDeep, true},
		{"doublestar_middle_no_match", dir + "/**/other.log", shallow, false},

		// ** at beginning — matches any prefix.
		{"doublestar_begin_any", "**", shallow, true},
		{"doublestar_begin_any_deep", "**", veryDeep, true},

		// ** with suffix constraint.
		{"doublestar_suffix_log", dir + "/**/*.log", nested, true},
		{"doublestar_suffix_log_deep", dir + "/**/*.log", veryDeep, true},
		{"doublestar_suffix_txt_no_match", dir + "/**/*.txt", nested, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr := logs.NewManager([]string{tc.pattern}, nil, false)
			got := mgr.IsAllowed(tc.path)
			if got != tc.want {
				t.Errorf("IsAllowed(pattern=%q, path=%q) = %v, want %v",
					tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}
