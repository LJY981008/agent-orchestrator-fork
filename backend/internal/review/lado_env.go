package review

// LADO fork: the environment values LADO puts on an AO reviewer of a project
// in LADO mode (issue #42): the compact window. A reviewer starts outside the
// session manager, so it gets them here. Without a source every reviewer
// starts as AO's.

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LadoReviewerEnv returns the environment values LADO puts on a reviewer of
// the project on harness, nil for none.
type LadoReviewerEnv func(ctx context.Context, project domain.ProjectID, harness domain.AgentHarness) (map[string]string, error)

var ladoReviewerEnv atomic.Pointer[LadoReviewerEnv]

// SetLadoReviewerEnv installs the source of LADO's reviewer values; nil
// removes it. Launches that start after the call use it.
func SetLadoReviewerEnv(source LadoReviewerEnv) {
	if source == nil {
		ladoReviewerEnv.Store(nil)
		return
	}
	ladoReviewerEnv.Store(&source)
}

// applyLadoReviewerEnv sets LADO's values on the reviewer's environment.
// They win over the project's. An error is logged and adds nothing: a review
// never waits on LADO.
func applyLadoReviewerEnv(ctx context.Context, spec LaunchSpec, env map[string]string) {
	source := ladoReviewerEnv.Load()
	if source == nil || env == nil {
		return
	}
	harness := domain.AgentHarness(spec.Harness)
	values, err := (*source)(ctx, spec.ProjectID, harness)
	if err != nil {
		slog.Default().Warn("LADO could not read its values for a reviewer; the reviewer starts without them",
			"worker", spec.WorkerID, "harness", spec.Harness, "err", err)
		return
	}
	for key, value := range values {
		if runtime.GOOS == "windows" {
			// Environment names fold case there; one spelling must win.
			for existing := range env {
				if existing != key && strings.EqualFold(existing, key) {
					delete(env, existing)
				}
			}
		}
		env[key] = value
	}
}
