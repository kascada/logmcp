package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/database"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

const dbMaxRows = 1000

// handleDBQuery implements the db_query MCP tool.
// It executes an arbitrary SQL statement on the named connection and returns
// the column names and rows as JSON.
func (s *Server) handleDBQuery(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	query, err := req.RequireString("query")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	s.mu.RLock()
	pool := s.dbPool
	s.mu.RUnlock()
	if pool == nil {
		return mcplib.NewToolResultError("database pool not initialised"), nil
	}
	if !pool.Known(name) {
		return mcplib.NewToolResultError(fmt.Sprintf("unknown database connection %q", name)), nil
	}

	db, err := pool.Get(name)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("connecting to %q: %v", name, err)), nil
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("query error: %v", err)), nil
	}
	defer rows.Close() //nolint:errcheck

	cols, err := rows.Columns()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("reading columns: %v", err)), nil
	}

	resultRows := make([]map[string]any, 0, 64)
	truncated := false

	for rows.Next() {
		if len(resultRows) >= dbMaxRows {
			truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("scanning row: %v", err)), nil
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			v := vals[i]
			// Convert []byte → string for readability.
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			// Convert sql.NullString and similar null types.
			if ns, ok := v.(sql.NullString); ok {
				if ns.Valid {
					v = ns.String
				} else {
					v = nil
				}
			}
			row[col] = v
		}
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("iterating rows: %v", err)), nil
	}

	type queryResult struct {
		Columns   []string         `json:"columns"`
		Rows      []map[string]any `json:"rows"`
		RowCount  int              `json:"row_count"`
		Truncated bool             `json:"truncated,omitempty"`
		Note      string           `json:"note,omitempty"`
	}
	out := queryResult{
		Columns:   cols,
		Rows:      resultRows,
		RowCount:  len(resultRows),
		Truncated: truncated,
	}
	if truncated {
		out.Note = fmt.Sprintf("result truncated to %d rows; use WHERE or LIMIT to narrow the query", dbMaxRows)
	}

	data, err := json.Marshal(out)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("serialising result: %v", err)), nil
	}
	return mcplib.NewToolResultText(string(data)), nil
}

// handleDBSchema implements the db_schema MCP tool.
// It returns the schema (databases → tables → columns) for the named
// connection, optionally filtered to a single database.
func (s *Server) handleDBSchema(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	_ = ctx
	name, err := req.RequireString("name")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	dbFilter := req.GetString("database", "")
	refresh := req.GetBool("refresh", false)

	s.mu.RLock()
	pool := s.dbPool
	schemaStore := s.schemaStore
	s.mu.RUnlock()
	if pool == nil {
		return mcplib.NewToolResultError("database pool not initialised"), nil
	}
	if !pool.Known(name) {
		return mcplib.NewToolResultError(fmt.Sprintf("unknown database connection %q", name)), nil
	}

	schemas, err := schemaStore.Get(pool, name, dbFilter, refresh)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("schema error: %v", err)), nil
	}

	data, err := json.Marshal(schemas)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("serialising schema: %v", err)), nil
	}
	return mcplib.NewToolResultText(string(data)), nil
}

// handleDBList implements the db_list MCP tool.
// It returns the list of database names visible on the named connection.
func (s *Server) handleDBList(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	_ = ctx
	name, err := req.RequireString("name")
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}

	s.mu.RLock()
	pool := s.dbPool
	s.mu.RUnlock()
	if pool == nil {
		return mcplib.NewToolResultError("database pool not initialised"), nil
	}
	if !pool.Known(name) {
		return mcplib.NewToolResultError(fmt.Sprintf("unknown database connection %q", name)), nil
	}

	db, err := pool.Get(name)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("connecting to %q: %v", name, err)), nil
	}

	rows, err := db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("listing databases: %v", err)), nil
	}
	defer rows.Close() //nolint:errcheck

	var names []string
	for rows.Next() {
		var dbName string
		if err := rows.Scan(&dbName); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("scanning: %v", err)), nil
		}
		// Filter out system databases.
		switch strings.ToLower(dbName) {
		case "information_schema", "performance_schema", "mysql", "sys":
			continue
		}
		names = append(names, dbName)
	}
	if err := rows.Err(); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("iterating: %v", err)), nil
	}

	type listResult struct {
		Connection string   `json:"connection"`
		Databases  []string `json:"databases"`
		Count      int      `json:"count"`
	}
	out := listResult{
		Connection: name,
		Databases:  names,
		Count:      len(names),
	}
	if out.Databases == nil {
		out.Databases = []string{}
	}

	data, err := json.Marshal(out)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("serialising result: %v", err)), nil
	}
	return mcplib.NewToolResultText(string(data)), nil
}

// handleDBSchemaResource serves the logmcp://db/{name}/schema MCP resource.
// The {name} variable is extracted from request.Params.Arguments by the
// mcp-go URI template matching logic.
func (s *Server) handleDBSchemaResource(ctx context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	_ = ctx

	// Extract the {name} variable populated by the URI template matcher.
	var name string
	if args := req.Params.Arguments; args != nil {
		if v, ok := args["name"]; ok {
			name, _ = v.(string)
		}
	}
	if name == "" {
		return nil, fmt.Errorf("missing {name} in resource URI %q", req.Params.URI)
	}

	s.mu.RLock()
	pool := s.dbPool
	schemaStore := s.schemaStore
	s.mu.RUnlock()

	if pool == nil || !pool.Known(name) {
		return nil, fmt.Errorf("unknown database connection %q", name)
	}

	schemas, err := schemaStore.Get(pool, name, "", false)
	if err != nil {
		return nil, fmt.Errorf("fetching schema for %q: %w", name, err)
	}

	data, err := json.Marshal(schemas)
	if err != nil {
		return nil, fmt.Errorf("serialising schema: %w", err)
	}

	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

// registerDBTools registers db_query, db_schema, and db_list MCP tools.
// Called only when len(cfg.Databases) > 0.
func (s *Server) registerDBTools() {
	if s.toolEnabled("db_query") && s.trackTool("db_query") {
		tool := mcplib.NewTool("db_query",
			mcplib.WithDescription("Execute a SQL query on a named database connection and return columns and rows as JSON. Results are capped at 1000 rows."),
			mcplib.WithString("name",
				mcplib.Required(),
				mcplib.Description("Name of the database connection as configured in databases[].name."),
			),
			mcplib.WithString("query",
				mcplib.Required(),
				mcplib.Description("SQL query to execute."),
			),
		)
		s.mcpSrv.AddTool(tool, withScope("logmcp:read", s.handleDBQuery))
	}

	if s.toolEnabled("db_schema") && s.trackTool("db_schema") {
		tool := mcplib.NewTool("db_schema",
			mcplib.WithDescription("Return the schema (databases → tables → columns with types) for a named connection. Results are cached for 5 minutes; pass refresh:true to force a reload."),
			mcplib.WithString("name",
				mcplib.Required(),
				mcplib.Description("Name of the database connection as configured in databases[].name."),
			),
			mcplib.WithString("database",
				mcplib.Description("Filter to a single database name. Omit to return all databases on the connection."),
			),
			mcplib.WithBoolean("refresh",
				mcplib.Description("Invalidate the schema cache and reload from the server before returning."),
				mcplib.DefaultBool(false),
			),
		)
		s.mcpSrv.AddTool(tool, withScope("logmcp:read", s.handleDBSchema))
	}

	if s.toolEnabled("db_list") && s.trackTool("db_list") {
		tool := mcplib.NewTool("db_list",
			mcplib.WithDescription("List the non-system databases accessible on a named connection."),
			mcplib.WithString("name",
				mcplib.Required(),
				mcplib.Description("Name of the database connection as configured in databases[].name."),
			),
		)
		s.mcpSrv.AddTool(tool, withScope("logmcp:read", s.handleDBList))
	}
}

// registerDBResources registers the logmcp://db/{name}/schema resource template.
// Called from registerResources only when len(cfg.Databases) > 0.
func (s *Server) registerDBResources() {
	template := mcplib.NewResourceTemplate(
		"logmcp://db/{name}/schema",
		"Database Schema",
		mcplib.WithTemplateDescription("Full schema (databases → tables → columns) for the named database connection. Cached for 5 minutes."),
		mcplib.WithTemplateMIMEType("application/json"),
	)
	s.mcpSrv.AddResourceTemplate(template, s.handleDBSchemaResource)
}

// buildDBComponents constructs a Pool and SchemaStore from the config.
// Returns nil, nil when no databases are configured.
func buildDBComponents(cfg *config.Config) (*database.Pool, *database.SchemaStore) {
	if len(cfg.Databases) == 0 {
		return nil, nil
	}
	pool := database.NewPool(cfg.Databases)
	store := database.NewSchemaStore(0) // 0 → default TTL (5 min)
	return pool, store
}
