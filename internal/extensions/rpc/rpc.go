// Package rpc implements the Redis-based RPC channel for clitool extensions.
// Instead of spawning a subprocess per tool call, it pushes a request onto
// a Redis list and waits for the worker to push a reply — eliminating the
// Python process-startup overhead of the CLI path.
// See docs/RPC.md for the protocol specification.
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/kleist-dev/logmcp/internal/extensions/clitool"
)

const reqKey = "sb:rpc:req"

// rpcRequest is the JSON envelope pushed onto sb:rpc:req.
type rpcRequest struct {
	Tool      string          `json:"tool"`
	Params    json.RawMessage `json:"params"`
	Caller    rpcCaller       `json:"caller"`
	ReplyKey  string          `json:"reply_key"`
	ExpiresAt float64         `json:"expires_at"` // Unix timestamp
}

// rpcCaller carries the authenticated caller identity.
type rpcCaller struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

// Call sends a single RPC request over Redis and waits for the reply.
//
// redisAddr is the Redis server address (e.g. "127.0.0.1:6379").
// toolName is the unprefixed tool name (e.g. "status").
// callerName and callerScopes are taken from the already-resolved MCP token context.
// params is the raw JSON parameters object (may be nil or "null").
// timeout governs both the expires_at field and the BLPOP wait duration.
//
// A new redis.Client is created per call; go-redis handles connection pooling internally.
func Call(ctx context.Context, redisAddr, toolName, callerName string, callerScopes []string, params json.RawMessage, timeout time.Duration) (*clitool.CallResult, error) {
	replyKey := "sb:rpc:reply:" + uuid.New().String()
	expiresAt := float64(time.Now().Add(timeout).UnixMilli()) / 1000.0

	if len(params) == 0 || string(params) == "null" {
		params = json.RawMessage("{}")
	}

	scopes := callerScopes
	if scopes == nil {
		scopes = []string{}
	}

	req := rpcRequest{
		Tool:      toolName,
		Params:    params,
		Caller:    rpcCaller{Name: callerName, Scopes: scopes},
		ReplyKey:  replyKey,
		ExpiresAt: expiresAt,
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("rpc: marshalling request: %w", err)
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr: redisAddr,
	})
	defer rdb.Close() //nolint:errcheck

	if err := rdb.LPush(ctx, reqKey, string(reqJSON)).Err(); err != nil {
		return nil, fmt.Errorf("rpc: LPUSH %s: %w", reqKey, err)
	}

	result, err := rdb.BLPop(ctx, timeout, replyKey).Result()
	if err != nil {
		if err == goredis.Nil {
			return &clitool.CallResult{
				OK:    false,
				Error: fmt.Sprintf("rpc: service unavailable (no reply within %s)", timeout),
				Code:  "execution_error",
			}, nil
		}
		return nil, fmt.Errorf("rpc: BLPOP %s: %w", replyKey, err)
	}

	// result is [key, value]; we want result[1].
	if len(result) < 2 {
		return &clitool.CallResult{
			OK:    false,
			Error: "rpc: unexpected BLPOP response format",
			Code:  "execution_error",
		}, nil
	}

	var callResult clitool.CallResult
	if jsonErr := json.Unmarshal([]byte(result[1]), &callResult); jsonErr != nil {
		return &clitool.CallResult{
			OK:    false,
			Error: fmt.Sprintf("rpc: invalid JSON in reply: %s", result[1]),
			Code:  "execution_error",
		}, nil
	}

	return &callResult, nil
}
