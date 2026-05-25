package macro

import (
	"context"
	"fmt"

	"github.com/kleist-dev/logmcp/internal/config"
	"github.com/kleist-dev/logmcp/internal/logs"
)

// RunResult is the combined output of all steps, keyed by step ID.
type RunResult map[string]any

// Runner holds the dependencies needed to execute macro steps.
type Runner struct {
	cfg    *config.Config
	logMgr *logs.Manager
}

// NewRunner creates a Runner.
func NewRunner(cfg *config.Config, logMgr *logs.Manager) *Runner {
	return &Runner{cfg: cfg, logMgr: logMgr}
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
		case "db_query":
			out, err = execDBQuery(ctx, step, params, result, r.cfg)
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
