package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// ladoSCM is AO's SCM provider: it answers PRs ready to merge at their head,
// counts the reads and merges, and records the references it was asked for.
type ladoSCM struct {
	refs    []ports.SCMPRRef
	err     error
	fetched bool
	merges  int
	// target and state are what the host has of the PR beyond its head.
	target string
	state  string
}

func (s *ladoSCM) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	s.refs = append(s.refs, refs...)
	if s.err != nil {
		return nil, s.err
	}
	head := strings.Repeat("ab", 20)
	return []ports.SCMObservation{{
		Fetched:      s.fetched,
		PR:           ports.SCMPRObservation{URL: refs[0].URL, Number: refs[0].Number, HeadSHA: head, TargetBranch: s.target, State: s.state, Merged: s.state == "merged"},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: head},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
	}}, nil
}

func (s *ladoSCM) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return ports.SCMReviewObservation{}, nil
}

func (s *ladoSCM) MergePullRequest(context.Context, ports.SCMMergeRequest) (ports.SCMMergeResult, error) {
	s.merges++
	return ports.SCMMergeResult{MergeCommitSHA: strings.Repeat("ef", 20)}, nil
}

// The daemon installs the verification loop as the gate of AO's merge: a
// LADO execution's merge request merges through AO only once its head's
// integration is approved in LADO's store.
func TestInstallLadoMergeGateGuardsAOsMerge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	worker := seedLadoVerifyExecution(t, store, cfg, at)
	head := strings.Repeat("ab", 20)
	url := "https://gitlab.com/acme/demo/-/merge_requests/7"
	if err := store.WriteSCMObservation(ctx, domain.PullRequest{
		URL: url, SessionID: worker, Number: 7, HeadSHA: head, TargetBranch: "main", Title: "로그인을 더한다", UpdatedAt: at,
		Provider: "gitlab", Host: "gitlab.com", Repo: "acme/demo", Mergeability: domain.MergeMergeable,
	}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write the merge request: %v", err)
	}
	// #53: the gate fetches the PR's target from the project's repository
	// before an approved head merges, so the project gets a real one whose
	// origin's main the approval was given against.
	repoDir, baseTip := ladoGateRepo(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: repoDir, RepoOriginURL: "https://gitlab.com/acme/demo.git", RegisteredAt: at}); err != nil {
		t.Fatalf("point the project at its repository: %v", err)
	}
	verify := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{}, slog.New(slog.DiscardHandler)).verify
	scm := &ladoSCM{fetched: true}
	actions := prsvc.NewActionService(prsvc.ActionDeps{Store: store, Reader: scm, Merger: scm})
	installLadoMergeGate(actions, nil, verify)
	installLadoMergeGate(nil, nil, verify)
	installLadoMergeGate(actions, nil, nil)

	merge := func() error {
		_, err := actions.Merge(ctx, prsvc.MergeRequest{PRID: "7", PRURL: url, ExpectedHeadSHA: head})
		return err
	}
	var apiErr *apierr.Error
	if err := merge(); !errors.As(err, &apiErr) || apiErr.Code != "LADO_INTEGRATION_APPROVAL_REQUIRED" || scm.merges != 0 || len(scm.refs) != 0 {
		t.Fatalf("merge before the approval = %v after %d reads and %d merges; want LADO's refusal before any", err, len(scm.refs), scm.merges)
	}

	var items []domain.LadoVerifyItem
	for _, kind := range domain.LadoVerifyKinds() {
		item, err := domain.NewLadoVerifyItem(kind, domain.LadoVerifyPass, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	record := domain.LadoVerification{ExecutionSessionID: worker, HeadSHA: head, PRURL: url, Items: items, ComputedAt: at}
	sum, err := record.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertLadoVerification(ctx, record); err != nil {
		t.Fatalf("UpsertLadoVerification: %v", err)
	}
	proof := domain.LadoPresenceProof{Nonce: "BBBBBBBBBBBBBBBBBBBBBB", Action: domain.LadoPresenceIntegrationApprove, SubjectID: string(worker) + "@" + head, ExpiresAt: at.Add(time.Minute), UsedAt: at}
	if err := store.ApproveLadoIntegration(ctx, domain.LadoIntegrationApproval{
		ID: "lado-integration-1", ExecutionSessionID: worker, PRURL: url, HeadSHA: head, VerificationSHA256: sum,
		Actor: "human", ApprovedAt: at, StatusState: domain.LadoIntegrationStatusNotApplicable, BaseTipSHA: baseTip,
	}, proof, domain.LadoAuditEvent{At: at, Actor: "human", Action: "integration.approved", SubjectKind: "execution", SubjectID: string(worker), Payload: []byte("{}")}); err != nil {
		t.Fatalf("ApproveLadoIntegration: %v", err)
	}
	// A target the gate cannot fetch refuses the merge (#53): it fails closed.
	origin := ladoGit(t, repoDir, "remote", "get-url", "origin")
	ladoGit(t, repoDir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	if err := merge(); !errors.As(err, &apiErr) || apiErr.Code != "LADO_MERGE_GATE_FAILED" || scm.merges != 0 {
		t.Fatalf("merge with an unreachable target = %v after %d merges; want LADO_MERGE_GATE_FAILED before any", err, scm.merges)
	}
	ladoGit(t, repoDir, "remote", "set-url", "origin", origin)
	if err := merge(); err != nil || scm.merges != 1 {
		t.Fatalf("merge of the approved head = %v after %d merges, want one merge", err, scm.merges)
	}
}

// #64: a task whose merge requests are in two child repositories of a
// workspace. The loop pins both from LADO's store, back first as the plan
// orders, and AO's merge gate lets front merge only once back merged.
func TestLadoMergeGateKeepsTheOrderOfATasksPRs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	worker := seedLadoVerifyExecutionWith(t, store, cfg, at, domain.LadoExecution{PRRepos: []string{"back", "front"}})
	root := t.TempDir()
	tips := map[string]string{}
	for _, name := range []string{"back", "front"} {
		_, tips[name] = ladoGateRepoAt(t, root, name)
	}
	if err := store.UpsertWorkspaceProject(ctx, domain.ProjectRecord{ID: "mer", Path: root, Kind: domain.ProjectKindWorkspace, RegisteredAt: at}, []domain.WorkspaceRepoRecord{
		{ProjectID: "mer", Name: "back", RelativePath: "back", RepoOriginURL: "https://gitlab.com/acme/back.git", DefaultBranch: "main"},
		{ProjectID: "mer", Name: "front", RelativePath: "front", RepoOriginURL: "https://gitlab.com/acme/front.git", DefaultBranch: "main"},
	}); err != nil {
		t.Fatalf("make the project a workspace: %v", err)
	}
	type mr struct {
		url, repo, head string
		number          int
	}
	back := mr{"https://gitlab.com/acme/back/-/merge_requests/1", "back", strings.Repeat("b1", 20), 1}
	front := mr{"https://gitlab.com/acme/front/-/merge_requests/2", "front", strings.Repeat("f1", 20), 2}
	write := func(m mr, updated time.Time, merged bool) {
		t.Helper()
		if err := store.WriteSCMObservation(ctx, domain.PullRequest{
			URL: m.url, SessionID: worker, Number: m.number, HeadSHA: m.head, TargetBranch: "main", Title: "로그인을 더한다", UpdatedAt: updated,
			Provider: "gitlab", Host: "gitlab.com", Repo: "acme/" + m.repo, Merged: merged,
		}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatalf("write the merge request: %v", err)
		}
	}
	write(back, at, false)
	write(front, at.Add(time.Second), false)

	verify := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{}, slog.New(slog.DiscardHandler)).verify
	// The heads are not in the repositories, so their records wait; the
	// pass pins the merge requests first.
	_ = verify.Tick(ctx)
	exec, ok, err := store.LadoExecution(ctx, worker)
	if err != nil || !ok {
		t.Fatalf("LadoExecution = %v, %v", ok, err)
	}
	var tracked []string
	for _, pr := range exec.PRs {
		tracked = append(tracked, fmt.Sprintf("%s@%s#%d", pr.PR.URL, pr.Repo, pr.Position))
	}
	if want := []string{back.url + "@back#0", front.url + "@front#1"}; !slices.Equal(tracked, want) || exec.PR.URL != back.url {
		t.Fatalf("tracked = %v with %s first, want %v", tracked, exec.PR.URL, want)
	}

	// Both heads are approved.
	var items []domain.LadoVerifyItem
	for _, kind := range domain.LadoVerifyKinds() {
		item, err := domain.NewLadoVerifyItem(kind, domain.LadoVerifyPass, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	var approvals []domain.LadoIntegrationApproval
	for i, m := range []mr{back, front} {
		record := domain.LadoVerification{ExecutionSessionID: worker, HeadSHA: m.head, PRURL: m.url, Items: items, ComputedAt: at}
		sum, err := record.SHA256()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpsertLadoVerification(ctx, record); err != nil {
			t.Fatalf("UpsertLadoVerification: %v", err)
		}
		approvals = append(approvals, domain.LadoIntegrationApproval{
			ID: fmt.Sprintf("lado-integration-%d", i), ExecutionSessionID: worker, PRURL: m.url, HeadSHA: m.head, TargetBranch: "main", VerificationSHA256: sum,
			Actor: "human", ApprovedAt: at, StatusState: domain.LadoIntegrationStatusNotApplicable, BaseTipSHA: tips[m.repo],
		})
	}
	proof := domain.LadoPresenceProof{Nonce: "CCCCCCCCCCCCCCCCCCCCCC", Action: domain.LadoPresenceIntegrationApprove,
		SubjectID: string(worker) + "@" + back.head + "," + front.head, ExpiresAt: at.Add(time.Minute), UsedAt: at}
	if err := store.ApproveLadoIntegrations(ctx, approvals, proof, domain.LadoAuditEvent{
		At: at, Actor: "human", Action: "integration.approved", SubjectKind: "execution", SubjectID: string(worker), Payload: []byte("{}"),
	}); err != nil {
		t.Fatalf("ApproveLadoIntegrations: %v", err)
	}

	gate := func(m mr) error {
		return verify.AuthorizeMerge(ctx, domain.PullRequest{URL: m.url, SessionID: worker, Number: m.number, TargetBranch: "main"}, m.head)
	}
	var apiErr *apierr.Error
	if err := gate(front); !errors.As(err, &apiErr) || apiErr.Code != "LADO_INTEGRATION_MERGE_ORDER" {
		t.Fatalf("merge front before back = %v, want LADO_INTEGRATION_MERGE_ORDER", err)
	}
	if waiting, _ := apiErr.Details["waitingFor"].([]string); !slices.Equal(waiting, []string{back.url}) {
		t.Errorf("front waits for %v, want back", apiErr.Details["waitingFor"])
	}
	if err := gate(back); err != nil {
		t.Fatalf("merge back = %v, want allowed", err)
	}
	write(back, at.Add(2*time.Second), true)
	if err := gate(front); err != nil {
		t.Fatalf("merge front once back merged = %v, want allowed", err)
	}
}

// ladoGateRepo makes a bare origin with one commit on main and a clone of it,
// and returns the clone and main's commit.
func ladoGateRepo(t *testing.T) (string, string) {
	t.Helper()
	return ladoGateRepoAt(t, t.TempDir(), "app")
}

// ladoGateRepoAt is ladoGateRepo with the clone at root/name, its origin
// beside it.
func ladoGateRepoAt(t *testing.T, root, name string) (string, string) {
	t.Helper()
	origin, dir := filepath.Join(root, name+".git"), filepath.Join(root, name)
	ladoGit(t, root, "init", "-q", "--bare", "-b", "main", origin)
	ladoGit(t, root, "clone", "-q", origin, dir)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ladoGit(t, dir, "add", "-A")
	ladoGit(t, dir, "-c", "user.name=LADO", "-c", "user.email=lado@example.com", "-c", "commit.gpgsign=false",
		"commit", "-q", "--no-verify", "-m", "Start the app")
	ladoGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	return dir, ladoGit(t, dir, "rev-parse", "HEAD")
}

// ladoGit runs git in dir, prompts off, and returns its output trimmed.
func ladoGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// ladoLiveHeads reads a merge request through AO's SCM reader with the
// reference AO's merge builds, and returns its head, target branch and state.
func TestLadoLiveHeadsReadThePRFromItsHost(t *testing.T) {
	ctx := context.Background()
	pr := domain.PullRequest{URL: "https://gitlab.com/acme/tools/demo/-/merge_requests/7", Number: 7, Provider: "gitlab", Host: "gitlab.com", Repo: "acme/tools/demo"}
	scm := &ladoSCM{fetched: true, target: "release"}
	live, err := ladoLiveHeads{reader: scm}.LivePR(ctx, pr)
	if want := (ladoverify.LivePR{HeadSHA: strings.Repeat("ab", 20), TargetBranch: "release"}); err != nil || live != want {
		t.Fatalf("LivePR = %+v, %v; want %+v", live, err, want)
	}
	want := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "acme/tools", Name: "demo", Repo: "acme/tools/demo"}, Number: 7, URL: pr.URL}
	if len(scm.refs) != 1 || scm.refs[0] != want {
		t.Fatalf("refs = %+v, want %+v", scm.refs, want)
	}

	for _, state := range []string{"merged", "closed"} {
		live, err := (ladoLiveHeads{reader: &ladoSCM{fetched: true, state: state}}).LivePR(ctx, pr)
		if err != nil || live.Merged != (state == "merged") || live.Closed != (state == "closed") {
			t.Errorf("LivePR of a %s PR = %+v, %v; want it %s", state, live, err, state)
		}
	}

	unknown := pr
	unknown.Repo = ""
	if _, err := (ladoLiveHeads{reader: scm}).LivePR(ctx, unknown); err == nil || len(scm.refs) != 1 {
		t.Errorf("LivePR of a PR without a repository = %v after %d reads, want an error without a read", err, len(scm.refs))
	}
	if _, err := (ladoLiveHeads{reader: &ladoSCM{}}).LivePR(ctx, pr); err == nil {
		t.Error("LivePR of a PR the host did not return = nil error")
	}
	failing := &ladoSCM{err: errors.New("the host answered 502")}
	if _, err := (ladoLiveHeads{reader: failing}).LivePR(ctx, pr); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("LivePR when the host fails = %v, want its error", err)
	}
}
