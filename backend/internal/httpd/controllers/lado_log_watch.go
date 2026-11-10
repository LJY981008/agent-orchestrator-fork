package controllers

// LADO fork: the production log watch routes (issue #87,
// docs/LJY-PRD/adr/0012-log-watch.md). The settings name each project's
// Docker Engine API hosts and the containers LADO follows read-only there;
// TLS material is copied under ~/.ao and never returned.

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// LadoLogWatchService serves a project's log watch (*ladologwatch.Service).
// It returns apierr errors: 400 LADO_LOG_WATCH_INVALID (details field),
// LADO_LOG_CERT_INVALID and LADO_LOG_CERT_KEY_ENCRYPTED; 404
// LADO_LOG_SOURCE_NOT_FOUND, LADO_LOG_FINGERPRINT_NOT_FOUND and
// LADO_LOG_CI_NOT_FOUND; 409 LADO_LOG_CI_UNSUPPORTED; 500
// LADO_LOG_WATCH_FAILED. Settings of a project that never saved any are the
// defaults, and a failed connection check is OK false, not an error.
type LadoLogWatchService interface {
	Settings(ctx context.Context, project domain.ProjectID) (domain.LadoLogWatchView, error)
	UpdateSettings(ctx context.Context, project domain.ProjectID, in domain.LadoLogWatchInput) (domain.LadoLogWatchView, error)
	ImportTLS(ctx context.Context, project domain.ProjectID, source domain.LadoLogSourceID, in domain.LadoLogTLSImport) (domain.LadoLogWatchView, error)
	DeleteTLS(ctx context.Context, project domain.ProjectID, source domain.LadoLogSourceID) (domain.LadoLogWatchView, error)
	Check(ctx context.Context, project domain.ProjectID, source domain.LadoLogSourceID) (domain.LadoLogWatchCheck, error)
	ImportFromCI(ctx context.Context, project domain.ProjectID) (domain.LadoLogCIImport, error)
	Incidents(ctx context.Context, project domain.ProjectID) ([]domain.LadoLogFingerprint, error)
	SetIgnored(ctx context.Context, project domain.ProjectID, id domain.LadoLogFingerprintID, ignored bool) (domain.LadoLogFingerprint, error)
}

const (
	ladoLogWatchPath         = "/api/v1/lado/projects/{projectId}/log-watch"
	ladoLogWatchTLSPath      = "/api/v1/lado/projects/{projectId}/log-watch/sources/{sourceId}/tls"
	ladoLogWatchCheckPath    = "/api/v1/lado/projects/{projectId}/log-watch/check"
	ladoLogWatchImportCIPath = "/api/v1/lado/projects/{projectId}/log-watch/import-ci"
	ladoLogWatchIncidentPath = "/api/v1/lado/projects/{projectId}/log-watch/incidents"
	ladoLogWatchIgnorePath   = "/api/v1/lado/projects/{projectId}/log-watch/fingerprints/{fingerprintId}/ignore"
	// maxLadoLogWatchBody fits ten sources of ten targets and forty patterns
	// of 500 characters, escaped.
	maxLadoLogWatchBody = 256 << 10
)

func (c *LadoController) registerLogWatch(r chi.Router) {
	r.Get("/lado/projects/{projectId}/log-watch", c.getLogWatch)
	r.Put("/lado/projects/{projectId}/log-watch", c.putLogWatch)
	r.Post("/lado/projects/{projectId}/log-watch/sources/{sourceId}/tls", c.importLogWatchTLS)
	r.Delete("/lado/projects/{projectId}/log-watch/sources/{sourceId}/tls", c.deleteLogWatchTLS)
	r.Post("/lado/projects/{projectId}/log-watch/check", c.checkLogWatch)
	r.Post("/lado/projects/{projectId}/log-watch/import-ci", c.importLogWatchCI)
	r.Get("/lado/projects/{projectId}/log-watch/incidents", c.listLogWatchIncidents)
	r.Post("/lado/projects/{projectId}/log-watch/fingerprints/{fingerprintId}/ignore", c.ignoreLogWatchFingerprint)
}

func (c *LadoController) getLogWatch(w http.ResponseWriter, r *http.Request) {
	if c.LogWatch == nil {
		apispec.NotImplemented(w, r, "GET", ladoLogWatchPath)
		return
	}
	view, err := c.LogWatch.Settings(r.Context(), ladoProjectID(r))
	writeLadoLogWatchView(w, r, view, err)
}

func (c *LadoController) putLogWatch(w http.ResponseWriter, r *http.Request) {
	if c.LogWatch == nil {
		apispec.NotImplemented(w, r, "PUT", ladoLogWatchPath)
		return
	}
	var req UpdateLadoLogWatchRequest
	if !decodeLadoBody(w, r, maxLadoLogWatchBody, &req) {
		return
	}
	in := ladoLogWatchInput(req)
	// The service validates too; refusing here keeps a bad endpoint from
	// reaching it at all.
	if err := domain.ValidateLadoLogWatchInput(in); err != nil {
		writeLadoLogWatchError(w, r, err)
		return
	}
	view, err := c.LogWatch.UpdateSettings(r.Context(), ladoProjectID(r), in)
	writeLadoLogWatchView(w, r, view, err)
}

func (c *LadoController) importLogWatchTLS(w http.ResponseWriter, r *http.Request) {
	if c.LogWatch == nil {
		apispec.NotImplemented(w, r, "POST", ladoLogWatchTLSPath)
		return
	}
	var req ImportLadoLogTLSRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	switch {
	case req.CAPath == "":
		writeLadoLogInvalid(w, r, "caPath", "CA 인증서 파일을 고르세요")
		return
	case (req.CertPath == "") != (req.KeyPath == ""):
		writeLadoLogInvalid(w, r, "certPath", "클라이언트 인증서와 키는 함께 주거나 둘 다 빼세요")
		return
	}
	view, err := c.LogWatch.ImportTLS(r.Context(), ladoProjectID(r), ladoLogSourceID(r), domain.LadoLogTLSImport{
		CAPath: req.CAPath, CertPath: req.CertPath, KeyPath: req.KeyPath,
	})
	writeLadoLogWatchView(w, r, view, err)
}

func (c *LadoController) deleteLogWatchTLS(w http.ResponseWriter, r *http.Request) {
	if c.LogWatch == nil {
		apispec.NotImplemented(w, r, "DELETE", ladoLogWatchTLSPath)
		return
	}
	view, err := c.LogWatch.DeleteTLS(r.Context(), ladoProjectID(r), ladoLogSourceID(r))
	writeLadoLogWatchView(w, r, view, err)
}

func (c *LadoController) checkLogWatch(w http.ResponseWriter, r *http.Request) {
	if c.LogWatch == nil {
		apispec.NotImplemented(w, r, "POST", ladoLogWatchCheckPath)
		return
	}
	var req CheckLadoLogSourceRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	if req.SourceID == "" {
		writeLadoLogInvalid(w, r, "sourceId", "확인할 출처를 고르세요(저장된 출처만 확인합니다)")
		return
	}
	check, err := c.LogWatch.Check(r.Context(), ladoProjectID(r), domain.LadoLogSourceID(req.SourceID))
	if err != nil {
		writeLadoLogWatchError(w, r, err)
		return
	}
	resp := LadoLogWatchCheckResponse{
		OK:            check.OK,
		Detail:        ladoLogScrub(check.Detail),
		EngineVersion: check.EngineVersion,
		APIVersion:    check.APIVersion,
		Insecure:      check.Insecure,
		Instances:     make([]LadoLogInstanceResponse, 0, len(check.Instances)),
	}
	for _, inst := range check.Instances {
		resp.Instances = append(resp.Instances, LadoLogInstanceResponse{
			TargetID: inst.TargetID, Name: inst.Name, ID: inst.ID, Image: inst.Image, Service: inst.Service, State: inst.State,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) importLogWatchCI(w http.ResponseWriter, r *http.Request) {
	if c.LogWatch == nil {
		apispec.NotImplemented(w, r, "POST", ladoLogWatchImportCIPath)
		return
	}
	found, err := c.LogWatch.ImportFromCI(r.Context(), ladoProjectID(r))
	if err != nil {
		writeLadoLogWatchError(w, r, err)
		return
	}
	resp := LadoLogCIImportResponse{Files: make([]LadoLogCIFileResponse, 0, len(found.Files))}
	for _, f := range found.Files {
		file := LadoLogCIFileResponse{
			Repo:            f.Repo,
			Path:            f.Path,
			Hosts:           make([]LadoLogCIHostResponse, 0, len(f.Hosts)),
			ComposeProjects: ladoNonNilStrings(f.ComposeProjects),
			Notes:           ladoNonNilStrings(f.Notes),
		}
		for _, h := range f.Hosts {
			file.Hosts = append(file.Hosts, LadoLogCIHostResponse{
				Variable: h.Variable, DisplayName: h.DisplayName, Endpoint: h.Endpoint, Unresolved: h.Unresolved,
			})
		}
		resp.Files = append(resp.Files, file)
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) listLogWatchIncidents(w http.ResponseWriter, r *http.Request) {
	if c.LogWatch == nil {
		apispec.NotImplemented(w, r, "GET", ladoLogWatchIncidentPath)
		return
	}
	fps, err := c.LogWatch.Incidents(r.Context(), ladoProjectID(r))
	if err != nil {
		writeLadoLogWatchError(w, r, err)
		return
	}
	resp := LadoLogIncidentListResponse{Fingerprints: make([]LadoLogFingerprintResponse, 0, len(fps))}
	for _, fp := range fps {
		resp.Fingerprints = append(resp.Fingerprints, ladoLogFingerprintResponse(fp))
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) ignoreLogWatchFingerprint(w http.ResponseWriter, r *http.Request) {
	if c.LogWatch == nil {
		apispec.NotImplemented(w, r, "POST", ladoLogWatchIgnorePath)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "fingerprintId"), 10, 64)
	if err != nil || id < 1 {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "LADO_LOG_FINGERPRINT_NOT_FOUND", "Unknown fingerprint", nil)
		return
	}
	var req SetLadoLogFingerprintIgnoredRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	fp, err := c.LogWatch.SetIgnored(r.Context(), ladoProjectID(r), domain.LadoLogFingerprintID(id), req.Ignored)
	if err != nil {
		writeLadoLogWatchError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoLogFingerprintResponse(fp))
}

func ladoLogSourceID(r *http.Request) domain.LadoLogSourceID {
	return domain.LadoLogSourceID(chi.URLParam(r, "sourceId"))
}

func ladoLogWatchInput(req UpdateLadoLogWatchRequest) domain.LadoLogWatchInput {
	in := domain.LadoLogWatchInput{
		Enabled:                req.Enabled,
		Repository:             req.Repository,
		Include:                ladoNonNilStrings(req.Include),
		Exclude:                ladoNonNilStrings(req.Exclude),
		HourlyIssueCap:         req.HourlyIssueCap,
		AutoPlan:               req.AutoPlan,
		AutoPlanDailyCap:       req.AutoPlanDailyCap,
		AutoPlanMinCount:       req.AutoPlanMinCount,
		DisconnectAlertMinutes: req.DisconnectAlertMinutes,
		Sources:                make([]domain.LadoLogSourceInput, 0, len(req.Sources)),
	}
	for _, src := range req.Sources {
		source := domain.LadoLogSourceInput{
			ID:          domain.LadoLogSourceID(src.ID),
			Kind:        src.Kind,
			DisplayName: src.DisplayName,
			Endpoint:    src.Endpoint,
			TLS:         src.TLS,
			Paused:      src.Paused,
			Targets:     make([]domain.LadoLogTargetInput, 0, len(src.Targets)),
		}
		for _, t := range src.Targets {
			source.Targets = append(source.Targets, domain.LadoLogTargetInput{
				ID:             domain.LadoLogTargetID(t.ID),
				Kind:           domain.LadoLogTargetKind(t.Kind),
				Value:          t.Value,
				ComposeProject: t.ComposeProject,
				Service:        t.Service,
				Paused:         t.Paused,
			})
		}
		in.Sources = append(in.Sources, source)
	}
	return in
}

func writeLadoLogWatchView(w http.ResponseWriter, r *http.Request, view domain.LadoLogWatchView, err error) {
	if err != nil {
		writeLadoLogWatchError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoLogWatchResponse(view))
}

// writeLadoLogWatchError writes the service's apierr error as it is, and
// gives the domain's own errors their codes when they come back unwrapped.
func writeLadoLogWatchError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *domain.LadoLogWatchInvalidError
	switch {
	case errors.As(err, &invalid):
		writeLadoLogInvalid(w, r, invalid.Field, invalid.Message)
	case errors.Is(err, domain.ErrLadoLogSourceNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "LADO_LOG_SOURCE_NOT_FOUND", "Unknown log source", nil)
	case errors.Is(err, domain.ErrLadoLogFingerprintNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "LADO_LOG_FINGERPRINT_NOT_FOUND", "Unknown fingerprint", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}

func writeLadoLogInvalid(w http.ResponseWriter, r *http.Request, field, message string) {
	envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "LADO_LOG_WATCH_INVALID", message, map[string]any{"field": field})
}

func ladoLogWatchResponse(view domain.LadoLogWatchView) LadoLogWatchResponse {
	s := view.Settings
	statuses := make(map[domain.LadoLogSourceID]domain.LadoLogSourceStatus, len(view.Statuses))
	for _, st := range view.Statuses {
		statuses[st.SourceID] = st
	}
	resp := LadoLogWatchResponse{
		ProjectID:              string(s.ProjectID),
		LadoMode:               view.LadoMode,
		Enabled:                s.Enabled,
		Repository:             s.Repository,
		Include:                ladoNonNilStrings(s.Include),
		Exclude:                ladoNonNilStrings(s.Exclude),
		HourlyIssueCap:         s.HourlyIssueCap,
		AutoPlan:               s.AutoPlan,
		AutoPlanDailyCap:       s.AutoPlanDailyCap,
		AutoPlanMinCount:       s.AutoPlanMinCount,
		DisconnectAlertMinutes: s.DisconnectAlertMinutes,
		BurstNotifiedAt:        ladoOptionalUTC(s.BurstNotifiedAt),
		Sources:                make([]LadoLogSourceResponse, 0, len(s.Sources)),
		UpdatedAt:              ladoOptionalUTC(s.UpdatedAt),
	}
	for _, src := range s.Sources {
		kind := src.Kind
		if kind == "" {
			kind = domain.LadoLogSourceKindDockerTCP
		}
		stored := view.TLSStored[src.ID]
		st, known := statuses[src.ID]
		if !known {
			// A source the daemon does not follow yet: off, insecure without
			// TLS.
			st = domain.LadoLogSourceStatus{SourceID: src.ID, State: domain.LadoLogSourceStateOff, Insecure: !src.TLS}
		}
		downSince := st.DownSince
		if downSince.IsZero() {
			downSince = src.DownSince
		}
		targetStatus := make(map[domain.LadoLogTargetID]domain.LadoLogTargetStatus, len(st.Targets))
		for _, ts := range st.Targets {
			targetStatus[ts.TargetID] = ts
		}
		source := LadoLogSourceResponse{
			ID:          string(src.ID),
			Kind:        kind,
			DisplayName: src.DisplayName,
			Endpoint:    src.Endpoint,
			TLS:         src.TLS,
			Paused:      src.Paused,
			TLSStored:   LadoLogTLSStoredResponse{CA: stored.CA, Cert: stored.Cert, Key: stored.Key},
			Status: LadoLogSourceStatusResponse{
				State:     st.State,
				Detail:    ladoLogScrub(st.Detail),
				Insecure:  st.Insecure || !src.TLS,
				DownSince: ladoOptionalUTC(downSince),
			},
			Targets: make([]LadoLogTargetResponse, 0, len(src.Targets)),
		}
		if source.Status.State == "" {
			source.Status.State = domain.LadoLogSourceStateOff
		}
		for _, t := range src.Targets {
			ts := targetStatus[t.ID]
			source.Targets = append(source.Targets, LadoLogTargetResponse{
				ID:             string(t.ID),
				Kind:           string(t.Kind),
				Value:          t.Value,
				ComposeProject: t.ComposeProject,
				Service:        t.Service,
				Paused:         t.Paused,
				Status:         LadoLogTargetStatusResponse{Instances: ts.Instances, LastLineAt: ladoOptionalUTC(ts.LastLineAt)},
			})
		}
		resp.Sources = append(resp.Sources, source)
	}
	return resp
}

func ladoLogFingerprintResponse(fp domain.LadoLogFingerprint) LadoLogFingerprintResponse {
	resp := LadoLogFingerprintResponse{
		ID:                  int64(fp.ID),
		ProjectID:           string(fp.ProjectID),
		Fingerprint:         fp.Fingerprint,
		Service:             fp.Service,
		ExceptionKind:       fp.ExceptionKind,
		Location:            fp.Location,
		Count:               fp.Count,
		FirstSeenAt:         fp.FirstSeenAt.UTC(),
		LastSeenAt:          fp.LastSeenAt.UTC(),
		TaskID:              string(fp.TaskID),
		IssueRef:            string(fp.IssueRef),
		PreviousIssueRef:    string(fp.PreviousIssueRef),
		IssueCreatedAt:      ladoOptionalUTC(fp.IssueCreatedAt),
		AutoPlanRequestedAt: ladoOptionalUTC(fp.AutoPlanRequestedAt),
		AutoPlanHeld:        string(fp.AutoPlanHeld),
		Ignored:             fp.Ignored,
		IgnoredAt:           ladoOptionalUTC(fp.IgnoredAt),
		Instances:           make([]LadoLogInstanceCountResponse, 0, len(fp.Instances)),
	}
	for _, inst := range fp.Instances {
		resp.Instances = append(resp.Instances, LadoLogInstanceCountResponse{
			Instance: inst.Instance, Image: inst.Image, Count: inst.Count, LastSeenAt: inst.LastSeenAt.UTC(),
		})
	}
	return resp
}

// ladoLogCredentialPath matches a path into LADO's Docker credential copies,
// such as one an error message names.
var ladoLogCredentialPath = regexp.MustCompile(`[^\s:"'(),]*credentials[/\\]docker[/\\][^\s:"'(),]*`)

// ladoLogScrub keeps the paths of the TLS copies out of a status detail.
func ladoLogScrub(detail string) string {
	return ladoLogCredentialPath.ReplaceAllString(detail, "<TLS 사본>")
}

func ladoNonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
