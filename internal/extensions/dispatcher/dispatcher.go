// Package dispatcher routes macro extension steps to the correct transport (CLI or RPC).
package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/extensions/clitool"
	"github.com/kleist-dev/logmcp/internal/extensions/rpc"
)

const (
	defaultTimeout = 10 * time.Second
	callerName     = "logmcp-macro"
)

// Dispatcher routes extension tool calls to the correct transport.
type Dispatcher struct {
	exts []config.CltoolExtension
}

// New creates a Dispatcher from the configured clitool extensions.
func New(exts []config.CltoolExtension) *Dispatcher {
	return &Dispatcher{exts: exts}
}

// Call invokes toolName on the named extension and returns the decoded result.
// params is a raw JSON object passed as-is to the extension tool.
func (d *Dispatcher) Call(ctx context.Context, extName, toolName string, params json.RawMessage) (any, error) {
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
		var err error
		result, err = rpc.Call(ctx, addr, toolName, callerName, []string{"switchboard:read"}, params, timeout)
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
