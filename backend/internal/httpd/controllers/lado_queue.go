package controllers

// LADO fork: the queue, execution and dispatch settings routes
// (docs/LJY-PRD/TDD.md §5.3 and §13).

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoqueue"
)

// LadoQueueService is the controller-facing dispatcher contract.
type LadoQueueService interface {
	Queue(ctx context.Context, projectID *domain.ProjectID) ([]domain.LadoQueueItem, error)
	Execution(ctx context.Context, sessionID domain.SessionID) (ladoqueue.ExecutionView, error)
	Settings(ctx context.Context) (domain.LadoDispatchSettings, error)
	SetSettings(ctx context.Context, settings domain.LadoDispatchSettings) (domain.LadoDispatchSettings, error)
}

const (
	ladoQueuePath            = "/api/v1/lado/queue"
	ladoExecutionPath        = "/api/v1/lado/executions/{sessionId}"
	ladoDispatchSettingsPath = "/api/v1/lado/dispatch-settings"
)

func (c *LadoController) listQueue(w http.ResponseWriter, r *http.Request) {
	if c.Queue == nil {
		apispec.NotImplemented(w, r, "GET", ladoQueuePath)
		return
	}
	var projectID *domain.ProjectID
	if raw := r.URL.Query().Get("projectId"); raw != "" {
		id := domain.ProjectID(raw)
		projectID = &id
	}
	items, err := c.Queue.Queue(r.Context(), projectID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	now := time.Now()
	resp := LadoQueueResponse{Entries: make([]LadoQueueItemResponse, 0, len(items))}
	for _, item := range items {
		entry := item.Entry
		state := entry.State(now)
		resp.Entries = append(resp.Entries, LadoQueueItemResponse{
			ID:            string(entry.ID),
			TaskID:        string(entry.TaskID),
			ProjectID:     string(entry.ProjectID),
			IssueRef:      string(item.IssueRef),
			Title:         item.Title,
			State:         string(state),
			Reason:        ladoQueueReason(entry, state),
			Attempts:      entry.AttemptCount,
			NextAttemptAt: ladoNextAttempt(entry, now),
			Error:         entry.ErrorMessage,
			EnqueuedAt:    entry.EnqueuedAt,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) getExecution(w http.ResponseWriter, r *http.Request) {
	if c.Queue == nil {
		apispec.NotImplemented(w, r, "GET", ladoExecutionPath)
		return
	}
	view, err := c.Queue.Execution(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := ladoExecutionResponse(view)
	// What LADO verified about the PR's current head and its integration
	// approval, when verification is wired.
	if c.Verify != nil {
		target := view.Execution.PR.TargetBranch
		if target == "" {
			target = view.Execution.BaseBranch
		}
		head, err := c.Verify.Head(r.Context(), view.Execution.SessionID, view.Execution.PR.URL, view.Execution.PR.HeadSHA, target)
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		resp.Verification = ladoHeadVerificationResponse(head)
		resp.Integration = ladoIntegrationApprovalResponse(head.Approval)
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) getDispatchSettings(w http.ResponseWriter, r *http.Request) {
	if c.Queue == nil {
		apispec.NotImplemented(w, r, "GET", ladoDispatchSettingsPath)
		return
	}
	settings, err := c.Queue.Settings(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoDispatchSettingsResponse(settings))
}

func (c *LadoController) putDispatchSettings(w http.ResponseWriter, r *http.Request) {
	if c.Queue == nil {
		apispec.NotImplemented(w, r, "PUT", ladoDispatchSettingsPath)
		return
	}
	var req UpdateLadoDispatchSettingsRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	// A missing field would otherwise read as 0 minutes, which means no
	// limit, or as no memory hold.
	if req.MaxConcurrent == nil || req.MaxRunMinutes == nil || req.HoldOnMemoryTight == nil {
		envelope.WriteError(w, r, ladoqueue.InvalidSettings("Send maxConcurrent, maxRunMinutes and holdOnMemoryTight."))
		return
	}
	settings, err := c.Queue.SetSettings(r.Context(), domain.LadoDispatchSettings{
		MaxConcurrent:     *req.MaxConcurrent,
		MaxRunMinutes:     *req.MaxRunMinutes,
		HoldOnMemoryTight: *req.HoldOnMemoryTight,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoDispatchSettingsResponse(settings))
}

// ladoQueueReason is the reason behind an entry's state: why it waits, needs
// review or is blocked. Other states have none.
func ladoQueueReason(entry domain.LadoQueueEntry, state domain.LadoQueueState) string {
	switch state {
	case domain.LadoQueueWaiting:
		return entry.WaitingReason
	case domain.LadoQueueReviewRequired:
		return entry.ReviewRequiredReason
	case domain.LadoQueueBlocked:
		return entry.BlockedReason
	}
	return ""
}

// ladoNextAttempt is when an entry is looked at again, while that is still
// ahead. A claim keeps the time it was due.
func ladoNextAttempt(entry domain.LadoQueueEntry, now time.Time) *time.Time {
	if !entry.NextAttemptAt.After(now) {
		return nil
	}
	return ladoOptionalTime(entry.NextAttemptAt)
}

func ladoTaskQueueResponse(entry domain.LadoQueueEntry, now time.Time) *LadoTaskQueueResponse {
	if entry.ID == "" {
		return nil
	}
	state := entry.State(now)
	return &LadoTaskQueueResponse{
		State:         string(state),
		Reason:        ladoQueueReason(entry, state),
		Attempts:      entry.AttemptCount,
		NextAttemptAt: ladoNextAttempt(entry, now),
		Error:         entry.ErrorMessage,
		EnqueuedAt:    entry.EnqueuedAt,
	}
}

func ladoTaskExecutionResponse(task domain.LadoTask) *LadoTaskExecutionResponse {
	exec := task.Execution
	if exec.SessionID == "" {
		return nil
	}
	resp := &LadoTaskExecutionResponse{
		SessionID:      string(exec.SessionID),
		PlanRevisionID: string(exec.PlanRevisionID),
		StartedAt:      exec.StartedAt,
		DeadlineAt:     ladoOptionalTime(exec.DeadlineAt),
		TimedOut:       !exec.TimedOutAt.IsZero(),
		SessionEnded:   exec.SessionTerminated,
		PR:             ladoExecutionPRResponse(exec.PR),
	}
	if task.Plan.Latest.ID == exec.PlanRevisionID {
		resp.PlanRevisionNumber = task.Plan.Latest.Number
	}
	if task.Plan.Approval.ID == exec.PlanApprovalID {
		resp.PolicySHA256 = task.Plan.Snapshot.SHA256
	}
	return resp
}

func ladoExecutionPRResponse(pr domain.LadoExecutionPR) *LadoExecutionPRResponse {
	if pr.URL == "" {
		return nil
	}
	return &LadoExecutionPRResponse{
		URL: pr.URL, Number: pr.Number, Merged: pr.Merged, Closed: pr.Closed,
		HeadSHA: pr.HeadSHA, TargetBranch: pr.TargetBranch, Title: pr.Title,
	}
}

func ladoExecutionResponse(view ladoqueue.ExecutionView) LadoExecutionResponse {
	exec, snapshot := view.Execution, view.Snapshot
	state, reason := exec.State()
	rules := make([]LadoExecutionRuleResponse, 0, len(snapshot.Rules))
	for _, rule := range snapshot.Rules {
		rules = append(rules, LadoExecutionRuleResponse{ID: string(rule.RuleID), Name: rule.Name, Version: rule.Version})
	}
	setup := make([]LadoExecutionSetupItemResponse, 0, len(snapshot.Setup))
	for _, it := range snapshot.Setup {
		setup = append(setup, LadoExecutionSetupItemResponse{ItemID: string(it.ItemID), Tool: string(it.Tool), Kind: string(it.Kind), Name: it.Name, Version: it.Version})
	}
	return LadoExecutionResponse{
		SessionID: string(exec.SessionID),
		Task: LadoExecutionTaskResponse{
			ID:       string(view.Task.ID),
			Title:    view.Task.Issue.Title,
			IssueRef: string(view.Task.IssueRef),
			IssueURL: view.Task.Issue.URL,
		},
		Plan: LadoExecutionPlanResponse{
			RevisionID: string(view.Revision.ID),
			Number:     view.Revision.Number,
			SHA256:     view.Revision.SHA256,
			ApprovedAt: view.Approval.ApprovedAt,
		},
		Snapshot: LadoExecutionSnapshotResponse{
			SHA256:            view.SnapshotSHA256,
			Agent:             string(snapshot.Agent.Harness),
			Model:             snapshot.Agent.Model,
			PermissionMode:    string(snapshot.Agent.PermissionMode),
			PermissionCeiling: string(snapshot.PermissionCeiling),
			Rules:             rules,
			Prompt:            snapshot.Prompt,
			Setup:             setup,
		},
		Base:         LadoExecutionBaseResponse{PlanSHA: exec.PlanBaseSHA, DispatchSHA: exec.DispatchBaseSHA, CommitsAhead: exec.BaseCommitsAhead},
		Attempt:      exec.Attempt,
		StartedAt:    exec.StartedAt,
		DeadlineAt:   ladoOptionalTime(exec.DeadlineAt),
		TimedOutAt:   ladoOptionalTime(exec.TimedOutAt),
		SessionEnded: exec.SessionTerminated,
		State:        string(state),
		StateReason:  reason,
		PR:           ladoExecutionPRResponse(exec.PR),
	}
}

func ladoDispatchSettingsResponse(settings domain.LadoDispatchSettings) LadoDispatchSettingsResponse {
	return LadoDispatchSettingsResponse{
		MaxConcurrent:     settings.MaxConcurrent,
		MaxRunMinutes:     settings.MaxRunMinutes,
		HoldOnMemoryTight: settings.HoldOnMemoryTight,
		UpdatedAt:         ladoOptionalTime(settings.UpdatedAt),
	}
}
