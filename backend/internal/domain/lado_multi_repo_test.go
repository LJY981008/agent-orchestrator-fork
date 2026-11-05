package domain_test

import (
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestParseLadoPlanReadsRepos(t *testing.T) {
	plan, problems := parse(t, partsPlan("repos: [\" back/ \", front]\n"))
	if len(problems) != 0 {
		t.Fatalf("problems = %+v, want none", problems)
	}
	if !slices.Equal(plan.Repos, []string{"back", "front"}) {
		t.Fatalf("repos = %q, want [back front]", plan.Repos)
	}

	plan, problems = parse(t, partsPlan(""))
	if len(problems) != 0 || plan.Repos != nil {
		t.Fatalf("a plan without repos: repos = %q, problems = %+v", plan.Repos, problems)
	}
	if _, problems = parse(t, partsPlan("repos: back\n")); !slices.Equal(problems, []domain.LadoPlanProblem{{Code: domain.LadoPlanInvalidField, Field: "repos"}}) {
		t.Fatalf("a scalar repos: problems = %+v", problems)
	}

	plan, problems = parse(t, partsPlan("repos: [back, \"\", /front, apps/../x, ./back, Back, apps/web]\n"))
	want := []domain.LadoPlanProblem{
		{Code: domain.LadoPlanReposInvalid, Field: "repos", Detail: ""},
		{Code: domain.LadoPlanReposInvalid, Field: "repos", Detail: "/front"},
		{Code: domain.LadoPlanReposInvalid, Field: "repos", Detail: "apps/../x"},
		{Code: domain.LadoPlanReposInvalid, Field: "repos", Detail: "./back"},
		{Code: domain.LadoPlanReposInvalid, Field: "repos", Detail: "Back"},
	}
	if !slices.Equal(problems, want) {
		t.Fatalf("problems = %+v, want %+v", problems, want)
	}
	if !slices.Equal(plan.Repos, []string{"back", "apps/web"}) {
		t.Fatalf("repos = %q, want the valid entries", plan.Repos)
	}
}

func TestLadoPlanRepos(t *testing.T) {
	children := []string{"back", "front/", "services/api"}
	plan := func(repos []string, paths ...string) domain.LadoPlan {
		return domain.LadoPlan{AffectedPaths: paths, Repos: repos}
	}
	cases := []struct {
		name    string
		plan    domain.LadoPlan
		issue   string
		want    []string
		problem string
	}{
		{name: "the repos list as written", plan: plan([]string{"front", "back"}, "back/**"), issue: "back", want: []string{"front", "back"}},
		{name: "one child", plan: plan(nil, "back/internal/**", "back/go.mod"), issue: "front", want: []string{"back"}},
		{name: "a nested child", plan: plan(nil, "services/api/**"), issue: "back", want: []string{"services/api"}},
		{name: "no child: the issue's", plan: plan(nil, "docs/**", "**"), issue: "back/", want: []string{"back"}},
		{name: "a single-repository project", plan: plan(nil, "backend/**"), issue: "", want: []string{""}},
		{name: "two children", plan: plan(nil, "back/**", "FRONT/src/**"), issue: "back", problem: "back, front"},
		{
			name: "a part in another child",
			plan: domain.LadoPlan{AffectedPaths: []string{"back/**"}, Parts: []domain.LadoPlanPart{{ID: "a", AffectedPaths: []string{"front/**"}}}},
			want: nil, problem: "back, front",
		},
		{name: "a sibling folder with a child's name as a prefix", plan: plan(nil, "backup/**"), issue: "front", want: []string{"front"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, problems := domain.LadoPlanRepos(tc.plan, children, tc.issue)
			if tc.problem != "" {
				want := []domain.LadoPlanProblem{{Code: domain.LadoPlanReposMissing, Field: "repos", Detail: tc.problem}}
				if got != nil || !slices.Equal(problems, want) {
					t.Fatalf("repos = %q, problems = %+v; want %+v", got, problems, want)
				}
				return
			}
			if len(problems) != 0 || !slices.Equal(got, tc.want) {
				t.Fatalf("repos = %q, problems = %+v; want %q", got, problems, tc.want)
			}
		})
	}
}

func TestLadoPlanOutsideRepos(t *testing.T) {
	parts := []domain.LadoPlanPart{{ID: "a", AffectedPaths: []string{"back/**"}}, {ID: "b", AffectedPaths: []string{"front/src/**", "docs/**"}}}
	withParts := domain.LadoPlan{AffectedPaths: []string{"back/**", "front/**"}, Parts: parts}
	if got := domain.LadoPlanOutsideRepos(withParts, []string{"back", "front/"}); !slices.Equal(got, []domain.LadoPlanProblem{{Code: domain.LadoPlanPartRepoOutside, Field: "parts", Detail: "b: docs/**"}}) {
		t.Fatalf("paths in both repositories and the root: problems = %+v", got)
	}
	want := []domain.LadoPlanProblem{
		{Code: domain.LadoPlanPartRepoOutside, Field: "affected_paths", Detail: "front/**"},
		{Code: domain.LadoPlanPartRepoOutside, Field: "parts", Detail: "b: front/src/**"},
		{Code: domain.LadoPlanPartRepoOutside, Field: "parts", Detail: "b: docs/**"},
	}
	if got := domain.LadoPlanOutsideRepos(withParts, []string{"back"}); !slices.Equal(got, want) {
		t.Fatalf("problems = %+v, want %+v", got, want)
	}

	// A plan without parts is checked once it names repos.
	single := domain.LadoPlan{AffectedPaths: []string{"back/**", "front/**"}, Repos: []string{"back"}}
	if got := domain.LadoPlanOutsideRepos(single, single.Repos); !slices.Equal(got, []domain.LadoPlanProblem{{Code: domain.LadoPlanPartRepoOutside, Field: "affected_paths", Detail: "front/**"}}) {
		t.Fatalf("a plan with repos: problems = %+v", got)
	}
	single.Repos = nil
	if got := domain.LadoPlanOutsideRepos(single, []string{"back"}); len(got) != 0 {
		t.Fatalf("a plan without parts or repos: problems = %+v", got)
	}
	if got := domain.LadoPlanOutsideRepos(withParts, []string{"back", ""}); len(got) != 0 {
		t.Fatalf("the root holds every path: problems = %+v", got)
	}
}

func TestLadoMatchWorkspaceRepo(t *testing.T) {
	child := func(relative, origin string) domain.WorkspaceRepoRecord {
		return domain.WorkspaceRepoRecord{RelativePath: relative, RepoOriginURL: origin}
	}
	pr := func(url string) domain.TrackerRepo {
		t.Helper()
		repo, err := domain.LadoPRRepo(url)
		if err != nil {
			t.Fatalf("LadoPRRepo(%q): %v", url, err)
		}
		return repo
	}
	children := []domain.WorkspaceRepoRecord{
		child("back", "git@gitlab.example.com:team/back.git"),
		child("front", "ssh://git@gitlab.example.com:2222/team/front.git"),
		child("web", "git@github.com:acme/web.git"),
		child("broken", "not a url"),
	}
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"same origin", "https://gitlab.example.com/team/front/-/merge_requests/3", "front"},
		{"github", "https://github.com/acme/web/pull/7", "web"},
		{"a relative URL root", "https://gitlab.example.com/gitlab/team/back/-/merge_requests/9", "back"},
		{"a relative URL root, any case", "https://GitLab.Example.com/GitLab/Team/Front/-/merge_requests/9", "front"},
		{"another host", "https://other.example.com/gitlab/team/back/-/merge_requests/9", ""},
		{"no child ends the path", "https://gitlab.example.com/gitlab/team/api/-/merge_requests/9", ""},
		{"github has no relative root", "https://github.com/x/web/pull/7", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := domain.LadoMatchWorkspaceRepo(pr(tc.url), children)
			if ok != (tc.want != "") || got.RelativePath != tc.want {
				t.Fatalf("match = %q, %v; want %q", got.RelativePath, ok, tc.want)
			}
		})
	}

	// An exact match wins over a suffix match.
	exact := append(slices.Clone(children), child("mirror", "git@gitlab.example.com:gitlab/team/back.git"))
	if got, ok := domain.LadoMatchWorkspaceRepo(pr("https://gitlab.example.com/gitlab/team/back/-/merge_requests/9"), exact); !ok || got.RelativePath != "mirror" {
		t.Fatalf("exact match = %q, %v; want mirror", got.RelativePath, ok)
	}
	// Two children that end the path are no match.
	twice := append(slices.Clone(children), child("back2", "git@gitlab.example.com:x/team/back.git"))
	if got, ok := domain.LadoMatchWorkspaceRepo(pr("https://gitlab.example.com/gitlab/x/team/back/-/merge_requests/9"), twice); ok {
		t.Fatalf("ambiguous match = %q, want none", got.RelativePath)
	}
}

// multiExec is an execution with an open back PR and an open front PR that
// both passed verification, expected in back and front.
func multiExec() domain.LadoExecution {
	tracked := func(repo string, position int, head string) domain.LadoExecutionTrackedPR {
		return domain.LadoExecutionTrackedPR{
			PR:           domain.LadoExecutionPR{URL: "https://gitlab.example.com/team/" + repo + "/-/merge_requests/1", Number: 1, HeadSHA: head, TargetBranch: "main"},
			Repo:         repo,
			Position:     position,
			Verification: domain.LadoExecutionVerification{HeadSHA: head, Outcome: domain.LadoVerifyPass},
		}
	}
	// Out of Position order on purpose: the state follows Position.
	prs := []domain.LadoExecutionTrackedPR{tracked("front", 1, "f1"), tracked("back", 0, "b1")}
	return domain.LadoExecution{
		SessionID:        "s1",
		IntegrationGated: true,
		PR:               prs[1].PR,
		Verification:     prs[1].Verification,
		PRs:              prs,
		PRRepos:          []string{"back", "front"},
	}
}

func TestLadoExecutionStateWithSeveralPRs(t *testing.T) {
	at := func(e *domain.LadoExecution, repo string) *domain.LadoExecutionTrackedPR {
		for i := range e.PRs {
			if e.PRs[i].Repo == repo {
				return &e.PRs[i]
			}
		}
		t.Fatalf("no PR in %s", repo)
		return nil
	}
	cases := []struct {
		name   string
		edit   func(e *domain.LadoExecution)
		state  domain.LadoTaskState
		reason string
	}{
		{"both pass", func(*domain.LadoExecution) {}, domain.LadoTaskIntegrationPending, ""},
		{"one approved", func(e *domain.LadoExecution) { at(e, "back").Integration.HeadApproved = true }, domain.LadoTaskIntegrationPending, ""},
		{"both approved", func(e *domain.LadoExecution) {
			at(e, "back").Integration.HeadApproved = true
			at(e, "front").Integration.HeadApproved = true
		}, domain.LadoTaskIntegrating, ""},
		{"a status not posted", func(e *domain.LadoExecution) {
			at(e, "back").Integration.HeadApproved = true
			at(e, "front").Integration = domain.LadoExecutionIntegration{HeadApproved: true, StatusState: domain.LadoIntegrationStatusFailed}
		}, domain.LadoTaskIntegrating, domain.LadoIntegratingStatusNotPosted},
		{"the first validating PR in merge order", func(e *domain.LadoExecution) {
			at(e, "front").Verification = domain.LadoExecutionVerification{HeadSHA: "f1", Outcome: domain.LadoVerifyFail, Reason: domain.LadoCodeBaseOverlap + "x"}
			at(e, "back").Verification = domain.LadoExecutionVerification{HeadSHA: "b1", Outcome: domain.LadoVerifyWait, Reason: "checks_pending"}
		}, domain.LadoTaskValidating, "checks_pending"},
		{"a new head", func(e *domain.LadoExecution) { at(e, "front").PR.HeadSHA = "f2" }, domain.LadoTaskValidating, domain.LadoValidatingVerifying},
		{"a conflict in one", func(e *domain.LadoExecution) { at(e, "front").PR.Mergeability = domain.MergeConflicting }, domain.LadoTaskValidating, domain.LadoValidatingMergeConflict},
		{"resolving the PR it is for", func(e *domain.LadoExecution) {
			e.Resolve = domain.LadoExecutionResolve{State: domain.LadoResolveResolving, PRURL: at(e, "front").PR.URL}
		}, domain.LadoTaskValidating, domain.LadoValidatingResolving},
		{"one closed", func(e *domain.LadoExecution) { at(e, "front").PR.Closed = true }, domain.LadoTaskClosedUnmerged, ""},
		{"back merged, front open", func(e *domain.LadoExecution) {
			back := at(e, "back")
			back.PR.Merged, back.Integration.MergedHeadApproved, back.Settled = true, true, true
		}, domain.LadoTaskIntegrationPending, ""},
		{"both merged", func(e *domain.LadoExecution) {
			for i := range e.PRs {
				e.PRs[i].PR.Merged, e.PRs[i].Integration.MergedHeadApproved = true, true
			}
		}, domain.LadoTaskCompleted, ""},
		{"one merged without approval", func(e *domain.LadoExecution) {
			for i := range e.PRs {
				e.PRs[i].PR.Merged = true
			}
			at(e, "back").Integration.MergedHeadApproved = true
		}, domain.LadoTaskCompleted, domain.LadoCompletedWithoutApproval},
		{"front missing", func(e *domain.LadoExecution) { e.PRs = e.PRs[1:] }, domain.LadoTaskValidating, domain.LadoValidatingPRMissing},
		{"front missing, back merged", func(e *domain.LadoExecution) {
			e.PRs = e.PRs[1:]
			e.PRs[0].PR.Merged = true
		}, domain.LadoTaskValidating, domain.LadoValidatingPRMissing},
		{"front missing after the worker's done report", func(e *domain.LadoExecution) {
			e.PRs, e.WorkerDone = e.PRs[1:], true
		}, domain.LadoTaskIntegrationPending, ""},
		{"front missing after the session ended", func(e *domain.LadoExecution) {
			e.PRs, e.SessionTerminated = e.PRs[1:], true
		}, domain.LadoTaskIntegrationPending, ""},
		{"front missing from a firm list", func(e *domain.LadoExecution) {
			e.PRs, e.WorkerDone, e.PRReposFirm = e.PRs[1:], true, true
		}, domain.LadoTaskValidating, domain.LadoValidatingPRMissing},
		{"no PR yet", func(e *domain.LadoExecution) {
			e.PRs, e.PR, e.Verification = nil, domain.LadoExecutionPR{}, domain.LadoExecutionVerification{}
		}, domain.LadoTaskRunning, ""},
		{"no PR and the session ended", func(e *domain.LadoExecution) {
			e.PRs, e.PR, e.SessionTerminated = nil, domain.LadoExecutionPR{}, true
		}, domain.LadoTaskFailed, domain.LadoFailedEndedWithoutPR},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := multiExec()
			tc.edit(&e)
			if state, reason := e.State(); state != tc.state || reason != tc.reason {
				t.Fatalf("state = %s (%q), want %s (%q)", state, reason, tc.state, tc.reason)
			}
		})
	}
}

func TestLadoExecutionStateWithOnePRIsUnchanged(t *testing.T) {
	// One tracked PR and one expected repository give the PR's own state,
	// read from the execution's fields as before.
	e := multiExec()
	e.PRs, e.PRRepos = e.PRs[1:], []string{"back"}
	e.Integration.HeadApproved = true
	if state, reason := e.State(); state != domain.LadoTaskIntegrating || reason != "" {
		t.Fatalf("state = %s (%q), want integrating", state, reason)
	}
	e.PRs, e.PRRepos = nil, nil
	e.TimedOutAt = time.Now()
	if state, _ := e.State(); state != domain.LadoTaskIntegrating {
		t.Fatalf("an execution without tracked PRs: state = %s, want integrating", state)
	}
}

func TestLadoExecutionTrackedPRs(t *testing.T) {
	e := multiExec()
	got := e.TrackedPRs()
	if len(got) != 2 || got[0].Repo != "back" || got[1].Repo != "front" || e.PRs[0].Repo != "front" {
		t.Fatalf("tracked = %+v, want back then front, the execution untouched", got)
	}
	e.PRs = nil
	e.SettledAt = time.Now()
	got = e.TrackedPRs()
	if len(got) != 1 || got[0].PR.URL != e.PR.URL || got[0].Verification != e.Verification || !got[0].Settled {
		t.Fatalf("an execution without tracked PRs: tracked = %+v", got)
	}
	e.PR = domain.LadoExecutionPR{}
	if got = e.TrackedPRs(); got != nil {
		t.Fatalf("an execution without a PR: tracked = %+v", got)
	}
}

func TestLadoMergeBlockers(t *testing.T) {
	e := multiExec()
	back, front := e.PRs[1].PR.URL, e.PRs[0].PR.URL
	urls := func(prs []domain.LadoExecutionTrackedPR) []string {
		var out []string
		for _, pr := range prs {
			out = append(out, pr.PR.URL)
		}
		return out
	}
	if got := urls(domain.LadoMergeBlockers(e, front)); !slices.Equal(got, []string{back}) {
		t.Fatalf("front's blockers = %q, want back's PR", got)
	}
	if got := domain.LadoMergeBlockers(e, back); len(got) != 0 {
		t.Fatalf("back's blockers = %+v, want none", got)
	}
	e.PRs[1].PR.Merged = true
	if got := domain.LadoMergeBlockers(e, front); len(got) != 0 {
		t.Fatalf("front's blockers once back merged = %+v, want none", got)
	}
	// A back PR closed without merging can never merge: front does not wait
	// for it (review of #64).
	e.PRs[1].PR.Merged, e.PRs[1].PR.Closed = false, true
	if got := domain.LadoMergeBlockers(e, front); len(got) != 0 {
		t.Fatalf("front's blockers once back closed unmerged = %+v, want none", got)
	}
	e.PRs[1].PR.Closed = false
	e.PRRepos = nil
	if got := domain.LadoMergeBlockers(e, front); len(got) != 0 {
		t.Fatalf("without an order: blockers = %+v, want none", got)
	}
	if got := domain.LadoMergeBlockers(multiExec(), "https://example.com/other"); len(got) != 0 {
		t.Fatalf("an untracked PR: blockers = %+v, want none", got)
	}
}

func TestLadoIntegrationApprovalRenewable(t *testing.T) {
	now := time.Now()
	cases := []struct {
		approval domain.LadoIntegrationApproval
		want     bool
	}{
		{domain.LadoIntegrationApproval{}, false},
		{domain.LadoIntegrationApproval{RevokedAt: now, RevokedReason: domain.LadoCodeBaseOverlap}, false},
		{domain.LadoIntegrationApproval{RevokedAt: now, RevokedReason: domain.LadoIntegrationSiblingRevoked}, true},
		{domain.LadoIntegrationApproval{RevokedReason: domain.LadoIntegrationSiblingRevoked}, false},
	}
	for i, tc := range cases {
		if got := tc.approval.Renewable(); got != tc.want {
			t.Errorf("case %d: Renewable() = %v, want %v", i, got, tc.want)
		}
	}
	// A sibling's revocation counts as no approval.
	revoked := domain.LadoIntegrationApproval{TargetBranch: "main", RevokedAt: now, RevokedReason: domain.LadoIntegrationSiblingRevoked}
	if _, ok := domain.LadoApprovalFor([]domain.LadoIntegrationApproval{revoked}, "main"); ok {
		t.Fatal("a sibling-revoked approval holds")
	}
}

func TestLadoPartRunRepoHelpers(t *testing.T) {
	if (domain.LadoPartRunRepo{BaseSHA: "a", HeadSHA: "a"}).Changed() || !(domain.LadoPartRunRepo{BaseSHA: "a", HeadSHA: "b"}).Changed() || (domain.LadoPartRunRepo{BaseSHA: "a"}).Changed() {
		t.Fatal("Changed is wrong")
	}
	if !(domain.LadoTaskPartRepo{StartSHA: "a", MergeTipSHA: "a"}).Empty() || (domain.LadoTaskPartRepo{StartSHA: "a", MergeTipSHA: "b"}).Empty() || (domain.LadoTaskPartRepo{}).Empty() {
		t.Fatal("Empty is wrong")
	}
}
