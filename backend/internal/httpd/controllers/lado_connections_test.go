package controllers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoconnect"
)

type fakeLadoConnectionService struct {
	check      ladoconnect.CheckResult
	err        error
	gotCheck   ladoconnect.CheckInput
	gotConnect ladoconnect.ConnectInput
	gotHost    string
	gotToken   string
	deleted    string
}

func (f *fakeLadoConnectionService) Check(_ context.Context, in ladoconnect.CheckInput) (ladoconnect.CheckResult, error) {
	f.gotCheck = in
	return f.check, f.err
}

func (f *fakeLadoConnectionService) Connect(_ context.Context, in ladoconnect.ConnectInput) (ladoconnect.ConnectResult, error) {
	f.gotConnect = in
	return ladoconnect.ConnectResult{ProjectID: "demo", Outcome: ladoconnect.ConnectCloned}, f.err
}

func (f *fakeLadoConnectionService) Credentials(context.Context) ([]ladoconnect.StoredCredential, error) {
	return []ladoconnect.StoredCredential{
		{Host: "github.com", Provider: domain.TrackerProviderGitHub},
		{Host: "gitlab.example.com", Provider: domain.TrackerProviderGitLab},
	}, f.err
}

func (f *fakeLadoConnectionService) StoreCredential(_ context.Context, host, token string) (ladoconnect.StoredCredential, string, error) {
	f.gotHost, f.gotToken = host, token
	return ladoconnect.StoredCredential{Host: host, Provider: domain.TrackerProviderGitLab}, "ljy", f.err
}

func (f *fakeLadoConnectionService) DeleteCredential(_ context.Context, host string) error {
	f.deleted = host
	return f.err
}

func TestLadoAPI_CheckConnection(t *testing.T) {
	svc := &fakeLadoConnectionService{check: ladoconnect.CheckResult{
		Repository: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
		Host:       "github.com",
		Token: ladoconnect.TokenCheck{Entered: true, Login: "ljy", Permissions: []ports.LadoPermission{
			{Name: ports.LadoPermissionRepository, Status: ports.LadoPermissionOK},
			{Name: ports.LadoPermissionIssues, Status: ports.LadoPermissionUnverified, Detail: "needs Issues: Read and write"},
		}},
		SSH:     ladoconnect.SSHCheck{Problem: "ssh: connect to host github.com port 22: timed out", ProblemCode: ladoconnect.ProblemSSHFailed},
		Project: &ladoconnect.LinkedProject{ID: "product", Name: "Product", Path: "/src/product", Repository: "back", RepoPath: "/src/product/back"},
		Ready:   false,
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoConnections: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/connections/check", `{"url":"git@github.com:acme/demo.git","token":"tok-secret","sshKeyPath":"~/.ssh/id"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	resp := decodeLadoJSON[controllers.LadoConnectionCheckResponse](t, body)
	if resp.Provider != "github" || resp.Host != "github.com" || resp.Repository != "acme/demo" || resp.Ready ||
		!resp.Token.Entered || resp.Token.Login != "ljy" || len(resp.Token.Permissions) != 2 ||
		resp.Token.Permissions[1] != (controllers.LadoPermissionResponse{Name: "issues", Status: "unverified", Detail: "needs Issues: Read and write"}) ||
		resp.SSH != (controllers.LadoSSHCheckResponse{Problem: "ssh: connect to host github.com port 22: timed out", ProblemCode: "ssh_failed"}) ||
		resp.Project == nil || *resp.Project != (controllers.LadoLinkedProjectResponse{ID: "product", Name: "Product", Path: "/src/product", Repository: "back"}) {
		t.Fatalf("response = %+v", resp)
	}
	if svc.gotCheck != (ladoconnect.CheckInput{URL: "git@github.com:acme/demo.git", Token: "tok-secret", SSHKeyPath: "~/.ssh/id"}) {
		t.Fatalf("service got %+v", svc.gotCheck)
	}
	if strings.Contains(string(body), "tok-secret") {
		t.Fatalf("response %s carries the token", body)
	}
}

func TestLadoAPI_CreateConnection(t *testing.T) {
	svc := &fakeLadoConnectionService{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoConnections: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/connections", `{"url":"git@github.com:acme/demo.git","destinationParent":"~/ao/projects"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if resp := decodeLadoJSON[controllers.LadoConnectionResponse](t, body); resp != (controllers.LadoConnectionResponse{ProjectID: "demo", Outcome: "cloned"}) {
		t.Fatalf("response = %+v", resp)
	}
	if svc.gotConnect.URL != "git@github.com:acme/demo.git" || svc.gotConnect.DestinationParent != "~/ao/projects" || svc.gotConnect.Placement != "" {
		t.Fatalf("service got %+v", svc.gotConnect)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/connections",
		`{"url":"git@github.com:acme/front.git","placement":"workspace","workspaceId":"product","destinationParent":"","workspaceName":""}`)
	if status != http.StatusOK {
		t.Fatalf("workspace status = %d, body %s", status, body)
	}
	if svc.gotConnect.Placement != ladoconnect.PlaceWorkspace || svc.gotConnect.WorkspaceID != "product" {
		t.Fatalf("service got %+v, want the workspace placement", svc.gotConnect)
	}
	body, _, _ = doRequest(t, srv, "POST", "/api/v1/lado/connections",
		`{"url":"git@github.com:acme/back.git","placement":"new_workspace","destinationParent":"~/src","workspaceName":"product"}`)
	if svc.gotConnect.Placement != ladoconnect.PlaceNewWorkspace || svc.gotConnect.WorkspaceName != "product" || svc.gotConnect.DestinationParent != "~/src" {
		t.Fatalf("service got %+v, want the new workspace placement; body %s", svc.gotConnect, body)
	}

	svc.err = apierr.Conflict("LADO_CONNECTION_NOT_READY", "github.com rejected the token.", nil)
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/connections", `{"url":"git@github.com:acme/demo.git"}`)
	if status != http.StatusConflict {
		t.Fatalf("not-ready status = %d, body %s", status, body)
	}
	if body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/connections", `{"url":1}`); status != http.StatusBadRequest {
		t.Fatalf("bad body status = %d, body %s", status, body)
	}
}

func TestLadoAPI_Credentials(t *testing.T) {
	svc := &fakeLadoConnectionService{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoConnections: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/credentials", "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, body %s", status, body)
	}
	listed := decodeLadoJSON[controllers.LadoCredentialListResponse](t, body)
	if len(listed.Credentials) != 2 || listed.Credentials[1] != (controllers.LadoCredentialResponse{Host: "gitlab.example.com", Provider: "gitlab"}) {
		t.Fatalf("credentials = %+v", listed.Credentials)
	}

	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/lado/credentials/gitlab.example.com", `{"token":"glpat-secret"}`)
	if status != http.StatusOK {
		t.Fatalf("store status = %d, body %s", status, body)
	}
	if stored := decodeLadoJSON[controllers.LadoCredentialResponse](t, body); stored.Login != "ljy" || stored.Host != "gitlab.example.com" {
		t.Fatalf("stored = %+v", stored)
	}
	if svc.gotHost != "gitlab.example.com" || svc.gotToken != "glpat-secret" || strings.Contains(string(body), "glpat-secret") {
		t.Fatalf("service got %q %q; response %s", svc.gotHost, svc.gotToken, body)
	}

	if body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/lado/credentials/gitlab.example.com", ""); status != http.StatusNoContent || svc.deleted != "gitlab.example.com" {
		t.Fatalf("delete status = %d, deleted %q, body %s", status, svc.deleted, body)
	}
}

func TestLadoAPI_ConnectionRoutesWithoutAServiceAnswerNotImplemented(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, route := range []struct{ method, path, body string }{
		{"POST", "/api/v1/lado/connections/check", `{"url":"git@github.com:a/b.git"}`},
		{"POST", "/api/v1/lado/connections", `{"url":"git@github.com:a/b.git"}`},
		{"GET", "/api/v1/lado/credentials", ""},
		{"PUT", "/api/v1/lado/credentials/github.com", `{"token":"x"}`},
		{"DELETE", "/api/v1/lado/credentials/github.com", ""},
	} {
		if body, status, _ := doRequest(t, srv, route.method, route.path, route.body); status != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, body %s", route.method, route.path, status, body)
		}
	}
}
