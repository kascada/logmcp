// Package database provides a connection pool and helpers for named MySQL
// database connections configured in LogMCP's config.yaml.
package database

import (
	"database/sql"
	"fmt"
	"sync"

	// MySQL driver — imported for side-effects (registers "mysql" with database/sql).
	_ "github.com/go-sql-driver/mysql"

	"github.com/kleist-dev/logmcp/internal/config"
)

// Pool manages a set of *sql.DB connections, one per named database entry.
// Connections are lazily initialised on first use and reused thereafter.
type Pool struct {
	mu      sync.Mutex
	configs map[string]string   // name → DSN
	conns   map[string]*sql.DB  // name → open connection (nil until first use)
}

// NewPool creates a new Pool from the given database configs.
// Connections are not opened at construction time.
func NewPool(cfgs []config.DatabaseConfig) *Pool {
	p := &Pool{
		configs: make(map[string]string, len(cfgs)),
		conns:   make(map[string]*sql.DB, len(cfgs)),
	}
	for _, c := range cfgs {
		p.configs[c.Name] = c.DSN
	}
	return p
}

// Get returns the *sql.DB for the named connection, opening it lazily on first
// call.  Returns an error if name is unknown or the connection cannot be
// opened.
func (p *Pool) Get(name string) (*sql.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if db, ok := p.conns[name]; ok {
		return db, nil
	}
	dsn, ok := p.configs[name]
	if !ok {
		return nil, fmt.Errorf("unknown database connection %q", name)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database %q: %w", name, err)
	}
	p.conns[name] = db
	return db, nil
}

// Names returns the list of configured connection names.
func (p *Pool) Names() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.configs))
	for n := range p.configs {
		names = append(names, n)
	}
	return names
}

// Known reports whether name is a configured connection.
func (p *Pool) Known(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.configs[name]
	return ok
}

// Close closes all open *sql.DB connections.  It is called during server
// shutdown (after SIGTERM/SIGINT).
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, db := range p.conns {
		_ = db.Close()
		delete(p.conns, name)
	}
}
