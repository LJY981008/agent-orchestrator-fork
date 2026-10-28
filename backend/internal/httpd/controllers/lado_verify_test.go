package controllers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// fakeLadoVerifyService answers from fixed values and, like the real
// service, checks an approval's presence before anything else.
type fakeLadoVerifyService struct {
	result     ladoverify.ApproveResult
	reads      []ladoverify.ProtectionRead
	err        error
	gotApprove ladoverify.ApproveInput
	gotProof   domain.LadoPresenceProof
	approvals  int
	calls      int
	gotProject domain.ProjectID
	gotRefresh bool
}

func (f *fakeLadoVerifyService) Head(context.Context, domain.SessionID, string) (ladoverify.HeadView, error) {
	return ladoverify.HeadView{}, errors.New("not used")
}

func (f *fakeLadoVerifyService) ApproveIntegration(_ context.Context, in ladoverify.ApproveInput) (ladoverify.ApproveResult, error) {
	f.calls++
	f.gotApprove = in
	result := f.result
	if in.DryRun {
		result.Approval = nil
		return result, f.err
	}
	if in.Presence == nil {
		return ladoverify.ApproveResult{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED", "Approve the integration in the AO desktop app.")
	}
	proof, err := in.Presence(domain.LadoPresenceIntegrationApprove, string(in.SessionID)+"@"+strings.ToLower(in.HeadSHA))
	if err != nil {
		return ladoverify.ApproveResult{}, err
	}
	f.gotProof = proof
	f.approvals++
	return result, f.err
}

func (f *fakeLadoVerifyService) RunChecksAgain(context.Context, domain.SessionID) (ladoverify.RunAgain, error) {
	return ladoverify.RunAgain{}, errors.New("not used")
}

func (f *fakeLadoVerifyService) CheckLog(context.Context, domain.SessionID, string) ([]byte, error) {
	return nil, errors.New("not used")
}

func (f *fakeLadoVerifyService) Protection(_ context.Context, projectID domain.ProjectID, refresh bool) ([]ladoverify.ProtectionRead, error) {
	f.gotProject, f.gotRefresh = projectID, refresh
	return f.reads, f.err
}

var (
	ladoVerifyHead    = strings.Repeat("e", 40)
	ladoVerifyOldHead = strings.Repeat("d", 40)
)

func mustLadoItem(t *testing.T, kind domain.LadoVerifyKind, outcome domain.LadoVerifyOutcome, code string, details any) domain.LadoVerifyItem {
	t.Helper()
	item, err := domain.NewLadoVerifyItem(kind, outcome, code, details)
	if err != nil {
		t.Fatalf("NewLadoVerifyItem %s: %v", kind, err)
	}
	return item
}

// ladoVerifyRecord is a record of head that needs confirming: a path is
// outside the plan and the base branch is not protected.
func ladoVerifyRecord(t *testing.T, session domain.SessionID, prURL, head string, at time.Time) domain.LadoVerification {
	t.Helper()
	exit := 0
	return domain.LadoVerification{
		ExecutionSessionID: session, HeadSHA: head, PRURL: prURL, ComputedAt: at,
		Items: []domain.LadoVerifyItem{
			mustLadoItem(t, domain.LadoVerifyRequiredChecks, domain.LadoVerifyPass, "", domain.LadoRequiredChecksDetails{
				Required: []string{"ci/test"}, RulesetRead: true, Checks: []domain.LadoVerifyCheck{{Name: "ci/test", Status: "passed", Required: true}},
			}),
			mustLadoItem(t, domain.LadoVerifyCommands, domain.LadoVerifyPass, "", domain.LadoCommandsDetails{
				Runs: []domain.LadoVerifyCommandRun{{Name: "unit", CheckRunID: "lado-check-1", Status: "passed", ExitCode: &exit, DurationMS: 1200}},
			}),
			mustLadoItem(t, domain.LadoVerifyCommitMessages, domain.LadoVerifyPass, "", domain.LadoCommitMessagesDetails{
				Commits: []domain.LadoVerifyCommit{{SHA: head, Subject: "feat: 검증 화면 추가", Result: "ok"}},
				PRTitle: domain.LadoVerifyText{Text: "검증 화면 추가", Result: "ok"},
			}),
			mustLadoItem(t, domain.LadoVerifyPaths, domain.LadoVerifyConfirm, domain.LadoCodeOutsidePlan, domain.LadoPathsDetails{
				OutsidePlan: []string{"docs/notes.md"}, OutsideCount: 1,
			}),
			mustLadoItem(t, domain.LadoVerifyBaseBranch, domain.LadoVerifyPass, domain.LadoCodeNoLocalBranch, domain.LadoBaseBranchDetails{Branch: "main", Remote: "origin"}),
			mustLadoItem(t, domain.LadoVerifyProtection, domain.LadoVerifyConfirm, domain.LadoCodeUnprotected, domain.LadoProtectionDetails{
				Provider: "github", Repo: "acme/demo", Branch: "main", Missing: []string{"deletion"},
			}),
			mustLadoItem(t, domain.LadoVerifyReview, domain.LadoVerifyInfo, domain.LadoCodeNotRequired, nil),
			mustLadoItem(t, domain.LadoVerifyEvidence, domain.LadoVerifyInfo, "", domain.LadoEvidenceDetails{
				FileVersion: 2, FileSHA256: strings.Repeat("f", 64), FileHead: head,
				Criteria: []domain.LadoEvidenceCriterion{
					{Index: 1, State: domain.LadoEvidenceLinked, Refs: []domain.LadoEvidenceRef{{Kind: domain.LadoEvidenceRefPRCheck, Key: "ci/test", SHA: head}}},
					{Index: 2, State: domain.LadoEvidenceInferred, Refs: []domain.LadoEvidenceRef{}},
				},
			}),
		},
	}
}

func ladoRecordSHA(t *testing.T, v domain.LadoVerification) string {
	t.Helper()
	sum, err := v.SHA256()
	if err != nil {
		t.Fatalf("SHA256: %v", err)
	}
	return sum
}

// ladoApproveResult is what approving ladoVerifyRecord answers.
func ladoApproveResult(t *testing.T, at time.Time) ladoverify.ApproveResult {
	t.Helper()
	record := ladoVerifyRecord(t, "mer-1", "https://github.com/acme/demo/pull/7", ladoVerifyHead, at)
	sum := ladoRecordSHA(t, record)
	return ladoverify.ApproveResult{
		Verification: record, SHA256: sum, Confirm: record.ConfirmCodes(),
		Approval: &domain.LadoIntegrationApproval{
			ID: "lado-integration-1", ExecutionSessionID: "mer-1", PRURL: record.PRURL, HeadSHA: ladoVerifyHead, VerificationSHA256: sum,
			Acknowledged: record.ConfirmCodes(), ProofNonce: "AAAAAAAAAAAAAAAAAAAAAA", Actor: "human", ApprovedAt: at,
			StatusState: domain.LadoIntegrationStatusPosted, StatusAttempts: 1, StatusPostedAt: at,
		},
	}
}

func ladoApproveBody(sum string, dryRun bool) string {
	return fmt.Sprintf(`{"prUrl":"https://github.com/acme/demo/pull/7","headSha":%q,"verificationSha256":%q,"acknowledge":["outside_plan","unprotected"],"dryRun":%v}`,
		ladoVerifyHead, sum, dryRun)
}

func TestLadoAPI_IntegrationApproveNeedsPresence(t *testing.T) {
	path := "/api/v1/lado/executions/mer-1/integration-approve"
	result := ladoApproveResult(t, presenceNow)
	approve := ladoApproveBody(result.SHA256, false)

	t.Run("a dry run needs no proof", func(t *testing.T) {
		svc := &fakeLadoVerifyService{result: result}
		srv := newLadoTestServer(t, httpd.APIDeps{LadoVerify: svc, LadoPresence: presenceVerifier()})
		body, status, _ := doRequest(t, srv, "POST", path, ladoApproveBody("", true))
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, body)
		}
		if !svc.gotApprove.DryRun || svc.gotApprove.Presence != nil || svc.approvals != 0 {
			t.Fatalf("the dry run reached the service as %+v with %d approvals; want no presence check", svc.gotApprove, svc.approvals)
		}
		resp := decodeLadoJSON[controllers.LadoIntegrationApproveResponse](t, body)
		if resp.VerificationSHA256 != result.SHA256 || resp.Verification.SHA256 != result.SHA256 || resp.Verification.Outcome != "confirm" ||
			!reflect.DeepEqual(resp.Confirm, []string{"outside_plan", "unprotected"}) || resp.Approval != nil {
			t.Fatalf("dry run = %+v, want the record, its hash and its two confirm codes without an approval", resp)
		}
	})
	t.Run("signed by the app", func(t *testing.T) {
		svc := &fakeLadoVerifyService{result: result}
		srv := newLadoTestServer(t, httpd.APIDeps{LadoVerify: svc, LadoPresence: presenceVerifier()})
		body, status := signedRequest(t, srv, "POST", path, approve, approve)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, body)
		}
		if svc.approvals != 1 || svc.gotProof.Nonce != "AAAAAAAAAAAAAAAAAAAAAA" || svc.gotProof.Action != domain.LadoPresenceIntegrationApprove ||
			svc.gotProof.SubjectID != "mer-1@"+ladoVerifyHead {
			t.Fatalf("approvals %d with proof %+v", svc.approvals, svc.gotProof)
		}
		resp := decodeLadoJSON[controllers.LadoIntegrationApproveResponse](t, body)
		want := controllers.LadoIntegrationApprovalResponse{
			ID: "lado-integration-1", HeadSHA: ladoVerifyHead, VerificationSHA256: result.SHA256, Acknowledged: []string{"outside_plan", "unprotected"},
			Actor: "human", ApprovedAt: presenceNow, StatusState: "posted", StatusAttempts: 1, StatusPostedAt: &presenceNow,
		}
		if resp.Approval == nil || resp.Approval.StatusPostedAt == nil || !resp.Approval.StatusPostedAt.Equal(presenceNow) || !resp.Approval.ApprovedAt.Equal(presenceNow) {
			t.Fatalf("approval = %+v, want %+v", resp.Approval, want)
		}
		got := *resp.Approval
		got.ApprovedAt, got.StatusPostedAt = want.ApprovedAt, want.StatusPostedAt
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("approval = %+v, want %+v", got, want)
		}
	})
	for _, tc := range []struct {
		name     string
		presence controllers.LadoPresence
		send     func(t *testing.T, srv *httptest.Server) ([]byte, int)
		code     string
	}{
		{"curl without a proof", presenceVerifier(), func(t *testing.T, srv *httptest.Server) ([]byte, int) {
			body, status, _ := doRequest(t, srv, "POST", path, approve)
			return body, status
		}, "LADO_PRESENCE_REQUIRED"},
		{"a proof over the dry run's bytes", presenceVerifier(), func(t *testing.T, srv *httptest.Server) ([]byte, int) {
			return signedRequest(t, srv, "POST", path, ladoApproveBody(result.SHA256, true), approve)
		}, "LADO_PRESENCE_INVALID"},
		{"a daemon without the app's token", nil, func(t *testing.T, srv *httptest.Server) ([]byte, int) {
			return signedRequest(t, srv, "POST", path, approve, approve)
		}, "LADO_PRESENCE_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeLadoVerifyService{result: result}
			deps := httpd.APIDeps{LadoVerify: svc}
			if tc.presence != nil {
				deps.LadoPresence = tc.presence
			}
			srv := newLadoTestServer(t, deps)
			body, status := tc.send(t, srv)
			wantLadoErrorCode(t, body, status, http.StatusForbidden, tc.code)
			if svc.approvals != 0 {
				t.Fatalf("%d approvals without a valid proof", svc.approvals)
			}
		})
	}
}

func TestLadoAPI_IntegrationApprovePassesTheExactBody(t *testing.T) {
	presence := &recordingPresence{}
	result := ladoApproveResult(t, presenceNow)
	svc := &fakeLadoVerifyService{result: result}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoVerify: svc, LadoPresence: presence})

	// Odd spacing and key order: the proof signs these bytes, not a
	// re-encoding of them.
	path := "/api/v1/lado/executions/mer-1/integration-approve"
	sent := "{ \"acknowledge\": [\"unprotected\",\"outside_plan\"],\n  \"verificationSha256\":\"" + result.SHA256 +
		"\", \"headSha\": \"" + strings.ToUpper(ladoVerifyHead) + "\", \"prUrl\":\"https://github.com/acme/demo/pull/7\" }"
	body, status := ladoHeaderRequest(t, srv, "POST", path, sent, map[string]string{"X-LADO-Presence": "v1.proof-from-the-app"})
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if presence.method != "POST" || presence.path != path || string(presence.body) != sent || presence.header != "v1.proof-from-the-app" ||
		presence.action != domain.LadoPresenceIntegrationApprove || presence.subject != "mer-1@"+ladoVerifyHead {
		t.Fatalf("verifier got %s %s body %q header %q for %s %s", presence.method, presence.path, presence.body, presence.header, presence.action, presence.subject)
	}
	in := svc.gotApprove
	if in.SessionID != "mer-1" || in.PRURL != "https://github.com/acme/demo/pull/7" || in.HeadSHA != strings.ToUpper(ladoVerifyHead) ||
		in.VerificationSHA256 != result.SHA256 || !reflect.DeepEqual(in.Acknowledge, []string{"unprotected", "outside_plan"}) || in.DryRun ||
		svc.gotProof.Nonce != "BBBBBBBBBBBBBBBBBBBBBB" {
		t.Fatalf("service got %+v with proof %+v; want the body as sent", in, svc.gotProof)
	}

	// The service's refusals reach the app as they are.
	svc.err = apierr.Conflict("LADO_INTEGRATION_NOT_READY", "LADO still verifies this head.", map[string]any{"reasons": []string{"checks_pending"}})
	body, status = ladoHeaderRequest(t, srv, "POST", path, sent, map[string]string{"X-LADO-Presence": "v1.proof-from-the-app"})
	wantLadoErrorCode(t, body, status, http.StatusConflict, "LADO_INTEGRATION_NOT_READY")
	if reasons := decodeLadoJSON[envelope.APIError](t, body).Details["reasons"]; fmt.Sprint(reasons) != "[checks_pending]" {
		t.Fatalf("details = %s, want the reasons", body)
	}

	// A body with an unknown field never reaches the service.
	before := svc.calls
	body, status = ladoHeaderRequest(t, srv, "POST", path, `{"prUrl":"x","headSha":"y","merge":true}`, map[string]string{"X-LADO-Presence": "v1.proof-from-the-app"})
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	if svc.calls != before {
		t.Fatalf("the service was called for a body with an unknown field")
	}
}

// writeLadoHeadPR writes the session's open PR number with its head, as AO
// observes it.
func writeLadoHeadPR(t *testing.T, store *sqlite.Store, session domain.SessionID, number int, head string, at time.Time) string {
	t.Helper()
	url := fmt.Sprintf("https://github.com/acme/demo/pull/%d", number)
	if err := store.WriteSCMObservation(context.Background(), domain.PullRequest{
		URL: url, SessionID: session, Number: number, HeadSHA: head, TargetBranch: "main", Title: "검증 화면 추가", UpdatedAt: at,
	}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write PR %d: %v", number, err)
	}
	return url
}

func TestLadoAPI_ExecutionCarriesVerification(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	seed := seedLadoQueued(t, store, "mer", 1, now.Add(-time.Hour))
	worker := runLado(t, store, "mer", seed, now.Add(-30*time.Minute), domain.LadoExecution{})
	prURL := writeLadoHeadPR(t, store, worker, 7, ladoVerifyHead, now.Add(-time.Minute))
	srv := newLadoTestServer(t, httpd.APIDeps{
		LadoQueue:  ladoqueue.New(ladoqueue.Deps{Store: store}),
		LadoVerify: ladoverify.New(ladoverify.Deps{Store: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}),
	})
	get := func() controllers.LadoExecutionResponse {
		t.Helper()
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/executions/"+string(worker), "")
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, body)
		}
		if strings.Contains(string(body), `"verification":null`) || strings.Contains(string(body), `"integration":null`) {
			t.Fatalf("body %s sends null for what it should omit", body)
		}
		return decodeLadoJSON[controllers.LadoExecutionResponse](t, body)
	}

	// A record of an older head says nothing about the PR's current head.
	if _, err := store.UpsertLadoVerification(ctx, ladoVerifyRecord(t, worker, prURL, ladoVerifyOldHead, now.Add(-10*time.Minute))); err != nil {
		t.Fatalf("store the old head's record: %v", err)
	}
	got := get()
	wantPR := controllers.LadoExecutionPRResponse{URL: prURL, Number: 7, HeadSHA: ladoVerifyHead, TargetBranch: "main", Title: "검증 화면 추가"}
	if got.PR == nil || *got.PR != wantPR {
		t.Fatalf("PR = %+v, want %+v", got.PR, wantPR)
	}
	if got.Verification != nil || got.Integration != nil {
		t.Fatalf("verification %+v, integration %+v; want both omitted before the head's record", got.Verification, got.Integration)
	}

	record := ladoVerifyRecord(t, worker, prURL, ladoVerifyHead, now.Add(-5*time.Minute))
	if _, err := store.UpsertLadoVerification(ctx, record); err != nil {
		t.Fatalf("store the head's record: %v", err)
	}
	got = get()
	v := got.Verification
	if v == nil || got.Integration != nil {
		t.Fatalf("verification %+v, integration %+v; want the record without an approval", v, got.Integration)
	}
	if v.HeadSHA != ladoVerifyHead || v.PRURL != prURL || v.Outcome != "confirm" || v.Reason != "" || v.SHA256 != ladoRecordSHA(t, record) ||
		!v.ComputedAt.Equal(now.Add(-5*time.Minute)) {
		t.Fatalf("verification = %+v, want the head's record", v)
	}
	var kinds []string
	for _, item := range v.Items {
		kinds = append(kinds, item.Kind+"/"+item.Outcome+"/"+item.Code)
	}
	wantKinds := []string{"required_checks/pass/", "commands/pass/", "commit_messages/pass/", "paths/confirm/outside_plan",
		"base_branch/pass/no_local_branch", "protection/confirm/unprotected", "review/info/not_required", "evidence/info/"}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("items = %v, want %v", kinds, wantKinds)
	}
	exit := 0
	if d := v.Items[0].Details; !reflect.DeepEqual(d.Required, []string{"ci/test"}) || !d.RulesetRead ||
		!reflect.DeepEqual(d.Checks, []controllers.LadoVerifyCheckResponse{{Name: "ci/test", Status: "passed", Required: true}}) {
		t.Fatalf("required_checks details = %+v", d)
	}
	if d := v.Items[1].Details; !reflect.DeepEqual(d.Runs, []controllers.LadoVerifyCommandRunResponse{{Name: "unit", CheckRunID: "lado-check-1", Status: "passed", ExitCode: &exit, DurationMS: 1200}}) {
		t.Fatalf("commands details = %+v", d)
	}
	if d := v.Items[2].Details; d.PRTitle == nil || d.PRTitle.Result != "ok" || len(d.Commits) != 1 || d.Commits[0].Subject != "feat: 검증 화면 추가" {
		t.Fatalf("commit_messages details = %+v", d)
	}
	if d := v.Items[3].Details; !reflect.DeepEqual(d.OutsidePlan, []string{"docs/notes.md"}) || d.OutsideCount != 1 {
		t.Fatalf("paths details = %+v", d)
	}
	if d := v.Items[5].Details; d.Provider != "github" || d.Repo != "acme/demo" || d.Branch != "main" || !reflect.DeepEqual(d.Missing, []string{"deletion"}) {
		t.Fatalf("protection details = %+v", d)
	}
	wantEvidence := []controllers.LadoEvidenceCriterionResponse{
		{Index: 1, State: "linked", Refs: []controllers.LadoEvidenceRefResponse{{Kind: "pr_check", Key: "ci/test", SHA: ladoVerifyHead}}},
		{Index: 2, State: "inferred", Refs: []controllers.LadoEvidenceRefResponse{}},
	}
	if !reflect.DeepEqual(v.Evidence, wantEvidence) || v.Items[7].Details.FileVersion != 2 || v.Items[7].Details.FileHead != ladoVerifyHead {
		t.Fatalf("evidence = %+v, item details %+v; want the evidence item's criteria", v.Evidence, v.Items[7].Details)
	}

	// The head's integration approval joins the record.
	approvedAt := now.Add(-time.Minute)
	approval := domain.LadoIntegrationApproval{
		ID: "lado-integration-1", ExecutionSessionID: worker, PRURL: prURL, HeadSHA: ladoVerifyHead, VerificationSHA256: ladoRecordSHA(t, record),
		Acknowledged: record.ConfirmCodes(), Actor: "human", ApprovedAt: approvedAt, StatusState: domain.LadoIntegrationStatusPending,
	}
	if err := store.ApproveLadoIntegration(ctx, approval,
		domain.LadoPresenceProof{Nonce: "nonce-integration-1", Action: domain.LadoPresenceIntegrationApprove, SubjectID: string(worker) + "@" + ladoVerifyHead, ExpiresAt: approvedAt.Add(time.Minute), UsedAt: approvedAt},
		domain.LadoAuditEvent{At: approvedAt, Actor: "human", Action: "integration.approved", SubjectKind: "execution", SubjectID: string(worker), Payload: []byte("{}")},
	); err != nil {
		t.Fatalf("approve the head: %v", err)
	}
	if err := store.SetLadoIntegrationStatus(ctx, "lado-integration-1", domain.LadoIntegrationStatusFailed, 1, time.Time{}, "the host answered 403"); err != nil {
		t.Fatalf("SetLadoIntegrationStatus: %v", err)
	}
	got = get()
	in := got.Integration
	if got.Verification == nil || in == nil || in.ID != "lado-integration-1" || in.HeadSHA != ladoVerifyHead || in.VerificationSHA256 != ladoRecordSHA(t, record) ||
		!reflect.DeepEqual(in.Acknowledged, []string{"outside_plan", "unprotected"}) || in.Actor != "human" || !in.ApprovedAt.Equal(approvedAt) ||
		in.StatusState != "failed" || in.StatusAttempts != 1 || in.StatusPostedAt != nil || in.StatusError != "the host answered 403" {
		t.Fatalf("integration = %+v, want the head's approval whose status failed", in)
	}

	// Without verification wired, the execution has neither.
	bare := newLadoTestServer(t, httpd.APIDeps{LadoQueue: ladoqueue.New(ladoqueue.Deps{Store: store})})
	body, _, _ := doRequest(t, bare, "GET", "/api/v1/lado/executions/"+string(worker), "")
	if strings.Contains(string(body), `"verification"`) || strings.Contains(string(body), `"integration"`) {
		t.Fatalf("execution without verification wired = %s", body)
	}
}

func setLadoChecks(t *testing.T, store *sqlite.Store, project domain.ProjectID, at time.Time, commands ...domain.LadoCheckCommand) {
	t.Helper()
	if err := store.SetLadoProjectCheckCommands(context.Background(), project, commands, nil, domain.LadoAuditEvent{
		At: at, Actor: "api", Action: "project.check_commands_changed", SubjectKind: "project", SubjectID: string(project), Payload: []byte("{}"),
	}); err != nil {
		t.Fatalf("SetLadoProjectCheckCommands: %v", err)
	}
}

func startLadoCheckRun(t *testing.T, store *sqlite.Store, id string, session domain.SessionID, name string, at time.Time) {
	t.Helper()
	if err := store.StartLadoCheckRun(context.Background(), domain.LadoCheckRun{
		ID: id, ExecutionSessionID: session, HeadSHA: ladoVerifyHead, Name: name, Command: "run " + name, StartedAt: at,
	}); err != nil {
		t.Fatalf("StartLadoCheckRun %s: %v", id, err)
	}
}

func TestLadoAPI_RunChecksAgain(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	seed := seedLadoQueued(t, store, "mer", 1, now.Add(-time.Hour))
	worker := runLado(t, store, "mer", seed, now.Add(-30*time.Minute), domain.LadoExecution{})
	writeLadoHeadPR(t, store, worker, 7, ladoVerifyHead, now.Add(-20*time.Minute))
	commands := []domain.LadoCheckCommand{{Name: "unit", Command: "run unit"}, {Name: "lint", Command: "run lint"}, {Name: "e2e", Command: "run e2e"}}
	setLadoChecks(t, store, "mer", now.Add(-time.Hour), commands...)
	// unit passed, lint still runs, e2e never ran.
	exit := 0
	startLadoCheckRun(t, store, "lado-check-unit", worker, "unit", now.Add(-10*time.Minute))
	if err := store.FinishLadoCheckRun(ctx, "lado-check-unit", domain.LadoCheckRunPassed, &exit, "", "", "", now.Add(-9*time.Minute)); err != nil {
		t.Fatalf("finish unit: %v", err)
	}
	startLadoCheckRun(t, store, "lado-check-lint", worker, "lint", now.Add(-5*time.Minute))
	srv := newLadoTestServer(t, httpd.APIDeps{LadoVerify: ladoverify.New(ladoverify.Deps{Store: store, Tasks: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/executions/"+string(worker)+"/checks/run", "")
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if got := decodeLadoJSON[controllers.LadoRunChecksResponse](t, body); got.HeadSHA != ladoVerifyHead || !reflect.DeepEqual(got.Commands, []string{"unit", "e2e"}) {
		t.Fatalf("response = %+v, want unit and e2e on the head while lint runs", got)
	}
	runs, err := store.ListLadoCheckRuns(ctx, worker, ladoVerifyHead)
	if err != nil {
		t.Fatalf("ListLadoCheckRuns: %v", err)
	}
	if len(runs) != 3 || runs[2].Name != "unit" || runs[2].Status != domain.LadoCheckRunCancelled || runs[2].ErrorMessage == "" {
		t.Fatalf("runs = %+v, want unit's finished run sent back as cancelled", runs)
	}
	// The loop runs those again, in order; lint finishes first.
	due := ladoverify.CommandsToRun(commands, runs)
	if len(due) != 2 || due[0].Name != "unit" || due[1].Name != "e2e" {
		t.Fatalf("commands due = %+v, want unit and e2e", due)
	}

	// An execution without an open PR has no head to run them on.
	second := seedLadoQueued(t, store, "mer", 2, now.Add(-time.Hour))
	noPR := runLado(t, store, "mer", second, now.Add(-time.Minute), domain.LadoExecution{})
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/executions/"+string(noPR)+"/checks/run", "")
	wantLadoErrorCode(t, body, status, http.StatusConflict, "LADO_EXECUTION_NO_PR")
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/executions/mer-404/checks/run", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "LADO_EXECUTION_NOT_FOUND")
}

// mapLadoBlobs keeps blobs in memory by their sha256.
type mapLadoBlobs map[string][]byte

func (b mapLadoBlobs) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	b[key] = append([]byte(nil), data...)
	return key, nil
}

func (b mapLadoBlobs) Get(sum string, limit int64) ([]byte, error) {
	data, ok := b[sum]
	if !ok || int64(len(data)) > limit {
		return nil, errors.New("no such blob")
	}
	return data, nil
}

func TestLadoAPI_CheckLog(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	first := seedLadoQueued(t, store, "mer", 1, now.Add(-time.Hour))
	worker := runLado(t, store, "mer", first, now.Add(-30*time.Minute), domain.LadoExecution{})
	second := seedLadoQueued(t, store, "mer", 2, now.Add(-time.Hour))
	other := runLado(t, store, "mer", second, now.Add(-20*time.Minute), domain.LadoExecution{})
	blobs := mapLadoBlobs{}
	output := "ok  \tgithub.com/acme/demo\t0.4s\n<script>alert(1)</script>\n--- FAIL: TestX\n"
	sum, _ := blobs.Put([]byte(output))
	exit := 1
	startLadoCheckRun(t, store, "lado-check-1", worker, "unit", now.Add(-10*time.Minute))
	if err := store.FinishLadoCheckRun(ctx, "lado-check-1", domain.LadoCheckRunFailed, &exit, sum, "", "", now.Add(-9*time.Minute)); err != nil {
		t.Fatalf("finish the run: %v", err)
	}
	startLadoCheckRun(t, store, "lado-check-2", worker, "lint", now.Add(-time.Minute))
	srv := newLadoTestServer(t, httpd.APIDeps{LadoVerify: ladoverify.New(ladoverify.Deps{Store: store, Blobs: blobs, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})})
	get := func(session domain.SessionID, run string) (*http.Response, []byte) {
		t.Helper()
		resp, err := http.Get(srv.URL + "/api/v1/lado/executions/" + string(session) + "/checks/" + run + "/log")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp, body
	}

	resp, body := get(worker, "lado-check-1")
	if resp.StatusCode != http.StatusOK || string(body) != output || resp.Header.Get("Content-Type") != "text/plain; charset=utf-8" ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("log: status %d, type %q, body %q; want the output as plain text", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	// A run still going has no log yet.
	if resp, body := get(worker, "lado-check-2"); resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("running run: status %d, body %q; want an empty log", resp.StatusCode, body)
	}
	// Another execution's run, and an unknown run, are not found.
	for _, c := range []struct {
		session domain.SessionID
		run     string
	}{{other, "lado-check-1"}, {worker, "lado-check-404"}} {
		resp, body := get(c.session, c.run)
		wantLadoErrorCode(t, body, resp.StatusCode, http.StatusNotFound, "LADO_CHECK_RUN_NOT_FOUND")
	}
}

func TestLadoAPI_ProjectProtection(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second)
	required := func(contexts ...string) ports.LadoBranchRule {
		rule := ports.LadoBranchRule{Type: ports.LadoBranchRuleRequiredStatusChecks}
		for _, c := range contexts {
			rule.RequiredChecks = append(rule.RequiredChecks, ports.LadoRequiredCheck{Context: c})
		}
		return rule
	}
	github := func(native string) domain.TrackerRepo {
		return domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: native}
	}
	token := "ghp_" + strings.Repeat("a", 36)
	svc := &fakeLadoVerifyService{reads: []ladoverify.ProtectionRead{
		{Provider: domain.TrackerProviderGitHub, Repo: github("acme/demo"), Branch: "main", ReadAt: at, Rules: ports.LadoBranchRules{Rules: []ports.LadoBranchRule{
			{Type: ports.LadoBranchRulePullRequest}, required("ci/test", "lado/integration"), {Type: ports.LadoBranchRuleNonFastForward}, {Type: ports.LadoBranchRuleDeletion},
		}}},
		{Provider: domain.TrackerProviderGitHub, Repo: github("acme/api"), Branch: "develop", ReadAt: at, Rules: ports.LadoBranchRules{Rules: []ports.LadoBranchRule{
			{Type: ports.LadoBranchRulePullRequest}, required("build"),
		}}},
		{Provider: domain.TrackerProviderGitHub, Repo: github("acme/web"), Branch: "main", ReadAt: at, Err: errors.New("the host answered 401 for token " + token)},
		{Provider: domain.TrackerProviderGitLab, Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/app", Host: "gitlab.example.com"}, Branch: "main", ReadAt: at},
		{Provider: domain.TrackerProviderGitHub, Repo: github("acme/docs"), ReadAt: at, Err: errors.New("resolve the base branch: no branch")},
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoVerify: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/protection", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if svc.gotProject != "mer" || svc.gotRefresh {
		t.Fatalf("service got project %q refresh %v, want mer from the cache", svc.gotProject, svc.gotRefresh)
	}
	if strings.Contains(string(body), token) {
		t.Fatalf("response %s carries the token", body)
	}
	resp := decodeLadoJSON[controllers.LadoProjectProtectionResponse](t, body)
	type state struct{ repo, branch, state, missing, checks, err string }
	var got []state
	for _, r := range resp.Repositories {
		if r.CheckedAt == nil || !r.CheckedAt.Equal(at) {
			t.Fatalf("%s checked at %v, want %s", r.Repository, r.CheckedAt, at)
		}
		got = append(got, state{r.Repository, r.Branch, r.State, strings.Join(r.Missing, ","), strings.Join(r.RequiredChecks, ","), r.Error})
	}
	want := []state{
		{"github:acme/demo", "main", "protected", "", "ci/test", ""},
		{"github:acme/api", "develop", "unprotected", "required_status_checks:lado/integration,non_fast_forward,deletion", "build", ""},
		{"github:acme/web", "main", "unknown", "", "", "the host answered 401 for token [redacted]"},
		{"gitlab:group/app@gitlab.example.com", "main", "unsupported", "", "", ""},
		{"github:acme/docs", "", "unknown", "", "", "resolve the base branch: no branch"},
	}
	if resp.ProjectID != "mer" || !reflect.DeepEqual(got, want) {
		t.Fatalf("protection of %s = %+v, want %+v", resp.ProjectID, got, want)
	}
	if !strings.Contains(string(body), `"missing":[]`) || !strings.Contains(string(body), `"requiredChecks":[]`) {
		t.Fatalf("body %s, want empty lists rather than null", body)
	}

	// refresh reads past the cache.
	if body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/protection?refresh=true", ""); status != http.StatusOK || !svc.gotRefresh {
		t.Fatalf("refresh: status %d, body %s, service got refresh %v", status, body, svc.gotRefresh)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/protection?refresh=soon", "")
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_QUERY")
	svc.err = apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/gone/protection", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "PROJECT_NOT_FOUND")
}

func TestLadoAPI_CheckCommandsNeedPresence(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoProjects: ladoproject.New(ladoproject.Deps{Store: store, Projects: store}), LadoPresence: presenceVerifier()})
	path := "/api/v1/lado/projects/mer/settings"
	stored := func() ([]domain.LadoCheckCommand, int) {
		t.Helper()
		commands, timeout, err := store.LadoProjectChecks(ctx, "mer")
		if err != nil {
			t.Fatalf("LadoProjectChecks: %v", err)
		}
		return commands, timeout
	}
	unit := []domain.LadoCheckCommand{{Name: "unit", Command: "go test ./..."}}
	change := `{"checkCommands":[{"name":"unit","command":"go test ./..."}]}`

	// Changing the commands needs the app's proof over the exact body.
	body, status, _ := doRequest(t, srv, "PUT", path, change)
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_REQUIRED")
	body, status = signedRequest(t, srv, "PUT", path, `{"checkCommands":[{"name":"unit","command":"go test ./... || true"}]}`, change)
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_INVALID")
	if commands, _ := stored(); len(commands) != 0 {
		t.Fatalf("commands after refused changes = %+v, want none", commands)
	}
	body, status = signedRequest(t, srv, "PUT", path, change, change)
	if status != http.StatusOK {
		t.Fatalf("signed change: status = %d, body %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoProjectSettingsResponse](t, body)
	if !reflect.DeepEqual(got.CheckCommands, []controllers.LadoCheckCommandBody{{Name: "unit", Command: "go test ./..."}}) || got.CheckTimeoutMinutes != 30 {
		t.Fatalf("settings = %+v, want unit with the default time limit", got)
	}
	if commands, _ := stored(); !reflect.DeepEqual(commands, unit) {
		t.Fatalf("stored commands = %+v, want %+v", commands, unit)
	}

	// The time limit alone, or with the same commands, needs no proof.
	for _, c := range []struct {
		body    string
		timeout int
	}{
		{`{"checkTimeoutMinutes":45}`, 45},
		{`{"checkCommands":[{"name":"unit","command":"go test ./..."}],"checkTimeoutMinutes":60}`, 60},
	} {
		body, status, _ := doRequest(t, srv, "PUT", path, c.body)
		if got := decodeLadoJSON[controllers.LadoProjectSettingsResponse](t, body); status != http.StatusOK || got.CheckTimeoutMinutes != c.timeout || len(got.CheckCommands) != 1 {
			t.Fatalf("PUT %s: status %d, body %s; want the time limit %d and the commands kept", c.body, status, body, c.timeout)
		}
	}

	// An invalid list or time limit is refused with where it fails, before
	// any proof is asked for.
	for _, c := range []struct {
		body    string
		details map[string]any
	}{
		{`{"checkCommands":[{"name":"unit","command":"a"},{"name":"unit","command":"b"}]}`, map[string]any{"index": float64(1), "field": "name", "reason": "duplicate"}},
		{`{"checkCommands":[{"name":"-unit","command":"a"}]}`, map[string]any{"index": float64(0), "field": "name", "reason": "invalid"}},
		{`{"checkCommands":[{"name":"unit","command":""}]}`, map[string]any{"index": float64(0), "field": "command", "reason": "required"}},
		{`{"checkTimeoutMinutes":3}`, map[string]any{"index": float64(-1), "field": "checkTimeoutMinutes", "reason": "invalid"}},
	} {
		body, status, _ := doRequest(t, srv, "PUT", path, c.body)
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_CHECK_COMMANDS_INVALID")
		if details := decodeLadoJSON[envelope.APIError](t, body).Details; !reflect.DeepEqual(details, c.details) {
			t.Fatalf("PUT %s: details = %v, want %v", c.body, details, c.details)
		}
	}
	if commands, timeout := stored(); !reflect.DeepEqual(commands, unit) || timeout != 60 {
		t.Fatalf("stored = %+v, %d; want unit within 60 minutes", commands, timeout)
	}
}

func TestLadoAPI_ProtectedPathsChangeNeedsPresence(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	rules := ladopolicy.New(ladopolicy.Deps{Store: store, Projects: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := rules.EnsureBuiltins(ctx); err != nil {
		t.Fatalf("EnsureBuiltins: %v", err)
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: rules, LadoPresence: presenceVerifier()})
	path := "/api/v1/lado/rules/" + string(domain.LadoRuleProtectedPaths)
	globs := func() (int, []string) {
		t.Helper()
		body, status, _ := doRequest(t, srv, "GET", path, "")
		if status != http.StatusOK {
			t.Fatalf("GET status = %d, body %s", status, body)
		}
		detail := decodeLadoJSON[controllers.LadoRuleDetailResponse](t, body)
		if detail.Rule.Paths == nil || len(detail.Versions) == 0 || detail.Versions[len(detail.Versions)-1].Paths == nil {
			t.Fatalf("rule = %+v, want its globs on the rule and its versions", detail)
		}
		return detail.Rule.Version, detail.Rule.Paths.Globs
	}
	if version, got := globs(); version != 1 || !reflect.DeepEqual(got, domain.DefaultLadoProtectedPaths()) {
		t.Fatalf("protected paths = version %d %v, want version 1 with the defaults", version, got)
	}
	change := `{"baseVersion":1,"paths":{"globs":[".github/**","deploy/**"]}}`

	body, status, _ := doRequest(t, srv, "PATCH", path, change)
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_REQUIRED")
	for _, c := range []struct {
		path, body string
		details    map[string]any
	}{
		{path, `{"baseVersion":1,"paths":{"globs":["../secrets/**"]}}`, map[string]any{"index": float64(0), "reason": "invalid"}},
		{path, `{"baseVersion":1,"paths":{"globs":[".github/**","/etc/**"]}}`, map[string]any{"index": float64(1), "reason": "invalid"}},
		{path, `{"baseVersion":1,"paths":{"globs":[]}}`, map[string]any{"index": float64(0), "reason": "required"}},
		{"/api/v1/lado/rules/" + string(domain.LadoRulePermissionCeiling), `{"baseVersion":1,"paths":{"globs":["docs/**"]}}`, map[string]any{"rule": string(domain.LadoRulePermissionCeiling)}},
	} {
		body, status, _ := doRequest(t, srv, "PATCH", c.path, c.body)
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_PROTECTED_PATHS_INVALID")
		if details := decodeLadoJSON[envelope.APIError](t, body).Details; !reflect.DeepEqual(details, c.details) {
			t.Fatalf("PATCH %s: details = %v, want %v", c.body, details, c.details)
		}
	}
	// A dry run shows the change without a proof and makes none.
	body, status, _ = doRequest(t, srv, "PATCH", path, `{"baseVersion":1,"paths":{"globs":[".github/**","deploy/**"]},"dryRun":true}`)
	if resp := decodeLadoJSON[controllers.LadoRuleChangeResponse](t, body); status != http.StatusOK || resp.Applied || resp.Rule == nil || resp.Rule.Paths == nil ||
		!reflect.DeepEqual(resp.Rule.Paths.Globs, []string{".github/**", "deploy/**"}) {
		t.Fatalf("dry run: status %d, body %s", status, body)
	}
	body, status = signedRequest(t, srv, "PATCH", path, `{"baseVersion":1,"paths":{"globs":[".github/**"]}}`, change)
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_INVALID")
	if version, got := globs(); version != 1 || !reflect.DeepEqual(got, domain.DefaultLadoProtectedPaths()) {
		t.Fatalf("protected paths after refused changes = version %d %v, want the defaults", version, got)
	}

	body, status = signedRequest(t, srv, "PATCH", path, change, change)
	if resp := decodeLadoJSON[controllers.LadoRuleChangeResponse](t, body); status != http.StatusOK || !resp.Applied || resp.Rule == nil || resp.Rule.Version != 2 {
		t.Fatalf("signed change: status %d, body %s", status, body)
	}
	if version, got := globs(); version != 2 || !reflect.DeepEqual(got, []string{".github/**", "deploy/**"}) {
		t.Fatalf("protected paths = version %d %v, want version 2 with the new globs", version, got)
	}
}
