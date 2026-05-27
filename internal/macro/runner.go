package macro

import (
	"context"
	"fmt"

	"github.com/kleist-dev/logmcp/internal/extensions/dispatcher"
	"github.com/kleist-dev/logmcp/internal/logs"
)

// RunResult is the combined output of all steps, keyed by step ID.
type RunResult map[string]any

// Runner holds the dependencies needed to execute macro steps.
type Runner struct {
	logMgr     *logs.Manager
	dispatcher *dispatcher.Dispatcher
}

// NewRunner creates a Runner. d may be nil if no extensions are configured.
func NewRunner(logMgr *logs.Manager, d *dispatcher.Dispatcher) *Runner {
	return &Runner{logMgr: logMgr, dispatcher: d}
}

// Run executes all steps of def sequentially.
// params contains the MCP tool call arguments (all as strings).
// On success, returns a RunResult keyed by step ID.
// On step timeout or step error, returns the partial result plus an error entry
// for the failed step, and stops execution.
func (r *Runner) Run(ctx context.Context, def MacroDef, params map[string]string) (RunResult, error) {
	result := make(RunResult)

	for _, step := range def.Steps {
		var (
			out any
			err error
		)

		switch step.Internal {
		case "extension":
			out, err = execExtension(ctx, step, params, result, r.dispatcher)
		case "read_file":
			out, err = execReadFile(ctx, step, params, result, r.logMgr)
		case "journalctl":
			out, err = execJournalctl(ctx, step, params, result)
		default:
			err = fmt.Errorf("unknown internal step type %q", step.Internal)
		}

		if err != nil {
			// Record the error for this step and stop execution.
			result[step.ID] = map[string]any{
				"error": err.Error(),
			}
			return result, fmt.Errorf("step %q (%s): %w", step.ID, step.Internal, err)
		}

		result[step.ID] = out
	}

	return result, nil
}
