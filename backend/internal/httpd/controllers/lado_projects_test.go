package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
)

type fakeLadoProjectService struct {
	settings  domain.LadoProjectSettings
	err       error
	gotID     domain.ProjectID
	gotUpdate ladoproject.UpdateInput
	updates   int
}

func (f *fakeLadoProjectService) Settings(_ context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	f.gotID = projectID
	return f.settings, f.err
}

func (f *fakeLadoProjectService) UpdateSettings(_ context.Context, projectID domain.ProjectID, in ladoproject.UpdateInput) (domain.LadoProjectSettings, error) {
	f.updates++
	f.gotID = projectID
	f.gotUpdate = in
	return f.settings, f.err
}

func newLadoTestServer(t *testing.T, deps httpd.APIDeps) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func decodeLadoSettings(t *testing.T, body []byte) controllers.LadoProjectSettingsResponse {
	t.Helper()
	var resp controllers.LadoProjectSettingsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return resp
}

func TestLadoAPI_GetProjectSettings(t *testing.T) {
	svc := &fakeLadoProjectService{settings: domain.LadoProjectSettings{ProjectID: "mer", LadoMode: true}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoProjects: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/settings", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if got := decodeLadoSettings(t, body); got != (controllers.LadoProjectSettingsResponse{ProjectID: "mer", LadoMode: true}) {
		t.Fatalf("response = %+v", got)
	}
	if svc.gotID != "mer" {
		t.Fatalf("service asked for %q, want mer", svc.gotID)
	}
}

func TestLadoAPI_PutProjectSettings(t *testing.T) {
	svc := &fakeLadoProjectService{settings: domain.LadoProjectSettings{ProjectID: "mer", LadoMode: true}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoProjects: svc})

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/settings", `{"ladoMode":true}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if got := decodeLadoSettings(t, body); !got.LadoMode || got.ProjectID != "mer" {
		t.Fatalf("response = %+v", got)
	}
	if svc.gotID != "mer" || svc.gotUpdate.LadoMode == nil || !*svc.gotUpdate.LadoMode {
		t.Fatalf("service got project %q update %+v, want mer with ladoMode true", svc.gotID, svc.gotUpdate)
	}
}

func TestLadoAPI_PutLeavesAMissingLadoModeToTheService(t *testing.T) {
	svc := &fakeLadoProjectService{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoProjects: svc})

	doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/settings", `{}`)
	if svc.updates != 1 || svc.gotUpdate.LadoMode != nil {
		t.Fatalf("service calls = %d with %+v, want one call without ladoMode", svc.updates, svc.gotUpdate)
	}
}

func TestLadoAPI_PutRejectsMalformedBodies(t *testing.T) {
	for _, body := range []string{`not json`, `{"ladoMode":true,"commands":[]}`} {
		t.Run(body, func(t *testing.T) {
			svc := &fakeLadoProjectService{}
			srv := newLadoTestServer(t, httpd.APIDeps{LadoProjects: svc})

			resp, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/settings", body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, body %s", status, resp)
			}
			if svc.updates != 0 {
				t.Fatalf("service calls = %d, want none", svc.updates)
			}
		})
	}
}

func TestLadoAPI_ServiceErrorsKeepTheirEnvelope(t *testing.T) {
	svc := &fakeLadoProjectService{err: apierr.Conflict("LADO_MODE_UNAVAILABLE", "not yet", nil)}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoProjects: svc})

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/settings", `{"ladoMode":true}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, body %s", status, body)
	}
	var apiErr envelope.APIError
	if err := json.Unmarshal(body, &apiErr); err != nil || apiErr.Code != "LADO_MODE_UNAVAILABLE" {
		t.Fatalf("envelope = %s (%v), want code LADO_MODE_UNAVAILABLE", body, err)
	}
}

func TestLadoAPI_WithoutAServiceAnswersNotImplemented(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, method := range []string{"GET", "PUT"} {
		body, status, _ := doRequest(t, srv, method, "/api/v1/lado/projects/mer/settings", `{"ladoMode":true}`)
		if status != http.StatusNotImplemented {
			t.Fatalf("%s status = %d, body %s", method, status, body)
		}
	}
}
