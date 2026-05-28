package macro

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kleist-dev/logmcp/internal/extensions/dispatcher"
	"github.com/kleist-dev/logmcp/internal/logs"
)

const defaultStepTimeout = 30 * time.Second

const defaultWindowSeconds = 30.0

// stepContext returns a child context with a step timeout applied.
// timeoutSeconds specifies the timeout in seconds; 0 means use the default (30 s).
func stepContext(parent context.Context, timeoutSeconds int) (context.Context, context.CancelFunc) {
	d := defaultStepTimeout
	if timeoutSeconds > 0 {
		d = time.Duration(timeoutSeconds) * time.Second
	}
	return context.WithTimeout(parent, d)
}

// execExtension executes an extension step by calling a registered clitool/rpc extension.
// All args except 'name' and 'tool' are forwarded as JSON params to the extension tool.
func execExtension(ctx context.Context, step StepDef, params map[string]string, stepResults map[string]any, d *dispatcher.Dispatcher, timeoutSeconds int) (any, error) {
	ctx, cancel := stepContext(ctx, timeoutSeconds)
	defer cancel()

	if d == nil {
		return nil, fmt.Errorf("extension step %q: no extensions configured", step.ID)
	}

	resolvedArgs := interpolateArgs(step.Args, params, stepResults)

	extName, _ := resolvedArgs["name"].(string)
	if extName == "" {
		return nil, fmt.Errorf("extension step %q: missing 'name' arg", step.ID)
	}
	toolName, _ := resolvedArgs["tool"].(string)
	if toolName == "" {
		return nil, fmt.Errorf("extension step %q: missing 'tool' arg", step.ID)
	}

	toolParams := make(map[string]any, len(resolvedArgs))
	for k, v := range resolvedArgs {
		if k == "name" || k == "tool" {
			continue
		}
		toolParams[k] = v
	}

	paramsJSON, err := json.Marshal(toolParams)
	if err != nil {
		return nil, fmt.Errorf("extension step %q: marshalling params: %w", step.ID, err)
	}

	return d.Call(ctx, extName, toolName, "logmcp-macro", []string{"switchboard:read"}, paramsJSON)
}

// execReadFile executes a read_file step, respecting logs.Manager access control.
func execReadFile(ctx context.Context, step StepDef, params map[string]string, stepResults map[string]any, logMgr *logs.Manager, timeoutSeconds int) (any, error) {
	ctx, cancel := stepContext(ctx, timeoutSeconds)
	defer cancel()

	resolvedArgs := interpolateArgs(step.Args, params, stepResults)

	path, _ := resolvedArgs["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("read_file step %q: missing 'path' arg", step.ID)
	}

	// Access control: same rules as the read_log MCP tool.
	if !logMgr.IsAllowed(path) {
		return nil, fmt.Errorf("access denied: %s is not in the whitelist", path)
	}

	opts := logs.ReadOptions{}

	if tail, ok := resolvedArgs["tail"]; ok {
		switch v := tail.(type) {
		case bool:
			opts.Tail = v
		case string:
			opts.Tail = v == "true"
		}
	}

	if linesVal, ok := resolvedArgs["lines"]; ok {
		switch v := linesVal.(type) {
		case int:
			opts.Lines = v
		case float64:
			opts.Lines = int(v)
		case string:
			var n int
			if _, err := fmt.Sscan(v, &n); err == nil {
				opts.Lines = n
			}
		}
	}
	if opts.Lines <= 0 {
		opts.Lines = 100
	}

	lines, err := logMgr.ReadFile(ctx, path, opts)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	return map[string]any{
		"path":  path,
		"lines": lines,
		"count": len(lines),
	}, nil
}

// execJournalctl executes a journalctl step.
func execJournalctl(ctx context.Context, step StepDef, params map[string]string, stepResults map[string]any, timeoutSeconds int) (any, error) {
	ctx, cancel := stepContext(ctx, timeoutSeconds)
	defer cancel()

	resolvedArgs := interpolateArgs(step.Args, params, stepResults)

	unit, _ := resolvedArgs["unit"].(string)

	args := []string{"--no-pager", "--output=short-iso"}
	if unit != "" {
		args = append(args, "--unit="+unit)
	}

	// around + window_s: compute since/until as [around - window_s, around + window_s].
	if aroundStr, ok := resolvedArgs["around"].(string); ok && aroundStr != "" {
		aroundTime, err := parseFlexibleTime(aroundStr)
		if err == nil {
			windowS := defaultWindowSeconds
			if ws, ok := resolvedArgs["window_s"]; ok {
				switch v := ws.(type) {
				case float64:
					windowS = v
				case int:
					windowS = float64(v)
				case string:
					var n float64
					if _, err := fmt.Sscan(v, &n); err == nil {
						windowS = n
					}
				}
			}
			window := time.Duration(windowS) * time.Second
			since := aroundTime.Add(-window)
			until := aroundTime.Add(window)
			args = append(args,
				"--since="+since.Format("2006-01-02 15:04:05"),
				"--until="+until.Format("2006-01-02 15:04:05"),
			)
		} else {
			// around present but unparseable — safe fallback.
			args = append(args, "-n", "200")
		}
	} else {
		// No around: fall back to since/until or tail.
		if sinceStr, ok := resolvedArgs["since"].(string); ok && sinceStr != "" {
			args = append(args, "--since="+sinceStr)
		}
		if untilStr, ok := resolvedArgs["until"].(string); ok && untilStr != "" {
			args = append(args, "--until="+untilStr)
		}
		if aroundVal, _ := resolvedArgs["around"].(string); aroundVal == "" {
			// Default: last 200 lines.
			args = append(args, "-n", "200")
		}
	}

	out, err := exec.CommandContext(ctx, "journalctl", args...).Output()
	if err != nil {
		// journalctl exits non-zero when no entries match; treat as empty.
		errMsg := err.Error()
		return map[string]any{
			"lines":  []string{},
			"error":  errMsg,
			"source": "journald://" + unit,
		}, nil
	}

	lines := splitLines(out)
	return map[string]any{
		"lines":  lines,
		"source": "journald://" + unit,
	}, nil
}

// splitLines splits command output into trimmed lines, dropping a trailing blank.
func splitLines(out []byte) []string {
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

// parseFlexibleTime tries to parse a time string in multiple formats.
// Accepts RFC3339, "2006-01-02 15:04:05", and relative durations.
func parseFlexibleTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}
	// Try logs.ParseTimeOrDuration first (RFC3339 + relative).
	if t, err := logs.ParseTimeOrDuration(s); err == nil {
		return t, nil
	}
	// Try ISO 8601 without timezone (Python datetime.isoformat).
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}
