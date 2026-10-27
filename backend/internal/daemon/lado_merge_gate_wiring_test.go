package daemon

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
}

func (s *ladoSCM) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	s.refs = append(s.refs, refs...)
	if s.err != nil {
		return nil, s.err
	}
	head := strings.Repeat("ab", 20)
	return []ports.SCMObservation{{
		Fetched:      s.fetched,
		PR:           ports.SCMPRObservation{URL: refs[0].URL, Number: refs[0].Number, HeadSHA: head},
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
		Actor: "human", ApprovedAt: at, StatusState: domain.LadoIntegrationStatusNotApplicable,
	}, proof, domain.LadoAuditEvent{At: at, Actor: "human", Action: "integration.approved", SubjectKind: "execution", SubjectID: string(worker), Payload: []byte("{}")}); err != nil {
		t.Fatalf("ApproveLadoIntegration: %v", err)
	}
	if err := merge(); err != nil || scm.merges != 1 {
		t.Fatalf("merge of the approved head = %v after %d merges, want one merge", err, scm.merges)
	}
}

// ladoLiveHeads reads a merge request through AO's SCM reader with the
// reference AO's merge builds, and returns its head.
func TestLadoLiveHeadsReadThePRFromItsHost(t *testing.T) {
	ctx := context.Background()
	pr := domain.PullRequest{URL: "https://gitlab.com/acme/tools/demo/-/merge_requests/7", Number: 7, Provider: "gitlab", Host: "gitlab.com", Repo: "acme/tools/demo"}
	scm := &ladoSCM{fetched: true}
	head, err := ladoLiveHeads{reader: scm}.LiveHead(ctx, pr)
	if err != nil || head != strings.Repeat("ab", 20) {
		t.Fatalf("LiveHead = %q, %v; want the host's head", head, err)
	}
	want := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "acme/tools", Name: "demo", Repo: "acme/tools/demo"}, Number: 7, URL: pr.URL}
	if len(scm.refs) != 1 || scm.refs[0] != want {
		t.Fatalf("refs = %+v, want %+v", scm.refs, want)
	}

	unknown := pr
	unknown.Repo = ""
	if _, err := (ladoLiveHeads{reader: scm}).LiveHead(ctx, unknown); err == nil || len(scm.refs) != 1 {
		t.Errorf("LiveHead of a PR without a repository = %v after %d reads, want an error without a read", err, len(scm.refs))
	}
	if _, err := (ladoLiveHeads{reader: &ladoSCM{}}).LiveHead(ctx, pr); err == nil {
		t.Error("LiveHead of a PR the host did not return = nil error")
	}
	failing := &ladoSCM{err: errors.New("the host answered 502")}
	if _, err := (ladoLiveHeads{reader: failing}).LiveHead(ctx, pr); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("LiveHead when the host fails = %v, want its error", err)
	}
}
