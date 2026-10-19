package github

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
	return domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}
}

func TestLadoCreateIssuePostsTitleAndBody(t *testing.T) {
	f := newFakeGH(t)
	f.on("POST", "/repos/acme/demo/issues", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":12,"title":"Add login","body":"Details\n\n<!-- lado-task:t1 -->","state":"open","html_url":"https://github.com/acme/demo/issues/12"}`))
	})
	issue, err := newTrackerForTest(t, f).LadoIssues().CreateIssue(ctx(), ladoTestRepo(), "Add login", "Details\n\n<!-- lado-task:t1 -->")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	want := domain.Issue{
		ID:    domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme/demo#12"},
		Title: "Add login",
		Body:  "Details\n\n<!-- lado-task:t1 -->",
		State: domain.IssueOpen,
		URL:   "https://github.com/acme/demo/issues/12",
	}
	if issue.ID != want.ID || issue.Title != want.Title || issue.Body != want.Body || issue.State != want.State || issue.URL != want.URL {
		t.Fatalf("issue = %+v, want %+v", issue, want)
	}
	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(calls[0].Body), &sent); err != nil {
		t.Fatalf("request body %q: %v", calls[0].Body, err)
	}
	if len(sent) != 2 || sent["title"] != "Add login" || sent["body"] != "Details\n\n<!-- lado-task:t1 -->" {
		t.Fatalf("request body = %v, want only title and body", sent)
	}
}

func TestLadoCreateIssueAnswersWithoutAnIssueAreNotCreated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		headers map[string]string
		message string
	}{
		{"bad token", http.StatusUnauthorized, nil, "Bad credentials"},
		{"no permission", http.StatusForbidden, nil, "Resource not accessible by personal access token"},
		{"secondary rate limit", http.StatusForbidden, map[string]string{"Retry-After": "60"}, "You have exceeded a secondary rate limit"},
		{"repository hidden", http.StatusNotFound, nil, "Not Found"},
		{"issues disabled", http.StatusGone, nil, "Issues are disabled for this repo"},
		{"validation", http.StatusUnprocessableEntity, nil, "Validation Failed"},
		{"rate limit", http.StatusTooManyRequests, nil, "API rate limit exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on("POST", "/repos/acme/demo/issues", func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"` + tc.message + `"}`))
			})
			_, err := newTrackerForTest(t, f).LadoIssues().CreateIssue(ctx(), ladoTestRepo(), "Add login", "")
			if !errors.Is(err, ports.ErrLadoIssueNotCreated) {
				t.Fatalf("err = %v, want ErrLadoIssueNotCreated", err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("err = %v, want GitHub's message %q", err, tc.message)
			}
		})
	}
}

func TestLadoCreateIssueUnknownOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"server error", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"Server Error"}`))
		}},
		{"bad gateway", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}},
		{"created with an unreadable answer", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":`))
		}},
		{"connection dropped", func(w http.ResponseWriter, r *http.Request) {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("response writer cannot hijack")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			_ = conn.Close()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on("POST", "/repos/acme/demo/issues", tc.handler)
			_, err := newTrackerForTest(t, f).LadoIssues().CreateIssue(ctx(), ladoTestRepo(), "Add login", "")
			if err == nil {
				t.Fatal("CreateIssue succeeded, want an error")
			}
			if errors.Is(err, ports.ErrLadoIssueNotCreated) {
				t.Fatalf("err = %v marks the issue as not created, but the outcome is unknown", err)
			}
		})
	}
}

type failingLadoTokens struct{}

func (failingLadoTokens) Token(context.Context) (string, error) {
	return "", ErrNoToken
}

func TestLadoCreateIssueSendsNothingWithoutAToken(t *testing.T) {
	f := newFakeGH(t)
	tr := newTrackerForTest(t, f)
	tr.tokens = failingLadoTokens{}
	_, err := tr.LadoIssues().CreateIssue(ctx(), ladoTestRepo(), "Add login", "")
	if !errors.Is(err, ports.ErrLadoIssueNotCreated) || !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrLadoIssueNotCreated wrapping ErrNoToken", err)
	}
	if len(f.calls()) != 0 {
		t.Fatalf("calls = %d, want none", len(f.calls()))
	}
}

func TestLadoCreateIssueRefusesOtherRepositories(t *testing.T) {
	f := newFakeGH(t)
	for _, repo := range []domain.TrackerRepo{
		{Provider: domain.TrackerProviderGitLab, Native: "acme/demo"},
		{Provider: domain.TrackerProviderGitHub, Native: "acme"},
	} {
		if _, err := newTrackerForTest(t, f).LadoIssues().CreateIssue(ctx(), repo, "Add login", ""); !errors.Is(err, ports.ErrLadoIssueNotCreated) {
			t.Fatalf("CreateIssue(%+v) err = %v, want ErrLadoIssueNotCreated", repo, err)
		}
	}
}

// ghAccessFixture serves the calls CheckAccess makes.
type ghAccessFixture struct {
	scopes      *string // nil: a fine-grained token, which sends no X-OAuth-Scopes header
	private     bool
	hasIssues   bool
	push        bool
	repoStatus  int
	issuesProbe int
	pullsProbe  int
	rulesProbe  int
}

func (fx ghAccessFixture) serve(t *testing.T) *fakeGH {
	t.Helper()
	f := newFakeGH(t)
	f.on("GET", "/user", func(w http.ResponseWriter, r *http.Request) {
		if fx.scopes != nil {
			w.Header().Set("X-OAuth-Scopes", *fx.scopes)
		}
		_, _ = w.Write([]byte(`{"login":"ljy"}`))
	})
	f.on("GET", "/repos/acme/demo", func(w http.ResponseWriter, r *http.Request) {
		if fx.repoStatus != 0 && fx.repoStatus != http.StatusOK {
			w.WriteHeader(fx.repoStatus)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"private":        fx.private,
			"has_issues":     fx.hasIssues,
			"default_branch": "main",
			"permissions":    map[string]bool{"pull": true, "push": fx.push},
		})
	})
	probe := func(path string, status int, body string) {
		f.on("GET", path, func(w http.ResponseWriter, r *http.Request) {
			if status != 0 && status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
				return
			}
			_, _ = w.Write([]byte(body))
		})
	}
	probe("/repos/acme/demo/issues", fx.issuesProbe, `[]`)
	probe("/repos/acme/demo/pulls", fx.pullsProbe, `[]`)
	probe("/repos/acme/demo/rules/branches/main", fx.rulesProbe, `[]`)
	return f
}

func ladoPermissionStatuses(access ports.LadoRepoAccess) map[ports.LadoPermissionName]ports.LadoPermissionStatus {
	out := map[ports.LadoPermissionName]ports.LadoPermissionStatus{}
	for _, p := range access.Permissions {
		out[p.Name] = p.Status
	}
	return out
}

func strPtr(s string) *string { return &s }

func TestLadoCheckAccess(t *testing.T) {
	const (
		ok         = ports.LadoPermissionOK
		missing    = ports.LadoPermissionMissing
		unverified = ports.LadoPermissionUnverified
	)
	for _, tc := range []struct {
		name string
		fx   ghAccessFixture
		want map[ports.LadoPermissionName]ports.LadoPermissionStatus
	}{
		{
			name: "classic token with repo on a private repository",
			fx:   ghAccessFixture{scopes: strPtr("repo, read:org"), private: true, hasIssues: true, push: true},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": ok, "pull_requests": ok, "branch_rules": ok, "commit_statuses": ok},
		},
		{
			name: "classic token without scopes on a public repository",
			fx:   ghAccessFixture{scopes: strPtr(""), hasIssues: true, push: true},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": missing, "pull_requests": ok, "branch_rules": ok, "commit_statuses": missing},
		},
		{
			name: "classic token with public_repo on a public repository",
			fx:   ghAccessFixture{scopes: strPtr("public_repo"), hasIssues: true, push: true},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": ok, "pull_requests": ok, "branch_rules": ok, "commit_statuses": ok},
		},
		{
			name: "classic token with repo but read-only access",
			fx:   ghAccessFixture{scopes: strPtr("repo"), private: true, hasIssues: true},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": ok, "pull_requests": ok, "branch_rules": ok, "commit_statuses": missing},
		},
		{
			name: "issues turned off",
			fx:   ghAccessFixture{scopes: strPtr("repo"), private: true, push: true},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": missing, "pull_requests": ok, "branch_rules": ok, "commit_statuses": ok},
		},
		{
			name: "fine-grained token that can read issues and pulls",
			fx:   ghAccessFixture{private: true, hasIssues: true, push: true},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": unverified, "pull_requests": ok, "branch_rules": ok, "commit_statuses": unverified},
		},
		{
			name: "fine-grained token without issue or pull access",
			fx:   ghAccessFixture{private: true, hasIssues: true, push: true, issuesProbe: http.StatusForbidden, pullsProbe: http.StatusForbidden, rulesProbe: http.StatusForbidden},
			want: map[ports.LadoPermissionName]ports.LadoPermissionStatus{"repository": ok, "issues": missing, "pull_requests": missing, "branch_rules": missing, "commit_statuses": unverified},
		},
		{
			name: "repository the token cannot see",
			fx:   ghAccessFixture{scopes: strPtr("repo"), repoStatus: http.StatusNotFound},
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
			if tc.fx.repoStatus == 0 && access.DefaultBranch != "main" {
				t.Fatalf("default branch = %q, want main", access.DefaultBranch)
			}
		})
	}
}

func TestLadoCheckAccessRejectedToken(t *testing.T) {
	f := newFakeGH(t)
	f.on("GET", "/user", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
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
	f := newFakeGH(t)
	f.on("GET", "/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"ljy"}`))
	})
	login, err := newTrackerForTest(t, f).LadoIssues().Login(ctx())
	if err != nil || login != "ljy" {
		t.Fatalf("Login = %q, %v; want ljy", login, err)
	}
}
