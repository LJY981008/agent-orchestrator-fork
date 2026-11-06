package ladoverify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// gitlabGuarded is a GitLab branch LADO counts as protected: a protected
// branch no one may push to or force push, a project that requires a
// succeeding pipeline and does not count skipped ones, and a Maintainer
// token with the api scope.
func gitlabGuarded() *ports.LadoGitLabProtection {
	yes, no := true, false
	return &ports.LadoGitLabProtection{
		Protected:              true,
		MatchedNames:           []string{"main"},
		PushAccess:             []ports.LadoGitLabAccess{{AccessLevel: 0}},
		MergeAccess:            []ports.LadoGitLabAccess{{AccessLevel: 40}},
		PipelineMustSucceed:    &yes,
		SkippedPipelineAllowed: &no,
		AccessLevel:            40,
		TokenScopes:            []string{"api", "read_user"},
		ScopesKnown:            true,
	}
}

var gitlabRepo = domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project", Host: "gitlab.example.com"}

func gitlabRead(protection *ports.LadoGitLabProtection) ProtectionRead {
	return ProtectionRead{Provider: domain.TrackerProviderGitLab, Repo: gitlabRepo, Branch: "main", Rules: ports.LadoBranchRules{GitLab: protection}}
}

func TestGitLabProtectionItem(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }
	all := []string{"protected_branch", "pipeline_must_succeed", "skipped_pipeline", "integration_status"}
	for _, tc := range []struct {
		name       string
		change     func(p *ports.LadoGitLabProtection)
		postFailed bool
		want       []string
	}{
		{name: "guarded", change: func(*ports.LadoGitLabProtection) {}},
		{name: "scopes unknown", change: func(p *ports.LadoGitLabProtection) { p.ScopesKnown, p.TokenScopes = false, nil }},
		{name: "Developer token", change: func(p *ports.LadoGitLabProtection) { p.AccessLevel = 30 }},
		{name: "code owner approval", change: func(p *ports.LadoGitLabProtection) { p.CodeOwnerApprovalRequired = true }},
		{name: "not protected", change: func(p *ports.LadoGitLabProtection) {
			p.Protected, p.MatchedNames, p.PushAccess, p.AllowForcePush = false, nil, nil, true
		}, want: []string{"protected_branch"}},
		{name: "Developers push", change: func(p *ports.LadoGitLabProtection) {
			p.PushAccess = []ports.LadoGitLabAccess{{AccessLevel: 0}, {AccessLevel: 30}}
		}, want: []string{"push_access"}},
		{name: "a user pushes", change: func(p *ports.LadoGitLabProtection) {
			p.PushAccess = []ports.LadoGitLabAccess{{AccessLevel: 0}, {User: true}}
		}, want: []string{"push_access"}},
		{name: "a group pushes", change: func(p *ports.LadoGitLabProtection) { p.PushAccess = []ports.LadoGitLabAccess{{Group: true}} }, want: []string{"push_access"}},
		{name: "a deploy key pushes", change: func(p *ports.LadoGitLabProtection) { p.PushAccess = []ports.LadoGitLabAccess{{DeployKey: true}} }, want: []string{"push_access"}},
		{name: "push access unsaid", change: func(p *ports.LadoGitLabProtection) { p.PushAccess = nil }, want: []string{"push_access"}},
		{name: "force push", change: func(p *ports.LadoGitLabProtection) { p.AllowForcePush = true }, want: []string{"force_push"}},
		{name: "pipelines need not succeed", change: func(p *ports.LadoGitLabProtection) { p.PipelineMustSucceed = boolPtr(false) }, want: []string{"pipeline_must_succeed"}},
		{name: "pipeline setting unsaid", change: func(p *ports.LadoGitLabProtection) { p.PipelineMustSucceed = nil }, want: []string{"pipeline_must_succeed"}},
		{name: "skipped pipelines pass", change: func(p *ports.LadoGitLabProtection) { p.SkippedPipelineAllowed = boolPtr(true) }, want: []string{"skipped_pipeline"}},
		{name: "skipped setting unsaid", change: func(p *ports.LadoGitLabProtection) { p.SkippedPipelineAllowed = nil }, want: []string{"skipped_pipeline"}},
		{name: "Reporter token", change: func(p *ports.LadoGitLabProtection) { p.AccessLevel = 20 }, want: []string{"integration_status"}},
		{name: "no api scope", change: func(p *ports.LadoGitLabProtection) { p.TokenScopes = []string{"read_api"} }, want: []string{"integration_status"}},
		{name: "last post failed", change: func(*ports.LadoGitLabProtection) {}, postFailed: true, want: []string{"integration_status"}},
		{name: "everything", change: func(p *ports.LadoGitLabProtection) {
			*p = ports.LadoGitLabProtection{Protected: true, PushAccess: []ports.LadoGitLabAccess{{AccessLevel: 40}}, AllowForcePush: true}
		}, want: []string{"push_access", "force_push", "pipeline_must_succeed", "skipped_pipeline", "integration_status"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			protection := gitlabGuarded()
			tc.change(protection)
			read := gitlabRead(protection)
			read.StatusPostFailed = tc.postFailed
			item, err := EvaluateProtection(read)
			if err != nil {
				t.Fatal(err)
			}
			details := protectionDetails(t, item)
			wantOutcome, wantCode := domain.LadoVerifyPass, ""
			if len(tc.want) > 0 {
				wantOutcome, wantCode = domain.LadoVerifyConfirm, domain.LadoCodeUnprotected
			}
			if item.Outcome != wantOutcome || item.Code != wantCode || !slices.Equal(details.Missing, protectionOrEmpty(tc.want)) {
				t.Fatalf("item = %s %q missing %v, want %s %q missing %v", item.Outcome, item.Code, details.Missing, wantOutcome, wantCode, tc.want)
			}
			if details.Provider != "gitlab" || details.Repo != "group/project" || details.Branch != "main" || details.ErrorClass != "" {
				t.Fatalf("details = %+v", details)
			}
			if details.CodeOwnerApproval != protection.CodeOwnerApprovalRequired {
				t.Fatalf("code owner approval = %v, want %v", details.CodeOwnerApproval, protection.CodeOwnerApprovalRequired)
			}
		})
	}

	t.Run("no protection read", func(t *testing.T) {
		item, err := EvaluateProtection(gitlabRead(nil))
		if err != nil {
			t.Fatal(err)
		}
		if details := protectionDetails(t, item); item.Code != domain.LadoCodeUnprotected || !slices.Equal(details.Missing, all) {
			t.Fatalf("item = %s %q missing %v, want unprotected missing %v", item.Outcome, item.Code, details.Missing, all)
		}
	})

	t.Run("a read that failed", func(t *testing.T) {
		read := gitlabRead(gitlabGuarded())
		read.Err = &ports.LadoHTTPError{Status: http.StatusForbidden, Message: "403 Forbidden"}
		item, err := EvaluateProtection(read)
		if err != nil {
			t.Fatal(err)
		}
		details := protectionDetails(t, item)
		if item.Outcome != domain.LadoVerifyConfirm || item.Code != domain.LadoCodeProtectionUnknown || details.ErrorClass != "http_403" || len(details.Missing) != 0 {
			t.Fatalf("item = %s %q %+v, want confirm protection_unknown http_403", item.Outcome, item.Code, details)
		}
		if read.ErrorMessage() == "" {
			t.Fatal("ErrorMessage of a failed GitLab read is empty")
		}
	})

	t.Run("a client that reads none", func(t *testing.T) {
		read := gitlabRead(nil)
		read.Err = ports.ErrLadoUnsupported
		item, err := EvaluateProtection(read)
		if err != nil {
			t.Fatal(err)
		}
		if item.Outcome != domain.LadoVerifyConfirm || item.Code != domain.LadoCodeUnprotectedGitLab || read.ErrorMessage() != "" {
			t.Fatalf("item = %s %q, error %q; want confirm unprotected_gitlab without one", item.Outcome, item.Code, read.ErrorMessage())
		}
	})

	t.Run("GitHub hashes as before", func(t *testing.T) {
		item, err := EvaluateProtection(ProtectionRead{Provider: domain.TrackerProviderGitHub, Repo: protectionRepo, Branch: "main", Rules: protectionRules(protectionAllTypes, "lado/integration")})
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(item.Details, &raw); err != nil {
			t.Fatal(err)
		}
		if _, ok := raw["codeOwnerApproval"]; ok {
			t.Fatalf("GitHub details %s carry codeOwnerApproval", item.Details)
		}
	})
}

// Every read, cached or new, carries how LADO's last status post on its
// repository went, on any of its branches, whatever case names it.
func TestProtectionReadCarriesTheLastPost(t *testing.T) {
	clock := &protectionClock{now: t0}
	host := &protectionHost{rules: ports.LadoBranchRules{GitLab: gitlabGuarded()}}
	p := NewProtection(host, clock.Now, time.Hour)
	ctx := context.Background()
	if read := p.Read(ctx, gitlabRepo, "main", false); read.StatusPostFailed {
		t.Fatal("a repository LADO never posted on reads as failed")
	}
	p.notePost(domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "Group/Project", Host: "gitlab.example.com"}, true)
	for _, branch := range []string{"main", "develop"} {
		if read := p.Read(ctx, gitlabRepo, branch, false); !read.StatusPostFailed {
			t.Fatalf("read of %s after a failed post = not failed", branch)
		}
	}
	if read := p.Read(ctx, protectionRepo, "main", false); read.StatusPostFailed {
		t.Fatal("another repository reads the failed post")
	}
	p.notePost(gitlabRepo, false)
	if read := p.Read(ctx, gitlabRepo, "main", false); read.StatusPostFailed {
		t.Fatal("a post that worked left the repository failed")
	}
	if n := host.count("main"); n != 2 {
		t.Fatalf("host reads of main = %d, want 2 (one per repository)", n)
	}
}

// gitlabFixture is a verify fixture whose GitLab branches are guarded.
func gitlabFixture(t *testing.T) *approveFixture {
	f := newApproveFixture(t)
	f.host.setGitLab(gitlabGuarded())
	return f
}

// addGitLab adds an execution whose task's repository is acme/demo on
// GitLab, its merge request from the source branch feature-<key>.
func (f *approveFixture) addGitLab(key, head string) domain.LadoExecution {
	f.t.Helper()
	exec := f.add(key, head)
	f.reads.updateTask(exec.TaskID, func(task *domain.LadoTask) {
		task.Repo = domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "acme/demo"}
	})
	f.store.setSourceBranch(exec.PR.URL, "feature-"+key)
	return exec
}

var labRepo = domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "acme/demo"}

func waitingPost(exec domain.LadoExecution, ref string) verifyPost {
	return verifyPost{repo: labRepo, sha: exec.PR.HeadSHA, status: ports.LadoCommitStatus{
		State: "pending", Context: "lado/integration", Description: "Waiting for approval in LADO", Ref: ref, MergeRequest: exec.PR.Number,
	}}
}

// #21: each pass posts the waiting status on the open GitLab heads without
// an approval, at most every two minutes per head; approved, merged and
// closed heads and GitHub's get none.
func TestTickPostsTheWaitingStatusOfUnapprovedGitLabHeads(t *testing.T) {
	f := gitlabFixture(t)
	waiting := f.addGitLab("1", verifyHead1)
	f.add("2", verifyHead2) // GitHub
	approved := f.addGitLab("3", verifyHead3)
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-3", ExecutionSessionID: approved.SessionID, PRURL: approved.PR.URL, HeadSHA: verifyHead3, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, StatusPostedAt: t0,
	})
	merged := f.addGitLab("4", verifyHead4)
	f.store.update(merged.SessionID, func(e *domain.LadoExecution) { e.PR.Merged = true })
	closed := f.addGitLab("5", verifyHead2)
	f.store.update(closed.SessionID, func(e *domain.LadoExecution) { e.PR.Closed = true })
	// An approval into another target does not count.
	other := f.addGitLab("6", verifyHead4)
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-6", ExecutionSessionID: other.SessionID, PRURL: other.PR.URL, HeadSHA: verifyHead4, TargetBranch: "release",
		StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, StatusPostedAt: t0,
	})

	f.tick()
	want := []verifyPost{waitingPost(waiting, "feature-1"), waitingPost(other, "feature-6")}
	if posts := f.host.postsMade(); !slices.Equal(posts, want) {
		t.Fatalf("posts = %+v\nwant %+v", posts, want)
	}
	f.clock.advance(waitingRepostEvery - time.Second)
	f.tick()
	if n := len(f.host.postsMade()); n != 2 {
		t.Fatalf("posts within two minutes = %d, want still 2", n)
	}
	f.clock.advance(time.Second)
	f.tick()
	want = append(want, want...)
	if posts := f.host.postsMade(); !slices.Equal(posts, want) {
		t.Fatalf("posts two minutes later = %+v\nwant %+v", posts, want)
	}

	// Once approved, the head gets its success and no waiting status.
	f.store.addApproval(domain.LadoIntegrationApproval{
		ID: "approval-1", ExecutionSessionID: waiting.SessionID, PRURL: waiting.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, StatusPostedAt: t0,
	})
	f.clock.advance(waitingRepostEvery)
	f.tick()
	if posts := f.host.postsMade(); len(posts) != 5 || posts[4] != waitingPost(other, "feature-6") {
		t.Fatalf("posts after the approval = %+v, want only head 6's waiting status more", posts)
	}
	// The memory holds the open GitLab heads looked at in the last two
	// minutes (1, 3 and 6), no more.
	if n := len(f.svc.waitingPosted); n != 3 {
		t.Fatalf("waiting posts kept = %d, want 3", n)
	}
}

// A waiting status the host refuses is logged, keeps the record clean, and
// makes the repository's protection lack integration_status until a post
// works again.
func TestAFailedGitLabPostLeavesTheBranchUnprotected(t *testing.T) {
	f := gitlabFixture(t)
	exec := f.addGitLab("1", verifyHead1)
	f.host.setPostErr(&ports.LadoHTTPError{Status: http.StatusForbidden, Message: "403 Forbidden"})

	f.tick()
	if item := verifyItemOf(t, f.record(exec.SessionID, verifyHead1), domain.LadoVerifyProtection); item.Outcome != domain.LadoVerifyPass {
		t.Fatalf("protection before any post = %s %q, want a pass", item.Outcome, item.Code)
	}
	f.tick() // the failed post of the first pass counts now
	item := verifyItemOf(t, f.record(exec.SessionID, verifyHead1), domain.LadoVerifyProtection)
	if details := protectionDetails(t, item); item.Code != domain.LadoCodeUnprotected || !slices.Equal(details.Missing, []string{"integration_status"}) {
		t.Fatalf("protection after a failed post = %s %q missing %v, want unprotected missing integration_status", item.Outcome, item.Code, details.Missing)
	}

	f.host.setPostErr(nil)
	f.clock.advance(waitingRepostEvery)
	f.tick()
	f.tick()
	if item := verifyItemOf(t, f.record(exec.SessionID, verifyHead1), domain.LadoVerifyProtection); item.Outcome != domain.LadoVerifyPass {
		t.Fatalf("protection after a post worked = %s %q, want a pass", item.Outcome, item.Code)
	}
}

// The loop reads no approval, then an approval is recorded before its
// waiting status goes up: the read under the lock after the post finds it
// and posts its success again, and the approval's own post, waiting for the
// lock, posts it once more. The host's last status is the success.
func TestTheWaitingStatusNeverBuriesAnApproval(t *testing.T) {
	for _, tc := range []struct {
		name string
		// post makes the approval post its own status, as the integration
		// approval does right after recording it.
		post bool
	}{{name: "recorded"}, {name: "recorded and posting", post: true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := gitlabFixture(t)
			exec := f.addGitLab("1", verifyHead1)
			approval := domain.LadoIntegrationApproval{
				ID: "approval-1", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main",
				StatusState: domain.LadoIntegrationStatusPending,
			}
			var once sync.Once
			var own sync.WaitGroup
			var ownErr error
			f.store.onApprovalsRead = func(session domain.SessionID, prURL, head string) {
				once.Do(func() {
					// The loop read no approval, under the head's lock.
					f.store.addApproval(approval)
					if tc.post {
						own.Add(1)
						go func() {
							defer own.Done()
							_, ownErr = f.svc.postIntegrationStatus(f.ctx, approval)
						}()
					}
				})
			}
			f.svc.postWaitingStatuses(f.ctx, []domain.LadoExecution{exec})
			own.Wait()
			if ownErr != nil {
				t.Fatalf("the approval's own post = %v", ownErr)
			}
			posts := f.host.postsMade()
			success := verifyPost{repo: labRepo, sha: verifyHead1, status: ports.LadoCommitStatus{
				State: "success", Context: "lado/integration", Description: "Approved in LADO", Ref: "feature-1", MergeRequest: exec.PR.Number,
			}}
			wantPosts := 2
			if tc.post {
				wantPosts = 3
			}
			if len(posts) != wantPosts || posts[0] != waitingPost(exec, "feature-1") || posts[len(posts)-1] != success {
				t.Fatalf("posts = %+v\nwant the waiting status, then the success last (%d posts)", posts, wantPosts)
			}
			if a := f.store.approval("approval-1"); a.StatusState != domain.LadoIntegrationStatusPosted {
				t.Fatalf("approval = %s, want posted", a.StatusState)
			}
		})
	}
}

// A GitLab approval given before LADO posted statuses there keeps
// not_applicable when LADO revokes it, with no status posted.
func TestRevokingALegacyGitLabApprovalPostsNothing(t *testing.T) {
	f := gitlabFixture(t)
	exec := f.addGitLab("1", verifyHead1)
	legacy := domain.LadoIntegrationApproval{
		ID: "approval-legacy", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main",
		StatusState: domain.LadoIntegrationStatusNotApplicable,
	}
	f.store.addApproval(legacy)
	revoked, err := f.svc.revokeApprovals(f.ctx, exec, []domain.LadoIntegrationApproval{legacy}, domain.LadoCodeBaseOverlap, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked) != 1 || revoked[0].StatusState != domain.LadoIntegrationStatusNotApplicable || len(f.host.postsMade()) != 0 {
		t.Fatalf("revoked = %+v, posts %+v; want it not_applicable and nothing posted", revoked, f.host.postsMade())
	}
}

// A GitLab approval whose merge request AO does not know fails as source
// branch unknown without asking the host.
func TestAGitLabStatusNeedsTheMergeRequest(t *testing.T) {
	f := gitlabFixture(t)
	exec := f.addGitLab("1", verifyHead1)
	approval := domain.LadoIntegrationApproval{
		ID: "approval-1", ExecutionSessionID: exec.SessionID, PRURL: "https://gitlab.example.com/acme/demo/-/merge_requests/99", HeadSHA: verifyHead1,
		TargetBranch: "main", StatusState: domain.LadoIntegrationStatusPending,
	}
	f.store.addApproval(approval)
	got, err := f.svc.postIntegrationStatus(f.ctx, approval)
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusState != domain.LadoIntegrationStatusFailed || got.StatusAttempts != 1 || got.StatusError != "source branch unknown" || len(f.host.postsMade()) != 0 {
		t.Fatalf("approval = %s after %d attempts (%q), posts %d; want failed as source branch unknown, nothing posted",
			got.StatusState, got.StatusAttempts, got.StatusError, len(f.host.postsMade()))
	}
	if f.svc.protection.lastPostFailed(labRepo) {
		t.Fatal("a post LADO never sent counts as the host refusing it")
	}
	if errors.Is(err, errSourceBranchUnknown) {
		t.Fatal("source branch unknown surfaced as an error")
	}
}
