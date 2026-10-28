package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// ladoVerifyPlan is an approved plan that needs no review.
const ladoVerifyPlan = `---
task: lado-task-1
issue: gitlab:acme/demo#12
base_ref: main
steps:
  - Add the login route
acceptance_criteria:
  - A user can sign in
affected_paths:
  - src/**
test_commands: []
risk: low
permission_ceiling: auto
review:
  required: false
plan_source: issue
---

Plan.
`

// seedLadoVerifyExecution gives project mer, a GitLab repository whose
// folder does not exist (so no git command starts), a task with an approved
// plan pinned in the blobs under cfg's state folder, and a worker session
// linked as the plan's execution, as the dispatcher links it.
func seedLadoVerifyExecution(t *testing.T, store *sqlite.Store, cfg config.Config, at time.Time) domain.SessionID {
	t.Helper()
	ctx := context.Background()
	origin := "https://gitlab.com/acme/demo.git"
	repo, err := domain.LadoTrackerRepoFromOrigin(origin)
	if err != nil {
		t.Fatalf("origin: %v", err)
	}
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: filepath.Join(t.TempDir(), "gone"), RepoOriginURL: origin, RegisteredAt: at}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := store.SetLadoMode(ctx, "mer", true, at); err != nil {
		t.Fatalf("turn LADO mode on: %v", err)
	}
	blobs := ladoblob.New(cfg.StateDir)
	t.Cleanup(func() { _ = blobs.Close() })
	plan, err := blobs.Put([]byte(ladoVerifyPlan))
	if err != nil {
		t.Fatalf("pin the plan: %v", err)
	}
	issue := strings.Repeat("1", 64)
	if _, err := store.CreateLadoTask(ctx, domain.LadoTask{
		ID: "lado-task-1", ProjectID: "mer", Repo: repo, IssueRef: domain.LadoIssueRef(domain.TrackerID{Provider: repo.Provider, Native: repo.Native + "#12"}),
		CreatedAt: at, UpdatedAt: at,
		Issue: domain.LadoObservedIssue{Title: "Add login", State: domain.IssueOpen, SnapshotSHA256: issue, ObservedAt: at},
	}); err != nil {
		t.Fatalf("CreateLadoTask: %v", err)
	}
	if _, err := store.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{ID: "req-1", TaskID: "lado-task-1", SessionID: "mer-orchestrator-1", IssueSnapshotSHA256: issue, RequestedAt: at}); err != nil {
		t.Fatalf("CreateLadoPlanRequest: %v", err)
	}
	rev, _, err := store.PinLadoPlanRevision(ctx, domain.LadoPlanRevision{
		ID: "lado-plan-1", TaskID: "lado-task-1", RequestID: "req-1", SessionID: "mer-orchestrator-1", Path: domain.LadoPlanPath("lado-task-1"),
		ArtifactVersion: 1, Source: "agent", SHA256: plan, BaseRef: "main", IssueSnapshotSHA256: issue, CreatedAt: at,
	})
	if err != nil {
		t.Fatalf("PinLadoPlanRevision: %v", err)
	}
	content, err := json.Marshal(map[string]string{"permissionCeiling": "auto"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.LadoPolicySnapshot{ID: "lado-snapshot-1", SHA256: strings.Repeat("3", 64), Content: content}
	proof := domain.LadoPresenceProof{Nonce: "AAAAAAAAAAAAAAAAAAAAAA", Action: domain.LadoPresencePlanApprove, SubjectID: string(rev.ID), ExpiresAt: at.Add(time.Minute), UsedAt: at}
	event := domain.LadoAuditEvent{At: at, Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: string(rev.ID), Payload: []byte("{}")}
	if _, _, err := store.ApproveLadoPlan(ctx, domain.LadoPlanApproval{ID: "approval-1", RevisionID: rev.ID, TaskID: rev.TaskID, SHA256: plan, ApprovedAt: at},
		proof, event, &snapshot, &domain.LadoQueueEntry{ID: "lado-queue-1"}); err != nil {
		t.Fatalf("ApproveLadoPlan: %v", err)
	}
	claimed, err := store.ClaimLadoQueueEntries(ctx, at, at.Add(time.Hour), 1, nil, func() string { return "lease-1" })
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v; want lado-queue-1", claimed, err)
	}
	worker, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		ClientRequestID: domain.LadoClientRequestID(claimed[0].ID, claimed[0].AttemptCount), ClientRequestHash: strings.Repeat("e", 64),
		CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.LinkLadoExecution(ctx, claimed[0].ID, claimed[0].LeaseToken, domain.LadoExecution{SessionID: worker.ID, StartedAt: at}); err != nil {
		t.Fatalf("LinkLadoExecution: %v", err)
	}
	return worker.ID
}

// writeLadoVerifyPR writes the worker's open merge request at head with
// checks, as AO's SCM observer does.
func writeLadoVerifyPR(t *testing.T, store *sqlite.Store, worker domain.SessionID, head string, at time.Time, checks ...domain.PullRequestCheck) string {
	t.Helper()
	url := "https://gitlab.com/acme/demo/-/merge_requests/7"
	if err := store.WriteSCMObservation(context.Background(), domain.PullRequest{
		URL: url, SessionID: worker, Number: 7, HeadSHA: head, TargetBranch: "main", Title: "로그인을 더한다", UpdatedAt: at,
	}, checks, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write the merge request: %v", err)
	}
	return url
}

type ladoModeRecorder struct {
	fn func(domain.ProjectID)
}

func (r *ladoModeRecorder) SetOnLadoModeOn(fn func(domain.ProjectID)) { r.fn = fn }

// The verification loop wireLadoWork builds runs once started: its first
// pass reads the execution, its plan from the pinned blob, the merge
// request's head and its CI checks through the store and the host's rules
// through the repository clients, and stores the head's record. Stopping
// the daemon's context and waiting ends the loop: a later head is not
// verified.
func TestWireLadoWorkStartsTheVerifier(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	at := time.Now().UTC().Truncate(time.Second)
	worker := seedLadoVerifyExecution(t, store, cfg, at)
	head := strings.Repeat("ab", 20)
	prURL := writeLadoVerifyPR(t, store, worker, head, at,
		domain.PullRequestCheck{Name: "unit", CommitHash: head, Status: domain.PRCheckPassed, CreatedAt: at})
	modes := &ladoModeRecorder{}

	verify := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{ladoModes: modes}, slog.New(slog.DiscardHandler)).verify
	if verify == nil || modes.fn == nil {
		t.Fatalf("wireLadoWork returned verifier %v and set LADO mode's hook %v, want both", verify, modes.fn != nil)
	}
	verify.Start(ctx)

	var record domain.LadoVerification
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, ok, err := store.GetLadoVerification(ctx, worker, head)
		if err != nil {
			t.Fatalf("GetLadoVerification: %v", err)
		}
		if ok {
			record = got
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the loop stored no record of the head within 10s")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if record.PRURL != prURL || len(record.Items) != len(domain.LadoVerifyKinds()) {
		t.Fatalf("record = %+v, want every item of %s", record, prURL)
	}
	items := map[domain.LadoVerifyKind]domain.LadoVerifyItem{}
	for _, item := range record.Items {
		items[item.Kind] = item
	}
	if item := items[domain.LadoVerifyRequiredChecks]; item.Outcome != domain.LadoVerifyPass || !strings.Contains(string(item.Details), `"unit"`) {
		t.Errorf("required checks = %+v, want the passed unit check", item)
	}
	if item := items[domain.LadoVerifyProtection]; item.Outcome != domain.LadoVerifyConfirm || item.Code != domain.LadoCodeUnprotectedGitLab {
		t.Errorf("protection = %+v, want GitLab's confirm", item)
	}
	if item := items[domain.LadoVerifyReview]; item.Outcome != domain.LadoVerifyInfo || item.Code != domain.LadoCodeNotRequired {
		t.Errorf("review = %+v, want not required by the pinned plan", item)
	}
	// The project folder is gone, so what git reads waits.
	if item := items[domain.LadoVerifyBaseBranch]; item.Outcome != domain.LadoVerifyWait {
		t.Errorf("base branch = %+v, want waiting on git", item)
	}
	exec, ok, err := store.LadoExecution(ctx, worker)
	if err != nil || !ok || exec.Verification.HeadSHA != head || exec.Verification.Outcome != domain.LadoVerifyWait {
		t.Fatalf("execution = %+v, %v, %v; want the record's wait on head %s", exec.Verification, ok, err, head)
	}

	cancel()
	waited := make(chan struct{})
	go func() {
		verify.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not stop within 10s of the daemon's context")
	}
	next := strings.Repeat("cd", 20)
	writeLadoVerifyPR(t, store, worker, next, at.Add(time.Minute))
	verify.Kick()
	verify.RefreshProtection("mer")
	time.Sleep(100 * time.Millisecond)
	if _, ok, err := store.GetLadoVerification(context.Background(), worker, next); err != nil || ok {
		t.Fatalf("record of a head pushed after the stop = %v, %v; want none", ok, err)
	}
}

// ladoReviewManager stands for AO's review service.
type ladoReviewManager struct {
	reviewsvc.Manager
	outcome   reviewsvc.TriggerOutcome
	err       error
	requests  []reviewsvc.TriggerRequest
	cancelled []domain.SessionID
}

func (m *ladoReviewManager) TriggerRequested(_ context.Context, _ domain.SessionID, req reviewsvc.TriggerRequest) (reviewsvc.TriggerOutcome, error) {
	m.requests = append(m.requests, req)
	return m.outcome, m.err
}

func (m *ladoReviewManager) Cancel(_ context.Context, worker domain.SessionID) (reviewcore.CancelResult, error) {
	m.cancelled = append(m.cancelled, worker)
	return reviewcore.CancelResult{}, m.err
}

// LADO asks AO for a review as a person's request that refuses a reviewed
// head, and records the runs AO created: each of several reviewers', or the
// one run of an older answer. A refusal comes back as it is.
func TestLadoReviewsAskAOAndReturnTheCreatedRuns(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		outcome reviewcore.TriggerResult
		want    []string
	}{
		{"several reviewers", reviewcore.TriggerResult{Created: true, Run: domain.ReviewRun{ID: "run-1"}, CreatedRuns: []domain.ReviewRun{{ID: "run-1"}, {ID: "run-2"}}}, []string{"run-1", "run-2"}},
		{"one run", reviewcore.TriggerResult{Created: true, Run: domain.ReviewRun{ID: "run-3"}}, []string{"run-3"}},
		{"none created", reviewcore.TriggerResult{Run: domain.ReviewRun{ID: "run-4"}}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &ladoReviewManager{outcome: reviewsvc.TriggerOutcome{TriggerResult: tc.outcome}}
			ids, err := ladoReviews{manager: manager}.Trigger(ctx, "mer-7", "https://github.com/acme/demo/pull/7", domain.ReviewerHarness("codex"))
			if err != nil || !slices.Equal(ids, tc.want) {
				t.Fatalf("Trigger = %v, %v; want %v", ids, err, tc.want)
			}
			want := reviewsvc.TriggerRequest{Harness: "codex", Source: domain.ReviewTriggerManual, PRURL: "https://github.com/acme/demo/pull/7", RejectReviewedHead: true}
			if len(manager.requests) != 1 || manager.requests[0] != want {
				t.Fatalf("requests = %+v, want %+v", manager.requests, want)
			}
		})
	}
	manager := &ladoReviewManager{err: reviewsvc.ErrHeadAlreadyReviewed}
	if _, err := (ladoReviews{manager: manager}).Trigger(ctx, "mer-7", "https://github.com/acme/demo/pull/7", ""); !errors.Is(err, reviewsvc.ErrHeadAlreadyReviewed) {
		t.Fatalf("Trigger on a reviewed head = %v, want AO's refusal", err)
	}
	manager.err = nil
	if err := (ladoReviews{manager: manager}).Cancel(ctx, "mer-7"); err != nil || !slices.Equal(manager.cancelled, []domain.SessionID{"mer-7"}) {
		t.Fatalf("Cancel = %v, cancelled %v; want mer-7", err, manager.cancelled)
	}
}
