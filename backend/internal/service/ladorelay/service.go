// Package ladorelay relays a developer's instruction from a LADO project's
// orchestrator to a running worker of a task (issue #49,
// docs/LJY-PRD/adr/0011-worker-relay.md).
//
// The worker gate still refuses a message an orchestrator sends a worker
// itself (LADO_SEND_REFUSED). Instead the orchestrator asks LADO, and this
// service checks, in order: the request, that the caller is a live Chat
// orchestrator of a LADO project, the task, the worker, and that the
// evidence is a sentence the developer typed in the orchestrator's
// conversation. It then marks an instruction that is not part of that
// message (beyond evidence) or that may leave the approved plan (outside
// plan), records the relay as sending, delivers it as the daemon, and records
// the outcome. Refusals and relays are audited.
package ladorelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Store reads the sessions, project settings, tasks and executions a relay
// checks and keeps the relays and the audit log (*store.Store).
type Store interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
	ListLadoTasks(ctx context.Context, projectID domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error)
	LadoRelayExecutions(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoRelayExecution, error)

	InsertLadoRelay(ctx context.Context, r domain.LadoRelay) error
	MarkLadoRelayDelivered(ctx context.Context, id domain.LadoRelayID, at time.Time) (bool, error)
	MarkLadoRelayFailed(ctx context.Context, id domain.LadoRelayID, failure string) (bool, error)
	LadoRelay(ctx context.Context, id domain.LadoRelayID) (domain.LadoRelay, bool, error)
	LadoRelaysByTask(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoRelay, error)

	AppendLadoAuditEvent(ctx context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error)
}

// Conversations reads the project's orchestrator conversation.
type Conversations interface {
	// ProjectMessages are messages of the project's orchestrator
	// conversation, rows before a /clear boundary included; none when the
	// project has no such conversation. The daemon returns only the
	// developer's (role user, origin human); the service checks role and
	// origin again, so an implementation that returns every row is safe.
	ProjectMessages(ctx context.Context, project domain.ProjectID) ([]domain.ConversationMessage, error)
}

// Plans reads the plan an approval pinned (the daemon's ladoApprovedPlans).
type Plans interface {
	ApprovedPlan(ctx context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlan, error)
}

// Messenger delivers the relay's message to the worker as the daemon: no
// sender session, so the worker gate lets it through, and on a Chat worker
// under domain.LadoRelaySenderLabel.
type Messenger interface {
	Relay(ctx context.Context, worker domain.SessionID, message string) error
}

// Deps wires a Service.
type Deps struct {
	Store         Store
	Conversations Conversations
	Plans         Plans
	Messenger     Messenger
	// Clock stamps relays; nil uses time.Now.
	Clock func() time.Time
	// NewID names a relay; nil uses a random UUID.
	NewID func() string
	Log   *slog.Logger
}

// Service relays developer instructions to workers.
type Service struct {
	deps Deps
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps}
}

// Request is one relay request of an orchestrator.
type Request struct {
	// SessionID is the orchestrator asking (AO_SESSION_ID).
	SessionID domain.SessionID
	// Task is a task id (lado-task-…), an issue number (#49 or 49) or a
	// canonical issue id of the orchestrator's project.
	Task string
	// WorkerSessionID names the worker; empty picks the task's one live
	// worker.
	WorkerSessionID domain.SessionID
	// Message is the instruction and Evidence the developer's sentence, as
	// the developer wrote it.
	Message  string
	Evidence string
	// OutsidePlan is the orchestrator's mark that the instruction leaves the
	// approved plan.
	OutsidePlan bool
}

// Relay checks req, records the relay and delivers it. It returns the relay
// as delivered, or an *apierr.Error with a domain.LadoRelay* code; a relay
// that LADO recorded but could not deliver is LadoRelayDeliveryFailed with
// its id in the details.
func (s *Service) Relay(ctx context.Context, req Request) (domain.LadoRelay, error) {
	now := s.deps.Clock().UTC()
	relay, err := s.check(ctx, req, now)
	if err != nil {
		if refusal, ok := asRefusal(err); ok {
			s.audit(ctx, now, domain.LadoRelayAuditRefused, "session", string(req.SessionID), map[string]any{
				"code": refusal.Code, "details": refusal.Details, "task": req.Task,
				"workerSessionId": string(req.WorkerSessionID),
			})
		}
		return domain.LadoRelay{}, err
	}
	return s.deliver(ctx, relay)
}

// List lists a task's relays, newest first.
func (s *Service) List(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoRelay, error) {
	if _, ok, err := s.deps.Store.LadoTask(ctx, taskID); err != nil {
		return nil, fmt.Errorf("read LADO task %s: %w", taskID, err)
	} else if !ok {
		return nil, apierr.NotFound(domain.LadoRelayTaskNotFound, fmt.Sprintf("No LADO task %s.", taskID))
	}
	relays, err := s.deps.Store.LadoRelaysByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return relays, nil
}

// check runs every check of req and returns the relay to record.
func (s *Service) check(ctx context.Context, req Request, now time.Time) (domain.LadoRelay, error) {
	instruction, evidence, err := validate(req)
	if err != nil {
		return domain.LadoRelay{}, err
	}
	project, err := s.orchestratorProject(ctx, req.SessionID)
	if err != nil {
		return domain.LadoRelay{}, err
	}
	task, err := s.resolveTask(ctx, project, req.Task)
	if err != nil {
		return domain.LadoRelay{}, err
	}
	worker, err := s.pickWorker(ctx, task, req.WorkerSessionID)
	if err != nil {
		return domain.LadoRelay{}, err
	}
	message, err := s.verifyEvidence(ctx, project, evidence, now)
	if err != nil {
		return domain.LadoRelay{}, err
	}
	reasons, err := s.outsidePlan(ctx, worker, instruction, req.OutsidePlan)
	if err != nil {
		return domain.LadoRelay{}, err
	}
	return domain.LadoRelay{
		ID:                    domain.LadoRelayID("lado-relay-" + s.deps.NewID()),
		TaskID:                task.ID,
		ProjectID:             project,
		OrchestratorSessionID: req.SessionID,
		WorkerSessionID:       worker.SessionID,
		WorkerRole:            worker.Role,
		PartID:                worker.PartID,
		Instruction:           instruction,
		Evidence:              evidence,
		EvidenceMessageID:     message.ID,
		EvidenceAt:            message.CreatedAt.UTC(),
		BeyondEvidence:        beyondEvidence(instruction, message.Text),
		OutsidePlan:           len(reasons) > 0,
		OutsidePlanReasons:    reasons,
		State:                 domain.LadoRelaySending,
		CreatedAt:             now,
	}, nil
}

// validate trims the instruction and the evidence and checks their lengths.
func validate(req Request) (instruction, evidence string, err error) {
	instruction = strings.TrimSpace(req.Message)
	evidence = strings.TrimSpace(req.Evidence)
	switch {
	case strings.TrimSpace(string(req.SessionID)) == "":
		return "", "", invalid("sessionId", "The orchestrator session is required (AO_SESSION_ID).")
	case strings.TrimSpace(req.Task) == "":
		return "", "", invalid("task", "The task is required: a task id or an issue number.")
	case instruction == "":
		return "", "", invalid("message", "The instruction is required.")
	case domain.LadoRelayTextLen(instruction) > domain.LadoRelayMaxInstructionChars:
		return "", "", invalid("message", fmt.Sprintf("The instruction is longer than %d characters.", domain.LadoRelayMaxInstructionChars))
	}
	switch n := domain.LadoRelayTextLen(evidence); {
	case n < domain.LadoRelayMinEvidenceChars:
		return "", "", invalid("evidence", fmt.Sprintf(
			"The evidence must quote the developer's message, at least %d characters.", domain.LadoRelayMinEvidenceChars))
	case n > domain.LadoRelayMaxEvidenceChars:
		return "", "", invalid("evidence", fmt.Sprintf(
			"The evidence is longer than %d characters; quote the sentence that gives the instruction.", domain.LadoRelayMaxEvidenceChars))
	}
	return instruction, evidence, nil
}

func invalid(field, message string) error {
	return apierr.Invalid(domain.LadoRelayInvalid, message, map[string]any{"field": field})
}

// orchestratorProject checks that the caller is a live Chat orchestrator of
// a project in LADO mode and returns that project.
func (s *Service) orchestratorProject(ctx context.Context, id domain.SessionID) (domain.ProjectID, error) {
	rec, ok, err := s.deps.Store.GetSession(ctx, id)
	if err != nil {
		return "", fmt.Errorf("read session %s: %w", id, err)
	}
	if !ok || rec.Kind != domain.KindOrchestrator || rec.ProjectID == "" ||
		rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
		return "", apierr.New(apierr.KindForbidden, domain.LadoRelayNotOrchestrator,
			"Only a live orchestrator of a LADO project can ask LADO to relay an instruction.",
			map[string]any{"sessionId": string(id)})
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return "", evidenceUnverified(domain.LadoRelayEvidenceTerminalOrchestrator,
			"LADO can verify the developer's instruction only in a Chat orchestrator's conversation; "+
				"a terminal orchestrator's prompts include what AO and workers paste. "+
				"Ask the developer to paste the instruction into the worker's session.")
	}
	settings, err := s.deps.Store.LadoProjectSettings(ctx, rec.ProjectID)
	if err != nil {
		return "", fmt.Errorf("read LADO settings of project %s: %w", rec.ProjectID, err)
	}
	if !settings.LadoMode {
		return "", apierr.Conflict(domain.LadoRelayNotLadoProject,
			"This project is not in LADO mode: message the worker directly.",
			map[string]any{"projectId": string(rec.ProjectID)})
	}
	return rec.ProjectID, nil
}

// deliver records r as sending, delivers it and records the outcome.
func (s *Service) deliver(ctx context.Context, r domain.LadoRelay) (domain.LadoRelay, error) {
	if err := s.deps.Store.InsertLadoRelay(ctx, r); err != nil {
		return domain.LadoRelay{}, fmt.Errorf("record the relay: %w", err)
	}
	// The outcome is recorded even when the caller gives up meanwhile.
	record := context.WithoutCancel(ctx)
	if s.deps.Messenger == nil {
		return s.fail(record, r, "LADO has no messenger")
	}
	if err := s.deps.Messenger.Relay(ctx, r.WorkerSessionID, domain.LadoRelayMessageText(r)); err != nil {
		return s.fail(record, r, err.Error())
	}
	at := s.deps.Clock().UTC()
	if _, err := s.deps.Store.MarkLadoRelayDelivered(record, r.ID, at); err != nil {
		// The worker has the message; the row stays sending.
		s.deps.Log.Warn("LADO could not mark a relay delivered", "relay", r.ID, "err", err)
	} else {
		r.State, r.DeliveredAt = domain.LadoRelayDelivered, at
	}
	s.audit(record, at, domain.LadoRelayAuditSent, "relay", string(r.ID), auditRelay(r))
	return r, nil
}

// fail records r as failed for failure and returns LadoRelayDeliveryFailed.
func (s *Service) fail(ctx context.Context, r domain.LadoRelay, failure string) (domain.LadoRelay, error) {
	if _, err := s.deps.Store.MarkLadoRelayFailed(ctx, r.ID, failure); err != nil {
		s.deps.Log.Warn("LADO could not mark a relay failed", "relay", r.ID, "err", err)
	}
	r.State, r.Failure = domain.LadoRelayFailed, failure
	s.audit(ctx, s.deps.Clock().UTC(), domain.LadoRelayAuditSent, "relay", string(r.ID), auditRelay(r))
	return domain.LadoRelay{}, apierr.Conflict(domain.LadoRelayDeliveryFailed,
		"LADO could not deliver the instruction to the worker: "+failure,
		map[string]any{"relayId": string(r.ID), "workerSessionId": string(r.WorkerSessionID), "reason": failure})
}

func auditRelay(r domain.LadoRelay) map[string]any {
	return map[string]any{
		"taskId": string(r.TaskID), "projectId": string(r.ProjectID),
		"orchestratorSessionId": string(r.OrchestratorSessionID), "workerSessionId": string(r.WorkerSessionID),
		"workerRole": r.WorkerRole, "partId": r.PartID, "evidenceMessageId": r.EvidenceMessageID,
		"beyondEvidence": r.BeyondEvidence, "outsidePlan": r.OutsidePlan, "outsidePlanReasons": r.OutsidePlanReasons,
		"state": string(r.State), "failure": r.Failure,
	}
}

// audit appends an audit event; a failure is logged, not returned, so the
// audit never changes a relay's outcome.
func (s *Service) audit(ctx context.Context, at time.Time, action, kind, subject string, payload map[string]any) {
	data, err := json.Marshal(payload)
	if err != nil {
		s.deps.Log.Warn("LADO could not encode a relay audit event", "action", action, "err", err)
		return
	}
	if _, err := s.deps.Store.AppendLadoAuditEvent(ctx, domain.LadoAuditEvent{
		At: at, Actor: "lado", Action: action, SubjectKind: kind, SubjectID: subject, Payload: data,
	}); err != nil {
		s.deps.Log.Warn("LADO could not audit a relay", "action", action, "subject", subject, "err", err)
	}
}

// asRefusal is the API error of a refused relay, false for a read failure.
func asRefusal(err error) (*apierr.Error, bool) {
	var e *apierr.Error
	if !errors.As(err, &e) || e == nil || !strings.HasPrefix(e.Code, "LADO_RELAY_") {
		return nil, false
	}
	return e, true
}
