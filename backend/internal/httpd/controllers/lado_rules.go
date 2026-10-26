package controllers

// LADO fork: rule, binding and effective-rules routes (docs/LJY-PRD/TDD.md
// §13, docs/LJY-PRD/HARNESS.md §3-§8 and §14). A rule edit reads the raw body
// first, because changing the permission ceiling needs a presence proof over
// its sha256.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

// LadoRuleService is the controller-facing rules contract.
type LadoRuleService interface {
	List(ctx context.Context, includeArchived bool) ([]ladopolicy.RuleView, error)
	Rule(ctx context.Context, id domain.LadoRuleID) (ladopolicy.RuleView, []domain.LadoRuleVersion, error)
	Create(ctx context.Context, in ladopolicy.CreateInput) (ladopolicy.ChangeResult, error)
	Update(ctx context.Context, id domain.LadoRuleID, in ladopolicy.UpdateInput) (ladopolicy.ChangeResult, error)
	Delete(ctx context.Context, id domain.LadoRuleID) error
	SetBinding(ctx context.Context, in ladopolicy.BindingInput) (ladopolicy.ChangeResult, error)
	Effective(ctx context.Context, in ladopolicy.EffectiveInput) (ladopolicy.EffectiveView, error)
}

const (
	ladoRulesPath     = "/api/v1/lado/rules"
	ladoRulePath      = "/api/v1/lado/rules/{ruleId}"
	ladoBindingsPath  = "/api/v1/lado/bindings"
	ladoEffectivePath = "/api/v1/lado/effective"
)

func (c *LadoController) listRules(w http.ResponseWriter, r *http.Request) {
	if c.Rules == nil {
		apispec.NotImplemented(w, r, "GET", ladoRulesPath)
		return
	}
	includeArchived := false
	if raw := r.URL.Query().Get("includeArchived"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "includeArchived must be true or false", nil)
			return
		}
		includeArchived = value
	}
	views, err := c.Rules.List(r.Context(), includeArchived)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoRuleListResponse{Rules: make([]LadoRuleResponse, 0, len(views))}
	for _, view := range views {
		resp.Rules = append(resp.Rules, ladoRuleResponse(view))
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) createRule(w http.ResponseWriter, r *http.Request) {
	if c.Rules == nil {
		apispec.NotImplemented(w, r, "POST", ladoRulesPath)
		return
	}
	var req CreateLadoRuleRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	result, err := c.Rules.Create(r.Context(), ladopolicy.CreateInput{
		Name:        req.Name,
		Category:    req.Category,
		Instruction: req.Instruction,
		Conflicts:   ladoRuleIDs(req.ConflictsWith),
		Scope:       domain.LadoRuleScope(req.Scope),
		ScopeID:     req.ScopeID,
		On:          req.On,
		DryRun:      req.DryRun,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoRuleChangeResponse(result))
}

func (c *LadoController) getRule(w http.ResponseWriter, r *http.Request) {
	if c.Rules == nil {
		apispec.NotImplemented(w, r, "GET", ladoRulePath)
		return
	}
	view, versions, err := c.Rules.Rule(r.Context(), ladoRuleID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoRuleDetailResponse{Rule: ladoRuleResponse(view), Versions: make([]LadoRuleVersionResponse, 0, len(versions))}
	for _, v := range versions {
		resp.Versions = append(resp.Versions, LadoRuleVersionResponse{
			ID:            string(v.ID),
			Number:        v.Number,
			Name:          v.Content.Name,
			Category:      v.Content.Category,
			Instruction:   v.Content.Instruction,
			Enforcement:   ladoStrings(v.Content.Enforcement),
			Ceiling:       ladoRuleCeiling(v.RuleID, v.Content),
			ConflictsWith: ladoStrings(v.Content.Conflicts),
			SHA256:        v.SHA256,
			Actor:         v.Actor,
			CreatedAt:     v.CreatedAt,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) updateRule(w http.ResponseWriter, r *http.Request) {
	if c.Rules == nil {
		apispec.NotImplemented(w, r, "PATCH", ladoRulePath)
		return
	}
	// A ceiling change needs a presence proof over these exact bytes.
	body, ok := readLadoPresenceBody(w, r, maxLadoSmallBody)
	if !ok {
		return
	}
	var req UpdateLadoRuleRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	in := ladopolicy.UpdateInput{
		BaseVersion: req.BaseVersion,
		Name:        req.Name,
		Category:    req.Category,
		Instruction: req.Instruction,
		Archived:    req.Archived,
		Reason:      req.Reason,
		DryRun:      req.DryRun,
		Presence:    c.presenceCheck(r, body),
	}
	if req.ConflictsWith != nil {
		conflicts := ladoRuleIDs(*req.ConflictsWith)
		in.Conflicts = &conflicts
	}
	if req.Ceiling != nil {
		mode := domain.PermissionMode(req.Ceiling.Mode)
		in.CeilingMode = &mode
	}
	result, err := c.Rules.Update(r.Context(), ladoRuleID(r), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoRuleChangeResponse(result))
}

func (c *LadoController) deleteRule(w http.ResponseWriter, r *http.Request) {
	if c.Rules == nil {
		apispec.NotImplemented(w, r, "DELETE", ladoRulePath)
		return
	}
	if err := c.Rules.Delete(r.Context(), ladoRuleID(r)); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *LadoController) setBinding(w http.ResponseWriter, r *http.Request) {
	if c.Rules == nil {
		apispec.NotImplemented(w, r, "PUT", ladoBindingsPath)
		return
	}
	var req SetLadoBindingRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	result, err := c.Rules.SetBinding(r.Context(), ladopolicy.BindingInput{
		RuleID:  domain.LadoRuleID(req.RuleID),
		Scope:   domain.LadoRuleScope(req.Scope),
		ScopeID: req.ScopeID,
		State:   domain.LadoBindingState(req.State),
		DryRun:  req.DryRun,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoRuleChangeResponse(result))
}

func (c *LadoController) getEffective(w http.ResponseWriter, r *http.Request) {
	if c.Rules == nil {
		apispec.NotImplemented(w, r, "GET", ladoEffectivePath)
		return
	}
	query := r.URL.Query()
	projectID, taskID := query.Get("projectId"), query.Get("taskId")
	if (projectID == "") == (taskID == "") {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "LADO_EFFECTIVE_SCOPE_REQUIRED", "Name a project or a task, not both.", nil)
		return
	}
	view, err := c.Rules.Effective(r.Context(), ladopolicy.EffectiveInput{ProjectID: domain.ProjectID(projectID), TaskID: domain.LadoTaskID(taskID)})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoEffectiveResponse(view))
}

func ladoRuleID(r *http.Request) domain.LadoRuleID {
	return domain.LadoRuleID(chi.URLParam(r, "ruleId"))
}

func ladoRuleIDs(ids []string) []domain.LadoRuleID {
	out := make([]domain.LadoRuleID, 0, len(ids))
	for _, id := range ids {
		out = append(out, domain.LadoRuleID(id))
	}
	return out
}

// ladoStrings converts a list of string-typed values, never returning nil.
func ladoStrings[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, string(v))
	}
	return out
}

// ladoRuleCeiling returns the permission ceiling's setting, nil for any other
// rule.
func ladoRuleCeiling(id domain.LadoRuleID, content domain.LadoRuleContent) *LadoRuleCeiling {
	if id != domain.LadoRulePermissionCeiling {
		return nil
	}
	var cfg domain.LadoCeilingConfig
	if json.Unmarshal(content.Config, &cfg) != nil || domain.LadoPermissionRank(cfg.Mode) == 0 {
		return nil
	}
	return &LadoRuleCeiling{Mode: string(cfg.Mode)}
}

func ladoRuleResponse(view ladopolicy.RuleView) LadoRuleResponse {
	rule := view.Rule
	content := rule.Version.Content
	overrides := make([]LadoRuleOverrideResponse, 0, len(rule.Overrides))
	for _, b := range rule.Overrides {
		overrides = append(overrides, LadoRuleOverrideResponse{
			Scope:   string(b.Scope),
			ScopeID: b.ScopeID,
			State:   string(b.State),
			Label:   view.Labels[ladopolicy.LabelKey(b.Scope, b.ScopeID)],
		})
	}
	return LadoRuleResponse{
		ID:            string(rule.ID),
		Name:          content.Name,
		Category:      content.Category,
		Instruction:   content.Instruction,
		Builtin:       rule.Builtin,
		Protected:     rule.Protected,
		Archived:      !rule.ArchivedAt.IsZero(),
		Version:       rule.Version.Number,
		VersionID:     string(rule.Version.ID),
		SHA256:        rule.Version.SHA256,
		Enforcement:   ladoStrings(content.Enforcement),
		Strength:      string(content.Strength()),
		Ceiling:       ladoRuleCeiling(rule.ID, content),
		ConflictsWith: ladoStrings(content.Conflicts),
		Global:        string(rule.Global),
		Overrides:     overrides,
		CreatedAt:     rule.CreatedAt,
		UpdatedAt:     rule.Version.CreatedAt,
		ArchivedAt:    ladoOptionalTime(rule.ArchivedAt),
	}
}

func ladoRuleChangeResponse(result ladopolicy.ChangeResult) LadoRuleChangeResponse {
	resp := LadoRuleChangeResponse{Applied: result.Applied, Impact: ladoImpactResponse(result.Impact)}
	if result.Rule.Rule.ID != "" {
		rule := ladoRuleResponse(result.Rule)
		resp.Rule = &rule
	}
	return resp
}

// ladoImpactResponse is what a rule or setup change reaches.
func ladoImpactResponse(impact ladopolicy.Impact) LadoRuleImpactResponse {
	resp := LadoRuleImpactResponse{
		Projects: make([]LadoImpactProjectResponse, 0, len(impact.Projects)),
		Tasks:    make([]LadoImpactTaskResponse, 0, len(impact.Tasks)),
	}
	for _, p := range impact.Projects {
		resp.Projects = append(resp.Projects, LadoImpactProjectResponse{ID: string(p.ID), Name: p.Name})
	}
	for _, t := range impact.Tasks {
		resp.Tasks = append(resp.Tasks, LadoImpactTaskResponse{ID: string(t.ID), ProjectID: string(t.ProjectID), Title: t.Title, IssueRef: string(t.IssueRef)})
	}
	return resp
}

func ladoEffectiveResponse(view ladopolicy.EffectiveView) LadoEffectiveResponse {
	resp := LadoEffectiveResponse{
		ProjectID:         string(view.Content.ProjectID),
		TaskID:            string(view.Content.TaskID),
		Agent:             LadoEffectiveAgentResponse{Harness: string(view.Agent.Harness), Model: view.Agent.Model, Source: view.Agent.Source},
		PermissionCeiling: string(view.Ceiling),
		PermissionMode:    string(view.Mode),
		Rules:             make([]LadoEffectiveRuleResponse, 0, len(view.Entries)),
		AORules:           make([]LadoAORuleResponse, 0, len(view.AORules)),
		Prompt:            view.Prompt,
		Capabilities:      make([]LadoCapabilityResponse, 0, len(view.Capabilities)),
		Problems:          ladoPolicyProblems(view.Problems),
		Blocking:          view.Blocking(),
		SnapshotSHA256:    view.SHA256,
		Setup:             ladoEffectiveSetupResponse(view.Setup),
	}
	for _, e := range view.Entries {
		content := e.Rule.Version.Content
		resp.Rules = append(resp.Rules, LadoEffectiveRuleResponse{
			RuleID:      string(e.Rule.ID),
			Name:        content.Name,
			Category:    content.Category,
			Builtin:     e.Rule.Builtin,
			Protected:   e.Rule.Protected,
			Version:     e.Rule.Version.Number,
			On:          e.On,
			Strength:    string(content.Strength()),
			Enforcement: ladoStrings(content.Enforcement),
			Global:      string(e.Rule.Global),
			Project:     string(e.Project),
			Task:        string(e.Task),
			Source:      LadoRuleSourceResponse{Scope: string(e.Source.Scope), ScopeID: e.Source.ScopeID, State: string(e.Source.State)},
		})
	}
	for _, rule := range view.AORules {
		resp.AORules = append(resp.AORules, LadoAORuleResponse{Kind: string(rule.Kind), Path: rule.Path, SHA256: rule.SHA256, Chars: rule.Chars, ReadError: rule.ReadError})
	}
	for _, capability := range view.Capabilities {
		resp.Capabilities = append(resp.Capabilities, LadoCapabilityResponse{Class: string(capability.Class), Status: string(capability.Status)})
	}
	if approved := view.Approved; approved != nil {
		resp.Approved = &LadoApprovedRulesResponse{
			SnapshotSHA256: approved.Snapshot.SHA256,
			ApprovedAt:     approved.ApprovedAt,
			Revision:       approved.RevisionNumber,
			Matches:        approved.Matches,
			Blocking:       approved.Snapshot.Blocking,
			Added:          ladoStrings(approved.Added),
			Removed:        ladoStrings(approved.Removed),
			Changed:        ladoStrings(approved.Changed),
			SetupAdded:     ladoStrings(approved.SetupAdded),
			SetupRemoved:   ladoStrings(approved.SetupRemoved),
			SetupChanged:   ladoStrings(approved.SetupChanged),
			Setup:          ladoSnapshotSetup(approved.Content.Setup),
			AOSystemSHA256: approved.Content.AOSystem.SHA256,
			Accepted:       make([]LadoSetupAcceptedConflictResponse, 0, len(approved.Content.AcceptedConflicts)),
		}
		for _, a := range approved.Content.AcceptedConflicts {
			resp.Approved.Accepted = append(resp.Approved.Accepted, LadoSetupAcceptedConflictResponse{
				Code: a.Code, ItemID: string(a.ItemID), Detail: a.Detail, AcceptanceID: string(a.AcceptanceID),
			})
		}
	}
	return resp
}

// ladoPolicyProblems lists rule problems with their details, never nil.
func ladoPolicyProblems(problems []domain.LadoPolicyProblem) []LadoPolicyProblemResponse {
	out := make([]LadoPolicyProblemResponse, 0, len(problems))
	for _, problem := range problems {
		details := problem.Details
		if details == nil {
			details = map[string]string{}
		}
		out = append(out, LadoPolicyProblemResponse{
			Code:     problem.Code,
			Severity: string(problem.Severity),
			Rules:    ladoStrings(problem.Rules),
			Details:  details,
		})
	}
	return out
}
