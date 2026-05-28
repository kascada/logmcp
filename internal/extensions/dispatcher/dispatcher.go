// Package dispatcher routes macro extension steps to the correct transport (CLI or RPC).
package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/extensions/clitool"
	"github.com/kleist-dev/logmcp/internal/extensions/rpc"
)

const defaultTimeout = 10 * time.Second

// Dispatcher routes extension tool calls to the correct transport.
// For RPC extensions a *goredis.Client is cached per Redis address so that
// the connection pool is reused across calls instead of being torn down
// and rebuilt on every invocation.
type Dispatcher struct {
	exts       []config.CltoolExtension
	rpcClients map[string]*goredis.Client
	mu         sync.Mutex
}

// New creates a Dispatcher from the configured clitool extensions.
func New(exts []config.CltoolExtension) *Dispatcher {
	return &Dispatcher{
		exts:       exts,
		rpcClients: make(map[string]*goredis.Client),
	}
}

// Close releases all cached Redis clients. It should be called once the
// Dispatcher is no longer needed (e.g. during server shutdown).
func (d *Dispatcher) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, rdb := range d.rpcClients {
		rdb.Close() //nolint:errcheck
	}
	d.rpcClients = make(map[string]*goredis.Client)
}

// Call invokes toolName on the named extension and returns the decoded result.
// callerName and callerScopes are forwarded to the RPC worker as the caller identity.
// params is a raw JSON object passed as-is to the extension tool.
func (d *Dispatcher) Call(ctx context.Context, extName, toolName, callerName string, callerScopes []string, params json.RawMessage) (any, error) {
	ext, ok := d.find(extName)
	if !ok {
		return nil, fmt.Errorf("extension %q not configured", extName)
	}

	timeout := defaultTimeout
	if ext.TimeoutSeconds > 0 {
		timeout = time.Duration(ext.TimeoutSeconds) * time.Second
	}

	var result *clitool.CallResult

	switch ext.Mode {
	case "rpc":
		addr := ext.RedisAddr
		if addr == "" {
			addr = "127.0.0.1:6379"
		}
		d.mu.Lock()
		rdb, ok := d.rpcClients[addr]
		if !ok {
			rdb = goredis.NewClient(&goredis.Options{Addr: addr})
			d.rpcClients[addr] = rdb
		}
		d.mu.Unlock()
		var err error
		result, err = rpc.Call(ctx, rdb, toolName, callerName, callerScopes, params, timeout)
		if err != nil {
			return nil, fmt.Errorf("extension %q rpc call %q: %w", extName, toolName, err)
		}
	default: // "cli" or ""
		var err error
		result, err = clitool.Call(ctx, ext.Command, toolName, "", params, timeout)
		if err != nil {
			return nil, fmt.Errorf("extension %q cli call %q: %w", extName, toolName, err)
		}
	}

	if !result.OK {
		return nil, fmt.Errorf("extension %q tool %q failed: %s", extName, toolName, result.Error)
	}

	var out any
	if err := json.Unmarshal(result.Result, &out); err != nil {
		return nil, fmt.Errorf("extension %q tool %q: invalid result JSON: %w", extName, toolName, err)
	}
	return out, nil
}

func (d *Dispatcher) find(name string) (config.CltoolExtension, bool) {
	for _, ext := range d.exts {
		if ext.Name == name {
			return ext, true
		}
	}
	return config.CltoolExtension{}, false
}
