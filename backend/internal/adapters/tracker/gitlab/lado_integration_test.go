package gitlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	ladoProjectAPI   = "/projects/group/project"
	ladoTestSHA      = "0123456789abcdef0123456789abcdef01234567"
	ladoGuardedMain  = `{"name":"main","push_access_levels":[{"access_level":0}],"merge_access_levels":[{"access_level":40}],"allow_force_push":false,"code_owner_approval_required":false}`
	ladoGuardedFlags = `{"default_branch":"main","only_allow_merge_if_pipeline_succeeds":true,"allow_merge_on_skipped_pipeline":false,"permissions":{"project_access":{"access_level":30},"group_access":null}}`
)

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// onBranchReads answers the three reads BranchRules makes.
func onBranchReads(f *fakeGL, project, scopes, protected string) {
	f.on("GET", ladoProjectAPI, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, project) })
	f.on("GET", "/personal_access_tokens/self", func(w http.ResponseWriter, r *http.Request) {
		if scopes == "" {
			writeJSON(w, http.StatusNotFound, `{"message":"404 Not Found"}`)
			return
		}
		writeJSON(w, http.StatusOK, scopes)
	})
	f.on("GET", ladoProjectAPI+"/protected_branches", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			f.t.Errorf("per_page = %q, want 100", got)
		}
		writeJSON(w, http.StatusOK, protected)
	})
}

func boolPtr(v bool) *bool { return &v }

func TestLadoBranchRulesReadsAGuardedBranch(t *testing.T) {
	f := newFakeGL(t)
	onBranchReads(f, ladoGuardedFlags, `{"scopes":["api","read_user"]}`, "["+ladoGuardedMain+`,{"name":"develop","push_access_levels":[{"access_level":30}],"allow_force_push":true}]`)
	rules, err := newTrackerForTest(t, f).LadoIssues().BranchRules(ctx(), ladoTestRepo(), "main")
	if err != nil {
		t.Fatalf("BranchRules: %v", err)
	}
	if len(rules.Rules) != 0 {
		t.Fatalf("Rules = %+v, want none on GitLab", rules.Rules)
	}
	want := &ports.LadoGitLabProtection{
		Protected:              true,
		MatchedNames:           []string{"main"},
		PushAccess:             []ports.LadoGitLabAccess{{AccessLevel: 0}},
		MergeAccess:            []ports.LadoGitLabAccess{{AccessLevel: 40}},
		PipelineMustSucceed:    boolPtr(true),
		SkippedPipelineAllowed: boolPtr(false),
		AccessLevel:            30,
		TokenScopes:            []string{"api", "read_user"},
		ScopesKnown:            true,
	}
	if !reflect.DeepEqual(rules.GitLab, want) {
		t.Fatalf("GitLab = %+v, want %+v", rules.GitLab, want)
	}
}

func TestLadoBranchRulesMatchesWildcards(t *testing.T) {
	f := newFakeGL(t)
	onBranchReads(f, ladoGuardedFlags, `{"scopes":["api"]}`, `[{"name":"release/*","push_access_levels":[{"access_level":0}],"allow_force_push":false},{"name":"main","push_access_levels":[{"access_level":30}],"allow_force_push":true}]`)
	rules, err := newTrackerForTest(t, f).LadoIssues().BranchRules(ctx(), ladoTestRepo(), "release/1.0/hotfix")
	if err != nil {
		t.Fatalf("BranchRules: %v", err)
	}
	got := rules.GitLab
	if !got.Protected || !reflect.DeepEqual(got.MatchedNames, []string{"release/*"}) || got.AllowForcePush ||
		!reflect.DeepEqual(got.PushAccess, []ports.LadoGitLabAccess{{AccessLevel: 0}}) {
		t.Fatalf("GitLab = %+v, want only release/* matched", got)
	}
}

func TestLadoBranchRulesUnprotectedBranch(t *testing.T) {
	f := newFakeGL(t)
	onBranchReads(f, ladoGuardedFlags, `{"scopes":["api"]}`, `[{"name":"develop","push_access_levels":[{"access_level":30}],"allow_force_push":true}]`)
	rules, err := newTrackerForTest(t, f).LadoIssues().BranchRules(ctx(), ladoTestRepo(), "main")
	if err != nil {
		t.Fatalf("BranchRules: %v", err)
	}
	got := rules.GitLab
	if got.Protected || len(got.MatchedNames) != 0 || len(got.PushAccess) != 0 || got.AllowForcePush {
		t.Fatalf("GitLab = %+v, want an unprotected branch", got)
	}
	if got.PipelineMustSucceed == nil || !*got.PipelineMustSucceed {
		t.Fatalf("PipelineMustSucceed = %v, want the project setting kept", got.PipelineMustSucceed)
	}
}

func TestLadoBranchRulesJoinsMatchingEntries(t *testing.T) {
	f := newFakeGL(t)
	onBranchReads(f, ladoGuardedFlags, `{"scopes":["api"]}`, `[`+ladoGuardedMain+`,
		{"name":"ma*","push_access_levels":[{"access_level":30},{"access_level":0}],"merge_access_levels":[{"access_level":40}],"allow_force_push":true,"code_owner_approval_required":true},
		{"name":"*","push_access_levels":[{"access_level":40,"user_id":5,"group_id":null},{"access_level":40,"group_id":9},{"access_level":40,"deploy_key_id":3}],"allow_force_push":false}]`)
	rules, err := newTrackerForTest(t, f).LadoIssues().BranchRules(ctx(), ladoTestRepo(), "main")
	if err != nil {
		t.Fatalf("BranchRules: %v", err)
	}
	got := rules.GitLab
	wantPush := []ports.LadoGitLabAccess{
		{AccessLevel: 0},
		{AccessLevel: 30},
		{AccessLevel: 40, User: true},
		{AccessLevel: 40, Group: true},
		{AccessLevel: 40, DeployKey: true},
	}
	if !reflect.DeepEqual(got.MatchedNames, []string{"main", "ma*", "*"}) ||
		!reflect.DeepEqual(got.PushAccess, wantPush) ||
		!reflect.DeepEqual(got.MergeAccess, []ports.LadoGitLabAccess{{AccessLevel: 40}}) ||
		!got.AllowForcePush || !got.CodeOwnerApprovalRequired {
		t.Fatalf("GitLab = %+v, want the entries joined", got)
	}
}

func TestLadoBranchRulesReadsMissingFieldsConservatively(t *testing.T) {
	f := newFakeGL(t)
	// No merge settings, group access above project access, scopes not
	// reported, force push not said.
	onBranchReads(f, `{"permissions":{"project_access":{"access_level":20},"group_access":{"access_level":40}}}`, "", `[{"name":"main","push_access_levels":[]}]`)
	rules, err := newTrackerForTest(t, f).LadoIssues().BranchRules(ctx(), ladoTestRepo(), "main")
	if err != nil {
		t.Fatalf("BranchRules: %v", err)
	}
	got := rules.GitLab
	if !got.Protected || len(got.PushAccess) != 0 || !got.AllowForcePush ||
		got.PipelineMustSucceed != nil || got.SkippedPipelineAllowed != nil ||
		got.AccessLevel != 40 || got.ScopesKnown || len(got.TokenScopes) != 0 {
		t.Fatalf("GitLab = %+v", got)
	}
}

func TestLadoBranchRulesReportsTheTokensStanding(t *testing.T) {
	f := newFakeGL(t)
	onBranchReads(f, `{"permissions":{"project_access":{"access_level":20}}}`, `{"scopes":["read_api"]}`, `[]`)
	rules, err := newTrackerForTest(t, f).LadoIssues().BranchRules(ctx(), ladoTestRepo(), "main")
	if err != nil {
		t.Fatalf("BranchRules: %v", err)
	}
	if got := rules.GitLab; got.AccessLevel != 20 || !got.ScopesKnown || !reflect.DeepEqual(got.TokenScopes, []string{"read_api"}) {
		t.Fatalf("GitLab = %+v, want Reporter with read_api", got)
	}
}

func TestLadoBranchRulesFollowsPages(t *testing.T) {
	for _, tc := range []struct {
		name string
		next func(w http.ResponseWriter, base string)
	}{
		{"X-Next-Page", func(w http.ResponseWriter, _ string) { w.Header().Set("X-Next-Page", "2") }},
		{"Link", func(w http.ResponseWriter, base string) {
			w.Header().Set("Link", fmt.Sprintf(`<%s/projects/group%%2Fproject/protected_branches?page=2&per_page=100>; rel="next"`, base))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGL(t)
			onBranchReads(f, ladoGuardedFlags, `{"scopes":["api"]}`, "")
			f.on("GET", ladoProjectAPI+"/protected_branches", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") == "2" {
					writeJSON(w, http.StatusOK, "["+ladoGuardedMain+"]")
					return
				}
				tc.next(w, f.server.URL)
				writeJSON(w, http.StatusOK, `[{"name":"develop"}]`)
			})
			rules, err := newTrackerForTest(t, f).LadoIssues().BranchRules(ctx(), ladoTestRepo(), "main")
			if err != nil {
				t.Fatalf("BranchRules: %v", err)
			}
			if !rules.GitLab.Protected || !reflect.DeepEqual(rules.GitLab.MatchedNames, []string{"main"}) {
				t.Fatalf("GitLab = %+v, want main found on page 2", rules.GitLab)
			}
		})
	}
}

func TestLadoBranchRulesStopsAtTenPages(t *testing.T) {
	f := newFakeGL(t)
	onBranchReads(f, ladoGuardedFlags, `{"scopes":["api"]}`, "")
	pages := 0
	f.on("GET", ladoProjectAPI+"/protected_branches", func(w http.ResponseWriter, r *http.Request) {
		pages++
		w.Header().Set("X-Next-Page", fmt.Sprint(pages+1))
		writeJSON(w, http.StatusOK, "["+ladoGuardedMain+"]")
	})
	_, err := newTrackerForTest(t, f).LadoIssues().BranchRules(ctx(), ladoTestRepo(), "main")
	if err == nil || !strings.Contains(err.Error(), "past 10 pages") {
		t.Fatalf("err = %v, want the page cap", err)
	}
	if pages != 10 {
		t.Fatalf("pages read = %d, want 10", pages)
	}
}

func TestLadoBranchRulesRefusalsKeepTheirStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt string
	}{
		{"project", ladoProjectAPI},
		{"protected branches", ladoProjectAPI + "/protected_branches"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGL(t)
			onBranchReads(f, ladoGuardedFlags, `{"scopes":["api"]}`, "[]")
			f.on("GET", tc.failAt, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusForbidden, `{"message":"403 Forbidden for Bearer tkn-test"}`)
			})
			_, err := newTrackerForTest(t, f).LadoIssues().BranchRules(ctx(), ladoTestRepo(), "main")
			var refused *ports.LadoHTTPError
			if !errors.As(err, &refused) || refused.Status != http.StatusForbidden {
				t.Fatalf("err = %v, want LadoHTTPError 403", err)
			}
			if strings.Contains(err.Error(), "tkn-test") {
				t.Fatalf("err = %v, holds the token", err)
			}
		})
	}
}

func TestLadoBranchRulesNeedsABranch(t *testing.T) {
	f := newFakeGL(t)
	if _, err := newTrackerForTest(t, f).LadoIssues().BranchRules(ctx(), ladoTestRepo(), " "); err == nil {
		t.Fatal("BranchRules with no branch succeeded")
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("requests = %+v, want none", calls)
	}
}

func TestLadoRefMatches(t *testing.T) {
	for _, tc := range []struct {
		name, branch string
		want         bool
	}{
		{"main", "main", true},
		{"main", "main2", false},
		{"*", "feature/x", true},
		{"release/*", "release/1.0", true},
		{"release/*", "release/1.0/hotfix", true},
		{"release/*", "release", false},
		{"*-stable", "13-1-stable", true},
		{"*-stable", "13-1-stable-x", false},
		{"rel.ase-*", "relXase-1", false},
		{"rel.ase-*", "rel.ase-1", true},
	} {
		if got := ladoRefMatches(tc.name, tc.branch); got != tc.want {
			t.Errorf("ladoRefMatches(%q, %q) = %v, want %v", tc.name, tc.branch, got, tc.want)
		}
	}
}

// statusPost is the body PostCommitStatus sent.
type statusPost map[string]any

func (f *fakeGL) statusPosts(t *testing.T) []statusPost {
	t.Helper()
	var out []statusPost
	for _, call := range f.calls() {
		if call.Method != http.MethodPost {
			continue
		}
		if call.Path != ladoProjectAPI+"/statuses/"+ladoTestSHA {
			t.Fatalf("posted to %s", call.Path)
		}
		var body statusPost
		if err := json.Unmarshal([]byte(call.Body), &body); err != nil {
			t.Fatalf("status body %q: %v", call.Body, err)
		}
		out = append(out, body)
	}
	return out
}

func onStatusPost(f *fakeGL, status int, body string) {
	f.on("POST", ladoProjectAPI+"/statuses/"+ladoTestSHA, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, status, body) })
}

func onPipelines(f *fakeGL, body string) {
	f.on("GET", ladoProjectAPI+"/pipelines", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("sha") != ladoTestSHA || q.Get("order_by") != "id" || q.Get("sort") != "desc" || q.Get("per_page") != "20" || q.Get("ref") != "" {
			f.t.Errorf("pipelines query = %s", r.URL.RawQuery)
		}
		writeJSON(w, http.StatusOK, body)
	})
}

func onMergeRequest(f *fakeGL, body string) {
	f.on("GET", ladoProjectAPI+"/merge_requests/7", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, body) })
}

func approvedStatus() ports.LadoCommitStatus {
	return ports.LadoCommitStatus{
		State: "success", Context: "lado/integration", Description: "Approved in LADO",
		Ref: "feature/login", MergeRequest: 7,
	}
}

func TestLadoPostCommitStatusJoinsTheMergeRequestEventPipeline(t *testing.T) {
	f := newFakeGL(t)
	onMergeRequest(f, `{"iid":7,"head_pipeline":{"id":77,"sha":"`+ladoTestSHA+`","ref":"refs/merge-requests/7/head","source":"merge_request_event"}}`)
	onStatusPost(f, http.StatusCreated, `{"id":1}`)
	if err := newTrackerForTest(t, f).LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), ladoTestSHA, approvedStatus()); err != nil {
		t.Fatalf("PostCommitStatus: %v", err)
	}
	posts := f.statusPosts(t)
	want := statusPost{"state": "success", "name": "lado/integration", "description": "Approved in LADO", "pipeline_id": float64(77)}
	if len(posts) != 1 || !reflect.DeepEqual(posts[0], want) {
		t.Fatalf("posts = %+v, want %+v", posts, want)
	}
	for _, call := range f.calls() {
		if call.Path == ladoProjectAPI+"/pipelines" {
			t.Fatal("read the pipelines although the merge request named its head pipeline")
		}
	}
}

func TestLadoPostCommitStatusJoinsABranchPipeline(t *testing.T) {
	t.Run("merge request head pipeline", func(t *testing.T) {
		f := newFakeGL(t)
		onMergeRequest(f, `{"iid":7,"head_pipeline":{"id":55,"sha":"`+ladoTestSHA+`","ref":"feature/login","source":"push"}}`)
		onStatusPost(f, http.StatusCreated, `{}`)
		if err := newTrackerForTest(t, f).LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), ladoTestSHA, approvedStatus()); err != nil {
			t.Fatalf("PostCommitStatus: %v", err)
		}
		if posts := f.statusPosts(t); len(posts) != 1 || posts[0]["pipeline_id"] != float64(55) || posts[0]["ref"] != nil {
			t.Fatalf("posts = %+v, want pipeline 55 and no ref", posts)
		}
	})
	t.Run("head pipeline of an older commit", func(t *testing.T) {
		f := newFakeGL(t)
		onMergeRequest(f, `{"iid":7,"head_pipeline":{"id":40,"sha":"ffffffffffffffffffffffffffffffffffffffff","source":"push"}}`)
		onPipelines(f, `[{"id":90,"sha":"`+ladoTestSHA+`","source":"external"},{"id":88,"sha":"`+ladoTestSHA+`","source":"push"}]`)
		onStatusPost(f, http.StatusCreated, `{}`)
		if err := newTrackerForTest(t, f).LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), ladoTestSHA, approvedStatus()); err != nil {
			t.Fatalf("PostCommitStatus: %v", err)
		}
		if posts := f.statusPosts(t); len(posts) != 1 || posts[0]["pipeline_id"] != float64(88) || posts[0]["ref"] != nil {
			t.Fatalf("posts = %+v, want the newest pipeline that is not external", posts)
		}
	})
	t.Run("source branch only", func(t *testing.T) {
		f := newFakeGL(t)
		onPipelines(f, `[{"id":88,"sha":"`+ladoTestSHA+`","source":"push"}]`)
		onStatusPost(f, http.StatusOK, `{}`)
		status := approvedStatus()
		status.MergeRequest = 0
		if err := newTrackerForTest(t, f).LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), ladoTestSHA, status); err != nil {
			t.Fatalf("PostCommitStatus: %v", err)
		}
		if posts := f.statusPosts(t); len(posts) != 1 || posts[0]["pipeline_id"] != float64(88) || posts[0]["ref"] != nil {
			t.Fatalf("posts = %+v, want pipeline 88 and no ref", posts)
		}
	})
	t.Run("only external pipelines", func(t *testing.T) {
		f := newFakeGL(t)
		onMergeRequest(f, `{"iid":7,"head_pipeline":null}`)
		onPipelines(f, `[{"id":91,"sha":"`+ladoTestSHA+`","source":"external"}]`)
		onStatusPost(f, http.StatusCreated, `{}`)
		if err := newTrackerForTest(t, f).LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), ladoTestSHA, approvedStatus()); err != nil {
			t.Fatalf("PostCommitStatus: %v", err)
		}
		if posts := f.statusPosts(t); len(posts) != 1 || posts[0]["pipeline_id"] != float64(91) || posts[0]["ref"] != nil {
			t.Fatalf("posts = %+v, want the external pipeline joined, not another made", posts)
		}
	})
}

func TestLadoPostCommitStatusWithoutAPipelineSendsTheSourceBranch(t *testing.T) {
	f := newFakeGL(t)
	onMergeRequest(f, `{"iid":7,"head_pipeline":null}`)
	onPipelines(f, `[]`)
	onStatusPost(f, http.StatusCreated, `{}`)
	status := approvedStatus()
	status.State, status.Description, status.TargetURL = "pending", "Waiting for approval in LADO", "http://127.0.0.1:3001/lado"
	if err := newTrackerForTest(t, f).LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), ladoTestSHA, status); err != nil {
		t.Fatalf("PostCommitStatus: %v", err)
	}
	want := statusPost{"state": "pending", "name": "lado/integration", "description": "Waiting for approval in LADO", "target_url": "http://127.0.0.1:3001/lado", "ref": "feature/login"}
	if posts := f.statusPosts(t); len(posts) != 1 || !reflect.DeepEqual(posts[0], want) {
		t.Fatalf("posts = %+v, want %+v", posts, want)
	}
}

func TestLadoPostCommitStatusWithoutAPipelineOrBranchSendsNothing(t *testing.T) {
	f := newFakeGL(t)
	onMergeRequest(f, `{"iid":7,"head_pipeline":null}`)
	onPipelines(f, `[]`)
	status := approvedStatus()
	status.Ref = ""
	if err := newTrackerForTest(t, f).LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), ladoTestSHA, status); err == nil {
		t.Fatal("PostCommitStatus succeeded with nowhere to post")
	}
	if posts := f.statusPosts(t); len(posts) != 0 {
		t.Fatalf("posts = %+v, want none", posts)
	}
}

func TestLadoPostCommitStatusAnswers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		wantErr int
	}{
		{"created", http.StatusCreated, `{"id":1}`, 0},
		{"ok", http.StatusOK, `{"id":1}`, 0},
		{"already in that state", http.StatusBadRequest, `{"message":"Cannot transition status via :enqueue from :pending (Reasons: ...)"}`, 0},
		{"other bad request", http.StatusBadRequest, `{"message":{"state":["is invalid"]}}`, http.StatusBadRequest},
		{"forbidden", http.StatusForbidden, `{"message":"403 Forbidden tkn-test"}`, http.StatusForbidden},
		{"server error", http.StatusInternalServerError, `{"message":"500"}`, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGL(t)
			onMergeRequest(f, `{"iid":7,"head_pipeline":{"id":77,"sha":"`+ladoTestSHA+`"}}`)
			onStatusPost(f, tc.status, tc.body)
			err := newTrackerForTest(t, f).LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), ladoTestSHA, approvedStatus())
			if tc.wantErr == 0 {
				if err != nil {
					t.Fatalf("PostCommitStatus = %v, want nil", err)
				}
				return
			}
			var refused *ports.LadoHTTPError
			if !errors.As(err, &refused) || refused.Status != tc.wantErr {
				t.Fatalf("err = %v, want LadoHTTPError %d", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "tkn-test") {
				t.Fatalf("err = %v, holds the token", err)
			}
		})
	}
}

func TestLadoPostCommitStatusMergeRequestReadRefused(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", ladoProjectAPI+"/merge_requests/7", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusForbidden, `{"message":"403 Forbidden"}`)
	})
	err := newTrackerForTest(t, f).LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), ladoTestSHA, approvedStatus())
	var refused *ports.LadoHTTPError
	if !errors.As(err, &refused) || refused.Status != http.StatusForbidden {
		t.Fatalf("err = %v, want LadoHTTPError 403", err)
	}
	if posts := f.statusPosts(t); len(posts) != 0 {
		t.Fatalf("posts = %+v, want none", posts)
	}
}

func TestLadoPostCommitStatusRefusesBadInputWithoutARequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sha    string
		status ports.LadoCommitStatus
	}{
		{"no merge request or branch", ladoTestSHA, ports.LadoCommitStatus{State: "success", Context: "lado/integration"}},
		{"upper case sha", strings.ToUpper(ladoTestSHA), approvedStatus()},
		{"short sha", "0123456", approvedStatus()},
		{"not hex", strings.Repeat("g", 40), approvedStatus()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGL(t)
			if err := newTrackerForTest(t, f).LadoIssues().PostCommitStatus(ctx(), ladoTestRepo(), tc.sha, tc.status); err == nil {
				t.Fatal("PostCommitStatus succeeded")
			}
			if calls := f.calls(); len(calls) != 0 {
				t.Fatalf("requests = %+v, want none", calls)
			}
		})
	}
}
