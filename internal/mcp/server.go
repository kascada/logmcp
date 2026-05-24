package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kleist-dev/logmcp/internal/audit"
	"github.com/kleist-dev/logmcp/internal/auth"
	"github.com/kleist-dev/logmcp/internal/check"
	"github.com/kleist-dev/logmcp/internal/config"
	switchboardext "github.com/kleist-dev/logmcp/internal/extensions/switchboard"
	"github.com/kleist-dev/logmcp/internal/logs"
	internaltls "github.com/kleist-dev/logmcp/internal/tls"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// clientIPKey is the context key used to propagate client IP to tool handlers.
type clientIPKey struct{}

// Server wraps the MCP server and its HTTP configuration.
type Server struct {
	cfg    *config.Config
	logMgr *logs.Manager
	mcpSrv *server.MCPServer
	httpSrv *server.StreamableHTTPServer
}

// New creates a new MCP Server with all tools registered.
func New(cfg *config.Config, logMgr *logs.Manager) (*Server, error) {
	s := &Server{
		cfg:    cfg,
		logMgr: logMgr,
	}

	// Build the MCP server.
	s.mcpSrv = server.NewMCPServer(
		"LogMCP",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithInstructions(loadServerDesc()),
	)

	s.registerTools()

	// Build the StreamableHTTP transport.
	s.httpSrv = server.NewStreamableHTTPServer(
		s.mcpSrv,
		server.WithEndpointPath("/mcp"),
		server.WithHTTPContextFunc(s.httpContextFunc),
	)

	return s, nil
}

// httpContextFunc injects the client IP and token name into the request context.
func (s *Server) httpContextFunc(ctx context.Context, r *http.Request) context.Context {
	ip := extractClientIP(r, s.cfg.Proxy.TrustedProxy)
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// clientIPFromCtx retrieves the client IP injected by httpContextFunc.
func clientIPFromCtx(ctx context.Context) string {
	if ip, ok := ctx.Value(clientIPKey{}).(string); ok {
		return ip
	}
	return "unknown"
}

// extractClientIP returns the best available client IP for the request.
// If trustedProxy is true, the first entry in X-Forwarded-For is used;
// otherwise the direct remote address is used.
func extractClientIP(r *http.Request, trustedProxy bool) string {
	if trustedProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.SplitN(xff, ",", 2)
			return strings.TrimSpace(parts[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// registerTools adds all MCP tools to the server.
// toolEnabled reports whether the named tool should be registered.
func (s *Server) toolEnabled(name string) bool {
	return !slices.Contains(s.cfg.Tools.Disabled, name)
}

func (s *Server) registerTools() {
	// --- list_logs ---
	if s.toolEnabled("list_logs") {
		llDesc := loadToolDesc("list_logs")
		listLogsTool := mcp.NewTool("list_logs",
			mcp.WithDescription(llDesc.Description),
		)
		s.mcpSrv.AddTool(listLogsTool, s.handleListLogs)
	}

	// --- read_log ---
	if s.toolEnabled("read_log") {
		rlDesc := loadToolDesc("read_log")
		readLogTool := mcp.NewTool("read_log",
			mcp.WithDescription(rlDesc.Description),
			mcp.WithString("path",
				mcp.Required(),
				mcp.Description(rlDesc.Params["path"]),
			),
			mcp.WithNumber("lines",
				mcp.Description(rlDesc.Params["lines"]),
				mcp.DefaultNumber(100),
			),
			mcp.WithBoolean("tail",
				mcp.Description(rlDesc.Params["tail"]),
				mcp.DefaultBool(false),
			),
			mcp.WithNumber("offset",
				mcp.Description(rlDesc.Params["offset"]),
				mcp.DefaultNumber(0),
			),
			mcp.WithString("since",
				mcp.Description(rlDesc.Params["since"]),
			),
			mcp.WithString("until",
				mcp.Description(rlDesc.Params["until"]),
			),
		)
		s.mcpSrv.AddTool(readLogTool, s.handleReadLog)
	}

	// --- search_log ---
	if s.toolEnabled("search_log") {
		slDesc := loadToolDesc("search_log")
		searchLogTool := mcp.NewTool("search_log",
			mcp.WithDescription(slDesc.Description),
			mcp.WithString("path",
				mcp.Required(),
				mcp.Description(slDesc.Params["path"]),
			),
			mcp.WithString("pattern",
				mcp.Required(),
				mcp.Description(slDesc.Params["pattern"]),
			),
			mcp.WithString("since",
				mcp.Description(slDesc.Params["since"]),
			),
			mcp.WithString("until",
				mcp.Description(slDesc.Params["until"]),
			),
			mcp.WithNumber("max_results",
				mcp.Description(slDesc.Params["max_results"]),
				mcp.DefaultNumber(200),
			),
			mcp.WithNumber("context_lines",
				mcp.Description(slDesc.Params["context_lines"]),
				mcp.DefaultNumber(0),
			),
		)
		s.mcpSrv.AddTool(searchLogTool, s.handleSearchLog)
	}

	// --- check_environment ---
	if s.toolEnabled("check_environment") {
		ceDesc := loadToolDesc("check_environment")
		checkTool := mcp.NewTool("check_environment",
			mcp.WithDescription(ceDesc.Description),
		)
		s.mcpSrv.AddTool(checkTool, s.handleCheckEnvironment)
	}

	// --- switchboard_debug (only when extension is enabled) ---
	if s.cfg.Extensions.Switchboard.Enabled && s.toolEnabled("switchboard_debug") {
		sbDesc := loadToolDesc("switchboard_debug")
		switchboardDebugTool := mcp.NewTool("switchboard_debug",
			mcp.WithDescription(sbDesc.Description),
			mcp.WithString("call_id",
				mcp.Description(sbDesc.Params["call_id"]),
			),
		)
		s.mcpSrv.AddTool(switchboardDebugTool, s.handleSwitchboardDebug)
	}

	// --- log_info ---
	if s.toolEnabled("log_info") {
		liDesc := loadToolDesc("log_info")
		logInfoTool := mcp.NewTool("log_info",
			mcp.WithDescription(liDesc.Description),
			mcp.WithString("path",
				mcp.Required(),
				mcp.Description(liDesc.Params["path"]),
			),
		)
		s.mcpSrv.AddTool(logInfoTool, s.handleLogInfo)
	}
}

// handleListLogs implements the list_logs tool.
func (s *Server) handleListLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	clientIP := clientIPFromCtx(ctx)

	files, err := s.logMgr.ListAccessible()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing log files: %v", err)), nil
	}

	_ = audit.Log("list_logs", "<all>", clientIP)

	data, err := json.Marshal(files)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("serialising results: %v", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// handleReadLog implements the read_log tool.
func (s *Server) handleReadLog(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	clientIP := clientIPFromCtx(ctx)

	path := req.GetString("path", "")
	if path == "" {
		return mcp.NewToolResultError("parameter 'path' is required"), nil
	}

	if !s.logMgr.IsAllowed(path) {
		_ = audit.LogDenied(path, clientIP, "not_in_whitelist")
		return mcp.NewToolResultError(fmt.Sprintf("access denied: %s is not in the whitelist", path)), nil
	}

	opts := logs.ReadOptions{
		Lines:  int(req.GetFloat("lines", 100)),
		Tail:   req.GetBool("tail", false),
		Offset: int(req.GetFloat("offset", 0)),
	}

	if sinceStr := req.GetString("since", ""); sinceStr != "" {
		t, err := logs.ParseTimeOrDuration(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid 'since' value: %v", err)), nil
		}
		opts.Since = &t
	}
	if untilStr := req.GetString("until", ""); untilStr != "" {
		t, err := logs.ParseTimeOrDuration(untilStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid 'until' value: %v", err)), nil
		}
		opts.Until = &t
	}

	var (
		lines []string
		err   error
	)
	if logs.IsJournaldPath(path) {
		lines, err = s.logMgr.ReadJournald(path, opts)
	} else {
		lines, err = s.logMgr.ReadFile(path, opts)
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reading %s: %v", path, err)), nil
	}

	_ = audit.Log("read_log", path, clientIP)

	result := struct {
		Path  string   `json:"path"`
		Lines []string `json:"lines"`
		Count int      `json:"count"`
	}{
		Path:  path,
		Lines: lines,
		Count: len(lines),
	}

	data, err := json.Marshal(result)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("serialising result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// handleSearchLog implements the search_log tool.
func (s *Server) handleSearchLog(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	clientIP := clientIPFromCtx(ctx)

	path := req.GetString("path", "")
	if path == "" {
		return mcp.NewToolResultError("parameter 'path' is required"), nil
	}
	pattern := req.GetString("pattern", "")
	if pattern == "" {
		return mcp.NewToolResultError("parameter 'pattern' is required"), nil
	}

	if !s.logMgr.IsAllowed(path) {
		_ = audit.LogDenied(path, clientIP, "not_in_whitelist")
		return mcp.NewToolResultError(fmt.Sprintf("access denied: %s is not in the whitelist", path)), nil
	}

	opts := logs.SearchOptions{
		Pattern:      pattern,
		MaxResults:   int(req.GetFloat("max_results", 200)),
		ContextLines: int(req.GetFloat("context_lines", 0)),
	}

	if sinceStr := req.GetString("since", ""); sinceStr != "" {
		t, err := logs.ParseTimeOrDuration(sinceStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid 'since' value: %v", err)), nil
		}
		opts.Since = &t
	}
	if untilStr := req.GetString("until", ""); untilStr != "" {
		t, err := logs.ParseTimeOrDuration(untilStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid 'until' value: %v", err)), nil
		}
		opts.Until = &t
	}

	var (
		matches []logs.Match
		err     error
	)
	if logs.IsJournaldPath(path) {
		matches, err = s.logMgr.SearchJournald(path, opts)
	} else {
		matches, err = s.logMgr.SearchFile(path, opts)
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("searching %s: %v", path, err)), nil
	}

	_ = audit.LogSearch(path, clientIP)

	result := struct {
		Path    string       `json:"path"`
		Pattern string       `json:"pattern_redacted"`
		Matches []logs.Match `json:"matches"`
		Count   int          `json:"count"`
	}{
		Path:    path,
		Pattern: "<redacted>",
		Matches: matches,
		Count:   len(matches),
	}

	data, err := json.Marshal(result)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("serialising result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// handleLogInfo implements the log_info tool.
func (s *Server) handleLogInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	clientIP := clientIPFromCtx(ctx)

	path := req.GetString("path", "")
	if path == "" {
		return mcp.NewToolResultError("parameter 'path' is required"), nil
	}

	if !s.logMgr.IsAllowed(path) {
		_ = audit.LogDenied(path, clientIP, "not_in_whitelist")
		return mcp.NewToolResultError(fmt.Sprintf("access denied: %s is not in the whitelist", path)), nil
	}

	var fi logs.FileInfo
	if logs.IsJournaldPath(path) {
		fi = s.logMgr.JournaldInfo()
	} else {
		var err error
		fi, err = s.logMgr.FileInfo(path)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("stat %s: %v", path, err)), nil
		}
	}

	_ = audit.Log("log_info", path, clientIP)

	data, err := json.Marshal(fi)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("serialising result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// handleCheckEnvironment implements the check_environment tool.
func (s *Server) handleCheckEnvironment(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result := check.Run(s.cfg, check.Options{
		ConfigPath:  config.DefaultConfigPath,
		IncludePort: true,
	})

	data, err := json.Marshal(result)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("serialising result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// handleSwitchboardDebug implements the switchboard_debug tool.
func (s *Server) handleSwitchboardDebug(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	callID := req.GetString("call_id", "")
	result, err := switchboardext.Debug(s.cfg, callID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("switchboard_debug: %v", err)), nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("serialising result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// Start launches the MCP HTTP server, blocking until it exits.
func (s *Server) Start() error {
	cfg := s.cfg
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	handler := s.buildHandler()

	switch cfg.Server.TLS.Mode {
	case "self-signed", "custom":
		tlsCfg, err := internaltls.LoadTLSConfig(cfg.Server.TLS.Cert, cfg.Server.TLS.Key)
		if err != nil {
			return fmt.Errorf("loading TLS config: %w", err)
		}
		httpSrv := &http.Server{
			Addr:         addr,
			Handler:      handler,
			TLSConfig:    tlsCfg,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 120 * time.Second,
			IdleTimeout:  120 * time.Second,
		}
		fmt.Printf("LogMCP listening on https://%s/mcp\n", addr)
		return httpSrv.ListenAndServeTLS("", "")

	case "off":
		httpSrv := &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 120 * time.Second,
			IdleTimeout:  120 * time.Second,
		}
		fmt.Printf("LogMCP listening on http://%s/mcp\n", addr)
		return httpSrv.ListenAndServe()

	default:
		return fmt.Errorf("unknown TLS mode %q; expected self-signed, custom, or off", cfg.Server.TLS.Mode)
	}
}

// buildHandler wraps the MCP StreamableHTTPServer with the auth middleware.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	entries := make([]auth.TokenEntry, len(s.cfg.Auth.Tokens))
	for i, t := range s.cfg.Auth.Tokens {
		entries[i] = auth.TokenEntry{Name: t.Name, Value: t.Token, Scopes: t.Scopes}
	}
	protected := auth.BearerTokenMiddleware(entries)(s.httpSrv)

	prefix := strings.TrimRight(s.cfg.Proxy.PathPrefix, "/")
	mux.Handle(prefix+"/mcp", protected)
	mux.Handle(prefix+"/mcp/", protected)

	return mux
}
