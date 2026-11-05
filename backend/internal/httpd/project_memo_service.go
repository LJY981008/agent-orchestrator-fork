package httpd

// LADO fork: the project memo API's service (issue #50). It checks the project
// exists and maps the projectmemo store's errors to the API's error codes.

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/projectmemo"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// projectLookup is the part of the project manager the memo routes need.
type projectLookup interface {
	Get(ctx context.Context, id domain.ProjectID) (projectsvc.GetResult, error)
}

type projectMemoService struct {
	projects projectLookup
	store    *projectmemo.Store
}

// newProjectMemoService returns nil when the API has no project manager or no
// data dir, which leaves the memo routes answering 501.
func newProjectMemoService(projects projectLookup, dataDir string) *projectMemoService {
	if projects == nil || dataDir == "" {
		return nil
	}
	return &projectMemoService{projects: projects, store: projectmemo.New(dataDir)}
}

func (s *projectMemoService) Current(ctx context.Context, project domain.ProjectID) (projectmemo.Memo, bool, []projectmemo.Memo, error) {
	if err := s.requireProject(ctx, project); err != nil {
		return projectmemo.Memo{}, false, nil, err
	}
	memo, err := s.store.Get(project)
	ok := err == nil
	if err != nil && !errors.Is(err, projectmemo.ErrNotFound) {
		return projectmemo.Memo{}, false, nil, projectMemoError(err)
	}
	revisions, err := s.store.ListRevisions(project)
	if err != nil {
		return projectmemo.Memo{}, false, nil, projectMemoError(err)
	}
	return memo, ok, revisions, nil
}

func (s *projectMemoService) Save(ctx context.Context, project domain.ProjectID, body, baseRevision string) (projectmemo.Memo, error) {
	if err := s.requireProject(ctx, project); err != nil {
		return projectmemo.Memo{}, err
	}
	base := baseRevision
	memo, err := s.store.Save(project, projectmemo.SaveInput{Body: body, Source: projectmemo.SourceUser, BaseRevision: &base})
	if err != nil {
		return projectmemo.Memo{}, projectMemoError(err)
	}
	return memo, nil
}

func (s *projectMemoService) Revision(ctx context.Context, project domain.ProjectID, revision string) (projectmemo.Memo, error) {
	if err := s.requireProject(ctx, project); err != nil {
		return projectmemo.Memo{}, err
	}
	memo, err := s.store.GetRevision(project, revision)
	if err != nil {
		return projectmemo.Memo{}, projectMemoError(err)
	}
	return memo, nil
}

// requireProject answers PROJECT_NOT_FOUND for an unknown or archived project,
// so a memo directory is never made for one.
func (s *projectMemoService) requireProject(ctx context.Context, project domain.ProjectID) error {
	_, err := s.projects.Get(ctx, project)
	return err
}

func projectMemoError(err error) error {
	switch {
	case errors.Is(err, projectmemo.ErrTooLong):
		return apierr.Invalid("PROJECT_MEMO_TOO_LONG",
			fmt.Sprintf("The memo is longer than %d characters", projectmemo.MaxRunes),
			map[string]any{"maxChars": projectmemo.MaxRunes})
	case errors.Is(err, projectmemo.ErrInvalid):
		return apierr.Invalid("PROJECT_MEMO_INVALID", err.Error(), nil)
	case errors.Is(err, projectmemo.ErrConflict):
		return apierr.Conflict("PROJECT_MEMO_CONFLICT",
			"The memo changed since it was read; reload it and edit again", nil)
	case errors.Is(err, projectmemo.ErrNotFound):
		return apierr.NotFound("PROJECT_MEMO_NOT_FOUND", "No such memo revision")
	default:
		return fmt.Errorf("project memo: %w", err)
	}
}
