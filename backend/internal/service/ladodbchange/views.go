package ladodbchange

// The views of database changes the app reads: an execution's SQL files per
// PR with how far each got and the runs, one file's content, and each PR's
// state on the project's board.

import (
	"context"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// ExecutionDBChanges returns an execution's database changes: for each
// tracked PR whose current head has SQL files or that has runs, the files
// with their status, the applied files the head changes, and the runs.
func (s *Service) ExecutionDBChanges(ctx context.Context, session domain.SessionID) (domain.LadoDBChangeExecutionView, error) {
	view := domain.LadoDBChangeExecutionView{SessionID: session, PRs: []domain.LadoDBChangePRView{}}
	exec, ok, err := s.deps.Store.LadoExecution(ctx, session)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read an execution", "session", session, "err", err)
		return view, loadFailed()
	case !ok:
		return view, apierr.NotFound("LADO_EXECUTION_NOT_FOUND", "This session is not a LADO execution")
	}
	view.TaskID = exec.TaskID
	task, ok, err := s.deps.Store.LadoTask(ctx, exec.TaskID)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read a task", "task", exec.TaskID, "err", err)
		return view, loadFailed()
	case !ok:
		return view, apierr.NotFound("LADO_TASK_NOT_FOUND", "The execution's task is gone")
	}
	runs, err := s.deps.Store.ListLadoDBChangeRunsForExecution(ctx, session)
	if err != nil {
		s.deps.Log.Error("LADO could not read database change runs", "session", session, "err", err)
		return view, loadFailed()
	}
	for _, tracked := range exec.TrackedPRs() {
		pr, err := s.prView(ctx, task.ProjectID, session, tracked, runs)
		if err != nil {
			s.deps.Log.Error("LADO could not read a pull request's database changes", "session", session, "pr", tracked.PR.URL, "err", err)
			return view, loadFailed()
		}
		if len(pr.Files) > 0 || len(pr.Modified) > 0 || len(pr.Runs) > 0 {
			view.PRs = append(view.PRs, pr)
		}
	}
	return view, nil
}

// prView is one tracked PR's database changes at its current head.
func (s *Service) prView(ctx context.Context, project domain.ProjectID, session domain.SessionID, tracked domain.LadoExecutionTrackedPR, runs []domain.LadoDBChangeRun) (domain.LadoDBChangePRView, error) {
	view := domain.LadoDBChangePRView{
		PRURL: tracked.PR.URL, HeadSHA: tracked.PR.HeadSHA, Repo: tracked.Repo,
		Files: []domain.LadoDBChangeFileView{}, Modified: []domain.LadoDBChangeModified{}, Runs: []domain.LadoDBChangeRun{},
	}
	for _, run := range runs {
		if run.PRURL == tracked.PR.URL {
			view.Runs = append(view.Runs, run)
		}
	}
	if tracked.PR.HeadSHA == "" {
		return view, nil
	}
	details, ok, err := s.headDetails(ctx, session, tracked.PR.URL, tracked.PR.HeadSHA)
	if err != nil || !ok {
		return view, err
	}
	view.Repo = details.Repo
	if details.Modified != nil {
		view.Modified = details.Modified
	}
	files, err := s.fileViews(ctx, project, details)
	if err != nil {
		return view, err
	}
	view.Files = files
	return view, nil
}

// fileViews returns each file of details with how far it got: applied once
// a row applied the path (or recorded it at the baseline), else the newest
// row's result, pending without one.
func (s *Service) fileViews(ctx context.Context, project domain.ProjectID, details domain.LadoDBChangesDetails) ([]domain.LadoDBChangeFileView, error) {
	views := make([]domain.LadoDBChangeFileView, 0, len(details.Files))
	if len(details.Files) == 0 {
		return views, nil
	}
	paths := make([]string, len(details.Files))
	for i, file := range details.Files {
		paths[i] = file.Path
	}
	rows, err := s.deps.Store.ListLadoDBChangeApplications(ctx, project, details.Repo, paths)
	if err != nil {
		return nil, err
	}
	for _, file := range details.Files {
		view := domain.LadoDBChangeFileView{LadoDBChangeFileRef: file, Status: domain.LadoDBChangeFilePending}
		var applied, newest *domain.LadoDBChangeApplication
		for i := range rows {
			row := &rows[i]
			if row.Path != file.Path {
				continue
			}
			if row.Result != domain.LadoDBChangeResultBaseline {
				view.Attempts++
			}
			if row.Result.CountsApplied() {
				applied = row
			}
			newest = row
		}
		switch {
		case applied != nil:
			view.Status, view.AppliedAt = domain.LadoDBChangeFileApplied, applied.FinishedAt
			view.LastMessage, view.OutputSHA256 = applied.Message, applied.OutputSHA256
		case newest != nil:
			switch newest.Result {
			case domain.LadoDBChangeResultRunning:
				view.Status = domain.LadoDBChangeFileApplying
			case domain.LadoDBChangeResultFailed:
				view.Status = domain.LadoDBChangeFileFailed
			case domain.LadoDBChangeResultInterrupted:
				view.Status = domain.LadoDBChangeFileInterrupted
			}
			view.LastMessage, view.OutputSHA256 = newest.Message, newest.OutputSHA256
		}
		views = append(views, view)
	}
	return views, nil
}

// DBChangeFile returns the content of one SQL file of an execution's PR: a
// blob the PR's current head lists, as a file or as the head's side of a
// changed applied file. Any other blob is not found.
func (s *Service) DBChangeFile(ctx context.Context, session domain.SessionID, prURL, blobSHA string) (domain.LadoDBChangeFileContent, error) {
	none := domain.LadoDBChangeFileContent{}
	blobSHA = strings.ToLower(strings.TrimSpace(blobSHA))
	notFound := apierr.NotFound("LADO_DB_CHANGE_NOT_FOUND", "The pull request's head has no such SQL file")
	if !IsObjectID(blobSHA) {
		return none, notFound
	}
	_, tracked, project, err := s.trackedPR(ctx, session, prURL)
	if err != nil {
		return none, err
	}
	if tracked.PR.HeadSHA == "" {
		return none, notFound
	}
	details, ok, err := s.headDetails(ctx, session, tracked.PR.URL, tracked.PR.HeadSHA)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read a head's database changes", "session", session, "err", err)
		return none, loadFailed()
	case !ok:
		return none, notFound
	}
	var file domain.LadoDBChangeFileRef
	if at := slices.IndexFunc(details.Files, func(f domain.LadoDBChangeFileRef) bool { return f.BlobSHA == blobSHA }); at >= 0 {
		file = details.Files[at]
	} else if at := slices.IndexFunc(details.Modified, func(m domain.LadoDBChangeModified) bool { return m.HeadBlobSHA == blobSHA }); at >= 0 {
		file = domain.LadoDBChangeFileRef{Path: details.Modified[at].Path, BlobSHA: blobSHA}
	} else {
		return none, notFound
	}
	dir, err := s.RepoDir(ctx, project, details.Repo)
	if err != nil {
		s.deps.Log.Error("LADO could not find a repository's folder", "project", project, "err", err)
		return none, loadFailed()
	}
	content, err := s.FileContent(ctx, dir, file)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a SQL file", "session", session, "path", file.Path, "err", RedactLine(err.Error(), nil))
		return none, apierr.Internal("LADO_DB_CHANGE_LOAD_FAILED", "LADO could not read the SQL file")
	}
	return content, nil
}

// boardPriority orders the board states: the first a PR has is the one its
// card shows.
var boardPriority = []domain.LadoDBChangeBoardState{
	domain.LadoDBChangeBoardMergedUnapplied,
	domain.LadoDBChangeBoardApplyFailed,
	domain.LadoDBChangeBoardMergeFailed,
	domain.LadoDBChangeBoardPreflightBlocked,
	domain.LadoDBChangeBoardAfterDeployPending,
	domain.LadoDBChangeBoardApplying,
	domain.LadoDBChangeBoardApplyWaiting,
}

// ProjectDBChanges returns, for the project's board, one summary per PR
// with a run that is not done: the state its card shows, the files left to
// apply and those that failed.
func (s *Service) ProjectDBChanges(ctx context.Context, projectID domain.ProjectID) ([]domain.LadoDBChangeSummary, error) {
	if _, err := s.project(ctx, projectID); err != nil {
		return nil, err
	}
	runs, err := s.deps.Store.ListLadoDBChangeRunsForProject(ctx, projectID)
	if err != nil {
		s.deps.Log.Error("LADO could not read database change runs", "project", projectID, "err", err)
		return nil, loadFailed()
	}
	summaries := []domain.LadoDBChangeSummary{}
	for _, run := range runs {
		if run.State == domain.LadoDBChangeRunDone {
			continue
		}
		merged := run.Trigger == domain.LadoDBChangeTriggerMergedUnapplied
		if !merged && run.Kind == domain.LadoDBChangeRunPreMerge {
			if pr, found, err := s.deps.Store.GetPR(ctx, run.PRURL); err == nil && found {
				merged = pr.Merged
			}
		}
		summary := domain.LadoDBChangeSummary{
			TaskID: run.TaskID, SessionID: run.ExecutionSessionID, PRURL: run.PRURL, Repo: run.Repo,
			State: boardState(run, merged), Message: run.Error,
		}
		if details, ok, err := s.headDetails(ctx, run.ExecutionSessionID, run.PRURL, run.HeadSHA); err == nil && ok {
			if pending, err := s.pendingOf(ctx, projectID, details, run.Kind.Phase()); err == nil {
				summary.Pending = len(pending)
			}
			if files, err := s.fileViews(ctx, projectID, details); err == nil {
				for _, file := range files {
					if file.Phase == run.Kind.Phase() && (file.Status == domain.LadoDBChangeFileFailed || file.Status == domain.LadoDBChangeFileInterrupted) {
						summary.Failed++
					}
				}
			}
		}
		at := slices.IndexFunc(summaries, func(other domain.LadoDBChangeSummary) bool { return other.PRURL == run.PRURL })
		switch {
		case at < 0:
			summaries = append(summaries, summary)
		case slices.Index(boardPriority, summary.State) < slices.Index(boardPriority, summaries[at].State):
			summaries[at] = summary
		}
	}
	slices.SortStableFunc(summaries, func(a, b domain.LadoDBChangeSummary) int {
		if c := strings.Compare(string(a.TaskID), string(b.TaskID)); c != 0 {
			return c
		}
		return strings.Compare(a.PRURL, b.PRURL)
	})
	return summaries, nil
}

// boardState is what a run that is not done shows on its card; merged says
// its PR merged.
func boardState(run domain.LadoDBChangeRun, merged bool) domain.LadoDBChangeBoardState {
	switch {
	case run.State == domain.LadoDBChangeRunApplying:
		return domain.LadoDBChangeBoardApplying
	case run.Kind == domain.LadoDBChangeRunPreMerge && merged:
		return domain.LadoDBChangeBoardMergedUnapplied
	case run.State == domain.LadoDBChangeRunFailed:
		return domain.LadoDBChangeBoardApplyFailed
	case run.State == domain.LadoDBChangeRunMergeFailed:
		return domain.LadoDBChangeBoardMergeFailed
	case run.Kind == domain.LadoDBChangeRunAfterDeploy && run.State != domain.LadoDBChangeRunQueued:
		return domain.LadoDBChangeBoardAfterDeployPending
	case run.State == domain.LadoDBChangeRunAwaitingHuman:
		return domain.LadoDBChangeBoardPreflightBlocked
	case run.State == domain.LadoDBChangeRunApplied || run.State == domain.LadoDBChangeRunMerging:
		return domain.LadoDBChangeBoardApplying
	}
	return domain.LadoDBChangeBoardApplyWaiting
}
