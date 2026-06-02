package database

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

const defaultSchemaCacheTTL = 5 * time.Minute

// ColumnInfo describes a single column returned by schema queries.
type ColumnInfo struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"`
	Nullable string `json:"is_nullable"`
}

// TableInfo describes a table and its columns.
type TableInfo struct {
	Name    string       `json:"name"`
	Columns []ColumnInfo `json:"columns"`
}

// DatabaseSchema describes all tables in a single database.
type DatabaseSchema struct {
	Database string      `json:"database"`
	Tables   []TableInfo `json:"tables"`
}

// schemaCache holds the cached schema for a single connection.
type schemaCache struct {
	mu        sync.Mutex
	data      []DatabaseSchema
	fetchedAt time.Time
	ttl       time.Duration
}

// valid reports whether the cache entry is still fresh.
func (c *schemaCache) valid() bool {
	return !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl
}

// SchemaStore manages per-connection schema caches.
type SchemaStore struct {
	mu    sync.Mutex
	ttl   time.Duration
	cache map[string]*schemaCache // keyed by connection name
}

// NewSchemaStore creates a SchemaStore with the given cache TTL.
// If ttl is zero, the default (5 minutes) is used.
func NewSchemaStore(ttl time.Duration) *SchemaStore {
	if ttl <= 0 {
		ttl = defaultSchemaCacheTTL
	}
	return &SchemaStore{
		ttl:   ttl,
		cache: make(map[string]*schemaCache),
	}
}

// getOrCreate returns the cache entry for name, creating it on first access.
func (s *SchemaStore) getOrCreate(name string) *schemaCache {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cache[name]; ok {
		return c
	}
	c := &schemaCache{ttl: s.ttl}
	s.cache[name] = c
	return c
}

// Invalidate clears the cached schema for the given connection name so that
// the next call to Get fetches fresh data.
func (s *SchemaStore) Invalidate(name string) {
	c := s.getOrCreate(name)
	c.mu.Lock()
	c.data = nil
	c.fetchedAt = time.Time{}
	c.mu.Unlock()
}

// Get returns the (possibly cached) schema for a connection.
// When dbFilter is non-empty, only the matching database is returned.
// When refresh is true, the cache is invalidated before fetching.
func (s *SchemaStore) Get(pool *Pool, name string, dbFilter string, refresh bool) ([]DatabaseSchema, error) {
	if refresh {
		s.Invalidate(name)
	}

	c := s.getOrCreate(name)
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.valid() {
		db, err := pool.Get(name)
		if err != nil {
			return nil, err
		}
		schemas, err := fetchAllSchemas(db)
		if err != nil {
			return nil, err
		}
		c.data = schemas
		c.fetchedAt = time.Now()
	}

	if dbFilter == "" {
		return c.data, nil
	}

	// Return only the matching database.
	for _, schema := range c.data {
		if schema.Database == dbFilter {
			return []DatabaseSchema{schema}, nil
		}
	}
	return nil, fmt.Errorf("database %q not found in connection %q", dbFilter, name)
}

// fetchAllSchemas queries INFORMATION_SCHEMA and returns the full schema for
// all non-system databases visible to the current user.
func fetchAllSchemas(db *sql.DB) ([]DatabaseSchema, error) {
	// Query all databases that are not MySQL internals.
	rows, err := db.Query(`
		SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, DATA_TYPE, IS_NULLABLE
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA NOT IN (
			'information_schema', 'performance_schema', 'mysql', 'sys'
		)
		ORDER BY TABLE_SCHEMA, TABLE_NAME, ORDINAL_POSITION
	`)
	if err != nil {
		return nil, fmt.Errorf("querying information_schema: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	// Collect results into a nested map for deduplication and ordering.
	type dbKey = string
	type tableKey = string
	dbOrder := make([]string, 0)
	dbSeen := make(map[dbKey]bool)
	tableOrder := make(map[dbKey][]string)
	tableSeen := make(map[dbKey]map[tableKey]bool)
	columns := make(map[dbKey]map[tableKey][]ColumnInfo)

	for rows.Next() {
		var dbName, tableName, colName, dataType, nullable string
		if err := rows.Scan(&dbName, &tableName, &colName, &dataType, &nullable); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		if !dbSeen[dbName] {
			dbOrder = append(dbOrder, dbName)
			dbSeen[dbName] = true
			tableOrder[dbName] = nil
			tableSeen[dbName] = make(map[tableKey]bool)
			columns[dbName] = make(map[tableKey][]ColumnInfo)
		}
		if !tableSeen[dbName][tableName] {
			tableOrder[dbName] = append(tableOrder[dbName], tableName)
			tableSeen[dbName][tableName] = true
		}
		columns[dbName][tableName] = append(columns[dbName][tableName], ColumnInfo{
			Name:     colName,
			DataType: dataType,
			Nullable: nullable,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	schemas := make([]DatabaseSchema, 0, len(dbOrder))
	for _, dbName := range dbOrder {
		tables := make([]TableInfo, 0, len(tableOrder[dbName]))
		for _, tbl := range tableOrder[dbName] {
			tables = append(tables, TableInfo{
				Name:    tbl,
				Columns: columns[dbName][tbl],
			})
		}
		schemas = append(schemas, DatabaseSchema{
			Database: dbName,
			Tables:   tables,
		})
	}
	return schemas, nil
}
