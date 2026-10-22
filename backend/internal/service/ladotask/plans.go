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

// OrchestratorSession is what LADO needs to know about an orchestrator.
type OrchestratorSession struct {
	ID domain.SessionID
	// ArtifactDir is the session's artifact directory, where LADO puts the
	// pinned issue and the orchestrator writes the plan.
	ArtifactDir string
	Terminated  bool
}

// ErrOrchestratorGone means the orchestrator can no longer take messages: it
// was terminated, its agent exited or the session is gone.
var ErrOrchestratorGone = errors.New("the orchestrator session has ended")

// Orchestrators finds a project's orchestrator and delivers messages to it.
type Orchestrators interface {
	// Orchestrator returns the project's active orchestrator, starting one
	// when there is none.
	Orchestrator(ctx context.Context, projectID domain.ProjectID) (OrchestratorSession, error)
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

// RequestPlan pins the task's issue with its trusted comments and asks the
// project's orchestrator for a plan. The request is kept even when the
// orchestrator cannot take it yet, such as while it starts, and LADO keeps
// delivering it in the background.
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
	issue, comments, err := s.readIssueWithComments(ctx, task)
	if err != nil {
		return domain.LadoPlanRequest{}, err
	}
	snapshot, selection := domain.NewLadoIssueSnapshot(task.IssueRef, issue.Title, issue.Body, comments)
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
	orchestrator, err := s.deps.Orchestrators.Orchestrator(ctx, task.ProjectID)
	if err != nil {
		s.deps.Log.Warn("LADO could not find or start the orchestrator", "project", task.ProjectID, "err", err)
		return domain.LadoPlanRequest{}, apierr.New(apierr.KindUnavailable, "LADO_ORCHESTRATOR_UNAVAILABLE",
			fmt.Sprintf("LADO could not reach the project's orchestrator: %v", err), map[string]any{"reason": err.Error()})
	}
	if err := writeIssueFile(orchestrator.ArtifactDir, task, snapshot, sum, selection); err != nil {
		s.deps.Log.Error("LADO could not write the pinned issue for the orchestrator", "task", task.ID, "err", err)
		return domain.LadoPlanRequest{}, apierr.Internal("LADO_SNAPSHOT_SAVE_FAILED", "Failed to save the issue snapshot")
	}
	now := s.deps.Clock().UTC()
	req, err := s.deps.Store.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{
		ID:                  domain.LadoPlanRequestID("lado-plan-request-" + s.deps.NewID()),
		TaskID:              task.ID,
		SessionID:           orchestrator.ID,
		IssueSnapshotSHA256: sum,
		SourceCommentID:     sourceComment,
		Note:                note,
		BasedOn:             basedOn.ID,
		RequestedAt:         now,
	})
	if err != nil {
		return domain.LadoPlanRequest{}, apierr.Internal("LADO_PLAN_REQUEST_SAVE_FAILED", "Failed to save the plan request")
	}
	s.deliver(ctx, task, req, snapshot, orchestrator, basedOn)
	if stored, ok, err := s.deps.Store.LadoPlanRequest(ctx, req.ID); err == nil && ok {
		req = stored
	}
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

func issueReadFailed(ref domain.IssueID, err error) error {
	return apierr.New(apierr.KindUnavailable, "LADO_ISSUE_READ_FAILED", fmt.Sprintf("Could not read %s: %v", ref, err),
		map[string]any{"issue": string(ref), "reason": err.Error()})
}

// writeIssueFile puts the pinned snapshot where the orchestrator reads it.
// The file is a copy for reading; the blob store keeps the original.
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

// deliver sends a request to its orchestrator once. A failure is recorded;
// the plan loop retries until the delivery timeout.
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
// (docs/LJY-PRD/TDD.md §5.2). sha256 is the hash the developer saw; the
// approval, the proof and the audit event commit together.
func (s *Service) ApprovePlan(ctx context.Context, revisionID domain.LadoPlanRevisionID, sha256 string, proof domain.LadoPresenceProof) (domain.LadoPlanApproval, error) {
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
	if _, err := s.deps.Blobs.Get(rev.SHA256, domain.LadoPlanMaxBytes); err != nil {
		s.deps.Log.Error("LADO could not read a pinned plan before approval", "revision", rev.ID, "err", err)
		return domain.LadoPlanApproval{}, apierr.Conflict("LADO_PLAN_BLOB_MISSING", "The pinned copy of this plan is missing or changed on disk, so it cannot be approved.", nil)
	}
	now := s.deps.Clock().UTC()
	actor := "human"
	if proof.Dev {
		actor = "human:dev-presence"
	}
	payload, _ := json.Marshal(map[string]any{
		"task":          task.ID,
		"issue":         task.IssueRef,
		"revision":      rev.Number,
		"sha256":        rev.SHA256,
		"issueSnapshot": rev.IssueSnapshotSHA256,
		"proof":         proof.Nonce,
		"dev":           proof.Dev,
	})
	approval, _, err := s.deps.Store.ApproveLadoPlan(ctx, domain.LadoPlanApproval{
		ID:         domain.LadoPlanApprovalID("lado-approval-" + s.deps.NewID()),
		RevisionID: rev.ID,
		TaskID:     task.ID,
		SHA256:     rev.SHA256,
		ProofNonce: proof.Nonce,
		ApprovedAt: now,
	}, proof, domain.LadoAuditEvent{
		At:          now,
		Actor:       actor,
		Action:      "plan.approved",
		SubjectKind: "plan_revision",
		SubjectID:   string(rev.ID),
		Payload:     payload,
	}, nil) // no rule snapshot until the task service reads the rules
	switch {
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return domain.LadoPlanApproval{}, ladopresence.Replayed()
	case errors.Is(err, domain.ErrLadoPlanSuperseded):
		return domain.LadoPlanApproval{}, superseded()
	case err != nil:
		s.deps.Log.Error("LADO could not record a plan approval", "revision", rev.ID, "err", err)
		return domain.LadoPlanApproval{}, apierr.Internal("LADO_PLAN_APPROVE_FAILED", "Failed to save the approval")
	}
	return approval, nil
}

func superseded() error {
	return apierr.Conflict("LADO_PLAN_SUPERSEDED", "A newer version of this plan exists. Review the latest plan and approve that one.", nil)
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
		Task:     task,
		Revision: rev,
		Latest:   task.Plan.Latest.ID == rev.ID,
		Approved: task.Plan.ApprovalValid() && task.Plan.Latest.ID == rev.ID,
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

// PlanHistory is a task's plan requests, revisions and approvals.
type PlanHistory struct {
	Requests  []domain.LadoPlanRequest
	Revisions []domain.LadoPlanRevision
	Approvals []domain.LadoPlanApproval
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
// cancelled, its issue open, its project in LADO mode.
func (s *Service) openTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, error) {
	task, err := s.task(ctx, id)
	if err != nil {
		return domain.LadoTask{}, err
	}
	switch task.State() {
	case domain.LadoTaskCancelled:
		return domain.LadoTask{}, apierr.Conflict("LADO_TASK_CANCELLED", "This task was cancelled.", nil)
	case domain.LadoTaskClosedUnmerged:
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
	data, err := s.deps.Blobs.Get(req.IssueSnapshotSHA256, maxSnapshotBytes)
	if err != nil {
		return domain.LadoIssueSnapshot{}, err
	}
	var snapshot domain.LadoIssueSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return domain.LadoIssueSnapshot{}, fmt.Errorf("decode issue snapshot %s: %w", req.IssueSnapshotSHA256, err)
	}
	return snapshot, nil
}
