package ladoverify

// LADO fork: what LADO's routes read from verification and ask of it (step 8
// plan §15): a head's record and integration approval for the execution
// view, a check run's log, running the check commands again and the
// protection of a project's base branches. Every refusal is an
// *apierr.Error.

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// HeadView is what LADO holds about one head of an execution's PR: the
// head's verification record with its hash, and its integration approval
// into the branch the PR merges into now. Each is nil while there is none.
type HeadView struct {
	Verification *domain.LadoVerification
	SHA256       string
	Approval     *domain.LadoIntegrationApproval
}

// Head reads the verification record of one head of an execution's PR and
// the PR's integration approval at that head into target. An empty head has
// neither.
func (s *Service) Head(ctx context.Context, session domain.SessionID, prURL, headSHA, target string) (HeadView, error) {
	var view HeadView
	if headSHA == "" {
		return view, nil
	}
	record, ok, err := s.deps.Store.GetLadoVerification(ctx, session, headSHA)
	if err != nil {
		s.deps.Log.Error("LADO could not read the verification of a head", "session", session, "head", headSHA, "err", err)
		return HeadView{}, verifyFailed()
	}
	if ok {
		sum, err := record.SHA256()
		if err != nil {
			s.deps.Log.Error("LADO could not hash a verification record", "session", session, "head", headSHA, "err", err)
			return HeadView{}, verifyFailed()
		}
		view.Verification, view.SHA256 = &record, sum
	}
	approvals, err := s.deps.Store.ListLadoIntegrationApprovals(ctx, session, prURL, headSHA)
	if err != nil {
		s.deps.Log.Error("LADO could not read the integration approval of a head", "session", session, "head", headSHA, "err", err)
		return HeadView{}, verifyFailed()
	}
	if approval, ok := domain.LadoApprovalFor(approvals, target); ok {
		view.Approval = &approval
	}
	return view, nil
}

// RepostIntegrationStatus posts the lado/integration status of the approved
// current head of the execution's open PR again, its attempts started over
// (S8 of #15): after twelve failed posts, as with a token that may not write
// commit statuses, nothing else would post it once the token is fixed. It
// needs no presence: the head is approved, and the same status goes up
// again. It answers the approval as recorded after the post; a post that
// fails again is in its status. GitLab gets no status: its approval stays
// not_applicable.
func (s *Service) RepostIntegrationStatus(ctx context.Context, session domain.SessionID) (domain.LadoIntegrationApproval, error) {
	exec, ok, err := s.deps.Store.LadoExecution(ctx, session)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read an execution to post its status again", "session", session, "err", err)
		return domain.LadoIntegrationApproval{}, verifyFailed()
	case !ok:
		return domain.LadoIntegrationApproval{}, apierr.NotFound("LADO_EXECUTION_NOT_FOUND", "This session is not a LADO execution")
	case exec.PR.URL == "" || exec.PR.Merged || exec.PR.Closed || exec.PR.HeadSHA == "":
		return domain.LadoIntegrationApproval{}, noOpenPR(session)
	}
	approvals, err := s.deps.Store.ListLadoIntegrationApprovals(ctx, session, exec.PR.URL, exec.PR.HeadSHA)
	if err != nil {
		s.deps.Log.Error("LADO could not read the integration approvals of a head", "session", session, "err", err)
		return domain.LadoIntegrationApproval{}, verifyFailed()
	}
	approval, ok := domain.LadoApprovalFor(approvals, approvalTarget(exec))
	if !ok {
		return domain.LadoIntegrationApproval{}, apierr.Conflict("LADO_INTEGRATION_NOT_APPROVED",
			"The pull request's current head has no integration approval to post a status for.",
			map[string]any{"sessionId": string(session), "headSha": exec.PR.HeadSHA})
	}
	if approval.StatusState == domain.LadoIntegrationStatusNotApplicable || approval.StatusState == domain.LadoIntegrationStatusPosted {
		return approval, nil
	}
	if err := s.deps.Store.ResetLadoIntegrationStatus(ctx, approval.ID); err != nil {
		s.deps.Log.Error("LADO could not start an integration status over", "approval", approval.ID, "err", err)
		return domain.LadoIntegrationApproval{}, verifyFailed()
	}
	approval.StatusState, approval.StatusAttempts, approval.StatusError = domain.LadoIntegrationStatusPending, 0, ""
	posted, err := s.postIntegrationStatus(ctx, approval)
	if err != nil {
		s.deps.Log.Warn("LADO could not post an integration status again; the loop posts it", "approval", approval.ID, "err", err)
		return approval, nil
	}
	return posted, nil
}

// CheckLog returns the log of one of an execution's check runs: the end of
// the command's output, at most MaxCommandOutput bytes, and nothing while the
// command runs or when it left no output. A run of another execution is not
// found.
func (s *Service) CheckLog(ctx context.Context, session domain.SessionID, runID string) ([]byte, error) {
	run, ok, err := s.deps.Store.GetLadoCheckRun(ctx, runID)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read a check run", "run", runID, "err", err)
		return nil, verifyFailed()
	case !ok || run.ExecutionSessionID != session:
		return nil, apierr.NotFound("LADO_CHECK_RUN_NOT_FOUND", "This execution has no such check run")
	case run.LogSHA256 == "":
		return []byte{}, nil
	case s.deps.Blobs == nil:
		return nil, apierr.Unavailable("LADO_VERIFY_UNAVAILABLE", "LADO cannot read check run logs now.")
	}
	data, err := s.deps.Blobs.Get(run.LogSHA256, MaxCommandOutput)
	if err != nil {
		s.deps.Log.Error("LADO could not read a check run's log", "run", runID, "err", err)
		return nil, apierr.Internal("LADO_VERIFY_FAILED", "LADO could not read the check run's log")
	}
	return data, nil
}

// RunAgain is what running the check commands again set off: the head they
// run on and the commands that run, in order.
type RunAgain struct {
	HeadSHA  string
	Commands []string
}

// runAgainNote is the error message of the cancelled run that sends a
// finished command back to the loop.
const runAgainNote = "The developer asked LADO to run the command again."

// RunChecksAgain runs the project's check commands again on the current head
// of the execution's open PR. Each command whose newest run on the head
// finished gets a cancelled run, which the loop runs again in order, as it
// does a run a stopped daemon left; one that never ran or was cancelled runs
// anyway, and one still running is left to finish. A pass is kicked.
func (s *Service) RunChecksAgain(ctx context.Context, session domain.SessionID) (RunAgain, error) {
	exec, ok, err := s.deps.Store.LadoExecution(ctx, session)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read an execution to run its checks again", "session", session, "err", err)
		return RunAgain{}, verifyFailed()
	case !ok:
		return RunAgain{}, apierr.NotFound("LADO_EXECUTION_NOT_FOUND", "This session is not a LADO execution")
	case exec.PR.URL == "" || exec.PR.Merged || exec.PR.Closed || exec.PR.HeadSHA == "":
		return RunAgain{}, noOpenPR(session)
	}
	task, ok, err := s.deps.Tasks.LadoTask(ctx, exec.TaskID)
	if err != nil || !ok {
		s.deps.Log.Error("LADO could not read the task of an execution to run its checks again", "task", exec.TaskID, "found", ok, "err", err)
		return RunAgain{}, verifyFailed()
	}
	commands, _, err := s.deps.Store.LadoProjectChecks(ctx, task.ProjectID)
	if err != nil {
		s.deps.Log.Error("LADO could not read a project's check commands", "project", task.ProjectID, "err", err)
		return RunAgain{}, verifyFailed()
	}
	head := exec.PR.HeadSHA
	runs, err := s.deps.Store.ListLadoCheckRuns(ctx, session, head)
	if err != nil {
		s.deps.Log.Error("LADO could not read the check runs of a head", "session", session, "head", head, "err", err)
		return RunAgain{}, verifyFailed()
	}
	again := RunAgain{HeadSHA: head, Commands: []string{}}
	now := s.now()
	for _, command := range commands {
		run, ran := newestCheckRun(runs, command)
		if ran && run.Status == domain.LadoCheckRunRunning {
			continue
		}
		again.Commands = append(again.Commands, command.Name)
		if !ran || run.Status == domain.LadoCheckRunCancelled {
			continue
		}
		marker := domain.LadoCheckRun{
			ID:                 "lado-check-" + s.deps.NewID(),
			ExecutionSessionID: session,
			HeadSHA:            head,
			Name:               command.Name,
			Command:            command.Command,
			Status:             domain.LadoCheckRunRunning,
			StartedAt:          now,
		}
		if err := s.deps.Store.StartLadoCheckRun(ctx, marker); err != nil {
			s.deps.Log.Error("LADO could not record a check command to run again", "session", session, "command", command.Name, "err", err)
			return RunAgain{}, verifyFailed()
		}
		if err := s.deps.Store.FinishLadoCheckRun(ctx, marker.ID, domain.LadoCheckRunCancelled, nil, "", runAgainNote, "", now); err != nil {
			s.deps.Log.Error("LADO could not record a check command to run again", "session", session, "command", command.Name, "err", err)
			return RunAgain{}, verifyFailed()
		}
	}
	s.Kick()
	return again, nil
}

// Protection reads the protection of the base branch of each of the
// project's repositories for the project's screen, as ProjectProtection
// does; refresh reads past the cache. An unknown or archived project is not
// found.
func (s *Service) Protection(ctx context.Context, projectID domain.ProjectID, refresh bool) ([]ProtectionRead, error) {
	project, ok, err := s.deps.Projects.GetProject(ctx, string(projectID))
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read a project", "project", projectID, "err", err)
		return nil, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	case !ok || !project.ArchivedAt.IsZero():
		return nil, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	reads, err := s.ProjectProtection(ctx, projectID, refresh)
	if err != nil {
		s.deps.Log.Error("LADO could not read the protection of a project's base branches", "project", projectID, "err", err)
		return nil, apierr.Internal("LADO_VERIFY_FAILED", "LADO could not read the protection of the project's base branches")
	}
	return reads, nil
}
