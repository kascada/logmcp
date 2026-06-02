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
// A single Redis client is shared across all RPC extensions and created lazily.
type Dispatcher struct {
	exts      []config.CltoolExtension
	redisCfg  config.RedisConfig
	rpcClient *goredis.Client
	mu        sync.Mutex
}

// New creates a Dispatcher from the configured clitool extensions.
func New(exts []config.CltoolExtension, redisCfg config.RedisConfig) *Dispatcher {
	return &Dispatcher{
		exts:     exts,
		redisCfg: redisCfg,
	}
}

// Close releases the cached Redis client. It should be called once the
// Dispatcher is no longer needed (e.g. during server shutdown).
func (d *Dispatcher) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rpcClient != nil {
		d.rpcClient.Close() //nolint:errcheck
		d.rpcClient = nil
	}
}

func (d *Dispatcher) getOrCreateRPCClient() *goredis.Client {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rpcClient == nil {
		d.rpcClient = goredis.NewClient(&goredis.Options{
			Addr:     d.redisCfg.Addr,
			Password: d.redisCfg.Password,
		})
	}
	return d.rpcClient
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
		rdb := d.getOrCreateRPCClient()
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
