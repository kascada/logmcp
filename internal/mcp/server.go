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
	"github.com/kleist-dev/logmcp/internal/database"
	"github.com/kleist-dev/logmcp/internal/extensions/clitool"
	"github.com/kleist-dev/logmcp/internal/extensions/dispatcher"
	"github.com/kleist-dev/logmcp/internal/logs"
	"github.com/kleist-dev/logmcp/internal/macro"
	"github.com/kleist-dev/logmcp/internal/notify"
	"github.com/kleist-dev/logmcp/internal/rag"
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
	tokenEntries     []auth.TokenEntry
	logMgr           *logs.Manager
	docsFS           embed.FS
	mcpSrv           *server.MCPServer
	httpSrv          *server.StreamableHTTPServer
	burstLimiter     *auth.RateLimiter
	sustainedLimiter *auth.RateLimiter
	verifyCache      *auth.VerifyCache
	dispatcher       *dispatcher.Dispatcher
	registeredTools  map[string]struct{}
	notifyService    *notify.Service      // nil when telegram.yaml is absent
	ragQuerier       *rag.Querier         // nil when RAG is not configured
	dbPool           *database.Pool       // nil when no databases are configured
	schemaStore      *database.SchemaStore // nil when no databases are configured
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
		dispatcher:       dispatcher.New(cfg.Extensions.Clitool),
		registeredTools:  make(map[string]struct{}),
	}
	if cfg.Auth.Authenticator != nil {
		s.verifyCache = auth.NewVerifyCache(makeAuthVerifyFunc(cfg), authVerifyCacheTTL)
	}
	s.tokenEntries = buildTokenEntries(cfg.Auth.Tokens)

	s.ragQuerier = buildQuerier(cfg)
	s.dbPool, s.schemaStore = buildDBComponents(cfg)

	// Load optional Telegram config. Missing file → service stays nil (feature disabled).
	if telegramCfg, ok, err := notify.LoadTelegramConfig(notify.TelegramConfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "logmcp: telegram config error: %v\n", err)
	} else if ok {
		s.notifyService = notify.NewService(telegramCfg)
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

	// Build the StreamableHTTP transport.
	s.httpSrv = server.NewStreamableHTTPServer(
		s.mcpSrv,
		server.WithEndpointPath("/mcp"),
		server.WithHTTPContextFunc(s.httpContextFunc),
	)

	return s, nil
}

// registerResources adds all embedded docs/*.md files as MCP resources,
// and the database schema resource template when databases are configured.
func (s *Server) registerResources() {
	if s.dbPool != nil {
		s.registerDBResources()
	}

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
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') &&
			r != '.' && r != ':' && r != '[' && r != ']' {
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

// trackTool marks name as registered. Returns false if already registered.
func (s *Server) trackTool(name string) bool {
	if _, exists := s.registeredTools[name]; exists {
		return false
	}
	s.registeredTools[name] = struct{}{}
	return true
}

// withScope wraps a tool handler, returning a scope-denied error if the caller's
// context does not contain the required scope.
func withScope(scope string, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if slices.Contains(auth.TokenScopesFromCtx(ctx), scope) {
			return h(ctx, req)
		}
		return mcp.NewToolResultError("missing required scope: " + scope), nil
	}
}

func (s *Server) registerTools() {
	// --- list_logs ---
	if s.toolEnabled("list_logs") {
		s.trackTool("list_logs")
		llDesc := loadToolDesc("list_logs")
		listLogsTool := mcp.NewTool("list_logs",
			mcp.WithDescription(llDesc.Description),
		)
		s.mcpSrv.AddTool(listLogsTool, withScope("logmcp:read", s.handleListLogs))
	}

	// --- read_log ---
	if s.toolEnabled("read_log") {
		s.trackTool("read_log")
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
		s.mcpSrv.AddTool(readLogTool, withScope("logmcp:read", s.handleReadLog))
	}

	// --- search_log ---
	if s.toolEnabled("search_log") {
		s.trackTool("search_log")
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
		s.mcpSrv.AddTool(searchLogTool, withScope("logmcp:read", s.handleSearchLog))
	}

	// --- check_environment ---
	if s.toolEnabled("check_environment") {
		s.trackTool("check_environment")
		ceDesc := loadToolDesc("check_environment")
		checkTool := mcp.NewTool("check_environment",
			mcp.WithDescription(ceDesc.Description),
		)
		s.mcpSrv.AddTool(checkTool, withScope("logmcp:read", s.handleCheckEnvironment))
	}

	// --- check_config ---
	if s.toolEnabled("check_config") {
		s.trackTool("check_config")
		ccDesc := loadToolDesc("check_config")
		configTool := mcp.NewTool("check_config",
			mcp.WithDescription(ccDesc.Description),
		)
		s.mcpSrv.AddTool(configTool, withScope("logmcp:read", s.handleCheckConfig))
	}

	// --- log_info ---
	if s.toolEnabled("log_info") {
		s.trackTool("log_info")
		liDesc := loadToolDesc("log_info")
		logInfoTool := mcp.NewTool("log_info",
			mcp.WithDescription(liDesc.Description),
			mcp.WithString("path",
				mcp.Required(),
				mcp.Description(liDesc.Params["path"]),
			),
		)
		s.mcpSrv.AddTool(logInfoTool, withScope("logmcp:read", s.handleLogInfo))
	}

	// --- server_status ---
	if s.toolEnabled("server_status") {
		s.trackTool("server_status")
		ssDesc := loadToolDesc("server_status")
		serverStatusTool := mcp.NewTool("server_status",
			mcp.WithDescription(ssDesc.Description),
		)
		s.mcpSrv.AddTool(serverStatusTool, withScope("logmcp:read", s.handleServerStatus))
	}

	// --- macros (dynamically loaded from macros dir) ---
	s.registerMacros()

	// --- extension tools (exposed from configured clitool/rpc extensions) ---
	s.registerExtensionTools()

	// --- rag_query (only when RAG is configured) ---
	if s.ragQuerier != nil && s.toolEnabled("rag_query") {
		s.trackTool("rag_query")
		rqDesc := loadToolDesc("rag_query")
		ragQueryTool := mcp.NewTool("rag_query",
			mcp.WithDescription(rqDesc.Description),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description(rqDesc.Params["query"]),
			),
			mcp.WithNumber("top_k",
				mcp.Description(rqDesc.Params["top_k"]),
				mcp.DefaultNumber(5),
			),
			mcp.WithString("source",
				mcp.Description(rqDesc.Params["source"]),
			),
		)
		s.mcpSrv.AddTool(ragQueryTool, withScope("logmcp:read", s.handleRagQuery))
	}

	// --- notify tools (only when telegram.yaml was loaded successfully) ---
	if s.notifyService != nil {
		s.registerNotifyTools()
	}

	// --- database tools (only when databases are configured) ---
	if s.dbPool != nil {
		s.registerDBTools()
	}
}

// registerMacros loads all macro YAML files from the configured macros directory
// and registers each as an MCP tool. Parse errors are logged and skipped.
func (s *Server) registerMacros() {
	dir := s.cfg.Extensions.Macros.Dir
	macros := macro.LoadDir(dir)
	runner := macro.NewRunner(s.logMgr, s.dispatcher)

	for _, m := range macros {
		if !s.toolEnabled(m.Name) {
			continue
		}
		if !s.trackTool(m.Name) {
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
		s.mcpSrv.AddTool(tool, withScope("logmcp:read", func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			params := make(map[string]string)
			for paramName := range macroDef.Parameters {
				params[paramName] = req.GetString(paramName, "")
			}
			result, err := runner.Run(ctx, macroDef, params)
			if err != nil {
				return marshalResult(result)
			}
			return marshalResult(result)
		}))
	}
}

// registerExtensionTools discovers tools from each configured clitool extension
// and registers them as MCP tools. Tools whose names are already registered
// (built-ins or macros) are skipped with a warning.
func (s *Server) registerExtensionTools() {
	for _, ext := range s.cfg.Extensions.Clitool {
		timeout := 5 * time.Second
		if ext.TimeoutSeconds > 0 {
			timeout = time.Duration(ext.TimeoutSeconds) * time.Second
		}
		tools, err := clitool.List(ext.Command, timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "logmcp: extension %q: list tools failed: %v\n", ext.Name, err)
			continue
		}
		extName := ext.Name
		for _, td := range tools {
			registeredName := extName + "_" + td.Name
			if !s.toolEnabled(registeredName) {
				continue
			}
			if !s.trackTool(registeredName) {
				fmt.Fprintf(os.Stderr, "logmcp: extension %q: tool %q skipped (name already registered)\n", extName, registeredName)
				continue
			}
			toolDef := td
			tool := mcp.NewToolWithRawSchema(registeredName, toolDef.Description, toolDef.InputSchema)
			s.mcpSrv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				params, err := json.Marshal(req.GetArguments())
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("marshalling params: %v", err)), nil
				}
				result, dispErr := s.dispatcher.Call(ctx, extName, toolDef.Name, "logmcp-mcp", auth.TokenScopesFromCtx(ctx), params)
				if dispErr != nil {
					return mcp.NewToolResultError(dispErr.Error()), nil
				}
				return marshalResult(result)
			})
		}
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
	pool := s.dbPool
	s.mu.RUnlock()
	result := check.Run(cfg, check.Options{
		ConfigPath:  config.DefaultConfigPath,
		IncludePort: true,
		DBPool:      pool,
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
			Explanation: "List tool names here to hide them from AI clients (e.g. [switchboard_status] when that extension tool is not needed).",
		})
	}
	if cfg.Extensions.Macros.Dir == "" {
		defaults = append(defaults, optionalParam{
			Name:        "extensions.macros.dir",
			Default:     "\"\" (disabled)",
			Explanation: "Directory for YAML macro files that define reusable log query shortcuts.",
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
		Macros: cfg.Extensions.Macros.Dir,
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


// handleServerStatus implements the server_status tool.
func (s *Server) handleServerStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.RLock()
	cfg := s.cfg
	toolCount := len(s.registeredTools)
	s.mu.RUnlock()

	result := check.Result{OK: true}
	add := func(name string, ok bool, detail string) {
		if !ok {
			result.OK = false
		}
		result.Checks = append(result.Checks, check.Item{Name: name, OK: ok, Detail: detail})
	}

	add("MCP responding", true, fmt.Sprintf("%d tool(s) registered", toolCount))

	for _, ext := range cfg.Extensions.Clitool {
		tools, err := clitool.List(ext.Command, 3*time.Second)
		if err != nil {
			add(fmt.Sprintf("extension %q", ext.Name), false, err.Error())
		} else {
			add(fmt.Sprintf("extension %q", ext.Name), true, fmt.Sprintf("%d tool(s)", len(tools)))
		}
	}

	return marshalResult(result)
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
		s.dispatcher.Close()
		s.mu.RLock()
		pool := s.dbPool
		s.mu.RUnlock()
		if pool != nil {
			pool.Close()
		}
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

// buildTokenEntries converts a slice of TokenConfig to a slice of auth.TokenEntry.
// The result is cached on the Server and recomputed only on config reload.
func buildTokenEntries(tokens []config.TokenConfig) []auth.TokenEntry {
	entries := make([]auth.TokenEntry, len(tokens))
	for i, t := range tokens {
		entries[i] = auth.TokenEntry{Name: t.Name, Value: t.Token, Scopes: t.Scopes}
	}
	return entries
}

// buildHandler wraps the MCP StreamableHTTPServer with the auth middleware.
// Auth tokens are read on every request so they reflect the latest reloaded config.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		s.mu.RLock()
		cfg := s.cfg
		entries := s.tokenEntries
		burst := s.burstLimiter
		sustained := s.sustainedLimiter
		verifyCache := s.verifyCache
		s.mu.RUnlock()

		getIP := func(r *http.Request) string { return extractClientIP(r, cfg.Proxy.TrustedProxy) }

		var resolve auth.VerifyFunc
		if cfg.Auth.Authenticator != nil {
			resolve = verifyCache.Verify
		} else {
			resolve = auth.StaticResolver(entries)
		}

		auth.Middleware(resolve, getIP, burst, sustained)(s.httpSrv).ServeHTTP(w, r)
	})

	s.mu.RLock()
	prefix := strings.TrimRight(s.cfg.Proxy.PathPrefix, "/")
	s.mu.RUnlock()

	mux.Handle(prefix+"/mcp", protected)
	mux.Handle(prefix+"/mcp/", protected)
	mux.HandleFunc(prefix+"/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc(prefix+"/status", s.handleStatus)

	return mux
}

// handleStatus returns a JSON health report. No authentication required so
// the endpoint works even when auth is misconfigured.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	result := check.Result{
		OK: true,
		Checks: []check.Item{
			{Name: "server running", OK: true, Detail: "HTTP handler responding"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	if !result.OK {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(result)
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
	s.tokenEntries = buildTokenEntries(newCfg.Auth.Tokens)
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
	// Reload telegram.yaml if notify tools were registered at startup.
	// If the file was absent at startup the tools are not registered — a restart is required.
	if s.notifyService != nil {
		if telegramCfg, ok, err := notify.LoadTelegramConfig(notify.TelegramConfigPath); err != nil {
			fmt.Fprintf(os.Stderr, "logmcp: telegram config reload error: %v\n", err)
		} else if ok {
			s.notifyService = notify.NewService(telegramCfg)
		}
	}
	s.mu.Unlock()
	fmt.Fprintln(os.Stderr, "logmcp: config reloaded")
	return nil
}
