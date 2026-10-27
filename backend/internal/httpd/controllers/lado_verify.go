package controllers

// LADO fork: verification, integration approval and project protection
// routes (step 8 plan §15; docs/LJY-PRD/TDD.md §7.1, §8 and §13). The
// integration approval reads the raw body first, because its presence proof
// signs the body's sha256.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
)

// LadoVerifyService is the controller-facing verification contract.
type LadoVerifyService interface {
	Head(ctx context.Context, session domain.SessionID, headSHA string) (ladoverify.HeadView, error)
	ApproveIntegration(ctx context.Context, in ladoverify.ApproveInput) (ladoverify.ApproveResult, error)
	RunChecksAgain(ctx context.Context, session domain.SessionID) (ladoverify.RunAgain, error)
	CheckLog(ctx context.Context, session domain.SessionID, runID string) ([]byte, error)
	Protection(ctx context.Context, projectID domain.ProjectID, refresh bool) ([]ladoverify.ProtectionRead, error)
}

const (
	ladoIntegrationApprovePath = "/api/v1/lado/executions/{sessionId}/integration-approve"
	ladoChecksRunPath          = "/api/v1/lado/executions/{sessionId}/checks/run"
	ladoCheckLogPath           = "/api/v1/lado/executions/{sessionId}/checks/{checkRunId}/log"
	ladoProjectProtectionPath  = "/api/v1/lado/projects/{projectId}/protection"
)

// registerVerify mounts the verification routes.
func (c *LadoController) registerVerify(r chi.Router) {
	r.Post("/lado/executions/{sessionId}/integration-approve", c.approveIntegration)
	r.Post("/lado/executions/{sessionId}/checks/run", c.runChecks)
	r.Get("/lado/executions/{sessionId}/checks/{checkRunId}/log", c.getCheckLog)
	r.Get("/lado/projects/{projectId}/protection", c.getProjectProtection)
}

func (c *LadoController) approveIntegration(w http.ResponseWriter, r *http.Request) {
	if c.Verify == nil {
		apispec.NotImplemented(w, r, "POST", ladoIntegrationApprovePath)
		return
	}
	// The approval needs a presence proof over these exact bytes. Its
	// subject names the head the body names, so the service checks it first,
	// before it reads anything.
	body, ok := readLadoPresenceBody(w, r, maxLadoPresenceBody)
	if !ok {
		return
	}
	var req ApproveLadoIntegrationRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	in := ladoverify.ApproveInput{
		SessionID:          ladoSessionID(r),
		PRURL:              req.PRURL,
		HeadSHA:            req.HeadSHA,
		VerificationSHA256: req.VerificationSHA256,
		Acknowledge:        req.Acknowledge,
		DryRun:             req.DryRun,
	}
	// A preview needs no confirmation, so it never checks one.
	if !req.DryRun {
		in.Presence = c.presenceCheck(r, body)
	}
	result, err := c.Verify.ApproveIntegration(r.Context(), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoIntegrationApproveResponse{
		Verification:       ladoVerificationResponse(result.Verification, result.SHA256),
		VerificationSHA256: result.SHA256,
		Confirm:            ladoOrEmptyStrings(result.Confirm),
		Approval:           ladoIntegrationApprovalResponse(result.Approval),
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) runChecks(w http.ResponseWriter, r *http.Request) {
	if c.Verify == nil {
		apispec.NotImplemented(w, r, "POST", ladoChecksRunPath)
		return
	}
	again, err := c.Verify.RunChecksAgain(r.Context(), ladoSessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, LadoRunChecksResponse{HeadSHA: again.HeadSHA, Commands: ladoOrEmptyStrings(again.Commands)})
}

func (c *LadoController) getCheckLog(w http.ResponseWriter, r *http.Request) {
	if c.Verify == nil {
		apispec.NotImplemented(w, r, "GET", ladoCheckLogPath)
		return
	}
	log, err := c.Verify.CheckLog(r.Context(), ladoSessionID(r), chi.URLParam(r, "checkRunId"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// The log is what a command printed; it is never rendered as markup.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(log) //nolint:gosec // G705: served as text/plain with nosniff, never as markup.
}

func (c *LadoController) getProjectProtection(w http.ResponseWriter, r *http.Request) {
	if c.Verify == nil {
		apispec.NotImplemented(w, r, "GET", ladoProjectProtectionPath)
		return
	}
	refresh := false
	if raw := r.URL.Query().Get("refresh"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "refresh must be true or false", nil)
			return
		}
		refresh = value
	}
	projectID := ladoProjectID(r)
	reads, err := c.Verify.Protection(r.Context(), projectID, refresh)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoProjectProtectionResponse{ProjectID: string(projectID), Repositories: make([]LadoBranchProtectionResponse, 0, len(reads))}
	for _, read := range reads {
		resp.Repositories = append(resp.Repositories, ladoBranchProtectionResponse(read))
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func ladoSessionID(r *http.Request) domain.SessionID {
	return domain.SessionID(chi.URLParam(r, "sessionId"))
}

// ladoOrEmptyStrings returns values, or an empty list for nil.
func ladoOrEmptyStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// ladoProtectionStates maps the protection item's codes to a branch's
// state; a plain pass is protected.
var ladoProtectionStates = map[string]string{
	"":                               "protected",
	domain.LadoCodeUnprotected:       "unprotected",
	domain.LadoCodeProtectionUnknown: "unknown",
	domain.LadoCodeUnprotectedGitLab: "unsupported",
}

// ladoBranchProtectionResponse evaluates a read of a base branch's rules as
// the protection item does, with its error redacted.
func ladoBranchProtectionResponse(read ladoverify.ProtectionRead) LadoBranchProtectionResponse {
	resp := LadoBranchProtectionResponse{
		Repository:     domain.LadoRepoRef(read.Repo),
		Branch:         read.Branch,
		State:          "unknown",
		Missing:        []string{},
		RequiredChecks: []string{},
		CheckedAt:      ladoOptionalTime(read.ReadAt),
	}
	item, err := ladoverify.EvaluateProtection(read)
	var details domain.LadoProtectionDetails
	if err == nil {
		err = json.Unmarshal(item.Details, &details)
	}
	if err != nil {
		resp.Error = "LADO could not evaluate the rules."
		return resp
	}
	if state, ok := ladoProtectionStates[item.Code]; ok {
		resp.State = state
	}
	resp.Missing = ladoOrEmptyStrings(details.Missing)
	resp.RequiredChecks = ladoOrEmptyStrings(details.RequiredChecks)
	resp.Error = details.Error
	return resp
}

// ladoVerificationResponse is a verification record with its hash, and the
// evidence of each acceptance criterion from its evidence item.
func ladoVerificationResponse(v domain.LadoVerification, sum string) LadoVerificationResponse {
	outcome, reason := v.Overall()
	resp := LadoVerificationResponse{
		HeadSHA:    v.HeadSHA,
		PRURL:      v.PRURL,
		Outcome:    string(outcome),
		Reason:     reason,
		SHA256:     sum,
		Items:      make([]LadoVerifyItemResponse, 0, len(v.Items)),
		Evidence:   []LadoEvidenceCriterionResponse{},
		ComputedAt: v.ComputedAt,
	}
	for _, item := range v.Items {
		entry := LadoVerifyItemResponse{Kind: string(item.Kind), Outcome: string(item.Outcome), Code: item.Code}
		// Details are the kind's struct, stored as JSON with the same keys;
		// a record that cannot be read keeps empty details.
		if len(item.Details) > 0 {
			_ = json.Unmarshal(item.Details, &entry.Details)
		}
		if item.Kind == domain.LadoVerifyEvidence && entry.Details.Criteria != nil {
			resp.Evidence = entry.Details.Criteria
		}
		resp.Items = append(resp.Items, entry)
	}
	return resp
}

// ladoHeadVerificationResponse is the execution view's record, nil without
// one.
func ladoHeadVerificationResponse(head ladoverify.HeadView) *LadoVerificationResponse {
	if head.Verification == nil {
		return nil
	}
	resp := ladoVerificationResponse(*head.Verification, head.SHA256)
	return &resp
}

func ladoIntegrationApprovalResponse(approval *domain.LadoIntegrationApproval) *LadoIntegrationApprovalResponse {
	if approval == nil {
		return nil
	}
	return &LadoIntegrationApprovalResponse{
		ID:                 approval.ID,
		HeadSHA:            approval.HeadSHA,
		VerificationSHA256: approval.VerificationSHA256,
		Acknowledged:       ladoOrEmptyStrings(approval.Acknowledged),
		Actor:              approval.Actor,
		ApprovedAt:         approval.ApprovedAt,
		StatusState:        string(approval.StatusState),
		StatusAttempts:     approval.StatusAttempts,
		StatusPostedAt:     ladoOptionalTime(approval.StatusPostedAt),
		StatusError:        approval.StatusError,
	}
}
