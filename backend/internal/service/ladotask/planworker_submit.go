package ladotask

// What plan workers submit (docs/LJY-PRD/TDD.md §5.1): the designer's draft
// and final plan, and the critic's findings. Plan workers write no files; the
// daemon keeps a draft in LADO's own folder and writes the final plan into
// the designer's artifact directory, where AO's artifact capture records it
// and the plan loop pins it.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// SubmitPlanInput is a designer's submission.
type SubmitPlanInput struct {
	RequestID domain.LadoPlanRequestID
	// SessionID is the session that submits, which must be the request's
	// designer.
	SessionID domain.SessionID
	Content   []byte
	// Draft marks the draft LADO reviews; without it the plan is final.
	Draft bool
}

// SubmitPlan records a designer's draft or final plan. The draft goes to the
// review; a final plan is written into the designer's artifact directory,
// and the last one submitted is the one LADO pins.
func (s *Service) SubmitPlan(ctx context.Context, in SubmitPlanInput) (domain.LadoPlanRequest, error) {
	req, err := s.workerRequest(ctx, in.RequestID)
	if err != nil {
		return domain.LadoPlanRequest{}, err
	}
	if in.SessionID == "" || in.SessionID != req.WorkerSession() {
		return domain.LadoPlanRequest{}, submitRefused("Only the plan worker of this request can submit its plan.", req)
	}
	content := bytes.TrimPrefix(in.Content, []byte("\ufeff"))
	switch {
	case len(bytes.TrimSpace(content)) == 0:
		return domain.LadoPlanRequest{}, apierr.Invalid("LADO_PLAN_EMPTY", "Send the plan file; it is empty.", nil)
	case len(content) > domain.LadoPlanMaxBytes:
		return domain.LadoPlanRequest{}, apierr.Invalid("LADO_PLAN_TOO_LARGE", fmt.Sprintf("Keep the plan within %d bytes.", domain.LadoPlanMaxBytes), map[string]any{"max": domain.LadoPlanMaxBytes})
	case !utf8.Valid(content):
		return domain.LadoPlanRequest{}, apierr.Invalid("LADO_PLAN_NOT_TEXT", "Send the plan as UTF-8 text.", nil)
	}
	sum, err := s.deps.Blobs.Put(content)
	if err != nil {
		s.deps.Log.Error("LADO could not keep a submitted plan", "request", req.ID, "err", err)
		return domain.LadoPlanRequest{}, apierr.Internal("LADO_PLAN_SUBMIT_FAILED", "Failed to save the plan")
	}
	now := s.deps.Clock().UTC()
	if in.Draft {
		if req.WorkerPhase() != domain.LadoPlanPhaseDesigning {
			return domain.LadoPlanRequest{}, apierr.Conflict("LADO_PLAN_DRAFT_REFUSED",
				"LADO already has this request's draft. Wait for its review, then submit the final plan without --draft.", map[string]any{"requestId": string(req.ID)})
		}
		if err := s.checkDraft(ctx, req, content); err != nil {
			return domain.LadoPlanRequest{}, err
		}
		if _, err := s.writeRequestFile(req.ID, "draft.md", content); err != nil {
			s.deps.Log.Warn("LADO could not keep a copy of a draft in the request's folder", "request", req.ID, "err", err)
		}
		ok, err := s.deps.Store.RecordLadoPlanDraft(ctx, req.ID, sum, now, s.planEvent(now, "plan_worker.draft_submitted", req, map[string]any{"sha256": sum}))
		if err != nil {
			return domain.LadoPlanRequest{}, apierr.Internal("LADO_PLAN_SUBMIT_FAILED", "Failed to save the draft")
		}
		if !ok {
			return domain.LadoPlanRequest{}, submitRefused("This plan request no longer takes a draft.", req)
		}
		return s.afterSubmit(ctx, req.ID)
	}
	switch req.WorkerPhase() {
	case domain.LadoPlanPhaseRevising, domain.LadoPlanPhaseSubmitted, domain.LadoPlanPhaseFixing:
	case domain.LadoPlanPhaseDesigning:
		return domain.LadoPlanRequest{}, apierr.Conflict("LADO_PLAN_DRAFT_FIRST",
			"Submit a draft first with --draft. LADO reviews it and messages you, then you submit the final plan.", map[string]any{"requestId": string(req.ID)})
	default:
		return domain.LadoPlanRequest{}, apierr.Conflict("LADO_PLAN_REVIEW_PENDING",
			"LADO is still reviewing the draft. Wait for its message, then submit the final plan.", map[string]any{"requestId": string(req.ID)})
	}
	rec, found, err := s.deps.PlanSessions.Session(ctx, req.WorkerSession())
	if err != nil || !found {
		return domain.LadoPlanRequest{}, apierr.Internal("LADO_PLAN_SUBMIT_FAILED", "LADO could not find the plan worker's session")
	}
	target := filepath.Join(s.artifactDir(rec), filepath.FromSlash(domain.LadoPlanPath(req.TaskID)))
	err = os.MkdirAll(filepath.Dir(target), 0o750)
	if err == nil {
		err = writeFileAtomic(target, content)
	}
	if err != nil {
		s.deps.Log.Error("LADO could not write a submitted plan", "request", req.ID, "err", err)
		return domain.LadoPlanRequest{}, apierr.Internal("LADO_PLAN_SUBMIT_FAILED", "Failed to write the plan file")
	}
	ok, err := s.deps.Store.RecordLadoPlanFinal(ctx, req.ID, sum, now, s.planEvent(now, "plan_worker.submitted", req, map[string]any{"sha256": sum}))
	if err != nil {
		return domain.LadoPlanRequest{}, apierr.Internal("LADO_PLAN_SUBMIT_FAILED", "Failed to save the plan")
	}
	if !ok {
		return domain.LadoPlanRequest{}, submitRefused("This plan request no longer takes a plan.", req)
	}
	return s.afterSubmit(ctx, req.ID)
}

// checkDraft refuses a draft LADO cannot read: its front matter does not
// parse, or a field the review decision and the critic need is missing or
// wrong. The designer gets the problems at once and submits the draft again;
// an unreadable draft would otherwise look like a small plan.
func (s *Service) checkDraft(ctx context.Context, req domain.LadoPlanRequest, content []byte) error {
	task, err := s.task(ctx, req.TaskID)
	if err != nil {
		return err
	}
	problems := draftProblems(content, task)
	if len(problems) == 0 {
		return nil
	}
	lines := make([]string, 0, len(problems))
	for _, p := range problems {
		lines = append(lines, strings.TrimSpace(strings.Join([]string{p.Code, p.Field, p.Detail}, " ")))
	}
	return apierr.Invalid("LADO_PLAN_DRAFT_INVALID",
		"LADO cannot read this draft: "+strings.Join(lines, "; ")+". Fix the front matter, quoting any list item that holds a colon followed by a space, and submit the draft again.",
		map[string]any{"requestId": string(req.ID), "problems": problems})
}

// draftProblems lists the problems that make a draft unreadable for the
// review: a front matter that is missing or does not parse, a missing or
// invalid field, another task or issue. A plan source or a base LADO checks
// once the plan is pinned.
func draftProblems(content []byte, task domain.LadoTask) []domain.LadoPlanProblem {
	_, problems := domain.ParseLadoPlan(content, domain.LadoPlanExpectations{Task: task.ID, Issue: task.IssueRef})
	return slices.DeleteFunc(problems, func(p domain.LadoPlanProblem) bool {
		switch p.Code {
		case domain.LadoPlanMissingFrontMatter, domain.LadoPlanInvalidFrontMatter, domain.LadoPlanMissingField,
			domain.LadoPlanInvalidField, domain.LadoPlanWrongTask, domain.LadoPlanWrongIssue:
			return false
		}
		return true
	})
}

// SubmitCritiqueInput is a critic's submission.
type SubmitCritiqueInput struct {
	RequestID domain.LadoPlanRequestID
	// SessionID is the session that submits, which must be the request's
	// running critic.
	SessionID domain.SessionID
	Findings  []domain.LadoPlanFinding
}

// SubmitCritique records a critic's findings, once.
func (s *Service) SubmitCritique(ctx context.Context, in SubmitCritiqueInput) (domain.LadoPlanCritique, error) {
	req, err := s.workerRequest(ctx, in.RequestID)
	if err != nil {
		return domain.LadoPlanCritique{}, err
	}
	critiques, err := s.deps.Store.ListLadoPlanCritiques(ctx, req.ID)
	if err != nil {
		return domain.LadoPlanCritique{}, apierr.Internal("LADO_PLANS_LOAD_FAILED", "Failed to load the plan request")
	}
	var running domain.LadoPlanCritique
	for _, c := range critiques {
		if c.State == domain.LadoCritiqueRunning && c.SessionID != "" && c.SessionID == in.SessionID {
			running = c
		}
	}
	if running.ID == "" {
		return domain.LadoPlanCritique{}, submitRefused("Only this request's running critic can submit findings.", req)
	}
	if err := domain.ValidateLadoPlanFindings(in.Findings); err != nil {
		return domain.LadoPlanCritique{}, apierr.Invalid("LADO_PLAN_CRITIQUE_INVALID", err.Error(), nil)
	}
	now := s.deps.Clock().UTC()
	critique := domain.LadoPlanCritique{Findings: in.Findings}
	p1, major, minor := critique.Counts()
	ok, err := s.deps.Store.FinishLadoPlanCritique(ctx, req.ID, running.Attempt, domain.LadoCritiqueDone, in.Findings, "", now,
		s.planEvent(now, "plan_worker.critique_submitted", req, map[string]any{"attempt": running.Attempt, "p1": p1, "major": major, "minor": minor}))
	if err != nil {
		return domain.LadoPlanCritique{}, apierr.Internal("LADO_PLAN_SUBMIT_FAILED", "Failed to save the findings")
	}
	if !ok {
		return domain.LadoPlanCritique{}, submitRefused("This review no longer takes findings.", req)
	}
	s.kickPlans()
	stored, _, err := s.deps.Store.LadoPlanCritique(ctx, req.ID, running.Attempt)
	if err != nil {
		return domain.LadoPlanCritique{}, apierr.Internal("LADO_PLANS_LOAD_FAILED", "Failed to load the findings")
	}
	return stored, nil
}

// workerRequest returns an open plan worker request.
func (s *Service) workerRequest(ctx context.Context, id domain.LadoPlanRequestID) (domain.LadoPlanRequest, error) {
	req, ok, err := s.deps.Store.LadoPlanRequest(ctx, id)
	switch {
	case err != nil:
		return domain.LadoPlanRequest{}, apierr.Internal("LADO_PLANS_LOAD_FAILED", "Failed to load the plan request")
	case !ok || !req.IsWorker():
		return domain.LadoPlanRequest{}, apierr.NotFound("LADO_PLAN_REQUEST_NOT_FOUND", "Unknown plan request")
	case !req.Open():
		return domain.LadoPlanRequest{}, submitRefused("This plan request is closed: it finished, failed or a newer request replaced it.", req)
	}
	return req, nil
}

func (s *Service) afterSubmit(ctx context.Context, id domain.LadoPlanRequestID) (domain.LadoPlanRequest, error) {
	s.kickPlans()
	req, _, err := s.deps.Store.LadoPlanRequest(ctx, id)
	if err != nil {
		return domain.LadoPlanRequest{}, apierr.Internal("LADO_PLANS_LOAD_FAILED", "Failed to load the plan request")
	}
	return req, nil
}

func submitRefused(message string, req domain.LadoPlanRequest) error {
	return apierr.Conflict("LADO_PLAN_SUBMIT_REFUSED", message, map[string]any{"requestId": string(req.ID)})
}

// errLadoPlanWorkersOff means the daemon cannot start plan workers.
var errLadoPlanWorkersOff = errors.New("plan workers are not wired")
