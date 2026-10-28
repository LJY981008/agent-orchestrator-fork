package github

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ladoIntegrationToken is a synthetic token; the tests check that no error
// repeats it.
const ladoIntegrationToken = "ghp_LadoSecretToken0123456789abcdefghijk"

// ladoSeenRequest is one request a LADO integration test server got, with
// its path as sent on the wire.
type ladoSeenRequest struct {
	Method, Path, Query, Auth, Body string
}

// newLadoIntegrationServer serves handle and returns a tracker pointed at it
// and the requests it got.
func newLadoIntegrationServer(t *testing.T, handle http.HandlerFunc) (*Tracker, func() []ladoSeenRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen []ladoSeenRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, ladoSeenRequest{
			Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery,
			Auth: r.Header.Get("Authorization"), Body: string(body),
		})
		mu.Unlock()
		handle(w, r)
	}))
	t.Cleanup(srv.Close)
	tr, err := New(Options{BaseURL: srv.URL, Token: StaticTokenSource(ladoIntegrationToken), HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr, func() []ladoSeenRequest {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

func TestBranchRulesReadsActiveRules(t *testing.T) {
	t.Run("rules, checks and pages", func(t *testing.T) {
		var srvURL string
		tr, seen := newLadoIntegrationServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "" {
				// GitHub links the next page by repository id.
				w.Header().Set("Link", `<`+srvURL+`/repositories/42/rules/branches/release/1.0?per_page=100&page=2>; rel="next", <`+srvURL+`/repositories/42/rules/branches/release/1.0?per_page=100&page=2>; rel="last"`)
				_, _ = w.Write([]byte(`[
					{"type":"pull_request","ruleset_source_type":"Repository","ruleset_id":7,"parameters":{"required_approving_review_count":1,"dismiss_stale_reviews_on_push":true}},
					{"type":"required_status_checks","ruleset_id":7,"parameters":{"strict_required_status_checks_policy":false,"required_status_checks":[{"context":"build","integration_id":15368},{"context":"lado/integration"}]}},
					{"type":"creation"}
				]`))
				return
			}
			_, _ = w.Write([]byte(`[
				{"type":"non_fast_forward","ruleset_source_type":"Organization","ruleset_id":9},
				{"type":"deletion","ruleset_id":9},
				{"type":"required_status_checks","ruleset_id":9,"parameters":{"required_status_checks":[{"context":"lint","integration_id":null}]}}
			]`))
		})
		srvURL = tr.baseURL

		rules, err := tr.LadoIssues().BranchRules(ctx(), ladoTestRepo(), "release/1.0")
		if err != nil {
			t.Fatalf("BranchRules: %v", err)
		}
		var types []string
		for _, rule := range rules.Rules {
			types = append(types, rule.Type)
		}
		wantTypes := []string{"pull_request", "required_status_checks", "creation", "non_fast_forward", "deletion", "required_status_checks"}
		if !slices.Equal(types, wantTypes) {
			t.Fatalf("rule types = %v, want %v", types, wantTypes)
		}
		first := rules.Rules[1].RequiredChecks
		if len(first) != 2 || first[0].Context != "build" || first[0].IntegrationID == nil || *first[0].IntegrationID != 15368 ||
			first[1].Context != "lado/integration" || first[1].IntegrationID != nil {
			t.Fatalf("first required checks = %+v, want build from app 15368 and lado/integration from any", first)
		}
		if second := rules.Rules[5].RequiredChecks; len(second) != 1 || second[0].Context != "lint" || second[0].IntegrationID != nil {
			t.Fatalf("second required checks = %+v, want lint from any", second)
		}
		for i, rule := range rules.Rules {
			if rule.Type != "required_status_checks" && rule.RequiredChecks != nil {
				t.Errorf("rule %d (%s) has required checks %+v", i, rule.Type, rule.RequiredChecks)
			}
		}

		calls := seen()
		if len(calls) != 2 {
			t.Fatalf("requests = %+v, want two pages", calls)
		}
		if calls[0].Method != http.MethodGet || calls[0].Path != "/repos/acme/demo/rules/branches/release/1.0" || calls[0].Query != "per_page=100" {
			t.Fatalf("first request = %+v, want GET /repos/acme/demo/rules/branches/release/1.0?per_page=100", calls[0])
		}
		if calls[1].Path != "/repositories/42/rules/branches/release/1.0" || calls[1].Query != "per_page=100&page=2" {
			t.Fatalf("second request = %+v, want the Link header's next page", calls[1])
		}
		for _, call := range calls {
			if call.Auth != "Bearer "+ladoIntegrationToken {
				t.Fatalf("Authorization = %q, want the tracker's token", call.Auth)
			}
		}
	})

	t.Run("branch escaped like the access check", func(t *testing.T) {
		tr, seen := newLadoIntegrationServer(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`[]`))
		})
		rules, err := tr.LadoIssues().BranchRules(ctx(), ladoTestRepo(), "feat/a b#1")
		if err != nil {
			t.Fatalf("BranchRules: %v", err)
		}
		if len(rules.Rules) != 0 {
			t.Fatalf("rules = %+v, want none", rules.Rules)
		}
		if calls := seen(); len(calls) != 1 || calls[0].Path != "/repos/acme/demo/rules/branches/feat/a%20b%231" {
			t.Fatalf("requests = %+v, want the branch path-escaped with its slash kept", calls)
		}
	})

	for _, tc := range []struct {
		status  int
		message string
	}{
		{http.StatusNotFound, "Not Found"},
		{http.StatusForbidden, "Resource not accessible by personal access token"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			tr, _ := newLadoIntegrationServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"` + tc.message + `"}`))
			})
			_, err := tr.LadoIssues().BranchRules(ctx(), ladoTestRepo(), "main")
			var httpErr *ports.LadoHTTPError
			if !errors.As(err, &httpErr) || httpErr.Status != tc.status || httpErr.Message != tc.message {
				t.Fatalf("err = %v, want a LadoHTTPError %d %q", err, tc.status, tc.message)
			}
			if strings.Contains(err.Error(), ladoIntegrationToken) {
				t.Fatalf("err %q holds the token", err)
			}
		})
	}

	t.Run("endless pages are no answer", func(t *testing.T) {
		tr, seen := newLadoIntegrationServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Link", `</repos/acme/demo/rules/branches/main?per_page=100&page=99>; rel="next"`)
			_, _ = w.Write([]byte(`[{"type":"deletion"}]`))
		})
		if rules, err := tr.LadoIssues().BranchRules(ctx(), ladoTestRepo(), "main"); err == nil {
			t.Fatalf("BranchRules = %+v, want an error past the page cap", rules)
		}
		if calls := seen(); len(calls) != ladoRuleMaxPages {
			t.Fatalf("requests = %d, want %d", len(calls), ladoRuleMaxPages)
		}
	})

	t.Run("refuses before sending", func(t *testing.T) {
		tr, seen := newLadoIntegrationServer(t, func(w http.ResponseWriter, r *http.Request) {})
		if _, err := tr.LadoIssues().BranchRules(ctx(), ladoTestRepo(), " "); err == nil {
			t.Fatal("BranchRules accepted an empty branch")
		}
		if _, err := tr.LadoIssues().BranchRules(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project"}, "main"); !errors.Is(err, ErrWrongProvider) {
			t.Fatalf("err = %v, want ErrWrongProvider for a GitLab repository", err)
		}
		if calls := seen(); len(calls) != 0 {
			t.Fatalf("requests = %+v, want none", calls)
		}
	})
}

func TestPostCommitStatus(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"

	t.Run("created", func(t *testing.T) {
		tr, seen := newLadoIntegrationServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1,"state":"success","context":"lado/integration"}`))
		})
		err := tr.LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), sha, ports.LadoCommitStatus{
			State: "success", Context: "lado/integration", Description: "Approved in LADO", TargetURL: "https://example.com/approvals/1",
		})
		if err != nil {
			t.Fatalf("PostCommitStatus: %v", err)
		}
		calls := seen()
		if len(calls) != 1 || calls[0].Method != http.MethodPost || calls[0].Path != "/repos/acme/demo/statuses/"+sha {
			t.Fatalf("requests = %+v, want POST /repos/acme/demo/statuses/%s", calls, sha)
		}
		if calls[0].Auth != "Bearer "+ladoIntegrationToken {
			t.Fatalf("Authorization = %q, want the tracker's token", calls[0].Auth)
		}
		var sent map[string]string
		if err := json.Unmarshal([]byte(calls[0].Body), &sent); err != nil {
			t.Fatalf("request body %q: %v", calls[0].Body, err)
		}
		want := map[string]string{"state": "success", "context": "lado/integration", "description": "Approved in LADO", "target_url": "https://example.com/approvals/1"}
		if len(sent) != len(want) {
			t.Fatalf("request body = %v, want %v", sent, want)
		}
		for k, v := range want {
			if sent[k] != v {
				t.Fatalf("request body = %v, want %v", sent, want)
			}
		}
	})

	t.Run("no target URL", func(t *testing.T) {
		tr, seen := newLadoIntegrationServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		})
		if err := tr.LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), sha, ports.LadoCommitStatus{State: "success", Context: "lado/integration"}); err != nil {
			t.Fatalf("PostCommitStatus: %v", err)
		}
		var sent map[string]any
		if err := json.Unmarshal([]byte(seen()[0].Body), &sent); err != nil {
			t.Fatal(err)
		}
		if _, ok := sent["target_url"]; ok {
			t.Fatalf("request body = %v, want no target_url", sent)
		}
	})

	t.Run("only 201 is success", func(t *testing.T) {
		tr, _ := newLadoIntegrationServer(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		})
		err := tr.LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), sha, ports.LadoCommitStatus{State: "success", Context: "lado/integration"})
		var httpErr *ports.LadoHTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != http.StatusOK {
			t.Fatalf("err = %v, want a LadoHTTPError 200", err)
		}
	})

	t.Run("unprocessable", func(t *testing.T) {
		tr, _ := newLadoIntegrationServer(t, func(w http.ResponseWriter, r *http.Request) {
			// A host that echoes the credential must not get it into LADO's error.
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Validation Failed for ` + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") + `"}`))
		})
		err := tr.LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), sha, ports.LadoCommitStatus{State: "success", Context: "lado/integration"})
		var httpErr *ports.LadoHTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != http.StatusUnprocessableEntity {
			t.Fatalf("err = %v, want a LadoHTTPError 422", err)
		}
		if !strings.Contains(err.Error(), "Validation Failed") {
			t.Fatalf("err = %q, want GitHub's message", err)
		}
		if strings.Contains(err.Error(), ladoIntegrationToken) || strings.Contains(httpErr.Message, ladoIntegrationToken) {
			t.Fatalf("err %q holds the token", err)
		}
	})

	t.Run("refuses before sending", func(t *testing.T) {
		tr, seen := newLadoIntegrationServer(t, func(w http.ResponseWriter, r *http.Request) {})
		for _, bad := range []string{"", "main", "0123456", "../../issues", strings.ToUpper(sha)} {
			if err := tr.LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), bad, ports.LadoCommitStatus{State: "success"}); err == nil {
				t.Errorf("PostCommitStatus accepted the commit %q", bad)
			}
		}
		if calls := seen(); len(calls) != 0 {
			t.Fatalf("requests = %+v, want none", calls)
		}
	})
}
