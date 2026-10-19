package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func ladoTestRepo() domain.TrackerRepo {
	return domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project"}
}

func TestLadoCreateIssuePostsTitleAndDescription(t *testing.T) {
	f := newFakeGL(t)
	f.on("POST", "/projects/group/project/issues", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"iid":7,"title":"Add login","description":"Details","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/7"}`))
	})
	issue, err := newTrackerForTest(t, f).LadoIssues().CreateIssue(ctx(), ladoTestRepo(), "Add login", "Details")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issue.ID != (domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#7"}) ||
		issue.Title != "Add login" || issue.Body != "Details" || issue.State != domain.IssueOpen ||
		issue.URL != "https://gitlab.com/group/project/-/issues/7" {
		t.Fatalf("issue = %+v", issue)
	}
	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(calls[0].Body), &sent); err != nil {
		t.Fatalf("request body %q: %v", calls[0].Body, err)
	}
	if len(sent) != 2 || sent["title"] != "Add login" || sent["description"] != "Details" {
		t.Fatalf("request body = %v, want only title and description", sent)
	}
}

func TestLadoCreateIssueOnASelfManagedHost(t *testing.T) {
	defaultSrv := newFakeGL(t)
	selfManaged := newFakeGL(t)
	selfManaged.on("POST", "/projects/group/project/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tkn-host" {
			t.Errorf("Authorization = %q, want the host token", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"iid":3,"title":"Add login","state":"opened","web_url":"https://gitlab.example.com/group/project/-/issues/3"}`))
	})
	tr := newHostAwareTrackerForTest(t, defaultSrv, map[string]struct {
		server *fakeGL
		token  string
	}{"gitlab.example.com": {server: selfManaged, token: "tkn-host"}})
	repo := ladoTestRepo()
	repo.Host = "gitlab.example.com"
	issue, err := tr.LadoIssues().CreateIssue(ctx(), repo, "Add login", "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issue.ID != (domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#3", Host: "gitlab.example.com"}) {
		t.Fatalf("issue id = %+v, want the self-managed host kept", issue.ID)
	}
	if len(defaultSrv.calls()) != 0 {
		t.Fatalf("gitlab.com got %d calls, want none", len(defaultSrv.calls()))
	}
}

func TestLadoCreateIssueAnswersWithoutAnIssueAreNotCreated(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newFakeGL(t)
			f.on("POST", "/projects/group/project/issues", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"refused here"}`))
			})
			_, err := newTrackerForTest(t, f).LadoIssues().CreateIssue(ctx(), ladoTestRepo(), "Add login", "")
			if !errors.Is(err, ports.ErrLadoIssueNotCreated) {
				t.Fatalf("err = %v, want ErrLadoIssueNotCreated", err)
			}
		})
	}
}

func TestLadoCreateIssueUnknownOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"server error", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }},
		{"created with an unreadable answer", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"iid":`))
		}},
		{"connection dropped", func(w http.ResponseWriter, r *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			_ = conn.Close()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGL(t)
			f.on("POST", "/projects/group/project/issues", tc.handler)
			_, err := newTrackerForTest(t, f).LadoIssues().CreateIssue(ctx(), ladoTestRepo(), "Add login", "")
			if err == nil || errors.Is(err, ports.ErrLadoIssueNotCreated) {
				t.Fatalf("err = %v, want an error with an unknown outcome", err)
			}
		})
	}
}

type failingLadoTokens struct{}

func (failingLadoTokens) Token(context.Context) (string, error) {
	return "", ErrNoToken
}

func TestLadoCreateIssueSendsNothingWithoutAToken(t *testing.T) {
	f := newFakeGL(t)
	tr := newTrackerForTest(t, f)
	tr.defaultHost.tokens = failingLadoTokens{}
	_, err := tr.LadoIssues().CreateIssue(ctx(), ladoTestRepo(), "Add login", "")
	if !errors.Is(err, ports.ErrLadoIssueNotCreated) || !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrLadoIssueNotCreated wrapping ErrNoToken", err)
	}
	if len(f.calls()) != 0 {
		t.Fatalf("calls = %d, want none", len(f.calls()))
	}
}

func TestLadoCreateIssueRefusesOtherRepositories(t *testing.T) {
	f := newFakeGL(t)
	for _, repo := range []domain.TrackerRepo{
		{Provider: domain.TrackerProviderGitHub, Native: "group/project"},
		{Provider: domain.TrackerProviderGitLab, Native: "group/project", Host: "not-allowed.example.com"},
	} {
		if _, err := newTrackerForTest(t, f).LadoIssues().CreateIssue(ctx(), repo, "Add login", ""); !errors.Is(err, ports.ErrLadoIssueNotCreated) {
			t.Fatalf("CreateIssue(%+v) err = %v, want ErrLadoIssueNotCreated", repo, err)
		}
	}
}

// glAccessFixture serves the calls CheckAccess makes.
type glAccessFixture struct {
	scopes        []string // nil: GitLab does not report the token's scopes
	projectStatus int
	issuesLevel   string
	projectAccess int
	groupAccess   int
	mrProbe       int
	rulesProbe    int
}

func (fx glAccessFixture) serve(t *testing.T) *fakeGL {
	t.Helper()
	f := newFakeGL(t)
	f.on("GET", "/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"username":"ljy"}`))
	})
	f.on("GET", "/personal_access_tokens/self", func(w http.ResponseWriter, r *http.Request) {
		if fx.scopes == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"scopes": fx.scopes})
	})
	f.on("GET", "/projects/group/project", func(w http.ResponseWriter, r *http.Request) {
		if fx.projectStatus != 0 && fx.projectStatus != http.StatusOK {
			w.WriteHeader(fx.projectStatus)
			_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))
			return
		}
		access := func(level int) any {
			if level == 0 {
				return nil
			}
			return map[string]int{"access_level": level}
		}
		level := fx.issuesLevel
		if level == "" {
			level = "enabled"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"default_branch":      "main",
			"issues_access_level": level,
			"permissions":         map[string]any{"project_access": access(fx.projectAccess), "group_access": access(fx.groupAccess)},
		})
	})
	probe := func(path string, status int) {
		f.on("GET", path, func(w http.ResponseWriter, r *http.Request) {
			if status != 0 && status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"403 Forbidden"}`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		})
	}
	probe("/projects/group/project/merge_requests", fx.mrProbe)
	probe("/projects/group/project/protected_branches", fx.rulesProbe)
	return f
}

func ladoPermissionStatuses(access ports.LadoRepoAccess) map[ports.LadoPermissionName]ports.LadoPermissionStatus {
	out := map[ports.LadoPermissionName]ports.LadoPermissionStatus{}
	for _, p := range access.Permissions {
		out[p.Name] = p.Status
	}
	return out
}

func TestLadoCheckAccess(t *testing.T) {
	const (
		ok         = ports.LadoPermissionOK
		missing    = ports.LadoPermissionMissing
		unverified = ports.LadoPermissionUnverified
	)
	for _, tc := range []struct {
		name string
		fx   glAccessFixture
		want map[ports.LadoPermissionName]ports.LadoPermissionStatus
	}{
		{
			name: "api token with the developer role",
			fx:   glAccessFixture{scopes: []string{"api"}, projectAccess: 30},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": ok, "pull_requests": ok, "branch_rules": ok, "commit_statuses": ok},
		},
		{
			name: "read_api token",
			fx:   glAccessFixture{scopes: []string{"read_api"}, projectAccess: 30},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": missing, "pull_requests": ok, "branch_rules": ok, "commit_statuses": missing},
		},
		{
			name: "scopes not reported",
			fx:   glAccessFixture{projectAccess: 40},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": unverified, "pull_requests": ok, "branch_rules": ok, "commit_statuses": unverified},
		},
		{
			name: "reporter role",
			fx:   glAccessFixture{scopes: []string{"api"}, projectAccess: 20, rulesProbe: http.StatusForbidden},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": ok, "pull_requests": ok, "branch_rules": missing, "commit_statuses": missing},
		},
		{
			name: "developer through the group",
			fx:   glAccessFixture{scopes: []string{"api"}, groupAccess: 30},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": ok, "pull_requests": ok, "branch_rules": ok, "commit_statuses": ok},
		},
		{
			name: "issues turned off",
			fx:   glAccessFixture{scopes: []string{"api"}, projectAccess: 30, issuesLevel: "disabled"},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": missing, "pull_requests": ok, "branch_rules": ok, "commit_statuses": ok},
		},
		{
			name: "project the token cannot see",
			fx:   glAccessFixture{scopes: []string{"api"}, projectStatus: http.StatusNotFound},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": missing},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.fx.serve(t)
			access, err := newTrackerForTest(t, f).LadoIssues().CheckAccess(ctx(), ladoTestRepo())
			if err != nil {
				t.Fatalf("CheckAccess: %v", err)
			}
			if access.Login != "ljy" {
				t.Fatalf("login = %q, want ljy", access.Login)
			}
			got := ladoPermissionStatuses(access)
			if len(got) != len(tc.want) {
				t.Fatalf("permissions = %v, want %v", got, tc.want)
			}
			for name, status := range tc.want {
				if got[name] != status {
					t.Fatalf("permissions = %v, want %v", got, tc.want)
				}
			}
			for _, p := range access.Permissions {
				if p.Status != ok && p.Detail == "" {
					t.Fatalf("%s is %s without a detail", p.Name, p.Status)
				}
			}
		})
	}
}

func TestLadoCheckAccessRejectedToken(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/user", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
	})
	_, err := newTrackerForTest(t, f).LadoIssues().CheckAccess(ctx(), ladoTestRepo())
	if !errors.Is(err, ports.ErrLadoTokenRejected) {
		t.Fatalf("err = %v, want ErrLadoTokenRejected", err)
	}
	if strings.Contains(err.Error(), "tkn-test") {
		t.Fatalf("err = %v leaks the token", err)
	}
}

func TestLadoLogin(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"username":"ljy"}`))
	})
	login, err := newTrackerForTest(t, f).LadoIssues().Login(ctx())
	if err != nil || login != "ljy" {
		t.Fatalf("Login = %q, %v; want ljy", login, err)
	}
}
