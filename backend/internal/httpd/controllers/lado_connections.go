package controllers

// LADO fork: repository connection and host token routes
// (docs/LJY-PRD/TDD.md §13).

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoconnect"
)

// LadoConnectionService is the controller-facing repository connection
// contract.
type LadoConnectionService interface {
	Check(ctx context.Context, in ladoconnect.CheckInput) (ladoconnect.CheckResult, error)
	Connect(ctx context.Context, in ladoconnect.ConnectInput) (ladoconnect.ConnectResult, error)
	Credentials(ctx context.Context) ([]ladoconnect.StoredCredential, error)
	StoreCredential(ctx context.Context, host, token string) (ladoconnect.StoredCredential, string, error)
	DeleteCredential(ctx context.Context, host string) error
}

const (
	ladoConnectionsCheckPath = "/api/v1/lado/connections/check"
	ladoConnectionsPath      = "/api/v1/lado/connections"
	ladoCredentialsPath      = "/api/v1/lado/credentials"        //nolint:gosec // G101: a route path, not a credential.
	ladoCredentialPath       = "/api/v1/lado/credentials/{host}" //nolint:gosec // G101: a route path, not a credential.
	maxLadoConnectionBytes   = 16 << 10
)

func (c *LadoController) checkConnection(w http.ResponseWriter, r *http.Request) {
	if c.Connections == nil {
		apispec.NotImplemented(w, r, "POST", ladoConnectionsCheckPath)
		return
	}
	var req CheckLadoConnectionRequest
	if !decodeLadoBody(w, r, maxLadoConnectionBytes, &req) {
		return
	}
	result, err := c.Connections.Check(r.Context(), ladoconnect.CheckInput{URL: req.URL, Token: req.Token, SSHKeyPath: req.SSHKeyPath})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ladoConnectionCheckResponse(result))
}

func (c *LadoController) createConnection(w http.ResponseWriter, r *http.Request) {
	if c.Connections == nil {
		apispec.NotImplemented(w, r, "POST", ladoConnectionsPath)
		return
	}
	var req CreateLadoConnectionRequest
	if !decodeLadoBody(w, r, maxLadoConnectionBytes, &req) {
		return
	}
	result, err := c.Connections.Connect(r.Context(), ladoconnect.ConnectInput{
		CheckInput:        ladoconnect.CheckInput{URL: req.URL, Token: req.Token, SSHKeyPath: req.SSHKeyPath},
		Placement:         ladoconnect.Placement(req.Placement),
		DestinationParent: req.DestinationParent,
		WorkspaceName:     req.WorkspaceName,
		WorkspaceID:       domain.ProjectID(req.WorkspaceID),
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoConnectionResponse{ProjectID: string(result.ProjectID), Outcome: string(result.Outcome)})
}

func (c *LadoController) listCredentials(w http.ResponseWriter, r *http.Request) {
	if c.Connections == nil {
		apispec.NotImplemented(w, r, "GET", ladoCredentialsPath)
		return
	}
	stored, err := c.Connections.Credentials(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := LadoCredentialListResponse{Credentials: make([]LadoCredentialResponse, 0, len(stored))}
	for _, credential := range stored {
		resp.Credentials = append(resp.Credentials, LadoCredentialResponse{Host: credential.Host, Provider: string(credential.Provider)})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) storeCredential(w http.ResponseWriter, r *http.Request) {
	if c.Connections == nil {
		apispec.NotImplemented(w, r, "PUT", ladoCredentialPath)
		return
	}
	var req StoreLadoCredentialRequest
	if !decodeLadoBody(w, r, maxLadoConnectionBytes, &req) {
		return
	}
	stored, login, err := c.Connections.StoreCredential(r.Context(), chi.URLParam(r, "host"), req.Token)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LadoCredentialResponse{Host: stored.Host, Provider: string(stored.Provider), Login: login})
}

func (c *LadoController) deleteCredential(w http.ResponseWriter, r *http.Request) {
	if c.Connections == nil {
		apispec.NotImplemented(w, r, "DELETE", ladoCredentialPath)
		return
	}
	if err := c.Connections.DeleteCredential(r.Context(), chi.URLParam(r, "host")); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func ladoConnectionCheckResponse(result ladoconnect.CheckResult) LadoConnectionCheckResponse {
	resp := LadoConnectionCheckResponse{
		Provider:   string(result.Repository.Provider),
		Host:       result.Host,
		Repository: result.Repository.Native,
		Token: LadoTokenCheckResponse{
			Entered:       result.Token.Entered,
			Stored:        result.Token.Stored,
			Login:         result.Token.Login,
			Permissions:   make([]LadoPermissionResponse, 0, len(result.Token.Permissions)),
			Problem:       result.Token.Problem,
			ProblemCode:   result.Token.ProblemCode,
			ProblemDetail: result.Token.ProblemDetail,
		},
		SSH: LadoSSHCheckResponse{
			OK: result.SSH.OK, Problem: result.SSH.Problem,
			ProblemCode: result.SSH.ProblemCode, ProblemDetail: result.SSH.ProblemDetail,
		},
		Ready: result.Ready,
	}
	for _, p := range result.Token.Permissions {
		resp.Token.Permissions = append(resp.Token.Permissions, LadoPermissionResponse{Name: string(p.Name), Status: string(p.Status), Detail: p.Detail})
	}
	if result.Project != nil {
		resp.Project = &LadoLinkedProjectResponse{ID: string(result.Project.ID), Name: result.Project.Name, Path: result.Project.Path, Repository: result.Project.Repository}
	}
	return resp
}

// decodeLadoBody decodes a size-limited JSON body strictly, answering 400 when
// it cannot.
func decodeLadoBody(w http.ResponseWriter, r *http.Request, limit int64, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := decodeJSONStrict(r, out); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return false
	}
	return true
}
