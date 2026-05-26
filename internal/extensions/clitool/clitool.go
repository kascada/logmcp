// Package clitool implements the clitool extension proxy for LogMCP.
// It discovers tools from external CLI programs (via `<cmd> list`) and
// forwards tool calls (via `<cmd> call <tool> --token-stdin`) transparently.
// See docs/CLITOOL.md for the interface convention.
package clitool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ToolDef describes a single tool returned by `<cmd> list`.
type ToolDef struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	InputSchema   json.RawMessage `json:"inputSchema"`
	RequiredScope string          `json:"requiredScope"`
}

// CallResult is the JSON envelope returned by `<cmd> call`.
type CallResult struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`
}

// List runs `<command> list` and returns the tool definitions.
// A non-zero exit code, timeout, or JSON parse error is returned as an error.
// Callers should treat this as a fatal configuration problem.
func List(command string, timeout time.Duration) ([]ToolDef, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	parts := strings.Fields(command)
	cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], "list")...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("clitool list timed out after %s: %s", timeout, command)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("clitool list failed (%s): %s", command, msg)
		}
		return nil, fmt.Errorf("clitool list failed (%s): %w", command, err)
	}

	var tools []ToolDef
	if err := json.Unmarshal(stdout.Bytes(), &tools); err != nil {
		return nil, fmt.Errorf("clitool list returned invalid JSON (%s): %w", command, err)
	}
	return tools, nil
}

// VerifyResult is the JSON response from `<cmd> verify`.
type VerifyResult struct {
	Authenticated bool     `json:"authenticated"`
	Name          string   `json:"name,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
}

// Verify runs `<command> verify` with the token on stdin and returns the result.
// A non-zero exit code, timeout, or JSON parse error is returned as a Go error
// (indicating a program failure, not an auth failure).
func Verify(command, token string, timeout time.Duration) (*VerifyResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	parts := strings.Fields(command)
	cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], "verify")...)
	cmd.Stdin = bytes.NewBufferString(token + "\n")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("authenticator verify timed out after %s: %s", timeout, command)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("authenticator verify failed (%s): %s", command, msg)
		}
		return nil, fmt.Errorf("authenticator verify failed (%s): %w", command, err)
	}

	var result VerifyResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("authenticator returned invalid JSON (%s): %w", command, err)
	}
	return &result, nil
}

// Call runs `<command> call <toolName> --token-stdin [--params <params>]`
// and returns the parsed CallResult.
//
// The bearer token is written to stdin followed by a newline. If params is
// non-empty (and not "null") it is passed as `--params <params>`.
//
// If the process times out or exits non-zero, stdout is still parsed as a
// CallResult (the CLI convention puts errors on stdout as JSON). If stdout
// cannot be parsed, a synthetic error CallResult is returned — never a Go error.
func Call(ctx context.Context, command, toolName, token string, params json.RawMessage, timeout time.Duration) (*CallResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"call", toolName, "--token-stdin"}
	if len(params) > 0 && string(params) != "null" {
		args = append(args, "--params", string(params))
	}

	var stdout bytes.Buffer
	parts := strings.Fields(command)
	cmd := exec.CommandContext(callCtx, parts[0], append(parts[1:], args...)...)
	cmd.Stdin = bytes.NewBufferString(token + "\n")
	cmd.Stdout = &stdout

	_ = cmd.Run() // ignore exit error — parse stdout regardless (CLI errors go to stdout as JSON)

	if callCtx.Err() == context.DeadlineExceeded {
		return &CallResult{
			OK:    false,
			Error: fmt.Sprintf("clitool call timed out after %s", timeout),
			Code:  "execution_error",
		}, nil
	}

	return parseCallResult(stdout.Bytes()), nil
}

// parseCallResult unmarshals stdout bytes into a CallResult.
// If the bytes are not valid JSON, a synthetic error result is returned.
func parseCallResult(out []byte) *CallResult {
	var result CallResult
	if err := json.Unmarshal(out, &result); err != nil {
		return &CallResult{
			OK:    false,
			Error: fmt.Sprintf("clitool returned invalid JSON: %s", string(out)),
			Code:  "execution_error",
		}
	}
	return &result
}
