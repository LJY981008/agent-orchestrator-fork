package store

// LADO fork: adding one repository to a registered workspace project
// (docs/LJY-PRD/TDD.md §5.1).

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// AddLadoWorkspaceRepo registers one more child repository of a workspace
// project, leaving the project row and its other children as they are.
func (s *Store) AddLadoWorkspaceRepo(ctx context.Context, repo domain.WorkspaceRepoRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertWorkspaceRepo(ctx, gen.UpsertWorkspaceRepoParams{
		ProjectID:     repo.ProjectID,
		Name:          repo.Name,
		RelativePath:  repo.RelativePath,
		RepoOriginURL: repo.RepoOriginURL,
		DefaultBranch: repo.DefaultBranch,
		RegisteredAt:  repo.RegisteredAt,
		GitStatus:     string(repo.GitStatus.WithDefault()),
	}); err != nil {
		return fmt.Errorf("add workspace repo %s to %s: %w", repo.Name, repo.ProjectID, err)
	}
	return nil
}
