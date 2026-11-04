package sessionmanager

// LADO fork: the marks on a LADO execution worker's Claude Code launch
// (issue #44, domain.LadoWorkerEnv). With them AO's hook command refuses the
// worker's pushes to a base branch and its merges before they run.

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// applyLadoWorkerEnv marks env when rec is a LADO execution worker (single,
// part, merge or integration) and lists its project's base branches.
// Another session's environment loses both marks, so a project's own
// environment cannot set them. A failed project lookup still marks the
// worker: the hook then guards main, master and the remote's default branch.
func (m *Manager) applyLadoWorkerEnv(ctx context.Context, rec domain.SessionRecord, env map[string]string) {
	if env == nil {
		return
	}
	if !domain.IsLadoRunClientRequestID(rec.ClientRequestID) {
		deleteProtectedEnv(env, domain.LadoWorkerEnv, envKeysCaseInsensitive)
		deleteProtectedEnv(env, domain.LadoBaseBranchesEnv, envKeysCaseInsensitive)
		return
	}
	setProtectedEnv(env, domain.LadoWorkerEnv, "1", envKeysCaseInsensitive)
	deleteProtectedEnv(env, domain.LadoBaseBranchesEnv, envKeysCaseInsensitive)
	if branches := domain.JoinLadoBaseBranches(m.ladoBaseBranches(ctx, rec)); branches != "" {
		setProtectedEnv(env, domain.LadoBaseBranchesEnv, branches, envKeysCaseInsensitive)
	}
}

// ladoBaseBranches returns the branches LADO's dispatcher starts a worker of
// rec's project from (ladoqueue workerBase): the project's default branch
// setting unless it is auto, and for a workspace project each registered
// child repository's default branch.
func (m *Manager) ladoBaseBranches(ctx context.Context, rec domain.SessionRecord) []string {
	if m.store == nil || rec.ProjectID == "" {
		return nil
	}
	project, ok, err := m.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil || !ok {
		if err != nil {
			m.logger.Warn("LADO could not read a worker's project; its push guard keeps only main, master and the remote's default branch",
				"sessionID", rec.ID, "projectID", rec.ProjectID, "err", err)
		}
		return nil
	}
	branches := []string{project.Config.WorktreeBaseBranch()}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return branches
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		m.logger.Warn("LADO could not list a workspace's repositories; its push guard keeps the project's branch only",
			"sessionID", rec.ID, "projectID", rec.ProjectID, "err", err)
		return branches
	}
	for _, repo := range repos {
		branches = append(branches, repo.DefaultBranch)
	}
	return branches
}
