package review

// LADO fork: LADO's section of a reviewer's system prompt (step 8 plan
// decision s). The daemon builds the launcher before it wires LADO, so LADO
// installs its source later; without one every system prompt stays AO's.

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LadoSystemSection returns the section LADO adds to the system prompt of
// the reviewer on harness that reviews the worker session, or "" for none.
type LadoSystemSection func(ctx context.Context, worker domain.SessionID, harness domain.AgentHarness) (string, error)

var ladoSystemSection atomic.Pointer[LadoSystemSection]

// SetLadoSystemSection installs the source of LADO's reviewer section; nil
// removes it. Launches that start after the call use it.
func SetLadoSystemSection(source LadoSystemSection) {
	if source == nil {
		ladoSystemSection.Store(nil)
		return
	}
	ladoSystemSection.Store(&source)
}

// ladoSystemText is LADO's section for the reviewer of spec, set apart from
// AO's text by a blank line, or "". A source error is logged and adds
// nothing: a review never waits on LADO.
func ladoSystemText(ctx context.Context, spec LaunchSpec) string {
	source := ladoSystemSection.Load()
	if source == nil {
		return ""
	}
	section, err := (*source)(ctx, spec.WorkerID, domain.AgentHarness(spec.Harness))
	if err != nil {
		slog.Default().Warn("LADO could not add the developer's instructions to a reviewer's system prompt; the reviewer starts without them",
			"worker", spec.WorkerID, "harness", spec.Harness, "err", err)
		return ""
	}
	if section = strings.TrimSpace(section); section == "" {
		return ""
	}
	return "\n" + section + "\n"
}
