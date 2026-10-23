package ladoqueue

// One dispatch of a claimed queue entry (docs/LJY-PRD/TDD.md §5.3): the
// checks in their order, then the worker's spawn and its link to the entry.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

const (
	// maxAttempts is how many times an entry tries to start its worker
	// before it fails.
	maxAttempts = 3
	// recheckAfter is how long an entry waits when a check could not run,
	// such as reading its issue. The wait is not an attempt.
	recheckAfter = time.Minute
	// baseResolveTimeout bounds resolving the branch a worker starts from.
	baseResolveTimeout = 30 * time.Second
	// maxProblemBytes caps the error an entry records.
	maxProblemBytes = 512
)

// retryAfter is the wait before an entry's second and third attempts.
var retryAfter = [maxAttempts - 1]time.Duration{30 * time.Second, 2 * time.Minute}

// dispatch checks a claimed entry in the order of docs/LJY-PRD/TDD.md §5.3
// and starts its worker when every check passes. The first check that fails
// settles the entry, and the later ones do not run: an approval that no
// longer holds needs review, a closed issue cancels the entry, what only a
// new plan or setup can fix blocks it, and a check that could not run holds
// it for a minute without spending its attempt. A pass that stops before the
// spawn hands the entry back as it was. It returns only the writes that
// failed.
func (s *Service) dispatch(ctx context.Context, entry domain.LadoQueueEntry, settings domain.LadoDispatchSettings, now time.Time) error {
	// 1. The entry's approval is the task's newest and still covers the
	// latest revision.
	task, ok, err := s.deps.Store.LadoTask(ctx, entry.TaskID)
	if err == nil && !ok {
		err = fmt.Errorf("task %s not found", entry.TaskID)
	}
	if err != nil {
		s.deps.Log.Warn("LADO could not read the task of a queue entry", "entry", entry.ID, "err", err)
		return s.recheck(ctx, entry, domain.LadoWaitingRetry, now)
	}
	switch {
	case !task.CancelledAt.IsZero():
		// Cancelling the task cancelled its entry too; only the claim is
		// left to give up.
		return s.release(ctx, entry, time.Time{}, "")
	case !task.Plan.ApprovalValid() || task.Plan.Approval.ID != entry.PlanApprovalID:
		return s.review(ctx, entry, domain.LadoReviewPlanSuperseded)
	}

	// 2. The issue is open and reads as it did when the plan was made.
	issue, err := s.deps.Tasks.IssueCheck(ctx, task)
	switch {
	case err != nil:
		s.deps.Log.Warn("LADO could not read the issue of a queued task", "entry", entry.ID, "task", task.ID,
			"problem", problem(err, "the issue could not be read"))
		return s.recheck(ctx, entry, domain.LadoWaitingIssueUnreadable, now)
	case issue.State == domain.IssueDone || issue.State == domain.IssueCancelled:
		// IssueCheck recorded the state, so the task shows Closed unmerged.
		return s.cancel(ctx, entry, now)
	case issue.SHA256 != task.Plan.Latest.IssueSnapshotSHA256:
		return s.review(ctx, entry, domain.LadoReviewIssueEdited)
	}

	// 3. The rules still give the approval's snapshot.
	current, err := s.deps.Policy.CurrentSHA256(ctx, task)
	if err != nil {
		s.deps.Log.Warn("LADO could not resolve the rules of a queued task", "entry", entry.ID, "task", task.ID,
			"problem", problem(err, "the rules could not be resolved"))
		return s.recheck(ctx, entry, domain.LadoWaitingRetry, now)
	}
	if current != task.Plan.Snapshot.SHA256 {
		return s.review(ctx, entry, domain.LadoReviewRulesChanged)
	}

	// 4. The plan's base is the branch AO starts the worker from.
	base, err := s.workerBase(ctx, task)
	if err != nil {
		s.deps.Log.Warn("LADO could not tell which branch a queued task's worker starts from", "entry", entry.ID, "task", task.ID,
			"problem", problem(err, "the base branch could not be resolved"))
		return s.recheck(ctx, entry, domain.LadoWaitingRetry, now)
	}
	if !namesBase(task.Plan.Latest.BaseRef, base) {
		return s.block(ctx, entry, domain.LadoBlockedBaseRefMismatch)
	}

	// 5. S08 admits the entry here: the global and project caps, and memory.

	// 6. The snapshot lets its agent run.
	in, err := s.deps.Tasks.RunInput(ctx, task)
	if err != nil {
		if ctx.Err() != nil {
			return s.handBack(ctx, entry)
		}
		return s.attemptFailed(ctx, entry, problem(err, "the approved plan could not be read"))
	}
	agent, err := snapshotAgent(in.Snapshot)
	if err != nil {
		return s.attemptFailed(ctx, entry, "the rule snapshot could not be read")
	}
	switch {
	case in.Snapshot.Blocking:
		return s.block(ctx, entry, domain.LadoBlockedSnapshot)
	case !runnable(agent.Harness):
		return s.block(ctx, entry, domain.LadoBlockedAgentUnsupported)
	case agent.PermissionMode == "" || agent.PermissionMode == domain.PermissionModeDefault:
		// Resolution picks a mode under the ceiling; a snapshot without one
		// cannot say how the worker may run.
		return s.block(ctx, entry, domain.LadoBlockedSnapshot)
	}

	if ctx.Err() != nil {
		return s.handBack(ctx, entry)
	}
	session, _, _, err := s.deps.Sessions.Spawn(ctx, spawnConfig(task, entry, in, agent))
	if err != nil {
		return s.attemptFailed(ctx, entry, problem(err, "the worker session could not start"))
	}
	return s.link(context.WithoutCancel(ctx), entry, in, session.ID, settings)
}

// spawnConfig starts the worker from the snapshot's agent and permission mode
// and from the plan and issue the developer approved, in Chat (decisions c,
// d and n of the step 6 plan). The attempt's own client request id is what
// the worker gate grants the spawn by. The branch is left to AO's naming.
func spawnConfig(task domain.LadoTask, entry domain.LadoQueueEntry, in ladotask.RunInput, agent ladopolicy.SnapshotAgent) ports.SpawnConfig {
	return ports.SpawnConfig{
		ProjectID:         task.ProjectID,
		IssueID:           task.IssueRef,
		ClientRequestID:   domain.LadoClientRequestID(entry.ID, entry.AttemptCount),
		ClientRequestHash: domain.LadoDispatchHash(in.Revision.SHA256, in.Snapshot.SHA256),
		IssueContext:      in.IssueContext,
		Kind:              domain.KindWorker,
		Harness:           agent.Harness,
		Prompt:            workerPrompt(task, in),
		AgentConfig:       ports.AgentConfig{Model: agent.Model, Permissions: agent.PermissionMode},
		RequestedMode:     domain.SessionModeChat,
		DisplayName:       oneLine(task.Issue.Title),
	}
}

// snapshotAgent reads the agent a rule snapshot fixed.
func snapshotAgent(snapshot domain.LadoPolicySnapshot) (ladopolicy.SnapshotAgent, error) {
	var content ladopolicy.SnapshotContent
	if err := json.Unmarshal(snapshot.Content, &content); err != nil {
		return ladopolicy.SnapshotAgent{}, fmt.Errorf("decode rule snapshot %s: %w", snapshot.ID, err)
	}
	return content.Agent, nil
}

// runnable reports whether an execution can run with the agent: rule
// resolution needs every execution's agent to take the permission mode LADO
// picks (ladopolicy checkAgent), which Claude Code and Codex do.
func runnable(agent domain.AgentHarness) bool {
	return ladopolicy.CapabilityOf(agent, ladopolicy.CapabilityPermissionMode) == ladopolicy.CapabilitySupported
}

// workerBase resolves the branch AO starts the task's worker from, the way
// AO's spawn resolves it (session_manager refreshDefaultBranchesBestEffort):
// for a single repository, the project's default branch setting, where auto
// means the repository's own default; for a workspace project, the branch
// recorded for the child repository that holds the task's issue.
func (s *Service) workerBase(ctx context.Context, task domain.LadoTask) (ports.WorkspaceDefaultBranch, error) {
	if s.deps.Branches == nil {
		return ports.WorkspaceDefaultBranch{}, errors.New("no branch resolver is wired")
	}
	project, ok, err := s.deps.Projects.GetProject(ctx, string(task.ProjectID))
	if err != nil {
		return ports.WorkspaceDefaultBranch{}, err
	}
	if !ok {
		return ports.WorkspaceDefaultBranch{}, fmt.Errorf("project %s not found", task.ProjectID)
	}
	dir, configured := project.Path, project.Config.WorktreeBaseBranch()
	if project.Kind.WithDefault() == domain.ProjectKindWorkspace {
		children, err := s.deps.Projects.ListWorkspaceRepos(ctx, project.ID)
		if err != nil {
			return ports.WorkspaceDefaultBranch{}, err
		}
		found := false
		for _, child := range children {
			repo, err := domain.LadoTrackerRepoFromOrigin(child.RepoOriginURL)
			if err == nil && domain.LadoSameRepo(repo, task.Repo) {
				dir, configured, found = filepath.Join(project.Path, filepath.FromSlash(child.RelativePath)), child.DefaultBranch, true
				break
			}
		}
		if !found {
			return ports.WorkspaceDefaultBranch{}, fmt.Errorf("no workspace repository has the origin %s", domain.LadoRepoRef(task.Repo))
		}
	}
	ctx, cancel := context.WithTimeout(ctx, baseResolveTimeout)
	defer cancel()
	return s.deps.Branches.ResolveDefaultBranch(ctx, dir, configured)
}

// namesBase reports whether a plan's base_ref names the branch a worker
// starts from: the branch itself, or its local or remote-tracking ref in a
// longer form.
func namesBase(ref string, base ports.WorkspaceDefaultBranch) bool {
	if ref == "" || base.Branch == "" {
		return false
	}
	names := []string{base.Branch, "refs/heads/" + base.Branch}
	if base.Remote != "" {
		names = append(names, base.Remote+"/"+base.Branch, "refs/remotes/"+base.Remote+"/"+base.Branch)
	}
	if base.BaseRef != "" {
		names = append(names, base.BaseRef)
	}
	return slices.Contains(names, ref)
}

// link records the session as the entry's execution, deadline included. A
// claim lost meanwhile is no failure: the session exists, and recovery links
// the session of the entry's attempt (S08).
func (s *Service) link(ctx context.Context, entry domain.LadoQueueEntry, in ladotask.RunInput, sessionID domain.SessionID, settings domain.LadoDispatchSettings) error {
	started := s.deps.Clock().UTC()
	exec := domain.LadoExecution{
		SessionID:        sessionID,
		TaskID:           entry.TaskID,
		QueueEntryID:     entry.ID,
		PlanRevisionID:   in.Revision.ID,
		PlanApprovalID:   entry.PlanApprovalID,
		PolicySnapshotID: entry.PolicySnapshotID,
		Attempt:          entry.AttemptCount,
		StartedAt:        started,
	}
	if settings.MaxRunMinutes > 0 {
		exec.DeadlineAt = started.Add(time.Duration(settings.MaxRunMinutes) * time.Minute)
	}
	err := s.deps.Store.LinkLadoExecution(ctx, entry.ID, entry.LeaseToken, exec)
	if errors.Is(err, domain.ErrLadoLeaseLost) {
		s.deps.Log.Warn("LADO started a worker but lost the claim of its queue entry before linking it",
			"entry", entry.ID, "task", entry.TaskID, "session", sessionID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("link session %s: %w", sessionID, err)
	}
	s.deps.Log.Info("LADO started a task's worker", "entry", entry.ID, "task", entry.TaskID, "session", sessionID, "attempt", entry.AttemptCount)
	s.stopIfUnwanted(ctx, entry, sessionID)
	return nil
}

// stopIfUnwanted stops a worker its task no longer wants. A cancel or a newer
// approval that lands while AO starts the worker finds no execution to stop
// yet, so the dispatcher looks again once the session is linked: a task never
// runs two workers, and a cancelled one none.
func (s *Service) stopIfUnwanted(ctx context.Context, entry domain.LadoQueueEntry, sessionID domain.SessionID) {
	task, ok, err := s.deps.Store.LadoTask(ctx, entry.TaskID)
	if err != nil || !ok {
		s.deps.Log.Warn("LADO could not read a task again after starting its worker", "task", entry.TaskID, "err", err)
		return
	}
	if task.CancelledAt.IsZero() && task.Plan.Approval.ID == entry.PlanApprovalID {
		return
	}
	if _, err := s.deps.Sessions.RequestKill(ctx, sessionID); err != nil {
		s.deps.Log.Warn("LADO could not stop a worker its task no longer wants", "task", entry.TaskID, "session", sessionID,
			"problem", problem(err, "the session could not be stopped"))
		return
	}
	s.deps.Log.Info("LADO stopped a worker its task no longer wants", "task", entry.TaskID, "session", sessionID)
}

// attemptFailed records an attempt that did not start the worker: the entry
// tries again 30 seconds after its first attempt and two minutes after its
// second, and fails with message after its third.
func (s *Service) attemptFailed(ctx context.Context, entry domain.LadoQueueEntry, message string) error {
	ctx = context.WithoutCancel(ctx)
	at := s.deps.Clock().UTC()
	if entry.AttemptCount >= maxAttempts {
		s.deps.Log.Warn("LADO gave up starting a task's worker", "entry", entry.ID, "task", entry.TaskID,
			"attempt", entry.AttemptCount, "problem", message)
		ok, err := s.deps.Store.FailLadoQueueEntry(ctx, entry.ID, entry.LeaseToken, message, at)
		return s.settled(entry, "failed", ok, err, slog.LevelInfo)
	}
	next := at.Add(retryAfter[max(entry.AttemptCount, 1)-1])
	s.deps.Log.Warn("LADO could not start a task's worker and tries again", "entry", entry.ID, "task", entry.TaskID,
		"attempt", entry.AttemptCount, "next", next, "problem", message)
	ok, err := s.deps.Store.ReleaseLadoQueueEntry(ctx, entry.ID, entry.LeaseToken, next, domain.LadoWaitingRetry)
	return s.settled(entry, "waiting: "+domain.LadoWaitingRetry, ok, err, slog.LevelInfo)
}

// The writes that settle a claimed entry, each with the claim's token. They
// run to the end when the pass is stopping, so the entry never keeps a claim
// nobody works on.

func (s *Service) review(ctx context.Context, entry domain.LadoQueueEntry, reason string) error {
	ok, err := s.deps.Store.MarkLadoQueueEntryReviewRequired(context.WithoutCancel(ctx), entry.ID, entry.LeaseToken, reason)
	return s.settled(entry, "review required: "+reason, ok, err, slog.LevelInfo)
}

func (s *Service) block(ctx context.Context, entry domain.LadoQueueEntry, reason string) error {
	ok, err := s.deps.Store.MarkLadoQueueEntryBlocked(context.WithoutCancel(ctx), entry.ID, entry.LeaseToken, reason)
	return s.settled(entry, "blocked: "+reason, ok, err, slog.LevelInfo)
}

func (s *Service) cancel(ctx context.Context, entry domain.LadoQueueEntry, at time.Time) error {
	ok, err := s.deps.Store.CancelLadoQueueEntry(context.WithoutCancel(ctx), entry.ID, entry.LeaseToken, at)
	return s.settled(entry, "cancelled", ok, err, slog.LevelInfo)
}

// recheck holds the entry for a minute, because a check could not run. When
// the pass is stopping, which fails the checks too, it hands the entry back
// instead.
func (s *Service) recheck(ctx context.Context, entry domain.LadoQueueEntry, waiting string, now time.Time) error {
	if ctx.Err() != nil {
		return s.handBack(ctx, entry)
	}
	return s.hold(ctx, entry, waiting, now.Add(recheckAfter))
}

// handBack returns the entry to the queue as it was before the claim.
func (s *Service) handBack(ctx context.Context, entry domain.LadoQueueEntry) error {
	return s.hold(ctx, entry, "", time.Time{})
}

// hold hands the entry back without spending the attempt its claim counted.
func (s *Service) hold(ctx context.Context, entry domain.LadoQueueEntry, waiting string, until time.Time) error {
	ok, err := s.deps.Store.HoldLadoQueueEntry(context.WithoutCancel(ctx), entry.ID, entry.LeaseToken, waiting, until)
	return s.settled(entry, "waiting: "+waiting, ok, err, slog.LevelDebug)
}

func (s *Service) release(ctx context.Context, entry domain.LadoQueueEntry, next time.Time, waiting string) error {
	ok, err := s.deps.Store.ReleaseLadoQueueEntry(context.WithoutCancel(ctx), entry.ID, entry.LeaseToken, next, waiting)
	return s.settled(entry, "released", ok, err, slog.LevelDebug)
}

// settled logs what became of the entry. A write that found the claim's
// token gone changed nothing: the entry moved on without this dispatcher.
func (s *Service) settled(entry domain.LadoQueueEntry, outcome string, ok bool, err error, level slog.Level) error {
	switch {
	case err != nil:
		return fmt.Errorf("record %q: %w", outcome, err)
	case !ok:
		s.deps.Log.Warn("LADO lost the claim of a queue entry before recording it", "entry", entry.ID, "outcome", outcome)
	default:
		s.deps.Log.Log(context.Background(), level, "LADO settled a queue entry", "entry", entry.ID, "task", entry.TaskID,
			"attempt", entry.AttemptCount, "outcome", outcome)
	}
	return nil
}

// credentialInURL matches the user information of a URL, where a remote can
// carry a token.
var credentialInURL = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]+@`)

// problem is what LADO records and logs of an error from outside the
// dispatcher (the tracker, the rules, git, AO's spawn): AO's error code and
// message with any URL credentials cut, or fallback for an error without
// them. A raw error can carry what a command line, a URL or an environment
// held, a token among them; an AO error's message is written for people.
func problem(err error, fallback string) string {
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || strings.TrimSpace(apiErr.Message) == "" {
		return fallback
	}
	text := oneLine(credentialInURL.ReplaceAllString(apiErr.Message, "$1"))
	if apiErr.Code != "" {
		text = apiErr.Code + ": " + text
	}
	if len(text) > maxProblemBytes {
		cut := maxProblemBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	return text
}

// oneLine collapses runs of white space, line breaks among them, into single
// spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
