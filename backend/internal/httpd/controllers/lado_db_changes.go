package controllers

// LADO fork: the production database change routes (#97,
// docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md). A project,
// or each child repository of a workspace, names the SQL files it applies to
// its production database by hand; the integration approval signs their blobs
// and LADO applies the pre-merge files before it merges. Changing the
// settings and applying files by hand need the desktop app's presence proof;
// secret values go in only and never come back.

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// LadoDBChangeService serves a project's production database changes
// (*ladodbchange.Service). The presence check runs only when the request
// needs it: project.db_changes on <projectId>:<repo> for a settings change,
// db_change.apply on <sessionId>@<head>:<phase> for an application. It
// returns apierr errors, such as 400 LADO_DB_CHANGE_INVALID{problems}, 404
// LADO_DB_CHANGE_NOT_FOUND and 409 LADO_DB_CHANGE_SETTINGS_CHANGED,
// LADO_DB_CHANGE_BASELINE_FAILED and LADO_DB_CHANGE_RUN_STATE; the domain's
// errors of the same meaning are answered the same way.
type LadoDBChangeService interface {
	DBChangeSettings(ctx context.Context, project domain.ProjectID) (domain.LadoDBChangeSettingsView, error)
	UpdateDBChangeSettings(ctx context.Context, project domain.ProjectID, in domain.LadoDBChangeSettingsUpdate, presence func(action, subject string) (domain.LadoPresenceProof, error)) (domain.LadoDBChangeSettingsView, error)
	ProjectDBChanges(ctx context.Context, project domain.ProjectID) ([]domain.LadoDBChangeSummary, error)
	ExecutionDBChanges(ctx context.Context, session domain.SessionID) (domain.LadoDBChangeExecutionView, error)
	DBChangeFile(ctx context.Context, session domain.SessionID, prURL, blobSHA string) (domain.LadoDBChangeFileContent, error)
	ApplyDBChanges(ctx context.Context, req domain.LadoDBChangeApplyRequest, presence func(action, subject string) (domain.LadoPresenceProof, error)) (domain.LadoDBChangeRun, error)
}

const (
	ladoDBChangeSettingsPath  = "/api/v1/lado/projects/{projectId}/db-changes/settings"
	ladoProjectDBChangesPath  = "/api/v1/lado/projects/{projectId}/db-changes"
	ladoExecutionDBChangePath = "/api/v1/lado/executions/{sessionId}/db-changes"
	ladoDBChangeFilePath      = "/api/v1/lado/executions/{sessionId}/db-changes/file"
	ladoDBChangeApplyPath     = "/api/v1/lado/executions/{sessionId}/db-changes/apply"
	// maxLadoDBChangeFiles caps the files one application names.
	maxLadoDBChangeFiles = 500
)

// ladoGitObjectPattern is a git object id, SHA-1 or SHA-256.
var ladoGitObjectPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func (c *LadoController) registerDBChanges(r chi.Router) {
	r.Get("/lado/projects/{projectId}/db-changes/settings", c.getDBChangeSettings)
	r.Put("/lado/projects/{projectId}/db-changes/settings", c.putDBChangeSettings)
	r.Get("/lado/projects/{projectId}/db-changes", c.listProjectDBChanges)
	r.Get("/lado/executions/{sessionId}/db-changes", c.getExecutionDBChanges)
	r.Get("/lado/executions/{sessionId}/db-changes/file", c.getDBChangeFile)
	r.Post("/lado/executions/{sessionId}/db-changes/apply", c.applyDBChanges)
}

func (c *LadoController) getDBChangeSettings(w http.ResponseWriter, r *http.Request) {
	if c.DBChanges == nil {
		apispec.NotImplemented(w, r, "GET", ladoDBChangeSettingsPath)
		return
	}
	project := ladoProjectID(r)
	view, err := c.DBChanges.DBChangeSettings(r.Context(), project)
	if err != nil {
		writeLadoDBChangeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoDBChangeSettingsResponse(project, view))
}

func (c *LadoController) putDBChangeSettings(w http.ResponseWriter, r *http.Request) {
	if c.DBChanges == nil {
		apispec.NotImplemented(w, r, "PUT", ladoDBChangeSettingsPath)
		return
	}
	// The change needs a presence proof over these exact bytes.
	body, ok := readLadoPresenceBody(w, r, maxLadoSettingsBody)
	if !ok {
		return
	}
	var req UpdateLadoDBChangeSettingsRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	project := ladoProjectID(r)
	in := domain.LadoDBChangeSettingsUpdate{
		Repo:           req.Repo,
		Enabled:        req.Enabled,
		Paths:          req.Paths,
		Command:        req.Command,
		TimeoutMinutes: req.TimeoutMinutes,
		SecretNames:    req.SecretNames,
		Secrets:        domain.LadoDBChangeSecretsUpdate{Set: req.Secrets.Set, Remove: req.Secrets.Remove},
		Base:           req.Base,
	}
	view, err := c.DBChanges.UpdateDBChangeSettings(r.Context(), project, in, c.presenceCheck(r, body))
	if err != nil {
		writeLadoDBChangeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoDBChangeSettingsResponse(project, view))
}

func (c *LadoController) listProjectDBChanges(w http.ResponseWriter, r *http.Request) {
	if c.DBChanges == nil {
		apispec.NotImplemented(w, r, "GET", ladoProjectDBChangesPath)
		return
	}
	summaries, err := c.DBChanges.ProjectDBChanges(r.Context(), ladoProjectID(r))
	if err != nil {
		writeLadoDBChangeError(w, r, err)
		return
	}
	resp := LadoDBChangeSummaryListResponse{Summaries: make([]LadoDBChangeSummaryResponse, 0, len(summaries))}
	for _, s := range summaries {
		resp.Summaries = append(resp.Summaries, LadoDBChangeSummaryResponse{
			TaskID: string(s.TaskID), SessionID: string(s.SessionID), PRURL: s.PRURL, Repo: s.Repo,
			State: string(s.State), Pending: s.Pending, Failed: s.Failed, Message: s.Message,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) getExecutionDBChanges(w http.ResponseWriter, r *http.Request) {
	if c.DBChanges == nil {
		apispec.NotImplemented(w, r, "GET", ladoExecutionDBChangePath)
		return
	}
	view, err := c.DBChanges.ExecutionDBChanges(r.Context(), ladoSessionID(r))
	if err != nil {
		writeLadoDBChangeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoDBChangeExecutionResponse(view))
}

func (c *LadoController) getDBChangeFile(w http.ResponseWriter, r *http.Request) {
	if c.DBChanges == nil {
		apispec.NotImplemented(w, r, "GET", ladoDBChangeFilePath)
		return
	}
	query := r.URL.Query()
	prURL, blobSHA := query.Get("prUrl"), query.Get("blobSha")
	if prURL == "" {
		writeLadoInvalidRequest(w, r, "prUrl", "prUrl is required")
		return
	}
	if !ladoGitObjectPattern.MatchString(blobSHA) {
		writeLadoInvalidRequest(w, r, "blobSha", "blobSha must be a git object id of 40 or 64 lowercase hex characters")
		return
	}
	content, err := c.DBChanges.DBChangeFile(r.Context(), ladoSessionID(r), prURL, blobSHA)
	if err != nil {
		writeLadoDBChangeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoDBChangeFileContentResponse{
		Path: content.Path, BlobSHA: content.BlobSHA, Content: content.Content, Truncated: content.Truncated,
	})
}

func (c *LadoController) applyDBChanges(w http.ResponseWriter, r *http.Request) {
	if c.DBChanges == nil {
		apispec.NotImplemented(w, r, "POST", ladoDBChangeApplyPath)
		return
	}
	// The application needs a presence proof over these exact bytes.
	body, ok := readLadoPresenceBody(w, r, maxLadoSmallBody)
	if !ok {
		return
	}
	var req ApplyLadoDBChangesRequest
	if !decodeLadoBytes(w, r, body, &req) {
		return
	}
	if field, message := ladoApplyDBChangesProblem(req); field != "" {
		writeLadoInvalidRequest(w, r, field, message)
		return
	}
	in := domain.LadoDBChangeApplyRequest{
		SessionID: ladoSessionID(r),
		PRURL:     req.PRURL,
		HeadSHA:   req.HeadSHA,
		Phase:     domain.LadoDBChangePhase(req.Phase),
		Files:     make([]domain.LadoDBChangeFileRef, 0, len(req.Files)),
	}
	for _, file := range req.Files {
		in.Files = append(in.Files, domain.LadoDBChangeFileRef{
			Path: file.Path, BlobSHA: file.BlobSHA, Phase: domain.LadoDBChangePhase(file.Phase),
		})
	}
	run, err := c.DBChanges.ApplyDBChanges(r.Context(), in, c.presenceCheck(r, body))
	if err != nil {
		writeLadoDBChangeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoDBChangeRunResponse(run))
}

// ladoApplyDBChangesProblem is the field of an application request that is
// wrong and why, "" when there is none.
func ladoApplyDBChangesProblem(req ApplyLadoDBChangesRequest) (string, string) {
	phase := domain.LadoDBChangePhase(req.Phase)
	switch {
	case req.PRURL == "":
		return "prUrl", "prUrl is required"
	case !ladoGitObjectPattern.MatchString(req.HeadSHA):
		return "headSha", "headSha must be a commit id of 40 or 64 lowercase hex characters"
	case phase != domain.LadoDBChangePreMerge && phase != domain.LadoDBChangeAfterDeploy:
		return "phase", "phase must be pre_merge or after_deploy"
	case len(req.Files) == 0:
		return "files", "files must name the files the developer confirmed"
	case len(req.Files) > maxLadoDBChangeFiles:
		return "files", "files names too many files"
	}
	for _, file := range req.Files {
		switch {
		case file.Path == "":
			return "files", "each file needs its path"
		case !ladoGitObjectPattern.MatchString(file.BlobSHA):
			return "files", "each file's blobSha must be a git object id"
		case domain.LadoDBChangePhase(file.Phase) != phase:
			return "files", "every file must be of the request's phase"
		}
	}
	return "", ""
}

// writeLadoDBChangeError answers a service error: an apierr error as it is,
// the domain's database change errors as their codes, anything else as 500.
func writeLadoDBChangeError(w http.ResponseWriter, r *http.Request, err error) {
	envelope.WriteError(w, r, ladoDBChangeAPIError(err))
}

func ladoDBChangeAPIError(err error) error {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return err
	}
	var invalid *domain.LadoDBChangeInvalidError
	var pending *domain.LadoDBChangePendingError
	switch {
	case errors.As(err, &invalid):
		problems := make([]map[string]any, 0, len(invalid.Problems))
		for _, p := range invalid.Problems {
			problems = append(problems, map[string]any{"field": p.Field, "reason": p.Reason})
		}
		return apierr.Invalid("LADO_DB_CHANGE_INVALID", "The database change settings are not valid.", map[string]any{"problems": problems})
	case errors.As(err, &pending):
		files := make([]map[string]any, 0, len(pending.Files))
		for _, f := range pending.Files {
			files = append(files, map[string]any{"path": f.Path, "blobSha": f.BlobSHA, "phase": string(f.Phase)})
		}
		return apierr.Conflict("LADO_DB_CHANGE_PENDING", "The head's pre-merge SQL is not applied yet.",
			map[string]any{"prUrl": pending.PRURL, "headSha": pending.HeadSHA, "files": files})
	case errors.Is(err, domain.ErrLadoDBChangePending):
		return apierr.Conflict("LADO_DB_CHANGE_PENDING", "The head's pre-merge SQL is not applied yet.", nil)
	case errors.Is(err, domain.ErrLadoDBChangeSettingsChanged):
		return apierr.Conflict("LADO_DB_CHANGE_SETTINGS_CHANGED", "The repository's database change settings changed. Read them again.", nil)
	case errors.Is(err, domain.ErrLadoDBChangeBaselineFailed):
		return apierr.Conflict("LADO_DB_CHANGE_BASELINE_FAILED", "LADO could not read the default branch to record its SQL files as applied, so the settings were not saved.", nil)
	case errors.Is(err, domain.ErrLadoDBChangeNotFound):
		return apierr.NotFound("LADO_DB_CHANGE_NOT_FOUND", "No such database change.")
	case errors.Is(err, domain.ErrLadoDBChangeRunState):
		return apierr.Conflict("LADO_DB_CHANGE_RUN_STATE", "The database change run is not in a state the request may change.", nil)
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return apierr.Forbidden("LADO_PRESENCE_REPLAYED", "This confirmation was already used. Confirm again in the AO desktop app.")
	}
	return err
}

func ladoDBChangeSettingsResponse(project domain.ProjectID, view domain.LadoDBChangeSettingsView) LadoDBChangeSettingsResponse {
	resp := LadoDBChangeSettingsResponse{
		ProjectID:    string(project),
		Repositories: make([]LadoDBChangeRepoSettingsResponse, 0, len(view.Repositories)),
		SHA256:       view.SHA256,
	}
	for _, repo := range view.Repositories {
		s := repo.LadoDBChangeSettings
		entry := LadoDBChangeRepoSettingsResponse{
			Repo:              s.Repo,
			Name:              repo.Name,
			Enabled:           s.Enabled,
			Paths:             ladoOrEmptyStrings(s.Paths),
			Command:           s.Command,
			TimeoutMinutes:    s.TimeoutMinutes,
			SecretNames:       ladoOrEmptyStrings(s.SecretNames),
			StoredSecretNames: ladoOrEmptyStrings(repo.StoredSecretNames),
			BaselineCommit:    s.BaselineCommit,
			EnabledAt:         ladoOptionalUTC(s.EnabledAt),
			UpdatedAt:         ladoOptionalUTC(s.UpdatedAt),
		}
		// A repository that never saved settings has no base to compare.
		if !s.UpdatedAt.IsZero() {
			entry.SHA256 = s.SHA256()
		}
		resp.Repositories = append(resp.Repositories, entry)
	}
	return resp
}

func ladoDBChangeExecutionResponse(view domain.LadoDBChangeExecutionView) LadoDBChangeExecutionResponse {
	resp := LadoDBChangeExecutionResponse{
		SessionID: string(view.SessionID),
		TaskID:    string(view.TaskID),
		PRs:       make([]LadoDBChangePRResponse, 0, len(view.PRs)),
	}
	for _, pr := range view.PRs {
		entry := LadoDBChangePRResponse{
			PRURL:    pr.PRURL,
			HeadSHA:  pr.HeadSHA,
			Repo:     pr.Repo,
			Files:    make([]LadoDBChangeFileResponse, 0, len(pr.Files)),
			Modified: ladoDBChangeModifiedResponses(pr.Modified),
			Runs:     make([]LadoDBChangeRunResponse, 0, len(pr.Runs)),
		}
		for _, f := range pr.Files {
			file := LadoDBChangeFileResponse{
				Path: f.Path, BlobSHA: f.BlobSHA, Phase: string(f.Phase), Status: f.Status,
				Attempts: f.Attempts, LastMessage: f.LastMessage, OutputSHA256: f.OutputSHA256,
			}
			if f.AppliedAt != nil {
				file.AppliedAt = ladoOptionalUTC(*f.AppliedAt)
			}
			entry.Files = append(entry.Files, file)
		}
		for _, run := range pr.Runs {
			entry.Runs = append(entry.Runs, ladoDBChangeRunResponse(run))
		}
		resp.PRs = append(resp.PRs, entry)
	}
	return resp
}

func ladoDBChangeModifiedResponses(modified []domain.LadoDBChangeModified) []LadoDBChangeModifiedResponse {
	out := make([]LadoDBChangeModifiedResponse, 0, len(modified))
	for _, m := range modified {
		out = append(out, LadoDBChangeModifiedResponse{
			Path: m.Path, AppliedBlobSHA: m.AppliedBlobSHA, HeadBlobSHA: m.HeadBlobSHA, Deleted: m.Deleted,
		})
	}
	return out
}

func ladoDBChangeRunResponse(run domain.LadoDBChangeRun) LadoDBChangeRunResponse {
	return LadoDBChangeRunResponse{
		ID:         run.ID,
		Repo:       run.Repo,
		SessionID:  string(run.ExecutionSessionID),
		TaskID:     string(run.TaskID),
		PRURL:      run.PRURL,
		HeadSHA:    run.HeadSHA,
		Kind:       string(run.Kind),
		Trigger:    string(run.Trigger),
		State:      string(run.State),
		ApprovalID: run.ApprovalID,
		Attempts:   run.Attempts,
		Error:      run.Error,
		CreatedAt:  run.CreatedAt.UTC(),
		UpdatedAt:  run.UpdatedAt.UTC(),
	}
}
