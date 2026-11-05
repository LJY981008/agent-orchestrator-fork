package controllers_test

// LADO fork: the API of a task whose PRs span a workspace's repositories
// (#64): the tracked PRs of an execution read, the task's integration
// approval of all of them in one request, and the parts run's repositories.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// An execution with a back PR and a front PR lists both, in the plan's
// order, each with its own state, record and approval; the front head's
// approval LADO withdrew for the back PR's base says so. The task read lists
// them too.
func TestLadoAPI_ExecutionListsTrackedPRs(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	seed := seedLadoQueued(t, store, "mer", 1, now.Add(-time.Hour))
	worker := runLado(t, store, "mer", seed, now.Add(-30*time.Minute), domain.LadoExecution{PRRepos: []string{"back", "front"}})
	srv := newLadoTestServer(t, httpd.APIDeps{
		LadoQueue:  ladoqueue.New(ladoqueue.Deps{Store: store}),
		LadoVerify: ladoverify.New(ladoverify.Deps{Store: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}),
		LadoTasks:  ladotask.New(ladotask.Deps{Store: store, Projects: store}),
	})
	getExecution := func() (controllers.LadoExecutionResponse, string) {
		t.Helper()
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/executions/"+string(worker), "")
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, body)
		}
		return decodeLadoJSON[controllers.LadoExecutionResponse](t, body), string(body)
	}

	// Before any PR the lists are empty, not null.
	got, raw := getExecution()
	if !strings.Contains(raw, `"trackedPrs":[]`) || !reflect.DeepEqual(got.PRRepos, []string{"back", "front"}) || got.PRReposFirm {
		t.Fatalf("execution without PRs = %s, want an empty trackedPrs and the plan's repos", raw)
	}

	backHead, frontHead := strings.Repeat("b", 40), strings.Repeat("c", 40)
	backURL := writeLadoRepoPR(t, store, worker, "acme/back", 3, backHead, now.Add(-3*time.Minute))
	frontURL := writeLadoRepoPR(t, store, worker, "acme/front", 4, frontHead, now.Add(-2*time.Minute))
	for i, pr := range []struct{ url, repo string }{{backURL, "back"}, {frontURL, "front"}} {
		if ok, err := store.TrackLadoExecutionPR(ctx, worker, pr.url, pr.repo, i, now.Add(-time.Minute)); err != nil || !ok {
			t.Fatalf("track %s: %v %v", pr.url, ok, err)
		}
	}
	backRecord := ladoVerifyRecord(t, worker, backURL, backHead, now.Add(-50*time.Second))
	frontRecord := ladoVerifyRecord(t, worker, frontURL, frontHead, now.Add(-50*time.Second))
	for _, record := range []domain.LadoVerification{backRecord, frontRecord} {
		if _, err := store.UpsertLadoVerification(ctx, record); err != nil {
			t.Fatalf("store the record of %s: %v", record.PRURL, err)
		}
	}
	// Both heads were approved together; the back PR's base then moved
	// under it, which withdrew the front head's approval as a sibling's.
	approvedAt := now.Add(-40 * time.Second)
	approvals := []domain.LadoIntegrationApproval{
		{ID: "lado-integration-back", ExecutionSessionID: worker, PRURL: backURL, HeadSHA: backHead, TargetBranch: "main", VerificationSHA256: ladoRecordSHA(t, backRecord),
			Acknowledged: backRecord.ConfirmCodes(), ProofNonce: "nonce-integration-1", Actor: "human", ApprovedAt: approvedAt, StatusState: domain.LadoIntegrationStatusPosted},
		{ID: "lado-integration-front", ExecutionSessionID: worker, PRURL: frontURL, HeadSHA: frontHead, TargetBranch: "main", VerificationSHA256: ladoRecordSHA(t, frontRecord),
			Acknowledged: frontRecord.ConfirmCodes(), ProofNonce: "nonce-integration-1#1", Actor: "human", ApprovedAt: approvedAt, StatusState: domain.LadoIntegrationStatusPosted},
	}
	if err := store.ApproveLadoIntegrations(ctx, approvals,
		domain.LadoPresenceProof{Nonce: "nonce-integration-1", Action: domain.LadoPresenceIntegrationApprove, SubjectID: string(worker) + "@" + backHead + "," + frontHead, ExpiresAt: approvedAt.Add(time.Minute), UsedAt: approvedAt},
		domain.LadoAuditEvent{At: approvedAt, Actor: "human", Action: "integration.approved", SubjectKind: "execution", SubjectID: string(worker), Payload: []byte("{}")},
	); err != nil {
		t.Fatalf("approve both heads: %v", err)
	}
	if ok, err := store.RevokeLadoIntegrationApproval(ctx, "lado-integration-front", domain.LadoIntegrationSiblingRevoked, now.Add(-30*time.Second),
		domain.LadoAuditEvent{At: now.Add(-30 * time.Second), Actor: "lado", Action: domain.LadoAuditIntegrationRevoked, SubjectKind: "execution", SubjectID: string(worker), Payload: []byte("{}")},
	); err != nil || !ok {
		t.Fatalf("revoke the front approval: %v %v", ok, err)
	}

	got, raw = getExecution()
	if len(got.TrackedPRs) != 2 {
		t.Fatalf("trackedPrs = %s, want the back and front PRs", raw)
	}
	back, front := got.TrackedPRs[0], got.TrackedPRs[1]
	if back.URL != backURL || back.Number != 3 || back.Repo != "back" || back.Position != 0 || back.HeadSHA != backHead || back.Settled ||
		back.State != "integrating" || back.StateReason != "" || back.Verification == nil || back.Verification.HeadSHA != backHead ||
		back.Verification.Outcome != "confirm" || !back.Integration.HeadApproved || back.Integration.StatusState != "posted" || back.Integration.RevokedReason != "" ||
		back.Record == nil || back.Record.SHA256 != ladoRecordSHA(t, backRecord) || back.Approval == nil || back.Approval.ID != "lado-integration-back" {
		t.Fatalf("back PR = %+v, want it tracked first with its record and approval", back)
	}
	if front.URL != frontURL || front.Repo != "front" || front.Position != 1 || front.State != "integration_pending" ||
		front.Integration.HeadApproved || front.Integration.RevokedReason != domain.LadoIntegrationSiblingRevoked ||
		front.Record == nil || front.Record.PRURL != frontURL || front.Approval != nil {
		t.Fatalf("front PR = %+v, want it waiting for its approval again, revoked as a sibling", front)
	}
	if got.PR == nil || got.PR.URL != backURL || got.Integration == nil || got.Integration.ID != "lado-integration-back" ||
		got.State != "integration_pending" || len(got.UntrackedPRs) != 0 {
		t.Fatalf("execution = %+v, want the back PR on top and the task waiting for the front approval", got)
	}

	// The task read lists the tracked PRs in short, without records.
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks?projectId=mer", "")
	if status != http.StatusOK {
		t.Fatalf("tasks: status = %d, body %s", status, body)
	}
	tasks := decodeLadoJSON[controllers.LadoTaskListResponse](t, body).Tasks
	if len(tasks) != 1 || tasks[0].Execution == nil {
		t.Fatalf("tasks = %s, want one with its execution", body)
	}
	task := tasks[0].Execution
	if len(task.TrackedPRs) != 2 || task.TrackedPRs[0].Repo != "back" || task.TrackedPRs[1].Repo != "front" ||
		task.TrackedPRs[1].State != "integration_pending" || task.TrackedPRs[0].Record != nil ||
		!reflect.DeepEqual(task.PRRepos, []string{"back", "front"}) || tasks[0].State != "integration_pending" {
		t.Fatalf("task execution = %+v, want both PRs in short and the plan's repos", task)
	}
}

// A repository the plan names without a PR keeps the task validating with
// pr_missing once the PRs it has passed, while the worker runs.
func TestLadoAPI_ExecutionWaitsForAMissingPR(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	seed := seedLadoQueued(t, store, "mer", 1, now.Add(-time.Hour))
	worker := runLado(t, store, "mer", seed, now.Add(-30*time.Minute), domain.LadoExecution{PRRepos: []string{"back", "front"}})
	srv := newLadoTestServer(t, httpd.APIDeps{LadoQueue: ladoqueue.New(ladoqueue.Deps{Store: store})})
	backHead := strings.Repeat("b", 40)
	backURL := writeLadoRepoPR(t, store, worker, "acme/back", 3, backHead, now.Add(-time.Minute))
	if ok, err := store.TrackLadoExecutionPR(ctx, worker, backURL, "back", 0, now); err != nil || !ok {
		t.Fatalf("track the back PR: %v %v", ok, err)
	}
	if _, err := store.UpsertLadoVerification(ctx, ladoVerifyRecord(t, worker, backURL, backHead, now)); err != nil {
		t.Fatalf("store the back head's record: %v", err)
	}
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/executions/"+string(worker), "")
	got := decodeLadoJSON[controllers.LadoExecutionResponse](t, body)
	if status != http.StatusOK || got.State != "validating" || got.StateReason != domain.LadoValidatingPRMissing ||
		len(got.TrackedPRs) != 1 || got.TrackedPRs[0].Repo != "back" || got.TrackedPRs[0].State != "integration_pending" {
		t.Fatalf("execution = %d %s, want it validating on pr_missing while the back PR waits for its approval", status, body)
	}
}

// The task's integration approval names its PRs in prs: the service gets
// each, the proof's subject names their heads, and the answer lists each
// PR's record and approval. A body mixing prs with the single PR's fields,
// or a PR of prs without its head, never reaches the service.
func TestLadoAPI_IntegrationApproveTaskPRs(t *testing.T) {
	path := "/api/v1/lado/executions/mer-1/integration-approve"
	backURL, frontURL := "https://github.com/acme/back/pull/3", "https://github.com/acme/front/pull/4"
	backHead, frontHead := strings.Repeat("b", 40), strings.Repeat("c", 40)
	backRecord := ladoVerifyRecord(t, "mer-1", backURL, backHead, presenceNow)
	frontRecord := ladoVerifyRecord(t, "mer-1", frontURL, frontHead, presenceNow)
	backSum, frontSum := ladoRecordSHA(t, backRecord), ladoRecordSHA(t, frontRecord)
	approval := func(id, url, head, sum string) *domain.LadoIntegrationApproval {
		return &domain.LadoIntegrationApproval{ID: id, ExecutionSessionID: "mer-1", PRURL: url, HeadSHA: head, VerificationSHA256: sum,
			Actor: "human", ApprovedAt: presenceNow, StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, StatusPostedAt: presenceNow}
	}
	backApproval, frontApproval := approval("lado-integration-back", backURL, backHead, backSum), approval("lado-integration-front", frontURL, frontHead, frontSum)
	result := ladoverify.ApproveResult{
		Verification: backRecord, SHA256: backSum, Confirm: backRecord.ConfirmCodes(), Approval: backApproval,
		PRs: []ladoverify.ApprovePRResult{
			{PRURL: backURL, Verification: backRecord, SHA256: backSum, Confirm: backRecord.ConfirmCodes(), Approval: backApproval},
			{PRURL: frontURL, Verification: frontRecord, SHA256: frontSum, Confirm: frontRecord.ConfirmCodes(), Approval: frontApproval},
		},
	}
	approve := `{"prs":[{"prUrl":"` + backURL + `","headSha":"` + backHead + `","verificationSha256":"` + backSum + `","acknowledge":["outside_plan","unprotected"]},` +
		`{"prUrl":"` + frontURL + `","headSha":"` + frontHead + `","verificationSha256":"` + frontSum + `","acknowledge":["outside_plan","unprotected"]}]}`

	svc := &fakeLadoVerifyService{result: result}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoVerify: svc, LadoPresence: presenceVerifier()})
	body, status := signedRequest(t, srv, "POST", path, approve, approve)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	in := svc.gotApprove
	wantPRs := []ladoverify.ApprovePR{
		{PRURL: backURL, HeadSHA: backHead, VerificationSHA256: backSum, Acknowledge: []string{"outside_plan", "unprotected"}},
		{PRURL: frontURL, HeadSHA: frontHead, VerificationSHA256: frontSum, Acknowledge: []string{"outside_plan", "unprotected"}},
	}
	if !reflect.DeepEqual(in.PRs, wantPRs) || in.PRURL != "" || in.HeadSHA != "" || in.DryRun || svc.approvals != 1 ||
		svc.gotProof.SubjectID != "mer-1@"+backHead+","+frontHead {
		t.Fatalf("service got %+v with proof %+v; want both PRs under one proof", in, svc.gotProof)
	}
	resp := decodeLadoJSON[controllers.LadoIntegrationApproveResponse](t, body)
	if len(resp.PRs) != 2 || resp.PRs[0].PRURL != backURL || resp.PRs[1].PRURL != frontURL ||
		resp.PRs[1].VerificationSHA256 != frontSum || resp.PRs[1].Verification.PRURL != frontURL || resp.PRs[1].Approval == nil ||
		resp.PRs[1].Approval.ID != "lado-integration-front" || resp.Approval == nil || resp.Approval.ID != "lado-integration-back" ||
		!reflect.DeepEqual(resp.PRs[1].Confirm, []string{"outside_plan", "unprotected"}) {
		t.Fatalf("answer = %+v, want each PR with its record and approval", resp)
	}

	// A dry run of the task's PRs needs no proof.
	dry := `{"prs":[{"prUrl":"` + backURL + `","headSha":"` + backHead + `"},{"prUrl":"` + frontURL + `","headSha":"` + frontHead + `"}],"dryRun":true}`
	body, status, _ = doRequest(t, srv, "POST", path, dry)
	if status != http.StatusOK || !svc.gotApprove.DryRun || svc.gotApprove.Presence != nil || len(svc.gotApprove.PRs) != 2 || svc.approvals != 1 {
		t.Fatalf("dry run = %d %s, service got %+v", status, body, svc.gotApprove)
	}

	for name, sent := range map[string]string{
		"prs with the single PR's fields": `{"prs":[{"prUrl":"` + backURL + `","headSha":"` + backHead + `"}],"prUrl":"` + backURL + `"}`,
		"a PR of prs without its head":    `{"prs":[{"prUrl":"` + backURL + `","headSha":""}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			before := svc.calls
			body, status := signedRequest(t, srv, "POST", path, sent, sent)
			wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
			if svc.calls != before {
				t.Fatalf("the service was called for %s", sent)
			}
		})
	}
}

// The parts view lists the run's repositories and how each part went in
// each, and the repository whose merge of a part conflicted.
func TestLadoAPI_TaskPartsAcrossRepos(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	srv := newLadoQueueServer(t, store)
	now := time.Now().UTC().Truncate(time.Second)
	seed := seedLadoQueued(t, store, "mer", 1, now.Add(-time.Minute))
	token := claimLado(t, store, seed.entry, now)
	backBase, frontBase := strings.Repeat("a", 40), strings.Repeat("d", 40)
	if err := store.StartLadoPartRunRepos(ctx, token, domain.LadoPartRun{
		QueueEntryID: seed.entry, TaskID: seed.task, ProjectID: "mer", PlanRevisionID: "lado-plan-1",
		Branch: "lado/task-1/integration", StartedAt: now,
	}, []domain.LadoPartRunRepo{
		{Relative: "back", Position: 0, Dir: "/tmp/mer/back", BaseSHA: backBase, HeadSHA: backBase, BaseBranch: "main"},
		{Relative: "front", Position: 1, Dir: "/tmp/mer/front", BaseSHA: frontBase, HeadSHA: frontBase, BaseBranch: "develop"},
	}, []domain.LadoTaskPart{
		{PartID: "api", Title: "API", Phase: 1, Steps: []int{1}, AffectedPaths: []string{"back/**", "front/**"}},
	}, domain.LadoAuditEvent{At: now, Actor: "lado", Action: "parts.started", SubjectKind: "queue_entry", SubjectID: string(seed.entry)}); err != nil {
		t.Fatalf("start the parts run: %v", err)
	}
	parts, err := store.LadoTaskParts(ctx, seed.entry)
	if err != nil || len(parts) != 1 {
		t.Fatalf("parts = %+v, %v", parts, err)
	}
	part := parts[0]
	part.State, part.MergeRepo = domain.LadoPartConflict, "front"
	merged := strings.Repeat("e", 40)
	if ok, err := store.SetLadoTaskPartRepos(ctx, part, domain.LadoPartWaiting, []domain.LadoTaskPartRepo{
		{Relative: "back", StartSHA: backBase, MergeTipSHA: merged, MergedSHA: merged},
		{Relative: "front", StartSHA: frontBase, MergeTipSHA: frontBase},
	}); err != nil || !ok {
		t.Fatalf("set the part's repositories: %v %v", ok, err)
	}

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/"+string(seed.task)+"/parts", "")
	got := decodeLadoJSON[controllers.LadoTaskPartsResponse](t, body)
	wantRepos := []controllers.LadoPartRunRepoResponse{
		{Repo: "back", Position: 0, BaseSHA: backBase, HeadSHA: backBase, BaseBranch: "main"},
		{Repo: "front", Position: 1, BaseSHA: frontBase, HeadSHA: frontBase, BaseBranch: "develop"},
	}
	if status != http.StatusOK || got.Branch != "lado/task-1/integration" || got.BaseSHA != backBase || !reflect.DeepEqual(got.Repos, wantRepos) {
		t.Fatalf("parts = %d %s, want both repositories", status, body)
	}
	wantPart := []controllers.LadoTaskPartRepoResponse{
		{Repo: "back", StartSHA: backBase, MergedSHA: merged},
		{Repo: "front", StartSHA: frontBase, Empty: true},
	}
	if len(got.Parts) != 1 || got.Parts[0].MergeRepo != "front" || got.Parts[0].State != "conflict" || !reflect.DeepEqual(got.Parts[0].Repos, wantPart) {
		t.Fatalf("part = %+v, want its conflict in front and how it went in each repository", got.Parts)
	}
}
