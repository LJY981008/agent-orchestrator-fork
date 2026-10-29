package ladotask

// Plan workers (docs/LJY-PRD/TDD.md §5.1, docs/LJY-PRD/adr/0007-plan-workers.md):
// each plan request gets a read-only designer session of its own. The plan
// loop starts designers within the Runs settings' cap, reviews each draft
// with a read-only critic session unless the plan is small, sends the
// designer what came of the review, waits for the final plan the daemon
// writes into the designer's artifact directory, pins it like any plan file
// and stops the sessions. Every step works from stored facts, so a restart
// picks up where it stopped.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

const (
	// planWorkerAttempts is how often LADO tries to start a request's
	// designer.
	planWorkerAttempts = 2
	// planIdleGrace is how long a plan worker's turn must have ended before
	// LADO reminds it to submit, or gives up after the reminder.
	planIdleGrace = 2 * time.Minute
	// planPinWait is how long LADO waits for AO to capture a submitted plan
	// before it gives up the request.
	planPinWait = 10 * time.Minute
	// maxDisplayNameRunes is the longest display name AO takes.
	maxDisplayNameRunes = 100
)

// PlanSessions starts, reads, messages and stops plan worker sessions. The
// daemon implements it with AO's session service and session manager.
type PlanSessions interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error)
	Session(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	// SessionByClientRequest finds the session a spawn made for a client
	// request id, also after a spawn that failed once AO had started it.
	SessionByClientRequest(ctx context.Context, clientRequestID string) (domain.SessionRecord, bool, error)
	// Send delivers a message from the daemon.
	Send(ctx context.Context, id domain.SessionID, message string) error
	Stop(ctx context.Context, id domain.SessionID) error
}

// PlanQueue is what plan workers use of the dispatcher: the branch a task's
// worker starts from, which a plan names as its base, and memory pressure.
type PlanQueue interface {
	WorkerBase(ctx context.Context, task domain.LadoTask) (ports.WorkspaceDefaultBranch, error)
	MemoryTight(ctx context.Context) bool
}

// DrivePlanWorkers takes one step for every plan worker request that is
// still open, oldest first: it starts waiting designers while the cap and
// memory allow, and moves each running request on.
func (s *Service) DrivePlanWorkers(ctx context.Context) {
	if s.deps.PlanSessions == nil {
		return
	}
	requests, err := s.deps.Store.ListOpenLadoPlanWorkerRequests(ctx)
	if err != nil {
		s.deps.Log.Warn("LADO could not list its plan worker requests", "err", err)
		return
	}
	settings, err := s.deps.Store.LadoDispatchSettings(ctx)
	if err != nil {
		s.deps.Log.Warn("LADO could not read the Runs settings; plan workers wait", "err", err)
		return
	}
	running := 0
	for _, req := range requests {
		if !req.WorkerStartedAt.IsZero() {
			running++
		}
	}
	newest := map[domain.LadoTaskID]domain.LadoPlanRequestID{}
	for _, req := range requests {
		newest[req.TaskID] = req.ID
	}
	for _, req := range requests {
		if ctx.Err() != nil {
			return
		}
		task, ok, err := s.deps.Store.LadoTask(ctx, req.TaskID)
		if err != nil || !ok {
			continue
		}
		now := s.deps.Clock().UTC()
		switch {
		case !task.CancelledAt.IsZero():
			s.cleanUp(ctx, req, "the task was cancelled")
		case newest[req.TaskID] != req.ID:
			// RequestPlan replaces older requests; a crash between its
			// writes leaves them to this pass.
			s.failWorker(ctx, req, domain.LadoPlanRequestReplaced, "a newer plan request replaced it")
		case req.WorkerStartedAt.IsZero():
			if s.admitDesigner(ctx, task, req, settings, running) {
				running++
			}
		default:
			s.advance(ctx, task, req, settings, now)
			if s.closed(ctx, req.ID) {
				// Its slot is free for the requests after it.
				running--
			}
		}
	}
}

// closed reports whether a request failed or was cleaned up.
func (s *Service) closed(ctx context.Context, id domain.LadoPlanRequestID) bool {
	req, ok, err := s.deps.Store.LadoPlanRequest(ctx, id)
	return err == nil && ok && !req.Open()
}

// admitDesigner starts a waiting request's designer when a slot is free and
// memory allows, and records why it waits otherwise. It reports whether the
// designer started.
func (s *Service) admitDesigner(ctx context.Context, task domain.LadoTask, req domain.LadoPlanRequest, settings domain.LadoDispatchSettings, running int) bool {
	waiting := ""
	switch {
	case running >= max(settings.MaxPlanWorkers, 1):
		waiting = domain.LadoPlanWaitingCapacity
	case settings.HoldOnMemoryTight && s.deps.Queue != nil && s.deps.Queue.MemoryTight(ctx):
		waiting = domain.LadoPlanWaitingMemory
	}
	if waiting != "" {
		if req.WaitingReason != waiting {
			if err := s.deps.Store.SetLadoPlanRequestWaiting(ctx, req.ID, waiting); err != nil {
				s.deps.Log.Warn("LADO could not record why a plan request waits", "request", req.ID, "err", err)
			}
		}
		return false
	}
	return s.startDesigner(ctx, task, req)
}

// planAgent is the agent and model a plan worker runs with: the project's
// orchestrator's (docs/LJY-PRD/TDD.md §5.1), in its configured agent, or the
// agent of the project's newest live orchestrator.
func (s *Service) planAgent(ctx context.Context, task domain.LadoTask) (domain.AgentHarness, ports.AgentConfig, error) {
	project, err := s.project(ctx, task.ProjectID)
	if err != nil {
		return "", ports.AgentConfig{}, err
	}
	cfg := project.Config
	harness := cmp.Or(cfg.Orchestrator.Harness, cfg.Worker.Harness)
	merged := cfg.AgentConfig
	role := cfg.Orchestrator
	if role.Harness == "" || role.Harness == harness {
		merged.Model = cmp.Or(role.AgentConfig.Model, merged.Model)
		merged.Effort = cmp.Or(role.AgentConfig.Effort, merged.Effort)
	}
	if harness == "" {
		return "", ports.AgentConfig{}, errors.New("the project names no orchestrator agent")
	}
	return harness, ports.AgentConfig{Model: merged.Model, Effort: merged.Effort, Permissions: domain.PermissionModeAuto}, nil
}

// planWorkerAgentSupported reports whether LADO keeps plan workers of the
// agent read-only: Claude Code through its tool lists and AO's hook
// decision, Codex through its read-only sandbox.
func planWorkerAgentSupported(harness domain.AgentHarness) bool {
	return harness == domain.HarnessClaudeCode || harness == domain.HarnessCodex
}

// startDesigner spawns a request's designer and records it. A request whose
// agent LADO cannot keep read-only fails, and so does one whose designer did
// not start on its second try. It reports whether the designer started.
func (s *Service) startDesigner(ctx context.Context, task domain.LadoTask, req domain.LadoPlanRequest) bool {
	harness, agent, err := s.planAgent(ctx, task)
	switch {
	case err != nil:
		s.failWorker(ctx, req, domain.LadoPlanWorkerAgentUnsupported, err.Error())
		return false
	case !planWorkerAgentSupported(harness):
		s.failWorker(ctx, req, domain.LadoPlanWorkerAgentUnsupported, fmt.Sprintf("LADO cannot keep %s read-only as a plan worker", harness))
		return false
	}
	attempt, ok, err := s.deps.Store.BeginLadoPlanWorkerAttempt(ctx, req.ID)
	if err != nil || !ok {
		return false
	}
	if attempt > planWorkerAttempts {
		s.failWorker(ctx, req, domain.LadoPlanWorkerSpawnFailed, cmp.Or(req.DeliveryError, "the plan worker did not start"))
		return false
	}
	prompt, issueContext, err := s.designerPrompt(ctx, task, req)
	if err != nil {
		s.deps.Log.Warn("LADO could not prepare a plan worker's prompt", "request", req.ID, "err", err)
		s.recordSpawnProblem(ctx, req, attempt, err)
		return false
	}
	id := domain.LadoPlanWorkerClientRequestID(req.ID, domain.LadoPlanRoleDesigner, attempt)
	session, err := s.spawnPlanWorker(ctx, ports.SpawnConfig{
		ProjectID:         task.ProjectID,
		IssueID:           task.IssueRef,
		IssueContext:      issueContext,
		ClientRequestID:   id,
		ClientRequestHash: domain.LadoPlanWorkerHash(id),
		Kind:              domain.KindWorker,
		Harness:           harness,
		Prompt:            prompt,
		AgentConfig:       agent,
		RequestedMode:     domain.SessionModeChat,
		DisplayName:       planWorkerName("계획", task),
	})
	if err != nil {
		s.deps.Log.Warn("LADO could not start a plan worker", "request", req.ID, "attempt", attempt, "err", err)
		s.recordSpawnProblem(ctx, req, attempt, err)
		return false
	}
	now := s.deps.Clock().UTC()
	started, err := s.deps.Store.StartLadoPlanWorker(ctx, req.ID, session, now, s.planEvent(now, "plan_worker.started", req, map[string]any{
		"session": session, "attempt": attempt, "agent": harness, "model": agent.Model,
	}))
	if err != nil || !started {
		// The request moved on meanwhile, as when a newer one replaced it.
		s.stopSession(ctx, session)
		return false
	}
	s.deps.Log.Info("LADO started a plan worker", "request", req.ID, "task", task.ID, "session", session)
	return true
}

// recordSpawnProblem keeps why a spawn failed; the second failure fails the
// request.
func (s *Service) recordSpawnProblem(ctx context.Context, req domain.LadoPlanRequest, attempt int, err error) {
	if attempt >= planWorkerAttempts {
		s.failWorker(ctx, req, domain.LadoPlanWorkerSpawnFailed, errorMessage(err))
		return
	}
	if err := s.deps.Store.RecordLadoPlanWorkerProblem(ctx, req.ID, errorMessage(err)); err != nil {
		s.deps.Log.Debug("LADO could not record a plan worker spawn problem", "request", req.ID, "err", err)
	}
}

// spawnPlanWorker spawns a plan worker session. AO can fail a spawn after it
// started the session, as when the reply is lost; the session that holds the
// client request id is then the worker.
func (s *Service) spawnPlanWorker(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionID, error) {
	session, _, _, err := s.deps.PlanSessions.Spawn(ctx, cfg)
	if err == nil {
		return session.ID, nil
	}
	rec, found, lookupErr := s.deps.PlanSessions.SessionByClientRequest(context.WithoutCancel(ctx), cfg.ClientRequestID)
	if lookupErr != nil || !found || !rec.ClientRequestCommitted || rec.ClientRequestHash != cfg.ClientRequestHash {
		return "", err
	}
	return rec.ID, nil
}

// planWorkerName is a plan worker's display name: what it does and the issue.
func planWorkerName(what string, task domain.LadoTask) string {
	name := what
	if id, err := domain.ParseLadoIssueRef(task.IssueRef); err == nil {
		if n := domain.LadoIssueNumber(id); n > 0 {
			name = fmt.Sprintf("%s #%d", what, n)
		}
	}
	if title := strings.Join(strings.Fields(task.Issue.Title), " "); title != "" {
		name += " " + title
	}
	if utf8.RuneCountInString(name) > maxDisplayNameRunes {
		name = string([]rune(name)[:maxDisplayNameRunes])
	}
	return name
}

// planSession is what a plan worker step needs of a session.
type planSession struct {
	record domain.SessionRecord
	found  bool
}

// ended reports whether the session can take no more turns.
func (p planSession) ended() bool {
	return !p.found || p.record.IsTerminated || p.record.Activity.State == domain.ActivityExited
}

// idleSince reports whether the session's turn ended after at and has been
// over for planIdleGrace by now: it is idle or waits at an empty prompt.
func (p planSession) idleSince(at, now time.Time) bool {
	if !p.found {
		return false
	}
	state := p.record.Activity.State
	last := p.record.Activity.LastActivityAt
	return (state == domain.ActivityIdle || state == domain.ActivityWaitingInput) && last.After(at) && now.Sub(last) >= planIdleGrace
}

func (s *Service) planSession(ctx context.Context, id domain.SessionID) planSession {
	if id == "" {
		return planSession{}
	}
	rec, found, err := s.deps.PlanSessions.Session(ctx, id)
	if err != nil {
		// A read that failed says nothing about the session: treat it as
		// live and busy.
		return planSession{record: domain.SessionRecord{ID: id, Activity: domain.Activity{State: domain.ActivityActive}}, found: true}
	}
	return planSession{record: rec, found: found}
}

// advance moves a running plan worker request on by one step.
func (s *Service) advance(ctx context.Context, task domain.LadoTask, req domain.LadoPlanRequest, settings domain.LadoDispatchSettings, now time.Time) {
	designer := s.planSession(ctx, req.WorkerSession())
	if now.Sub(req.WorkerStartedAt) > domain.LadoPlanRequestTimeout && req.FixRequestedAt.IsZero() {
		s.failWorker(ctx, req, domain.LadoPlanWorkerTimedOut, fmt.Sprintf("the plan request ran past %s", domain.LadoPlanRequestTimeout))
		return
	}
	switch req.WorkerPhase() {
	case domain.LadoPlanPhaseDesigning:
		switch {
		case designer.ended():
			s.failWorker(ctx, req, domain.LadoPlanWorkerNoPlan, "the plan worker ended without a draft")
		case now.Sub(req.WorkerStartedAt) > domain.LadoPlanDesignTimeout:
			s.failWorker(ctx, req, domain.LadoPlanWorkerTimedOut, fmt.Sprintf("no draft within %s", domain.LadoPlanDesignTimeout))
		default:
			s.remindIfIdle(ctx, req, designer, req.WorkerStartedAt, now, true)
		}
	case domain.LadoPlanPhaseCritiquing:
		if designer.ended() {
			s.failWorker(ctx, req, domain.LadoPlanWorkerNoPlan, "the plan worker ended during the review")
			return
		}
		s.driveCritique(ctx, task, req, settings, now)
	case domain.LadoPlanPhaseRevising:
		switch {
		case designer.ended():
			s.failWorker(ctx, req, domain.LadoPlanWorkerNoPlan, "the plan worker ended without the final plan")
		case now.Sub(req.FeedbackSentAt) > domain.LadoPlanReviseTimeout:
			s.failWorker(ctx, req, domain.LadoPlanWorkerTimedOut, fmt.Sprintf("no final plan within %s of the review", domain.LadoPlanReviseTimeout))
		default:
			s.remindIfIdle(ctx, req, designer, req.FeedbackSentAt, now, false)
		}
	case domain.LadoPlanPhaseSubmitted:
		rev, pinned := s.pinnedFinal(ctx, req, req.SubmittedAt)
		switch {
		case pinned && len(rev.Problems) > 0 && !designer.ended():
			s.requestFix(ctx, req, rev, now)
		case pinned:
			s.cleanUp(ctx, req, "")
		case now.Sub(req.SubmittedAt) > planPinWait:
			s.failWorker(ctx, req, domain.LadoPlanWorkerNoPlan, "AO did not record the submitted plan file")
		}
	case domain.LadoPlanPhaseFixing:
		_, fixed := s.pinnedFinal(ctx, req, req.FixRequestedAt)
		if fixed || designer.ended() || now.Sub(req.FixRequestedAt) > domain.LadoPlanFixWait {
			s.cleanUp(ctx, req, "")
		}
	}
}

// pinnedFinal returns the revision pinned from the plan the designer
// submitted last, when one was pinned after since.
func (s *Service) pinnedFinal(ctx context.Context, req domain.LadoPlanRequest, since time.Time) (domain.LadoPlanRevision, bool) {
	revisions, err := s.deps.Store.ListLadoPlanRevisions(ctx, req.TaskID)
	if err != nil {
		return domain.LadoPlanRevision{}, false
	}
	for i := len(revisions) - 1; i >= 0; i-- {
		rev := revisions[i]
		if rev.RequestID == req.ID && rev.SHA256 == req.FinalSHA256 && !rev.CreatedAt.Before(since) {
			return rev, true
		}
	}
	return domain.LadoPlanRevision{}, false
}

// remindIfIdle reminds a designer whose turn ended without the submission it
// owes, once per step; when its turn ends again without it, the request
// fails.
func (s *Service) remindIfIdle(ctx context.Context, req domain.LadoPlanRequest, designer planSession, since, now time.Time, draft bool) {
	if !designer.idleSince(since, now) {
		return
	}
	if req.RemindedAt.After(since) {
		if designer.idleSince(req.RemindedAt, now) {
			what := "final plan"
			if draft {
				what = "draft"
			}
			s.failWorker(ctx, req, domain.LadoPlanWorkerNoPlan, "the plan worker's turn ended twice without the "+what)
		}
		return
	}
	if err := s.deps.PlanSessions.Send(ctx, req.WorkerSession(), reminderMessage(req, draft)); err != nil {
		s.deps.Log.Debug("LADO could not remind a plan worker", "request", req.ID, "err", err)
		return
	}
	if err := s.deps.Store.RecordLadoPlanReminder(ctx, req.ID, now); err != nil {
		s.deps.Log.Warn("LADO could not record a plan worker reminder", "request", req.ID, "err", err)
	}
}

// driveCritique moves the review of a request's draft on: it decides whether
// to review, starts the critic, retries a critic that failed once, and sends
// the designer what came of it.
func (s *Service) driveCritique(ctx context.Context, task domain.LadoTask, req domain.LadoPlanRequest, settings domain.LadoDispatchSettings, now time.Time) {
	critiques, err := s.deps.Store.ListLadoPlanCritiques(ctx, req.ID)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a draft's reviews", "request", req.ID, "err", err)
		return
	}
	draft, err := s.deps.Blobs.Get(req.DraftSHA256, domain.LadoPlanMaxBytes)
	if err != nil {
		s.failWorker(ctx, req, domain.LadoPlanWorkerNoPlan, "LADO lost the draft: "+err.Error())
		return
	}
	if len(critiques) == 0 {
		plan, _ := domain.ParseLadoPlan(draft, domain.LadoPlanExpectations{Task: task.ID, Issue: task.IssueRef})
		decision := domain.DecideLadoPlanReview(plan, settings.PlanReview, ladoverify.MatchPath)
		// A draft LADO cannot read names no paths, so it would look small;
		// the critic reads it as text instead.
		if decision.Skip && len(draftProblems(draft, task)) == 0 {
			c := domain.LadoPlanCritique{ID: s.newCritiqueID(), RequestID: req.ID, State: domain.LadoCritiqueSkipped, SkipReason: decision.Reason, StartedAt: now, FinishedAt: now}
			if err := s.deps.Store.CreateLadoPlanCritique(ctx, c, s.planEvent(now, "plan_worker.critique_skipped", req, map[string]any{"reason": decision.Reason})); err != nil {
				s.deps.Log.Warn("LADO could not record a skipped review", "request", req.ID, "err", err)
				return
			}
			s.sendFeedback(ctx, req, skippedMessage(req, decision.Reason), now)
			return
		}
		s.beginCritique(ctx, task, req, 1, draft, now)
		return
	}
	latest := critiques[len(critiques)-1]
	switch latest.State {
	case domain.LadoCritiqueSkipped:
		s.sendFeedback(ctx, req, skippedMessage(req, latest.SkipReason), now)
	case domain.LadoCritiqueDone:
		s.stopSession(ctx, latest.SessionID)
		s.forgetCriticReminder(latest.ID)
		s.sendFeedback(ctx, req, findingsMessage(req, latest.Findings), now)
	case domain.LadoCritiqueFailed:
		s.stopSession(ctx, latest.SessionID)
		if latest.Attempt < domain.LadoPlanCritiqueAttempts {
			s.beginCritique(ctx, task, req, latest.Attempt+1, draft, now)
			return
		}
		s.sendFeedback(ctx, req, critiqueFailedMessage(req, latest.FailureReason), now)
	case domain.LadoCritiqueRunning:
		s.watchCritic(ctx, task, req, latest, draft, now)
	}
}

// beginCritique records a critic's attempt, then starts it.
func (s *Service) beginCritique(ctx context.Context, task domain.LadoTask, req domain.LadoPlanRequest, attempt int, draft []byte, now time.Time) {
	c := domain.LadoPlanCritique{ID: s.newCritiqueID(), RequestID: req.ID, Attempt: attempt, State: domain.LadoCritiqueRunning, StartedAt: now}
	if err := s.deps.Store.CreateLadoPlanCritique(ctx, c, s.planEvent(now, "plan_worker.critique_started", req, map[string]any{"attempt": attempt})); err != nil {
		s.deps.Log.Warn("LADO could not record a review", "request", req.ID, "err", err)
		return
	}
	s.startCritic(ctx, task, req, c, draft, now)
}

// startCritic spawns a running review's critic. One that does not start
// fails the attempt.
func (s *Service) startCritic(ctx context.Context, task domain.LadoTask, req domain.LadoPlanRequest, c domain.LadoPlanCritique, draft []byte, now time.Time) {
	harness, agent, err := s.planAgent(ctx, task)
	if err == nil && !planWorkerAgentSupported(harness) {
		err = fmt.Errorf("LADO cannot keep %s read-only", harness)
	}
	var session domain.SessionID
	if err == nil {
		id := domain.LadoPlanWorkerClientRequestID(req.ID, domain.LadoPlanRoleCritic, c.Attempt)
		session, err = s.spawnPlanWorker(ctx, ports.SpawnConfig{
			ProjectID:         task.ProjectID,
			ClientRequestID:   id,
			ClientRequestHash: domain.LadoPlanWorkerHash(id),
			Kind:              domain.KindWorker,
			Harness:           harness,
			Prompt:            criticPrompt(req, draft),
			AgentConfig:       agent,
			RequestedMode:     domain.SessionModeChat,
			DisplayName:       planWorkerName("검수", task),
		})
	}
	if err != nil {
		s.deps.Log.Warn("LADO could not start a critic", "request", req.ID, "attempt", c.Attempt, "err", err)
		s.failCritique(ctx, req, c, "the critic did not start: "+errorMessage(err), now)
		return
	}
	if ok, err := s.deps.Store.SetLadoPlanCritiqueSession(ctx, req.ID, c.Attempt, session, nil); err != nil || !ok {
		s.stopSession(ctx, session)
	}
}

// watchCritic fails a running critic that ended, ran past its time or whose
// turn ended twice without findings, and reminds it once.
func (s *Service) watchCritic(ctx context.Context, task domain.LadoTask, req domain.LadoPlanRequest, c domain.LadoPlanCritique, draft []byte, now time.Time) {
	if c.SessionID == "" {
		s.startCritic(ctx, task, req, c, draft, now)
		return
	}
	critic := s.planSession(ctx, c.SessionID)
	switch {
	case critic.ended():
		s.failCritique(ctx, req, c, "the critic ended without findings", now)
	case now.Sub(c.StartedAt) > domain.LadoPlanCritiqueTimeout:
		s.failCritique(ctx, req, c, fmt.Sprintf("no findings within %s", domain.LadoPlanCritiqueTimeout), now)
	case critic.idleSince(c.StartedAt, now):
		reminded := s.criticReminded(c.ID)
		switch {
		case reminded.IsZero():
			if err := s.deps.PlanSessions.Send(ctx, c.SessionID, criticReminder(req)); err == nil {
				s.rememberCriticReminder(c.ID, now)
			}
		case critic.idleSince(reminded, now):
			s.failCritique(ctx, req, c, "the critic's turn ended twice without findings", now)
		}
	}
}

func (s *Service) failCritique(ctx context.Context, req domain.LadoPlanRequest, c domain.LadoPlanCritique, reason string, now time.Time) {
	if _, err := s.deps.Store.FinishLadoPlanCritique(ctx, req.ID, c.Attempt, domain.LadoCritiqueFailed, nil, reason, now,
		s.planEvent(now, "plan_worker.critique_failed", req, map[string]any{"attempt": c.Attempt, "reason": reason})); err != nil {
		s.deps.Log.Warn("LADO could not record a failed review", "request", req.ID, "err", err)
	}
	s.stopSession(ctx, c.SessionID)
	s.forgetCriticReminder(c.ID)
}

// sendFeedback sends the designer what came of its draft's review and
// records it.
func (s *Service) sendFeedback(ctx context.Context, req domain.LadoPlanRequest, message string, now time.Time) {
	if err := s.deps.PlanSessions.Send(ctx, req.WorkerSession(), message); err != nil {
		s.deps.Log.Debug("LADO could not send a plan worker its review; it tries again", "request", req.ID, "err", err)
		return
	}
	if _, err := s.deps.Store.RecordLadoPlanFeedback(ctx, req.ID, now, nil); err != nil {
		s.deps.Log.Warn("LADO could not record a plan worker's review", "request", req.ID, "err", err)
	}
}

// requestFix sends a pinned revision's problems back once.
func (s *Service) requestFix(ctx context.Context, req domain.LadoPlanRequest, rev domain.LadoPlanRevision, now time.Time) {
	if err := s.deps.PlanSessions.Send(ctx, req.WorkerSession(), fixMessage(req, rev)); err != nil {
		s.deps.Log.Debug("LADO could not send a plan worker its plan's problems; it tries again", "request", req.ID, "err", err)
		return
	}
	if _, err := s.deps.Store.RecordLadoPlanFixRequested(ctx, req.ID, now); err != nil {
		s.deps.Log.Warn("LADO could not record a plan fix request", "request", req.ID, "err", err)
	}
}

// cleanUp stops a request's sessions and records it, with the final usage of
// its last revision. reason is why, when not because the plan was pinned.
func (s *Service) cleanUp(ctx context.Context, req domain.LadoPlanRequest, reason string) {
	s.stopRequestSessions(ctx, req)
	now := s.deps.Clock().UTC()
	payload := map[string]any{}
	if reason != "" {
		payload["reason"] = reason
	}
	if ok, err := s.deps.Store.CleanLadoPlanRequest(ctx, req.ID, now, s.planEvent(now, "plan_worker.cleaned", req, payload)); err != nil || !ok {
		return
	}
	s.refreshMetrics(ctx, req)
	s.deps.Log.Info("LADO stopped a plan request's workers", "request", req.ID, "task", req.TaskID)
}

// failWorker fails a request with code and stops its sessions.
func (s *Service) failWorker(ctx context.Context, req domain.LadoPlanRequest, code, detail string) {
	now := s.deps.Clock().UTC()
	ok, err := s.deps.Store.FailLadoPlanWorkerRequest(ctx, req.ID, now, code, detail, s.planEvent(now, "plan_worker.failed", req, map[string]any{"code": code, "detail": detail}))
	if err != nil {
		s.deps.Log.Warn("LADO could not record a failed plan request", "request", req.ID, "err", err)
		return
	}
	if ok {
		s.deps.Log.Info("LADO gave up a plan request", "request", req.ID, "task", req.TaskID, "code", code, "detail", detail)
	}
	s.stopRequestSessions(ctx, req)
}

// stopRequestSessions stops a request's designer and every critic of it.
func (s *Service) stopRequestSessions(ctx context.Context, req domain.LadoPlanRequest) {
	ctx = context.WithoutCancel(ctx)
	s.stopSession(ctx, req.WorkerSession())
	critiques, err := s.deps.Store.ListLadoPlanCritiques(ctx, req.ID)
	if err != nil {
		return
	}
	for _, c := range critiques {
		s.stopSession(ctx, c.SessionID)
		s.forgetCriticReminder(c.ID)
	}
}

// stopSession stops a plan worker session that still runs.
func (s *Service) stopSession(ctx context.Context, id domain.SessionID) {
	if id == "" || s.deps.PlanSessions == nil {
		return
	}
	if session := s.planSession(ctx, id); session.ended() {
		return
	}
	if err := s.deps.PlanSessions.Stop(context.WithoutCancel(ctx), id); err != nil {
		s.deps.Log.Warn("LADO could not stop a plan worker session", "session", id, "err", err)
	}
}

// planEvent is a plan worker audit event about req.
func (s *Service) planEvent(at time.Time, action string, req domain.LadoPlanRequest, payload map[string]any) *domain.LadoAuditEvent {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["task"] = req.TaskID
	data, _ := json.Marshal(payload)
	return &domain.LadoAuditEvent{At: at, Actor: "lado", Action: action, SubjectKind: "plan_request", SubjectID: string(req.ID), Payload: data}
}

func (s *Service) newCritiqueID() domain.LadoPlanCritiqueID {
	return domain.LadoPlanCritiqueID("lado-critique-" + s.deps.NewID())
}

// criticReminded returns when LADO reminded a critic, zero before. Critic
// reminders live only in memory: after a restart a critic is reminded again.
func (s *Service) criticReminded(id domain.LadoPlanCritiqueID) time.Time {
	s.criticMu.Lock()
	defer s.criticMu.Unlock()
	return s.criticReminders[id]
}

func (s *Service) rememberCriticReminder(id domain.LadoPlanCritiqueID, at time.Time) {
	s.criticMu.Lock()
	defer s.criticMu.Unlock()
	if s.criticReminders == nil {
		s.criticReminders = map[domain.LadoPlanCritiqueID]time.Time{}
	}
	s.criticReminders[id] = at
}

// forgetCriticReminder drops a review's reminder once it is no longer
// running, so the reminders do not pile up.
func (s *Service) forgetCriticReminder(id domain.LadoPlanCritiqueID) {
	s.criticMu.Lock()
	defer s.criticMu.Unlock()
	delete(s.criticReminders, id)
}

// requestDir is LADO's own folder for a request's files: the issue and the
// revision a change starts from, which no artifact capture reads.
func (s *Service) requestDir(id domain.LadoPlanRequestID) (string, error) {
	if s.deps.DataDir == "" {
		return "", errors.New("LADO has no data directory for plan requests")
	}
	return filepath.Join(s.deps.DataDir, "lado", "plan-requests", string(id)), nil
}

// writeRequestFile writes one of a request's files and returns its path.
func (s *Service) writeRequestFile(id domain.LadoPlanRequestID, name string, content []byte) (string, error) {
	dir, err := s.requestDir(id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	target := filepath.Join(dir, name)
	if err := writeFileAtomic(target, content); err != nil {
		return "", err
	}
	return target, nil
}

// writeFileAtomic writes content to target through a temporary file.
func writeFileAtomic(target string, content []byte) error {
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// artifactDir returns a session's artifact directory.
func (s *Service) artifactDir(rec domain.SessionRecord) string {
	return cmp.Or(rec.Metadata.ArtifactDir, sessionartifacts.Dir(s.deps.DataDir, rec.ID))
}

// openWorkerRequests returns the task's plan worker requests that are still
// open, but keep.
func (s *Service) openWorkerRequests(ctx context.Context, taskID domain.LadoTaskID, keep domain.LadoPlanRequestID) []domain.LadoPlanRequest {
	requests, err := s.deps.Store.ListLadoPlanRequests(ctx, taskID)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(requests, func(r domain.LadoPlanRequest) bool { return r.ID == keep || !r.Open() })
}
