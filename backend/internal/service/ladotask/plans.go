package ladotask

// Plan requests, plan revisions and approvals (docs/LJY-PRD/TDD.md §5.1 and
// §5.2).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	githubtracker "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/github"
	gitlabtracker "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

const (
	// maxPlanNoteRunes caps what a developer may ask to change.
	maxPlanNoteRunes = 4000
	// maxSnapshotBytes caps a pinned issue snapshot read back from the blob
	// store: a body and the comments' size limit, with room for JSON.
	maxSnapshotBytes = 4 << 20
)

// Blobs keeps pinned plans and issue snapshots under their sha256.
type Blobs interface {
	Put(data []byte) (string, error)
	Get(sum string, limit int64) ([]byte, error)
}

// OrchestratorSession is what LADO needs to know about the orchestrator of a
// plan request made before plan workers.
type OrchestratorSession struct {
	ID domain.SessionID
	// ArtifactDir is the session's artifact directory, where LADO put the
	// pinned issue and the orchestrator writes the plan.
	ArtifactDir string
	Terminated  bool
}

// ErrOrchestratorGone means the orchestrator can no longer take messages: it
// was terminated, its agent exited or the session is gone.
var ErrOrchestratorGone = errors.New("the orchestrator session has ended")

// Orchestrators reads the orchestrators that plan requests made before plan
// workers went to, and delivers those requests.
type Orchestrators interface {
	// Session returns an orchestrator by id.
	Session(ctx context.Context, id domain.SessionID) (OrchestratorSession, bool, error)
	// Send delivers a message from the daemon. An error wrapping
	// ErrOrchestratorGone means retrying cannot help; any other error may
	// pass, such as an agent that is still starting.
	Send(ctx context.Context, id domain.SessionID, message string) error
}

// Artifacts reads AO's artifact versions.
type Artifacts interface {
	// Versions lists a session's versions of one artifact path.
	Versions(ctx context.Context, sessionID domain.SessionID, path string) ([]domain.SessionArtifactVersion, error)
	// Read returns one version's content.
	Read(ctx context.Context, version domain.SessionArtifactVersion) ([]byte, error)
}

// PlanRequestInput asks the orchestrator for a plan, or for changes to the
// latest one.
type PlanRequestInput struct {
	TaskID domain.LadoTaskID
	// SourceCommentID names the issue comment that holds the plan, when the
	// developer chose one.
	SourceCommentID string
	// Note is what to change in the latest revision. It makes the request a
	// change request.
	Note string
}

// RequestPlan pins the task's issue with its trusted comments and asks for a
// plan, or for changes to the latest one: LADO keeps the request, and the plan
// loop starts a plan worker for it within the Runs settings' cap
// (docs/LJY-PRD/TDD.md §5.1). The request replaces the task's older ones, and
// their plan workers stop. No orchestrator is involved.
func (s *Service) RequestPlan(ctx context.Context, in PlanRequestInput) (domain.LadoPlanRequest, error) {
	task, err := s.openTask(ctx, in.TaskID)
	if err != nil {
		return domain.LadoPlanRequest{}, err
	}
	note := strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(note) > maxPlanNoteRunes {
		return domain.LadoPlanRequest{}, apierr.Invalid("LADO_PLAN_NOTE_TOO_LONG", fmt.Sprintf("Keep the change request within %d characters.", maxPlanNoteRunes), map[string]any{"max": maxPlanNoteRunes})
	}
	sourceComment := strings.TrimSpace(in.SourceCommentID)
	if note != "" && sourceComment != "" {
		return domain.LadoPlanRequest{}, apierr.Invalid("LADO_PLAN_REQUEST_AMBIGUOUS", "Ask for changes or name a comment as the plan, not both.", nil)
	}
	var basedOn domain.LadoPlanRevision
	if note != "" {
		if task.Plan.Latest.ID == "" {
			return domain.LadoPlanRequest{}, apierr.Conflict("LADO_PLAN_NOTHING_TO_CHANGE", "There is no plan to change yet. Ask for a plan first.", nil)
		}
		basedOn = task.Plan.Latest
	}
	if s.deps.PlanSessions == nil {
		s.deps.Log.Warn("LADO cannot take a plan request", "task", task.ID, "err", errLadoPlanWorkersOff)
		return domain.LadoPlanRequest{}, apierr.Unavailable("LADO_PLAN_WORKERS_UNAVAILABLE", "LADO cannot start plan workers in this daemon.")
	}
	issue, comments, err := s.readIssueWithComments(ctx, task)
	if err != nil {
		return domain.LadoPlanRequest{}, err
	}
	snapshot, _ := domain.NewLadoIssueSnapshot(task.IssueRef, issue.Title, issue.Body, comments)
	if sourceComment != "" && !snapshot.HasComment(sourceComment) {
		return domain.LadoPlanRequest{}, apierr.Invalid("LADO_PLAN_SOURCE_COMMENT_UNKNOWN",
			fmt.Sprintf("Comment %s is not one LADO can use: it is gone, or its author cannot work on the repository.", sourceComment),
			map[string]any{"comment": sourceComment})
	}
	sum, err := s.deps.Blobs.Put(snapshot.CanonicalJSON())
	if err != nil {
		s.deps.Log.Error("LADO could not pin an issue snapshot", "task", task.ID, "err", err)
		return domain.LadoPlanRequest{}, apierr.Internal("LADO_SNAPSHOT_SAVE_FAILED", "Failed to save the issue snapshot")
	}
	id := domain.LadoPlanRequestID("lado-plan-request-" + s.deps.NewID())
	req, err := s.deps.Store.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{
		ID:                  id,
		TaskID:              task.ID,
		Kind:                domain.LadoPlanRequestWorker,
		SessionID:           domain.LadoPlanRequestPlaceholder(id),
		IssueSnapshotSHA256: sum,
		SourceCommentID:     sourceComment,
		Note:                note,
		BasedOn:             basedOn.ID,
		RequestedAt:         s.deps.Clock().UTC(),
	})
	if err != nil {
		return domain.LadoPlanRequest{}, apierr.Internal("LADO_PLAN_REQUEST_SAVE_FAILED", "Failed to save the plan request")
	}
	for _, older := range s.openWorkerRequests(ctx, task.ID, req.ID) {
		s.failWorker(ctx, older, domain.LadoPlanRequestReplaced, "a newer plan request replaced it")
	}
	s.kickPlans()
	return req, nil
}

// readIssueWithComments reads a task's issue and its comments now, and
// records what it saw on the task.
func (s *Service) readIssueWithComments(ctx context.Context, task domain.LadoTask) (domain.Issue, []domain.LadoIssueComment, error) {
	id, err := domain.ParseLadoIssueRef(task.IssueRef)
	if err != nil {
		return domain.Issue{}, nil, apierr.Internal("LADO_TASK_LOAD_FAILED", "The task's issue ref is unreadable")
	}
	client, err := s.client(ctx, domain.LadoIssueRepo(id))
	if err != nil {
		return domain.Issue{}, nil, err
	}
	readCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	issue, err := client.Get(readCtx, id)
	if err != nil {
		return domain.Issue{}, nil, issueReadFailed(task.IssueRef, err)
	}
	comments, err := client.IssueComments(readCtx, id)
	if err != nil {
		return domain.Issue{}, nil, issueReadFailed(task.IssueRef, err)
	}
	issue.ID = id
	now := s.deps.Clock().UTC()
	if err := s.deps.Store.ObserveLadoTaskIssue(ctx, task.ID, observedIssue(issue, now), now); err != nil {
		s.deps.Log.Debug("LADO could not record a task's issue", "task", task.ID, "err", err)
	}
	return issue, comments, nil
}

// issueReadFailed is the error of an issue the tracker did not give. When the
// tracker answered that the issue does not exist, it also wraps
// domain.ErrLadoIssueNotFound.
func issueReadFailed(ref domain.IssueID, err error) error {
	failed := apierr.New(apierr.KindUnavailable, "LADO_ISSUE_READ_FAILED", fmt.Sprintf("Could not read %s: %v", ref, err),
		map[string]any{"issue": string(ref), "reason": err.Error()})
	if errors.Is(err, githubtracker.ErrNotFound) || errors.Is(err, gitlabtracker.ErrNotFound) {
		return fmt.Errorf("%w: %w", domain.ErrLadoIssueNotFound, failed)
	}
	return failed
}

// writeIssueFile puts the pinned snapshot where an orchestrator reads it, for
// a request made before plan workers. The file is a copy for reading; the
// blob store keeps the original.
func writeIssueFile(artifactDir string, task domain.LadoTask, snapshot domain.LadoIssueSnapshot, sum string, selection domain.LadoCommentSelection) error {
	if artifactDir == "" {
		return errors.New("the orchestrator has no artifact directory")
	}
	target := filepath.Join(artifactDir, filepath.FromSlash(domain.LadoIssueFilePath(task.ID)))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(renderIssueFile(task, snapshot, sum, selection)), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// deliver sends a request made before plan workers to its orchestrator once.
// A failure is recorded; the plan loop retries until the delivery timeout.
func (s *Service) deliver(ctx context.Context, task domain.LadoTask, req domain.LadoPlanRequest, snapshot domain.LadoIssueSnapshot, orchestrator OrchestratorSession, basedOn domain.LadoPlanRevision) {
	message := planRequestMessage(task, req, snapshot, orchestrator.ArtifactDir, basedOn)
	err := s.deps.Orchestrators.Send(ctx, req.SessionID, message)
	now := s.deps.Clock().UTC()
	switch {
	case err == nil:
		if err := s.deps.Store.MarkLadoPlanRequestDelivered(ctx, req.ID, now); err != nil {
			s.deps.Log.Warn("LADO could not record a delivered plan request", "request", req.ID, "err", err)
		}
	case errors.Is(err, ErrOrchestratorGone):
		s.deps.Log.Info("LADO gave up a plan request: the orchestrator has ended", "request", req.ID, "err", err)
		if err := s.deps.Store.FailLadoPlanRequest(ctx, req.ID, now, err.Error()); err != nil {
			s.deps.Log.Warn("LADO could not record a failed plan request", "request", req.ID, "err", err)
		}
	default:
		s.deps.Log.Debug("LADO will retry a plan request", "request", req.ID, "err", err)
		if err := s.deps.Store.RecordLadoPlanRequestAttempt(ctx, req.ID, err.Error()); err != nil {
			s.deps.Log.Warn("LADO could not record a plan request attempt", "request", req.ID, "err", err)
		}
	}
}

// ApprovePlan approves one exact plan revision with a human-presence proof
// (docs/LJY-PRD/TDD.md §5.2) and queues it for the dispatcher. sha256 is the
// plan hash and policySHA256 the rule snapshot hash the developer saw; the
// approval, the rule snapshot, the queue entry, the proof and the audit event
// commit together, and the approval supersedes the task's older open entries.
// A snapshot with a blocking problem is approved too, and the task shows as
// Blocked. Which tasks may be approved again is approvable's.
func (s *Service) ApprovePlan(ctx context.Context, revisionID domain.LadoPlanRevisionID, sha256, policySHA256 string, proof domain.LadoPresenceProof) (domain.LadoPlanApproval, error) {
	rev, err := s.revision(ctx, revisionID)
	if err != nil {
		return domain.LadoPlanApproval{}, err
	}
	task, err := s.openTask(ctx, rev.TaskID)
	if err != nil {
		return domain.LadoPlanApproval{}, err
	}
	switch {
	case sha256 != rev.SHA256:
		return domain.LadoPlanApproval{}, apierr.Conflict("LADO_PLAN_CHANGED", "This is not the plan you reviewed. Open the latest plan and review it again.", nil)
	case task.Plan.Latest.ID != rev.ID:
		return domain.LadoPlanApproval{}, superseded()
	case !rev.Valid():
		return domain.LadoPlanApproval{}, apierr.Conflict("LADO_PLAN_INVALID", "This plan has problems and cannot be approved. Ask for changes first.", nil)
	}
	// The state shows whether the rules changed since the last approval.
	if err := approvable(s.withPolicy(ctx, task)); err != nil {
		return domain.LadoPlanApproval{}, err
	}
	if _, err := s.deps.Blobs.Get(rev.SHA256, domain.LadoPlanMaxBytes); err != nil {
		s.deps.Log.Error("LADO could not read a pinned plan before approval", "revision", rev.ID, "err", err)
		return domain.LadoPlanApproval{}, apierr.Conflict("LADO_PLAN_BLOB_MISSING", "The pinned copy of this plan is missing or changed on disk, so it cannot be approved.", nil)
	}
	snapshot, err := s.ruleSnapshot(ctx, task, rev, policySHA256)
	if err != nil {
		return domain.LadoPlanApproval{}, err
	}
	now := s.deps.Clock().UTC()
	actor := "human"
	if proof.Dev {
		actor = "human:dev-presence"
	}
	approval := domain.LadoPlanApproval{
		ID:         domain.LadoPlanApprovalID("lado-approval-" + s.deps.NewID()),
		RevisionID: rev.ID,
		TaskID:     task.ID,
		SHA256:     rev.SHA256,
		ProofNonce: proof.Nonce,
		ApprovedAt: now,
	}
	entry := domain.LadoQueueEntry{
		ID:         domain.LadoQueueEntryID("lado-queue-" + s.deps.NewID()),
		TaskID:     task.ID,
		ProjectID:  task.ProjectID,
		EnqueuedAt: now,
	}
	payload, _ := json.Marshal(map[string]any{
		"task":           task.ID,
		"issue":          task.IssueRef,
		"revision":       rev.Number,
		"sha256":         rev.SHA256,
		"issueSnapshot":  rev.IssueSnapshotSHA256,
		"policySnapshot": snapshot.SHA256,
		"blocking":       snapshot.Blocking,
		"queueEntry":     entry.ID,
		"proof":          proof.Nonce,
		"dev":            proof.Dev,
	})
	snapshot.ApprovalID = approval.ID
	approval, _, err = s.deps.Store.ApproveLadoPlan(ctx, approval, proof, domain.LadoAuditEvent{
		At:          now,
		Actor:       actor,
		Action:      "plan.approved",
		SubjectKind: "plan_revision",
		SubjectID:   string(rev.ID),
		Payload:     payload,
	}, &snapshot, &entry)
	switch {
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return domain.LadoPlanApproval{}, ladopresence.Replayed()
	case errors.Is(err, domain.ErrLadoPlanSuperseded):
		return domain.LadoPlanApproval{}, superseded()
	case errors.Is(err, domain.ErrLadoRulesChanged):
		// A rule the snapshot used was deleted after it was resolved.
		return domain.LadoPlanApproval{}, rulesChanged()
	case err != nil:
		s.deps.Log.Error("LADO could not record a plan approval", "revision", rev.ID, "err", err)
		return domain.LadoPlanApproval{}, apierr.Internal("LADO_PLAN_APPROVE_FAILED", "Failed to save the approval")
	}
	return approval, nil
}

// approvable refuses an approval that would start a second worker for the
// task: any approval while the session of the task's newest execution lives,
// whatever its PR or deadline did, while its PR is verified or integrated, or
// after its work merged, and approving the revision of a queued or
// dispatching task again. That task's approval covers the latest revision,
// the only one ApprovePlan accepts; a newer revision makes the task Planned,
// and its approval supersedes the queued entry. A task that needs review, is
// blocked or failed approves its latest revision again.
// The task's state must carry the rules as they are now (withPolicy), so a
// queued task whose rules changed reads as needing review.
func approvable(task domain.LadoTask) error {
	state := task.State()
	details := map[string]any{"taskId": string(task.ID), "state": string(state)}
	live := task.Execution.SessionID != "" && !task.Execution.SessionTerminated
	switch {
	case live || state == domain.LadoTaskRunning || state == domain.LadoTaskValidating || state == domain.LadoTaskIntegrationPending ||
		state == domain.LadoTaskIntegrating || state == domain.LadoTaskCompleted:
		return apierr.Conflict("LADO_TASK_RUNNING", "This task's worker still runs, or its work merged. LADO runs one worker per task, so no plan can be approved now.", details)
	case state == domain.LadoTaskQueued || state == domain.LadoTaskDispatching:
		return apierr.Conflict("LADO_TASK_ALREADY_QUEUED", "This plan is approved and waits for its worker. Only a newer revision can replace it.", details)
	}
	return nil
}

func superseded() error {
	return apierr.Conflict("LADO_PLAN_SUPERSEDED", "A newer version of this plan exists. Review the latest plan and approve that one.", nil)
}

func rulesChanged() error {
	return apierr.Conflict("LADO_RULES_CHANGED", "The rules changed while you were approving. Review them again, then approve.", nil)
}

// ruleSnapshot fixes the rules for approving a revision. It refuses when
// they are not the rules the developer reviewed, so an approval never takes
// rules nobody saw.
func (s *Service) ruleSnapshot(ctx context.Context, task domain.LadoTask, rev domain.LadoPlanRevision, policySHA256 string) (domain.LadoPolicySnapshot, error) {
	if policySHA256 == "" {
		return domain.LadoPolicySnapshot{}, apierr.Invalid("LADO_POLICY_SHA_REQUIRED", "Send the sha256 of the rule snapshot you reviewed with the approval.", nil)
	}
	if s.deps.Policy == nil {
		return domain.LadoPolicySnapshot{}, apierr.Internal("LADO_RULES_UNAVAILABLE", "LADO cannot read its rules, so no plan can be approved.")
	}
	snapshot, err := s.deps.Policy.Snapshot(ctx, task, rev)
	if err != nil {
		var apiErr *apierr.Error
		if errors.As(err, &apiErr) {
			return domain.LadoPolicySnapshot{}, err
		}
		s.deps.Log.Error("LADO could not resolve the rules for an approval", "revision", rev.ID, "err", err)
		return domain.LadoPolicySnapshot{}, apierr.Internal("LADO_RULES_LOAD_FAILED", "Failed to load the rules")
	}
	if snapshot.SHA256 != policySHA256 {
		return domain.LadoPolicySnapshot{}, rulesChanged()
	}
	return snapshot, nil
}

// PlanView is one revision with its content, for review and for the
// presence confirmation.
type PlanView struct {
	Task     domain.LadoTask
	Revision domain.LadoPlanRevision
	// Content is the pinned plan file. It is empty when the pin is missing.
	Content string
	Plan    domain.LadoPlan
	// Latest says this is the task's newest revision; Approved says a valid
	// approval covers it.
	Latest   bool
	Approved bool
	// Critique is the newest review of the draft of the revision's request,
	// zero for a plan without one; Metrics is what making the revision took,
	// nil when LADO did not record it.
	Critique domain.LadoPlanCritique
	Metrics  *domain.LadoPlanRevisionMetrics
}

// Plan returns one revision with its pinned content.
func (s *Service) Plan(ctx context.Context, revisionID domain.LadoPlanRevisionID) (PlanView, error) {
	rev, err := s.revision(ctx, revisionID)
	if err != nil {
		return PlanView{}, err
	}
	task, err := s.task(ctx, rev.TaskID)
	if err != nil {
		return PlanView{}, err
	}
	view := PlanView{
		Task:     s.withPolicy(ctx, task),
		Revision: rev,
		Latest:   task.Plan.Latest.ID == rev.ID,
		Approved: task.Plan.ApprovalValid() && task.Plan.Latest.ID == rev.ID,
	}
	view.Critique, _ = s.lastCritique(ctx, rev.RequestID)
	if m, ok, err := s.deps.Store.LadoPlanRevisionMetrics(ctx, rev.ID); err == nil && ok {
		view.Metrics = &m
	}
	content, err := s.deps.Blobs.Get(rev.SHA256, domain.LadoPlanMaxBytes)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a pinned plan", "revision", rev.ID, "err", err)
		return view, nil
	}
	view.Content = string(content)
	view.Plan, _ = domain.ParseLadoPlan(content, domain.LadoPlanExpectations{Task: task.ID, Issue: task.IssueRef})
	return view, nil
}

// PlanHistory is a task's plan requests, revisions and approvals, with the
// reviews of plan workers' drafts and what making each revision took.
type PlanHistory struct {
	Requests  []domain.LadoPlanRequest
	Revisions []domain.LadoPlanRevision
	Approvals []domain.LadoPlanApproval
	Critiques []domain.LadoPlanCritique
	Metrics   map[domain.LadoPlanRevisionID]domain.LadoPlanRevisionMetrics
}

// Plans returns a task's plan history, oldest first.
func (s *Service) Plans(ctx context.Context, id domain.LadoTaskID) (PlanHistory, error) {
	if _, err := s.task(ctx, id); err != nil {
		return PlanHistory{}, err
	}
	var history PlanHistory
	var err error
	if history.Requests, err = s.deps.Store.ListLadoPlanRequests(ctx, id); err != nil {
		return PlanHistory{}, apierr.Internal("LADO_PLANS_LOAD_FAILED", "Failed to load the task's plans")
	}
	if history.Revisions, err = s.deps.Store.ListLadoPlanRevisions(ctx, id); err != nil {
		return PlanHistory{}, apierr.Internal("LADO_PLANS_LOAD_FAILED", "Failed to load the task's plans")
	}
	if history.Approvals, err = s.deps.Store.ListLadoPlanApprovals(ctx, id); err != nil {
		return PlanHistory{}, apierr.Internal("LADO_PLANS_LOAD_FAILED", "Failed to load the task's plans")
	}
	if history.Critiques, err = s.deps.Store.ListLadoPlanCritiquesByTask(ctx, id); err != nil {
		return PlanHistory{}, apierr.Internal("LADO_PLANS_LOAD_FAILED", "Failed to load the task's plans")
	}
	if history.Metrics, err = s.deps.Store.ListLadoPlanRevisionMetrics(ctx, id); err != nil {
		return PlanHistory{}, apierr.Internal("LADO_PLANS_LOAD_FAILED", "Failed to load the task's plans")
	}
	return history, nil
}

// TaskView is what the task view shows: the task, its issue and comments as
// they are now, and its plan history.
type TaskView struct {
	Task domain.LadoTask
	// Issue and Comments are read again for the view. IssueReadError is set
	// instead when the tracker could not be read.
	Issue          domain.Issue
	Comments       []domain.LadoIssueComment
	IssueReadError string
	// IssueChanged says the issue or a comment a plan may use changed since
	// the snapshot the latest revision was made from.
	IssueChanged bool
	History      PlanHistory
}

// View reads a task's issue and comments again and returns the task view.
func (s *Service) View(ctx context.Context, id domain.LadoTaskID) (TaskView, error) {
	task, err := s.task(ctx, id)
	if err != nil {
		return TaskView{}, err
	}
	view := TaskView{Task: task}
	if task.CancelledAt.IsZero() {
		issue, comments, err := s.readIssueWithComments(ctx, task)
		if err != nil {
			view.IssueReadError = errorMessage(err)
		} else {
			view.Issue, view.Comments = issue, comments
			if latest := task.Plan.Latest; latest.ID != "" {
				snapshot, _ := domain.NewLadoIssueSnapshot(task.IssueRef, issue.Title, issue.Body, comments)
				view.IssueChanged = snapshot.SHA256() != latest.IssueSnapshotSHA256
			}
		}
		if task, err = s.task(ctx, id); err == nil {
			view.Task = task
		}
	}
	if view.History, err = s.Plans(ctx, id); err != nil {
		return TaskView{}, err
	}
	view.Task = s.withPolicy(ctx, view.Task)
	return view, nil
}

func errorMessage(err error) string {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return apiErr.Message
	}
	return err.Error()
}

// openTask returns a task that a plan request or approval may act on: not
// cancelled, its issue open, its project in LADO mode. The issue's own state
// decides, because a task whose worker runs or merged shows that state while
// its issue is closed.
func (s *Service) openTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, error) {
	task, err := s.task(ctx, id)
	if err != nil {
		return domain.LadoTask{}, err
	}
	switch {
	case !task.CancelledAt.IsZero():
		return domain.LadoTask{}, apierr.Conflict("LADO_TASK_CANCELLED", "This task was cancelled.", nil)
	case task.Issue.State == domain.IssueDone || task.Issue.State == domain.IssueCancelled:
		return domain.LadoTask{}, apierr.Conflict("LADO_ISSUE_CLOSED", "The task's issue is closed. Reopen it on the tracker to plan this work.", nil)
	}
	if _, err := s.ladoRepos(ctx, task.ProjectID); err != nil {
		return domain.LadoTask{}, err
	}
	return task, nil
}

func (s *Service) revision(ctx context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlanRevision, error) {
	rev, ok, err := s.deps.Store.LadoPlanRevision(ctx, id)
	if err != nil {
		return domain.LadoPlanRevision{}, apierr.Internal("LADO_PLANS_LOAD_FAILED", "Failed to load the plan")
	}
	if !ok {
		return domain.LadoPlanRevision{}, apierr.NotFound("LADO_PLAN_NOT_FOUND", "Unknown plan revision")
	}
	return rev, nil
}

// snapshotOf reads a request's pinned issue snapshot back.
func (s *Service) snapshotOf(req domain.LadoPlanRequest) (domain.LadoIssueSnapshot, error) {
	return s.pinnedIssue(req.IssueSnapshotSHA256)
}

// pinnedIssue reads a pinned issue snapshot back by its sha256.
func (s *Service) pinnedIssue(sum string) (domain.LadoIssueSnapshot, error) {
	data, err := s.deps.Blobs.Get(sum, maxSnapshotBytes)
	if err != nil {
		return domain.LadoIssueSnapshot{}, err
	}
	var snapshot domain.LadoIssueSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return domain.LadoIssueSnapshot{}, fmt.Errorf("decode issue snapshot %s: %w", sum, err)
	}
	return snapshot, nil
}
