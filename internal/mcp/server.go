package mcp

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kleist-dev/logmcp/internal/audit"
	"github.com/kleist-dev/logmcp/internal/auth"
	"github.com/kleist-dev/logmcp/internal/check"
	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/extensions/clitool"
	"github.com/kleist-dev/logmcp/internal/extensions/rpc"
	"github.com/kleist-dev/logmcp/internal/logs"
	"github.com/kleist-dev/logmcp/internal/macro"
	internaltls "github.com/kleist-dev/logmcp/internal/tls"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const authVerifyCacheTTL = 10 * time.Minute
const maxRequestBodyBytes = 4 * 1024 * 1024 // 4 MB — prevents OOM from oversized MCP requests

// clientIPKey is the context key used to propagate client IP to tool handlers.
type clientIPKey struct{}

// Server wraps the MCP server and its HTTP configuration.
type Server struct {
	mu               sync.RWMutex
	cfg              *config.Config
	logMgr           *logs.Manager
	docsFS           embed.FS
	mcpSrv           *server.MCPServer
	httpSrv          *server.StreamableHTTPServer
	burstLimiter     *auth.RateLimiter
	sustainedLimiter *auth.RateLimiter
	verifyCache      *auth.VerifyCache
}

// New creates a new MCP Server with all tools registered.
func New(cfg *config.Config, logMgr *logs.Manager, docsFS embed.FS) (*Server, error) {
	burst, sustained := newRateLimiters(cfg)
	s := &Server{
		cfg:              cfg,
		logMgr:           logMgr,
		docsFS:           docsFS,
		burstLimiter:     burst,
		sustainedLimiter: sustained,
	}
	if cfg.Auth.Authenticator != nil {
		s.verifyCache = auth.NewVerifyCache(makeAuthVerifyFunc(cfg), authVerifyCacheTTL)
	}

	// Build the MCP server.
	s.mcpSrv = server.NewMCPServer(
		"LogMCP",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithInstructions(loadServerDesc()),
	)

	s.registerTools()
	s.registerResources()

	s.registerCltoolExtensions()

	// Build the StreamableHTTP transport.
	s.httpSrv = server.NewStreamableHTTPServer(
		s.mcpSrv,
		server.WithEndpointPath("/mcp"),
		server.WithHTTPContextFunc(s.httpContextFunc),
	)

	return s, nil
}

// registerResources adds all embedded docs/*.md files as MCP resources.
func (s *Server) registerResources() {
	fs.WalkDir(s.docsFS, "docs", func(path string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, readErr := s.docsFS.ReadFile(path)
		if readErr != nil {
			return nil
		}
		stem := strings.ToLower(strings.TrimSuffix(d.Name(), ".md"))
		uri := "logmcp://docs/" + stem
		name := docTitle(data)
		if name == "" {
			name = "LogMCP " + d.Name()
		}
		filePath := path
		resource := mcp.NewResource(uri, name, mcp.WithMIMEType("text/markdown"))
		s.mcpSrv.AddResource(resource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			content, err := s.docsFS.ReadFile(filePath)
			if err != nil {
				return nil, fmt.Errorf("reading embedded doc %s: %w", filePath, err)
			}
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      uri,
					MIMEType: "text/markdown",
					Text:     string(content),
				},
			}, nil
		})
		return nil
	})
}

// docTitle returns the text of the first "# " heading in a markdown file.
func docTitle(data []byte) string {
	for _, line := range strings.SplitN(string(data), "\n", 30) {
		if rest, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// httpContextFunc injects the client IP and token name into the request context.
func (s *Server) httpContextFunc(ctx context.Context, r *http.Request) context.Context {
	s.mu.RLock()
	trustedProxy := s.cfg.Proxy.TrustedProxy
	s.mu.RUnlock()
	ip := extractClientIP(r, trustedProxy)
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
			return sanitizeIP(strings.TrimSpace(parts[0]))
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sanitizeIP strips any character not valid in an IP address to prevent log injection.
func sanitizeIP(ip string) string {
	for _, r := range ip {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') ||
			r == '.' || r == ':' || r == '[' || r == ']') {
			return "invalid"
		}
	}
	return ip
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

	// --- check_config ---
	if s.toolEnabled("check_config") {
		ccDesc := loadToolDesc("check_config")
		configTool := mcp.NewTool("check_config",
			mcp.WithDescription(ccDesc.Description),
		)
		s.mcpSrv.AddTool(configTool, s.handleCheckConfig)
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

	// --- macros (dynamically loaded from macros dir) ---
	s.registerMacros()
}

// registerMacros loads all macro YAML files from the configured macros directory
// and registers each as an MCP tool. Parse errors are logged and skipped.
func (s *Server) registerMacros() {
	dir := s.cfg.Extensions.Macros.Dir
	macros := macro.LoadDir(dir)
	runner := macro.NewRunner(s.cfg, s.logMgr)

	for _, m := range macros {
		if !s.toolEnabled(m.Name) {
			continue
		}

		// Capture loop variable for the closure.
		macroDef := m
		opts := []mcp.ToolOption{mcp.WithDescription(macroDef.Description)}
		for paramName, paramDef := range macroDef.Parameters {
			propOpts := []mcp.PropertyOption{mcp.Description(paramDef.Description)}
			if !paramDef.Optional {
				propOpts = append(propOpts, mcp.Required())
			}
			opts = append(opts, mcp.WithString(paramName, propOpts...))
		}

		tool := mcp.NewTool(macroDef.Name, opts...)
		s.mcpSrv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Collect all string parameters from the MCP call.
			params := make(map[string]string)
			for paramName := range macroDef.Parameters {
				params[paramName] = req.GetString(paramName, "")
			}

			result, err := runner.Run(ctx, macroDef, params)
			if err != nil {
				// Return the partial result as JSON, with the step error embedded.
				return marshalResult(result)
			}
			return marshalResult(result)
		})
	}
}

// marshalResult marshals v to JSON and returns a tool result, or a tool error on failure.
func marshalResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("serialising result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// parseTimeParam parses a named time/duration parameter value.
// It returns nil, nil when value is empty (parameter was not supplied).
func parseTimeParam(name, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	t, err := logs.ParseTimeOrDuration(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %q value: %w", name, err)
	}
	return &t, nil
}

// handleListLogs implements the list_logs tool.
func (s *Server) handleListLogs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	clientIP := clientIPFromCtx(ctx)

	files, err := s.logMgr.ListAccessible()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing log files: %v", err)), nil
	}

	_ = audit.Log("list_logs", "<all>", clientIP)

	return marshalResult(files)
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

	var err error
	opts.Since, err = parseTimeParam("since", req.GetString("since", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	opts.Until, err = parseTimeParam("until", req.GetString("until", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var lines []string
	if logs.IsJournaldPath(path) {
		lines, err = s.logMgr.ReadJournald(ctx, path, opts)
	} else {
		lines, err = s.logMgr.ReadFile(ctx, path, opts)
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

	return marshalResult(result)
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

	var err error
	opts.Since, err = parseTimeParam("since", req.GetString("since", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	opts.Until, err = parseTimeParam("until", req.GetString("until", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var matches []logs.Match
	if logs.IsJournaldPath(path) {
		matches, err = s.logMgr.SearchJournald(ctx, path, opts)
	} else {
		matches, err = s.logMgr.SearchFile(ctx, path, opts)
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

	return marshalResult(result)
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

	return marshalResult(fi)
}

// handleCheckEnvironment implements the check_environment tool.
func (s *Server) handleCheckEnvironment(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	result := check.Run(cfg, check.Options{
		ConfigPath:  config.DefaultConfigPath,
		IncludePort: true,
	})

	return marshalResult(result)
}

// optionalParam describes a config parameter that is at its default value.
type optionalParam struct {
	Name        string `json:"name"`
	Default     string `json:"default"`
	Explanation string `json:"explanation"`
}

// currentValues holds the active configuration values reported by check_config.
type currentValues struct {
	Name        string   `json:"name"`
	ServerAddr  string   `json:"server_addr"`
	TLSMode     string   `json:"tls_mode"`
	ProxyMode   bool     `json:"proxy_mode"`
	PathPrefix  string   `json:"path_prefix,omitempty"`
	Domain      string   `json:"domain,omitempty"`
	Whitelist   []string `json:"whitelist"`
	Blacklist   []string `json:"blacklist,omitempty"`
	Journald    bool     `json:"journald"`
	AuditSyslog bool     `json:"audit_syslog"`
	Fail2ban    bool     `json:"fail2ban"`
	RateLimit   bool     `json:"rate_limit"`
	Disabled    []string `json:"tools_disabled,omitempty"`
	Macros      string   `json:"macros_dir,omitempty"`
	MySQL       int      `json:"mysql_connections"`
}

// collectDefaults returns the list of optional config parameters that are
// currently at their default (i.e. not explicitly configured).
func collectDefaults(cfg *config.Config) []optionalParam {
	var defaults []optionalParam

	if !cfg.Proxy.Enabled {
		defaults = append(defaults, optionalParam{
			Name:        "proxy.enabled",
			Default:     "false",
			Explanation: "Enable when serving behind a reverse proxy (Caddy, nginx). Required for correct client IP detection and URL generation.",
		})
	}
	if !cfg.Logs.Journald {
		defaults = append(defaults, optionalParam{
			Name:        "logs.journald",
			Default:     "false",
			Explanation: "Set to true to expose the systemd journal as a virtual journald:// log source.",
		})
	}
	if len(cfg.Logs.Blacklist) == 0 {
		defaults = append(defaults, optionalParam{
			Name:        "logs.blacklist",
			Default:     "[]",
			Explanation: "Paths to exclude from whitelist matches. Useful to hide sensitive files covered by a wildcard pattern.",
		})
	}
	if cfg.Security.RateLimit == nil {
		defaults = append(defaults, optionalParam{
			Name:        "security.rate_limit",
			Default:     "null (disabled)",
			Explanation: "Per-IP rate limiting for failed authentication attempts. Add burst and/or sustained sub-blocks to enable.",
		})
	}
	if !cfg.Security.Fail2ban.Enabled {
		defaults = append(defaults, optionalParam{
			Name:        "security.fail2ban.enabled",
			Default:     "false",
			Explanation: "Install a fail2ban filter and jail for brute-force protection against repeated auth failures.",
		})
	}
	if len(cfg.Tools.Disabled) == 0 {
		defaults = append(defaults, optionalParam{
			Name:        "tools.disabled",
			Default:     "[]",
			Explanation: "List tool names here to hide them from AI clients (e.g. [switchboard_debug] when the extension is not needed).",
		})
	}
	if cfg.Extensions.Macros.Dir == "" {
		defaults = append(defaults, optionalParam{
			Name:        "extensions.macros.dir",
			Default:     "\"\" (disabled)",
			Explanation: "Directory for YAML macro files that define reusable log query shortcuts.",
		})
	}
	if len(cfg.Extensions.Databases.MySQL) == 0 {
		defaults = append(defaults, optionalParam{
			Name:        "extensions.databases.mysql",
			Default:     "[]",
			Explanation: "MySQL connection configurations for database log access via the databases extension.",
		})
	}

	return defaults
}

// handleCheckConfig implements the check_config tool.
func (s *Server) handleCheckConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	current := currentValues{
		Name:        cfg.Name,
		ServerAddr:  fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		TLSMode:     cfg.Server.TLS.Mode,
		ProxyMode:   cfg.Proxy.Enabled,
		PathPrefix:  cfg.Proxy.PathPrefix,
		Domain:      cfg.Proxy.Domain,
		Whitelist:   cfg.Logs.Whitelist,
		Blacklist:   cfg.Logs.Blacklist,
		Journald:    cfg.Logs.Journald,
		AuditSyslog: cfg.Audit.Syslog,
		Fail2ban:    cfg.Security.Fail2ban.Enabled,
		RateLimit:   cfg.Security.RateLimit != nil,
		Disabled:    cfg.Tools.Disabled,
		Macros:      cfg.Extensions.Macros.Dir,
		MySQL:       len(cfg.Extensions.Databases.MySQL),
	}

	result := struct {
		Current  currentValues   `json:"current"`
		Defaults []optionalParam `json:"defaults"`
	}{
		Current:  current,
		Defaults: collectDefaults(cfg),
	}

	return marshalResult(result)
}

// registerCltoolExtensions discovers and registers all configured clitool extensions.
// For each extension, it calls `<command> list` to get tool definitions, then registers
// each tool with a name prefix of `<ext.Name>_`. If an extension cannot be reached,
// a warning is logged and the extension is skipped — the server starts regardless.
// Use `logmcp check` to diagnose extension access problems.
func (s *Server) registerCltoolExtensions() {
	for _, ext := range s.cfg.Extensions.Clitool {
		timeout := time.Duration(ext.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 10 * time.Second
		}

		tools, err := clitool.List(ext.Command, timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: clitool extension %q unavailable: %v (run `logmcp check` for details)\n", ext.Name, err)
			continue
		}

		// Capture loop variables for closures.
		extName := ext.Name
		extCommand := ext.Command
		extTimeout := timeout
		extMode := ext.Mode
		extRedisAddr := ext.RedisAddr
		if extRedisAddr == "" {
			extRedisAddr = "127.0.0.1:6379"
		}

		for _, toolDef := range tools {
			prefixedName := extName + "_" + toolDef.Name
			unprefixedName := toolDef.Name

			desc := toolDef.Description
			if len(toolDef.InputSchema) > 0 && string(toolDef.InputSchema) != "null" {
				desc += "\n\nInput schema: " + string(toolDef.InputSchema)
			}

			tool := mcp.NewTool(prefixedName, mcp.WithDescription(desc))
			s.mcpSrv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				// Extract raw params from the request arguments.
				var params json.RawMessage
				if req.Params.Arguments != nil {
					if data, err := json.Marshal(req.Params.Arguments); err == nil {
						params = data
					}
				}

				var result *clitool.CallResult
				var err error

				if extMode == "rpc" {
					callerName := auth.TokenNameFromCtx(ctx)
					callerScopes := auth.TokenScopesFromCtx(ctx)
					if callerScopes == nil {
						if t := s.cfg.Auth.Find(callerName); t != nil {
							callerScopes = t.Scopes
						} else {
							callerScopes = []string{}
						}
					}
					result, err = rpc.Call(ctx, extRedisAddr, unprefixedName, callerName, callerScopes, params, extTimeout)
				} else {
					token := auth.TokenValueFromCtx(ctx)
					result, err = clitool.Call(ctx, extCommand, unprefixedName, token, params, extTimeout)
				}

				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("clitool call error: %v", err)), nil
				}

				switch result.Code {
				case "auth_failed":
					return mcp.NewToolResultError("authentication failed: " + result.Error), nil
				case "scope_denied":
					return mcp.NewToolResultError("permission denied: " + result.Error), nil
				}

				if !result.OK {
					errMsg := result.Error
					if errMsg == "" {
						errMsg = "clitool call failed"
					}
					return mcp.NewToolResultError(errMsg), nil
				}

				if result.Result != nil {
					return mcp.NewToolResultText(string(result.Result)), nil
				}
				return mcp.NewToolResultText("{}"), nil
			})
		}
	}
}

// Start launches the MCP HTTP server, blocking until it exits.
// It handles SIGTERM and SIGINT by draining active requests (up to 10s) before
// shutting down. A clean shutdown returns nil.
func (s *Server) Start() error {
	cfg := s.cfg
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	handler := s.buildHandler()

	var httpSrv *http.Server
	var useTLS bool

	switch cfg.Server.TLS.Mode {
	case "self-signed", "custom":
		tlsCfg, err := internaltls.LoadTLSConfig(cfg.Server.TLS.Cert, cfg.Server.TLS.Key)
		if err != nil {
			return fmt.Errorf("loading TLS config: %w", err)
		}
		httpSrv = &http.Server{
			Addr:         addr,
			Handler:      handler,
			TLSConfig:    tlsCfg,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 120 * time.Second,
			IdleTimeout:  120 * time.Second,
		}
		useTLS = true

	case "off":
		httpSrv = &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 120 * time.Second,
			IdleTimeout:  120 * time.Second,
		}

	default:
		return fmt.Errorf("unknown TLS mode %q; expected self-signed, custom, or off", cfg.Server.TLS.Mode)
	}

	// Start signal handler before serving so no signal is missed.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-quit
		cleanupCancel()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	// Periodically prune expired entries from rate limiters to prevent unbounded growth
	// when many distinct IPs generate a single failure each (e.g. rotating-IP botnet).
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.mu.RLock()
				bl, sl := s.burstLimiter, s.sustainedLimiter
				s.mu.RUnlock()
				if bl != nil {
					bl.PruneAll()
				}
				if sl != nil {
					sl.PruneAll()
				}
			case <-cleanupCtx.Done():
				return
			}
		}
	}()

	var err error
	if useTLS {
		fmt.Printf("LogMCP listening on https://%s/mcp\n", addr)
		err = httpSrv.ListenAndServeTLS("", "")
	} else {
		fmt.Printf("LogMCP listening on http://%s/mcp\n", addr)
		err = httpSrv.ListenAndServe()
	}

	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// makeAuthVerifyFunc builds a VerifyFunc that calls the configured authenticator subprocess.
func makeAuthVerifyFunc(cfg *config.Config) auth.VerifyFunc {
	cmd := cfg.Auth.Authenticator.Command
	timeout := time.Duration(cfg.Auth.Authenticator.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return func(token string) (string, []string, bool, error) {
		result, err := clitool.Verify(cmd, token, timeout)
		if err != nil {
			return "", nil, false, err
		}
		return result.Name, result.Scopes, result.Authenticated, nil
	}
}

// newRateLimiters creates burst and sustained RateLimiters from config.
// Either may be nil if that tier is absent from the config.
func newRateLimiters(cfg *config.Config) (burst, sustained *auth.RateLimiter) {
	if cfg.Security.RateLimit == nil {
		return nil, nil
	}
	if b := cfg.Security.RateLimit.Burst; b != nil {
		burst = auth.NewRateLimiter(b.MaxFailures, time.Duration(b.WindowSeconds)*time.Second)
	}
	if s := cfg.Security.RateLimit.Sustained; s != nil {
		sustained = auth.NewRateLimiter(s.MaxFailures, time.Duration(s.WindowSeconds)*time.Second)
	}
	return
}

// buildTokenHandler serves one request using the static bearer-token list from cfg.
func buildTokenHandler(s *Server, cfg *config.Config, burst, sustained *auth.RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		getIP := func(r *http.Request) string { return extractClientIP(r, cfg.Proxy.TrustedProxy) }
		entries := make([]auth.TokenEntry, len(cfg.Auth.Tokens))
		for i, t := range cfg.Auth.Tokens {
			entries[i] = auth.TokenEntry{Name: t.Name, Value: t.Token, Scopes: t.Scopes}
		}
		auth.BearerTokenMiddleware(entries, getIP, burst, sustained)(next).ServeHTTP(w, r)
	})
}

// buildAuthenticatorHandler serves one request by delegating auth to the external authenticator.
func buildAuthenticatorHandler(s *Server, cfg *config.Config, burst, sustained *auth.RateLimiter, verifyCache *auth.VerifyCache, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		getIP := func(r *http.Request) string { return extractClientIP(r, cfg.Proxy.TrustedProxy) }
		auth.AuthenticatorMiddleware(verifyCache.Verify, "logmcp:read", getIP, burst, sustained)(next).ServeHTTP(w, r)
	})
}

// buildHandler wraps the MCP StreamableHTTPServer with the auth middleware.
// Auth tokens are read on every request so they reflect the latest reloaded config.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		s.mu.RLock()
		cfg := s.cfg
		burst := s.burstLimiter
		sustained := s.sustainedLimiter
		verifyCache := s.verifyCache
		s.mu.RUnlock()

		if cfg.Auth.Authenticator != nil {
			buildAuthenticatorHandler(s, cfg, burst, sustained, verifyCache, s.httpSrv).ServeHTTP(w, r)
			return
		}
		buildTokenHandler(s, cfg, burst, sustained, s.httpSrv).ServeHTTP(w, r)
	})

	s.mu.RLock()
	prefix := strings.TrimRight(s.cfg.Proxy.PathPrefix, "/")
	s.mu.RUnlock()

	mux.Handle(prefix+"/mcp", protected)
	mux.Handle(prefix+"/mcp/", protected)

	return mux
}

// Reload loads a new config from path and applies it without restarting the server.
// Network settings (port, TLS) are not updated — those require a full restart.
func (s *Server) Reload(path string) error {
	newCfg, err := config.Load(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	oldCmd := ""
	if s.cfg.Auth.Authenticator != nil {
		oldCmd = s.cfg.Auth.Authenticator.Command
	}
	s.cfg = newCfg
	s.burstLimiter, s.sustainedLimiter = newRateLimiters(newCfg)
	s.logMgr.Update(newCfg.Logs.Whitelist, newCfg.Logs.Blacklist, newCfg.Logs.Journald)
	if newCfg.Auth.Authenticator != nil {
		newCmd := newCfg.Auth.Authenticator.Command
		if s.verifyCache == nil || oldCmd != newCmd {
			s.verifyCache = auth.NewVerifyCache(makeAuthVerifyFunc(newCfg), authVerifyCacheTTL)
		}
	} else {
		s.verifyCache = nil
	}
	s.mu.Unlock()
	fmt.Fprintln(os.Stderr, "logmcp: config reloaded")
	return nil
}
