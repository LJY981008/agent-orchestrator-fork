package controllers

// LADO fork: plan request, plan and approval routes (docs/LJY-PRD/TDD.md
// §5.1, §5.2 and §13). Routes that need a human-presence proof read the raw
// body first, because the proof signs its sha256.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

// LadoPresence checks human-presence proofs
// (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md).
type LadoPresence interface {
	Verify(header, method, path string, body []byte, action, subject string) (domain.LadoPresenceProof, error)
}

const (
	ladoTaskPlanRequestsPath = "/api/v1/lado/tasks/{taskId}/plan-requests"
	ladoTaskPlansPath        = "/api/v1/lado/tasks/{taskId}/plans"
	ladoPlanPath             = "/api/v1/lado/plans/{revisionId}"
	ladoPlanApprovePath      = "/api/v1/lado/plans/{revisionId}/approve"
	maxLadoPresenceBody      = 4 << 10
)

func (c *LadoController) requestPlan(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "POST", ladoTaskPlanRequestsPath)
		return
	}
	var req CreateLadoPlanRequestRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	created, err := c.Tasks.RequestPlan(r.Context(), ladotask.PlanRequestInput{
		TaskID:          ladoTaskID(r),
		SourceCommentID: req.SourceCommentID,
		Note:            req.Note,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ladoPlanRequestResponse(created))
}

func (c *LadoController) listPlans(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "GET", ladoTaskPlansPath)
		return
	}
	history, err := c.Tasks.Plans(r.Context(), ladoTaskID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoPlanHistoryResponse(history))
}

func (c *LadoController) getPlan(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "GET", ladoPlanPath)
		return
	}
	view, err := c.Tasks.Plan(r.Context(), ladoRevisionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoPlanResponse{
		Task:     ladoTaskResponse(view.Task),
		Revision: ladoPlanRevisionResponse(view.Revision),
		Content:  view.Content,
		Plan:     ladoPlanContentResponse(view.Plan),
		Latest:   view.Latest,
		Approved: view.Approved,
	})
}

func (c *LadoController) approvePlan(w http.ResponseWriter, r *http.Request) {
	if c.Tasks == nil {
		apispec.NotImplemented(w, r, "POST", ladoPlanApprovePath)
		return
	}
	body, ok := readLadoPresenceBody(w, r)
	if !ok {
		return
	}
	revision := ladoRevisionID(r)
	proof, err := c.verifyPresence(r, body, domain.LadoPresencePlanApprove, string(revision))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	var req ApproveLadoPlanRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	approval, err := c.Tasks.ApprovePlan(r.Context(), revision, req.SHA256, proof)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoPlanApprovalResponse(approval))
}

// verifyPresence checks the request's proof for an action on subject.
func (c *LadoController) verifyPresence(r *http.Request, body []byte, action, subject string) (domain.LadoPresenceProof, error) {
	if c.Presence == nil {
		return domain.LadoPresenceProof{}, apierr.Forbidden("LADO_PRESENCE_UNAVAILABLE", "Approvals need the AO desktop app.")
	}
	return c.Presence.Verify(r.Header.Get(ladopresence.Header), r.Method, r.URL.EscapedPath(), body, action, subject)
}

// presenceCheck returns a check the service runs only when an action needs
// presence.
func (c *LadoController) presenceCheck(r *http.Request, body []byte) func(action, subject string) (domain.LadoPresenceProof, error) {
	return func(action, subject string) (domain.LadoPresenceProof, error) {
		return c.verifyPresence(r, body, action, subject)
	}
}

// readLadoPresenceBody reads a small body whole, so the proof's body hash
// can be checked against exactly these bytes.
func readLadoPresenceBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLadoPresenceBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Request body too large", nil)
			return nil, false
		}
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Unreadable request body", nil)
		return nil, false
	}
	return body, true
}

// decodeLadoBytes decodes an already read body strictly, answering 400 when
// it cannot.
func decodeLadoBytes(w http.ResponseWriter, r *http.Request, body []byte, out any) bool {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return false
	}
	return true
}

func ladoRevisionID(r *http.Request) domain.LadoPlanRevisionID {
	return domain.LadoPlanRevisionID(chi.URLParam(r, "revisionId"))
}

func (c *LadoController) getTaskView(ctx context.Context, id domain.LadoTaskID) (LadoTaskViewResponse, error) {
	view, err := c.Tasks.View(ctx, id)
	if err != nil {
		return LadoTaskViewResponse{}, err
	}
	resp := LadoTaskViewResponse{
		Task:                  ladoTaskResponse(view.Task),
		Comments:              make([]LadoIssueCommentResponse, 0, len(view.Comments)),
		IssueReadError:        view.IssueReadError,
		IssueChangedSincePlan: view.IssueChanged,
		History:               ladoPlanHistoryResponse(view.History),
	}
	if view.IssueReadError == "" && view.Task.CancelledAt.IsZero() {
		resp.Issue = &LadoIssueTextResponse{Title: view.Issue.Title, Body: view.Issue.Body, State: string(view.Issue.State), URL: view.Issue.URL}
		snapshot, _ := domain.NewLadoIssueSnapshot(view.Task.IssueRef, view.Issue.Title, view.Issue.Body, view.Comments)
		for _, comment := range view.Comments {
			resp.Comments = append(resp.Comments, LadoIssueCommentResponse{
				ID:            comment.ID,
				Author:        comment.Author,
				AuthorTrusted: comment.AuthorTrusted,
				ByLado:        comment.ByLado(),
				Included:      snapshot.HasComment(comment.ID),
				CreatedAt:     comment.CreatedAt,
				Body:          comment.Body,
				URL:           comment.URL,
			})
		}
	}
	return resp, nil
}

func ladoTaskPlanResponse(plan domain.LadoTaskPlan) LadoTaskPlanResponse {
	resp := LadoTaskPlanResponse{RequestPending: plan.RequestPending()}
	if plan.Request.ID != "" {
		at := plan.Request.RequestedAt
		resp.RequestedAt = &at
		if !plan.Request.FailedAt.IsZero() {
			resp.RequestError = plan.Request.DeliveryError
		}
	}
	if latest := plan.Latest; latest.ID != "" {
		resp.Latest = &LadoPlanRevisionSummaryResponse{
			ID: string(latest.ID), Number: latest.Number, SHA256: latest.SHA256, Source: latest.Source, Valid: latest.Valid(), CreatedAt: latest.CreatedAt,
		}
		if plan.ApprovalValid() {
			resp.ApprovedRevisionID = string(latest.ID)
		}
	}
	return resp
}

func ladoPlanRevisionResponse(rev domain.LadoPlanRevision) LadoPlanRevisionResponse {
	problems := make([]LadoPlanProblemResponse, 0, len(rev.Problems))
	for _, problem := range rev.Problems {
		problems = append(problems, LadoPlanProblemResponse{Code: problem.Code, Field: problem.Field, Detail: problem.Detail})
	}
	return LadoPlanRevisionResponse{
		ID:                  string(rev.ID),
		TaskID:              string(rev.TaskID),
		RequestID:           string(rev.RequestID),
		Number:              rev.Number,
		Source:              rev.Source,
		SHA256:              rev.SHA256,
		BaseRef:             rev.BaseRef,
		BaseSHA:             rev.BaseSHA,
		IssueSnapshotSHA256: rev.IssueSnapshotSHA256,
		Valid:               rev.Valid(),
		Problems:            problems,
		CreatedAt:           rev.CreatedAt,
	}
}

func ladoPlanRequestResponse(req domain.LadoPlanRequest) LadoPlanRequestResponse {
	return LadoPlanRequestResponse{
		ID:               string(req.ID),
		TaskID:           string(req.TaskID),
		SessionID:        string(req.SessionID),
		SourceCommentID:  req.SourceCommentID,
		Note:             req.Note,
		BasedOn:          string(req.BasedOn),
		RequestedAt:      req.RequestedAt,
		DeliveredAt:      ladoOptionalTime(req.DeliveredAt),
		DeliveryAttempts: req.DeliveryAttempts,
		DeliveryError:    req.DeliveryError,
		FailedAt:         ladoOptionalTime(req.FailedAt),
	}
}

func ladoPlanApprovalResponse(approval domain.LadoPlanApproval) LadoPlanApprovalResponse {
	return LadoPlanApprovalResponse{
		ID:         string(approval.ID),
		RevisionID: string(approval.RevisionID),
		TaskID:     string(approval.TaskID),
		SHA256:     approval.SHA256,
		ApprovedAt: approval.ApprovedAt,
	}
}

func ladoPlanHistoryResponse(history ladotask.PlanHistory) LadoPlanHistoryResponse {
	resp := LadoPlanHistoryResponse{
		Requests:  make([]LadoPlanRequestResponse, 0, len(history.Requests)),
		Revisions: make([]LadoPlanRevisionResponse, 0, len(history.Revisions)),
		Approvals: make([]LadoPlanApprovalResponse, 0, len(history.Approvals)),
	}
	for _, req := range history.Requests {
		resp.Requests = append(resp.Requests, ladoPlanRequestResponse(req))
	}
	for _, rev := range history.Revisions {
		resp.Revisions = append(resp.Revisions, ladoPlanRevisionResponse(rev))
	}
	for _, approval := range history.Approvals {
		resp.Approvals = append(resp.Approvals, ladoPlanApprovalResponse(approval))
	}
	return resp
}

func ladoPlanContentResponse(plan domain.LadoPlan) LadoPlanContentResponse {
	orEmpty := func(values []string) []string {
		if values == nil {
			return []string{}
		}
		return values
	}
	return LadoPlanContentResponse{
		BaseRef:            plan.BaseRef,
		Steps:              orEmpty(plan.Steps),
		AcceptanceCriteria: orEmpty(plan.AcceptanceCriteria),
		AffectedPaths:      orEmpty(plan.AffectedPaths),
		TestCommands:       orEmpty(plan.TestCommands),
		Risk:               string(plan.Risk),
		PermissionCeiling:  string(plan.PermissionCeiling),
		ReviewRequired:     plan.ReviewRequired,
		Reviewer:           plan.Reviewer,
		PlanSource:         plan.PlanSource,
		Body:               plan.Body,
	}
}
