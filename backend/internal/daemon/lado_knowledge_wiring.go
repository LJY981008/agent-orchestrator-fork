package daemon

// LADO fork: the projects' knowledge store (issue #24,
// docs/LJY-PRD/adr/0010-project-knowledge-store.md). One service serves the
// API's knowledge routes and the knowledge section of every LADO session's
// standing instructions.

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoknowledge"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// ladoKnowledgeIndex reads a project's knowledge index for the standing
// instructions (ladotask.PromptSource.Knowledge). Nil adds no knowledge
// section.
type ladoKnowledgeIndex interface {
	Index(ctx context.Context, projectID domain.ProjectID) (ladoknowledge.Index, error)
}

// newLadoKnowledge builds the knowledge store under cfg.StateDir over AO's
// projects and sessions and LADO's settings and audit log. claudeDir is the
// computer's own Claude Code folder, whose project memory the store imports;
// empty reports that import as claude_memory_not_found. Git is the system
// git.
func newLadoKnowledge(cfg config.Config, store *sqlite.Store, claudeDir string, log *slog.Logger) *ladoknowledge.Service {
	return ladoknowledge.New(ladoknowledge.Options{
		StateDir:  cfg.StateDir,
		Store:     store,
		ClaudeDir: claudeDir,
		Log:       log,
	})
}
