package controllers

// LADO fork: local setup routes (docs/LJY-PRD/HARNESS.md §9 and §10, step 7
// plan §13). Adoption, restore and changes read the raw body first, because
// the desktop app's confirmation signs its sha256; the service asks for the
// confirmation only when the request needs one, and refuses with 403
// without it.

import (
	"bytes"
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladosetup"
)

// LadoSetupService is the controller-facing local setup contract.
// *ladosetup.Service implements it.
type LadoSetupService interface {
	Overview(ctx context.Context, rescan bool) (ladosetup.Overview, error)
	ScopeItems(ctx context.Context, projectID domain.ProjectID, taskID domain.LadoTaskID) ([]ladopolicy.SetupItemState, error)
	ScopeConflicts(ctx context.Context, in ladosetup.ScopeConflictsInput) ([]ladosetup.Conflict, error)
	Adopt(ctx context.Context, req ladosetup.AdoptRequest) (ladosetup.AdoptResult, error)
	Adoption(ctx context.Context, id domain.LadoSetupAdoptionID) (ladosetup.AdoptionView, error)
	Restore(ctx context.Context, id domain.LadoSetupAdoptionID, presence func(action, subject string) (domain.LadoPresenceProof, error)) (ladosetup.RestoreResult, error)
	Item(ctx context.Context, id domain.LadoSetupItemID) (ladosetup.ItemView, error)
	Apply(ctx context.Context, c ladosetup.Change, presence func(action, subject string) (domain.LadoPresenceProof, error)) (ladosetup.ChangeResult, error)
}

const (
	ladoSetupInventoryPath   = "/api/v1/lado/setup/inventory"
	ladoSetupScanPath        = "/api/v1/lado/setup/scan"
	ladoSetupItemsPath       = "/api/v1/lado/setup/items"
	ladoSetupItemPath        = "/api/v1/lado/setup/items/{itemId}"
	ladoSetupItemDriftPath   = "/api/v1/lado/setup/items/{itemId}/drift"
	ladoSetupBindingsPath    = "/api/v1/lado/setup/bindings"
	ladoSetupConflictsPath   = "/api/v1/lado/setup/conflicts"
	ladoSetupAcceptancesPath = "/api/v1/lado/setup/conflicts/acceptances"
	ladoSetupAcceptancePath  = "/api/v1/lado/setup/conflicts/acceptances/{acceptanceId}"
	ladoSetupAdoptionsPath   = "/api/v1/lado/setup/adoptions"
	ladoSetupAdoptionPath    = "/api/v1/lado/setup/adoptions/{adoptionId}"
	ladoSetupRestorePath     = "/api/v1/lado/setup/adoptions/{adoptionId}/restore"
	// maxLadoSetupContentBody fits an item's content: up to 1 MiB of text,
	// which JSON escapes can make six times longer.
	maxLadoSetupContentBody = 8 << 20
	// maxLadoSetupAdoptionBody fits an adoption of every entry of a large
	// setup.
	maxLadoSetupAdoptionBody = 1 << 20
)

// registerSetup mounts the local setup routes.
func (c *LadoController) registerSetup(r chi.Router) {
	r.Get("/lado/setup/inventory", c.getSetupInventory)
	r.Post("/lado/setup/scan", c.scanSetup)
	r.Get("/lado/setup/items", c.listSetupItems)
	r.Post("/lado/setup/items", c.createSetupItem)
	r.Get("/lado/setup/items/{itemId}", c.getSetupItem)
	r.Patch("/lado/setup/items/{itemId}", c.updateSetupItem)
	r.Delete("/lado/setup/items/{itemId}", c.deleteSetupItem)
	r.Post("/lado/setup/items/{itemId}/drift", c.resolveSetupDrift)
	r.Put("/lado/setup/bindings", c.setSetupBinding)
	r.Get("/lado/setup/conflicts", c.listSetupConflicts)
	r.Post("/lado/setup/conflicts/acceptances", c.acceptSetupConflict)
	r.Delete("/lado/setup/conflicts/acceptances/{acceptanceId}", c.revokeSetupAcceptance)
	r.Post("/lado/setup/adoptions", c.adoptSetup)
	r.Get("/lado/setup/adoptions/{adoptionId}", c.getSetupAdoption)
	r.Post("/lado/setup/adoptions/{adoptionId}/restore", c.restoreSetup)
}

func (c *LadoController) getSetupInventory(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "GET", ladoSetupInventoryPath)
		return
	}
	c.writeSetupOverview(w, r, false)
}

func (c *LadoController) scanSetup(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "POST", ladoSetupScanPath)
		return
	}
	c.writeSetupOverview(w, r, true)
}

func (c *LadoController) writeSetupOverview(w http.ResponseWriter, r *http.Request, rescan bool) {
	overview, err := c.Setup.Overview(r.Context(), rescan)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoSetupInventoryResponse(overview))
}

func (c *LadoController) listSetupItems(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "GET", ladoSetupItemsPath)
		return
	}
	query := r.URL.Query()
	items, err := c.Setup.ScopeItems(r.Context(), domain.ProjectID(query.Get("projectId")), domain.LadoTaskID(query.Get("taskId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoSetupItemListResponse{Items: ladoSetupItemStates(items)})
}

func (c *LadoController) listSetupConflicts(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "GET", ladoSetupConflictsPath)
		return
	}
	query := r.URL.Query()
	conflicts, err := c.Setup.ScopeConflicts(r.Context(), ladosetup.ScopeConflictsInput{
		ProjectID: domain.ProjectID(query.Get("projectId")),
		TaskID:    domain.LadoTaskID(query.Get("taskId")),
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoSetupConflictListResponse{Conflicts: ladoSetupConflicts(conflicts)})
}

func (c *LadoController) adoptSetup(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "POST", ladoSetupAdoptionsPath)
		return
	}
	// The adoption needs a presence proof over these exact bytes.
	body, ok := readLadoPresenceBody(w, r, maxLadoSetupAdoptionBody)
	if !ok {
		return
	}
	var req AdoptLadoSetupRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	in := ladosetup.AdoptRequest{PlanSHA256: req.PlanSHA256, DryRun: req.DryRun}
	for _, e := range req.Entries {
		in.Entries = append(in.Entries, ladosetup.AdoptEntry{
			Tool:   domain.LadoSetupTool(e.Tool),
			Kind:   domain.LadoSetupKind(e.Kind),
			Name:   e.Name,
			Action: domain.LadoSetupAction(e.Action),
		})
	}
	// A preview needs no confirmation, so it never checks one.
	if !req.DryRun {
		in.Presence = c.presenceCheck(r, body)
	}
	result, err := c.Setup.Adopt(r.Context(), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoSetupAdoptResponse(result))
}

func (c *LadoController) getSetupAdoption(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "GET", ladoSetupAdoptionPath)
		return
	}
	view, err := c.Setup.Adoption(r.Context(), ladoSetupAdoptionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoSetupAdoptionDetailResponse{
		Adoption:       ladoSetupAdoptionResponse(view.Adoption, len(view.Items)),
		Items:          make([]LadoSetupItemResponse, 0, len(view.Items)),
		BackupReadable: view.Backup != nil,
		Backup:         make([]LadoSetupBackupEntryResponse, 0, len(view.Backup)),
	}
	for _, it := range view.Items {
		resp.Items = append(resp.Items, ladoSetupItemResponse(it))
	}
	for _, be := range view.Backup {
		resp.Backup = append(resp.Backup, LadoSetupBackupEntryResponse{
			Tool: string(be.Tool), Kind: string(be.Kind), Name: be.Name, Action: string(be.Action),
			Root: string(be.Root), Path: be.Path, Type: be.Type, Size: be.Size, SHA256: be.SHA256,
		})
	}
	if view.Report != nil {
		report := ladoSetupRestoreReportResponse(*view.Report)
		resp.Report = &report
	}
	if view.Preconditions != nil {
		pre := ladoSetupPreconditionsResponse(*view.Preconditions)
		resp.Preconditions = &pre
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) restoreSetup(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "POST", ladoSetupRestorePath)
		return
	}
	// The restore needs a presence proof over these exact bytes: none, or
	// an empty object.
	body, ok := readLadoPresenceBody(w, r, maxLadoPresenceBody)
	if !ok {
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		var req struct{}
		if !decodeLadoBytes(w, r, body, &req) {
			return
		}
	}
	result, err := c.Setup.Restore(r.Context(), ladoSetupAdoptionID(r), c.presenceCheck(r, body))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoSetupRestoreResponse{
		AdoptionID: string(result.AdoptionID),
		Applied:    result.Applied,
		RestoredAt: result.RestoredAt,
		Report:     ladoSetupRestoreReportResponse(result.Report),
		Warnings:   ladoStrings(result.Warnings),
	})
}

func (c *LadoController) getSetupItem(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "GET", ladoSetupItemPath)
		return
	}
	view, err := c.Setup.Item(r.Context(), ladoSetupItemID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoSetupItemDetailResponse{
		Item:        ladoSetupItemResponse(view.Item),
		ManagedPath: view.ManagedPath,
		Versions:    make([]LadoSetupVersionDetailResponse, 0, len(view.Versions)),
		Warnings:    ladoStrings(view.Warnings),
	}
	for _, v := range view.Versions {
		resp.Versions = append(resp.Versions, LadoSetupVersionDetailResponse{
			Version: ladoSetupVersionResponse(v.Version),
			Content: ladoSetupContentResponse(v.Content),
		})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) createSetupItem(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "POST", ladoSetupItemsPath)
		return
	}
	body, ok := readLadoPresenceBody(w, r, maxLadoSetupContentBody)
	if !ok {
		return
	}
	var req CreateLadoSetupItemRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	c.applySetupChange(w, r, body, ladosetup.Change{
		Op:      ladosetup.OpCreate,
		Tool:    domain.LadoSetupTool(req.Tool),
		Kind:    domain.LadoSetupKind(req.Kind),
		Name:    req.Name,
		Content: req.Content,
		DryRun:  req.DryRun,
	})
}

func (c *LadoController) updateSetupItem(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "PATCH", ladoSetupItemPath)
		return
	}
	body, ok := readLadoPresenceBody(w, r, maxLadoSetupContentBody)
	if !ok {
		return
	}
	var req UpdateLadoSetupItemRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	c.applySetupChange(w, r, body, ladosetup.Change{
		Op:          ladosetup.OpEdit,
		ItemID:      ladoSetupItemID(r),
		Content:     req.Content,
		BaseVersion: req.BaseVersion,
		DryRun:      req.DryRun,
	})
}

func (c *LadoController) deleteSetupItem(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "DELETE", ladoSetupItemPath)
		return
	}
	dryRun, ok := ladoDryRunQuery(w, r)
	if !ok {
		return
	}
	c.applySetupChange(w, r, nil, ladosetup.Change{Op: ladosetup.OpDelete, ItemID: ladoSetupItemID(r), DryRun: dryRun})
}

func (c *LadoController) resolveSetupDrift(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "POST", ladoSetupItemDriftPath)
		return
	}
	body, ok := readLadoPresenceBody(w, r, maxLadoSmallBody)
	if !ok {
		return
	}
	var req ResolveLadoSetupDriftRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	change := ladosetup.Change{ItemID: ladoSetupItemID(r), DriftSHA256: req.SHA256, DryRun: req.DryRun}
	switch req.Action {
	case "keep":
		change.Op = ladosetup.OpDriftKeep
	case "revert":
		change.Op = ladosetup.OpDriftRevert
	default:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", ladosetup.CodeChangeInvalid, "Keep or revert the drift.",
			map[string]any{"field": "action", "reason": "unknown"})
		return
	}
	c.applySetupChange(w, r, body, change)
}

func (c *LadoController) setSetupBinding(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "PUT", ladoSetupBindingsPath)
		return
	}
	body, ok := readLadoPresenceBody(w, r, maxLadoSmallBody)
	if !ok {
		return
	}
	var req SetLadoSetupBindingRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	c.applySetupChange(w, r, body, ladosetup.Change{
		Op:     ladosetup.OpBinding,
		ItemID: domain.LadoSetupItemID(req.ItemID),
		Binding: ladosetup.ChangeBinding{
			Scope:   domain.LadoRuleScope(req.Scope),
			ScopeID: req.ScopeID,
			State:   domain.LadoBindingState(req.State),
		},
		DryRun: req.DryRun,
	})
}

func (c *LadoController) acceptSetupConflict(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "POST", ladoSetupAcceptancesPath)
		return
	}
	body, ok := readLadoPresenceBody(w, r, maxLadoSmallBody)
	if !ok {
		return
	}
	var req AcceptLadoSetupConflictRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	c.applySetupChange(w, r, body, ladosetup.Change{
		Op:     ladosetup.OpAccept,
		ItemID: domain.LadoSetupItemID(req.ItemID),
		Acceptance: ladosetup.ChangeAcceptance{
			Code:    req.Code,
			Detail:  req.Detail,
			Scope:   domain.LadoRuleScope(req.Scope),
			ScopeID: req.ScopeID,
		},
		DryRun: req.DryRun,
	})
}

func (c *LadoController) revokeSetupAcceptance(w http.ResponseWriter, r *http.Request) {
	if c.Setup == nil {
		apispec.NotImplemented(w, r, "DELETE", ladoSetupAcceptancePath)
		return
	}
	dryRun, ok := ladoDryRunQuery(w, r)
	if !ok {
		return
	}
	c.applySetupChange(w, r, nil, ladosetup.Change{
		Op:           ladosetup.OpRevoke,
		AcceptanceID: domain.LadoConflictAcceptanceID(chi.URLParam(r, "acceptanceId")),
		DryRun:       dryRun,
	})
}

// applySetupChange shows or makes a change. The service checks the presence
// proof over body when the change needs the desktop app's confirmation.
func (c *LadoController) applySetupChange(w http.ResponseWriter, r *http.Request, body []byte, change ladosetup.Change) {
	result, err := c.Setup.Apply(r.Context(), change, c.presenceCheck(r, body))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoSetupChangeResponse(result))
}

// ladoDryRunQuery reads the dryRun query of a change without a body.
func ladoDryRunQuery(w http.ResponseWriter, r *http.Request) (bool, bool) {
	raw := r.URL.Query().Get("dryRun")
	if raw == "" {
		return false, true
	}
	dryRun, err := strconv.ParseBool(raw)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "dryRun must be true or false", nil)
		return false, false
	}
	return dryRun, true
}

func ladoSetupItemID(r *http.Request) domain.LadoSetupItemID {
	return domain.LadoSetupItemID(chi.URLParam(r, "itemId"))
}

func ladoSetupAdoptionID(r *http.Request) domain.LadoSetupAdoptionID {
	return domain.LadoSetupAdoptionID(chi.URLParam(r, "adoptionId"))
}

func ladoSetupInventoryResponse(o ladosetup.Overview) LadoSetupInventoryResponse {
	resp := LadoSetupInventoryResponse{
		OS:        o.OS,
		Verified:  make(map[string]bool, len(o.Verified)),
		ScannedAt: o.ScannedAt,
		Entries:   make([]LadoSetupEntryResponse, 0, len(o.Entries)),
		Roots:     make([]LadoSetupRootResponse, 0, len(o.Roots)),
		Projects:  make([]LadoSetupProjectScanResponse, 0, len(o.Projects)),
		Drift:     make([]LadoSetupDriftResponse, 0, len(o.Drift)),
		Adoptions: make([]LadoSetupAdoptionResponse, 0, len(o.Adoptions)),
		Items:     ladoSetupItemStates(o.Items),
	}
	for tool, verified := range o.Verified {
		resp.Verified[string(tool)] = verified
	}
	for _, e := range o.Entries {
		resp.Entries = append(resp.Entries, LadoSetupEntryResponse{
			Tool: string(e.Tool), Kind: string(e.Kind), Name: e.Name, Location: string(e.Location), Path: e.Path,
			Size: e.Size, SHA256: e.SHA256, Tracked: e.Tracked, Status: string(e.Status), Reason: string(e.Reason),
			DefaultAction: string(e.DefaultAction), KeptInPlace: ladoStrings(e.KeptInPlace), ItemID: string(e.ItemID), ProjectID: string(e.ProjectID),
		})
	}
	for _, root := range o.Roots {
		resp.Roots = append(resp.Roots, LadoSetupRootResponse{Tool: string(root.Tool), Location: string(root.Location), Path: root.Path, Found: root.Found, Error: root.Error})
	}
	for _, p := range o.Projects {
		resp.Projects = append(resp.Projects, LadoSetupProjectScanResponse{ID: string(p.ID), Name: p.Name, Path: p.Path, Error: p.Error})
	}
	for _, d := range o.Drift {
		resp.Drift = append(resp.Drift, LadoSetupDriftResponse{
			ItemID: string(d.ItemID), Kind: string(d.Kind), Tool: string(d.Tool), ItemKind: string(d.ItemKind), Name: d.Name, Path: d.Path, SHA256: d.SHA256,
		})
	}
	for _, a := range o.Adoptions {
		resp.Adoptions = append(resp.Adoptions, ladoSetupAdoptionResponse(a, o.AdoptionItems[a.ID]))
	}
	return resp
}

func ladoSetupAdoptionResponse(a domain.LadoSetupAdoption, items int) LadoSetupAdoptionResponse {
	return LadoSetupAdoptionResponse{
		ID: string(a.ID), CreatedAt: a.CreatedAt, Actor: a.Actor, OS: a.OS, PlanSHA256: a.PlanSHA256, ManifestSHA256: a.ManifestSHA256,
		State: string(a.State), ErrorMessage: a.ErrorMessage, RestoredAt: ladoOptionalTime(a.RestoredAt), ItemCount: items,
	}
}

func ladoSetupItemStates(items []ladopolicy.SetupItemState) []LadoSetupItemStateResponse {
	out := make([]LadoSetupItemStateResponse, 0, len(items))
	for _, it := range items {
		out = append(out, LadoSetupItemStateResponse{
			ItemID: string(it.ItemID), Tool: string(it.Tool), Kind: string(it.Kind), Name: it.Name,
			VersionID: string(it.VersionID), Version: it.Version, SHA256: it.SHA256, On: it.On,
			Source: LadoRuleSourceResponse{Scope: string(it.Source.Scope), ScopeID: it.Source.ScopeID, State: string(it.Source.State)},
			Global: string(it.Global), Project: string(it.Project), Task: string(it.Task), InstalledSHA256: it.InstalledSHA256,
		})
	}
	return out
}

func ladoSetupConflicts(conflicts []ladosetup.Conflict) []LadoSetupConflictResponse {
	out := make([]LadoSetupConflictResponse, 0, len(conflicts))
	for _, c := range conflicts {
		details := make(map[string]string, len(c.Details))
		for key, value := range c.Details {
			details[key] = value
		}
		out = append(out, LadoSetupConflictResponse{
			Code: c.Code, Severity: string(c.Severity), ItemID: string(c.ItemID), Path: c.Path, Detail: c.Detail,
			ContentSHA256: c.ContentSHA256, Details: details, AcceptanceID: string(c.AcceptanceID),
		})
	}
	return out
}

// ladoSetupPreconditionsResponse is what holds an adoption or a restore
// back.
func ladoSetupPreconditionsResponse(pre ladosetup.Preconditions) LadoSetupPreconditionsResponse {
	resp := LadoSetupPreconditionsResponse{
		OS:               pre.OS,
		Unverified:       ladoStrings(pre.Unverified),
		Sessions:         make([]LadoSetupRunningSessionResponse, 0, len(pre.Sessions)),
		Processes:        make([]LadoSetupRunningProcessResponse, 0, len(pre.Processes)),
		ProcessesIgnored: pre.ProcessesIgnored,
		OK:               pre.OK(),
	}
	for _, s := range pre.Sessions {
		resp.Sessions = append(resp.Sessions, LadoSetupRunningSessionResponse{ID: string(s.ID), Tool: string(s.Tool)})
	}
	for _, proc := range pre.Processes {
		resp.Processes = append(resp.Processes, LadoSetupRunningProcessResponse{PID: proc.PID, Name: proc.Program(), Tool: string(proc.Tool)})
	}
	return resp
}

func ladoSetupAdoptResponse(res ladosetup.AdoptResult) LadoSetupAdoptResponse {
	p := res.Preview
	resp := LadoSetupAdoptResponse{
		Preview: LadoSetupAdoptionPreviewResponse{
			PlanSHA256:    p.PlanSHA256,
			OS:            p.OS,
			Entries:       make([]LadoSetupPreviewEntryResponse, 0, len(p.Entries)),
			Rewrites:      make([]LadoSetupHookRewriteResponse, 0, len(p.Rewrites)),
			Conflicts:     ladoSetupConflicts(p.Conflicts),
			Preconditions: ladoSetupPreconditionsResponse(p.Preconditions),
			Warnings:      ladoStrings(p.Warnings),
		},
		Applied:      res.Applied,
		AdoptionID:   string(res.AdoptionID),
		State:        string(res.State),
		Items:        make([]LadoSetupAdoptedItemResponse, 0, len(res.Items)),
		Verification: make([]LadoSetupVerifyCheckResponse, 0, len(res.Verification)),
		Warnings:     ladoStrings(res.Warnings),
	}
	for _, e := range p.Entries {
		resp.Preview.Entries = append(resp.Preview.Entries, LadoSetupPreviewEntryResponse{
			Tool: string(e.Tool), Kind: string(e.Kind), Name: e.Name, Action: string(e.Action), OriginPath: e.OriginPath,
			SHA256: e.SHA256, Size: e.Size, ManagedPath: e.ManagedPath, Delivery: e.Delivery, KeptInPlace: ladoStrings(e.KeptInPlace),
		})
	}
	for _, rw := range p.Rewrites {
		resp.Preview.Rewrites = append(resp.Preview.Rewrites, LadoSetupHookRewriteResponse{
			Setting: rw.Setting, Event: rw.Event, Matcher: rw.Matcher, Before: rw.Before, After: rw.After, Paths: ladoStrings(rw.Paths),
		})
	}
	for _, it := range res.Items {
		resp.Items = append(resp.Items, LadoSetupAdoptedItemResponse{
			ItemID: string(it.ItemID), VersionID: string(it.VersionID), Tool: string(it.Tool), Kind: string(it.Kind),
			Name: it.Name, ManagedPath: it.ManagedPath, SHA256: it.SHA256,
		})
	}
	for _, check := range res.Verification {
		resp.Verification = append(resp.Verification, LadoSetupVerifyCheckResponse{ItemID: string(check.ItemID), Check: check.Check, OK: check.OK, Error: check.Error})
	}
	return resp
}

func ladoSetupRestoreReportResponse(report ladosetup.RestoreReport) LadoSetupRestoreReportResponse {
	resp := LadoSetupRestoreReportResponse{Entries: make([]LadoSetupRestoredEntryResponse, 0, len(report.Entries))}
	for _, e := range report.Entries {
		resp.Entries = append(resp.Entries, LadoSetupRestoredEntryResponse{
			ItemID: string(e.ItemID), Tool: string(e.Tool), Kind: string(e.Kind), Name: e.Name, Root: string(e.Root),
			Path: e.Path, Status: e.Status, Beside: e.Beside, Error: e.Error,
		})
	}
	return resp
}

func ladoSetupItemResponse(it domain.LadoSetupItem) LadoSetupItemResponse {
	return LadoSetupItemResponse{
		ID: string(it.ID), Tool: string(it.Tool), Kind: string(it.Kind), Name: it.Name, OriginPath: it.OriginPath,
		AdoptionID: string(it.AdoptionID), Live: it.Live(), CreatedAt: it.CreatedAt,
		DeletedAt: ladoOptionalTime(it.DeletedAt), RestoredAt: ladoOptionalTime(it.RestoredAt),
	}
}

func ladoSetupVersionResponse(v domain.LadoSetupItemVersion) LadoSetupItemVersionResponse {
	return LadoSetupItemVersionResponse{
		ID: string(v.ID), ItemID: string(v.ItemID), Version: v.Version, ContentKind: string(v.ContentKind), SHA256: v.SHA256,
		Size: v.Size, Executable: v.Executable, Actor: v.Actor, CreatedAt: v.CreatedAt,
	}
}

func ladoSetupContentResponse(content *ladosetup.ChangeContent) *LadoSetupContentResponse {
	if content == nil {
		return nil
	}
	resp := &LadoSetupContentResponse{
		ContentKind: string(content.ContentKind), SHA256: content.SHA256, Size: content.Size, Executable: content.Executable,
		Text: content.Text, Files: make([]LadoSetupTreeFileResponse, 0, len(content.Files)),
	}
	for _, f := range content.Files {
		resp.Files = append(resp.Files, LadoSetupTreeFileResponse{Path: f.Path, Mode: string(f.Mode), Size: f.Size, SHA256: f.SHA256})
	}
	return resp
}

func ladoSetupChangeResponse(res ladosetup.ChangeResult) LadoSetupChangeResponse {
	resp := LadoSetupChangeResponse{
		Op:               string(res.Op),
		ChangeSHA256:     res.ChangeSHA256,
		NeedsPresence:    res.NeedsPresence,
		Applied:          res.Applied,
		Actor:            res.Actor,
		Item:             ladoSetupItemResponse(res.Item),
		Before:           ladoSetupContentResponse(res.Before),
		After:            ladoSetupContentResponse(res.After),
		ConflictsAdded:   ladoSetupConflicts(res.ConflictsAdded),
		ConflictsRemoved: ladoSetupConflicts(res.ConflictsRemoved),
		Impact:           ladoImpactResponse(res.Impact),
		Pins:             res.Pins,
		Warnings:         ladoStrings(res.Warnings),
	}
	if res.Version != nil {
		v := ladoSetupVersionResponse(*res.Version)
		resp.Version = &v
	}
	if b := res.Binding; b != nil {
		resp.Binding = &LadoSetupBindingResponse{ItemID: string(b.ItemID), Scope: string(b.Scope), ScopeID: b.ScopeID, State: string(b.State), UpdatedAt: b.UpdatedAt}
	}
	if a := res.Acceptance; a != nil {
		resp.Acceptance = &LadoSetupAcceptanceResponse{
			ID: string(a.ID), Code: a.Code, ItemID: string(a.ItemID), Detail: a.Detail, ContentSHA256: a.ContentSHA256,
			Scope: string(a.Scope), ScopeID: a.ScopeID, Actor: a.Actor, CreatedAt: a.CreatedAt, RevokedAt: ladoOptionalTime(a.RevokedAt),
		}
	}
	return resp
}

// ladoEffectiveSetupResponse is the local setup part of an effective view.
func ladoEffectiveSetupResponse(setup ladopolicy.SetupResolution) LadoEffectiveSetupResponse {
	resp := LadoEffectiveSetupResponse{
		Items:          ladoSetupItemStates(setup.Items),
		AOSystemSHA256: setup.AOSystemSHA256,
		Conflicts:      ladoPolicyProblems(setup.Problems),
		Accepted:       make([]LadoSetupAcceptedConflictResponse, 0, len(setup.Accepted)),
	}
	for _, a := range setup.Accepted {
		resp.Accepted = append(resp.Accepted, LadoSetupAcceptedConflictResponse{Code: a.Code, ItemID: string(a.ItemID), Detail: a.Detail, AcceptanceID: string(a.AcceptanceID)})
	}
	return resp
}

// ladoSnapshotSetup lists the setup items a rule snapshot fixed.
func ladoSnapshotSetup(items []ladopolicy.SnapshotSetupItem) []LadoSnapshotSetupItemResponse {
	out := make([]LadoSnapshotSetupItemResponse, 0, len(items))
	for _, it := range items {
		out = append(out, LadoSnapshotSetupItemResponse{
			ItemID: string(it.ItemID), Tool: string(it.Tool), Kind: string(it.Kind), Name: it.Name,
			VersionID: string(it.VersionID), Version: it.Version, SHA256: it.SHA256,
			Source:          LadoRuleSourceResponse{Scope: string(it.Source.Scope), ScopeID: it.Source.ScopeID, State: string(it.Source.State)},
			InstalledSHA256: it.InstalledSHA256,
		})
	}
	return out
}
