package daemon

// LADO fork: what LADO's verification loop needs from AO (step 8 plan §12
// and §13; docs/LJY-PRD/TDD.md §7 and §8): AO's review service, the plans
// LADO pinned, the repository clients on the hosts' token chains, the session
// manager to tell workers what failed, LADO mode's turn-on, and AO's PR
// actions, whose merge asks the loop's merge gate, with their SCM reader.

import (
	"context"
	"fmt"
	"log/slog"

	scmmulti "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/multi"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// ladoModeHook is LADO's project settings service, which tells who is
// interested that a project's LADO mode was turned on.
type ladoModeHook interface {
	SetOnLadoModeOn(func(domain.ProjectID))
}

// wireLadoVerify builds the verification loop over LADO's store and the AO
// parts in work. The daemon starts it after the dispatcher and waits for it
// at shutdown. Turning LADO mode on reads the protection of the project's
// base branches again.
func wireLadoVerify(store *sqlite.Store, blobs *ladoblob.Store, clients *ladoRepoClients, work ladoWorkDeps, log *slog.Logger) *ladoverify.Service {
	deps := ladoverify.Deps{
		Store:     store,
		Tasks:     store,
		Plans:     ladoApprovedPlans{store: store, blobs: blobs},
		Snapshots: store,
		Projects:  store,
		Sessions:  store,
		Clients:   ladoVerifyClients{clients: clients},
		Blobs:     blobs,
		Branches:  work.branches,
		Log:       log,
	}
	if work.reviews != nil {
		deps.Reviews = ladoReviews{manager: work.reviews, runs: store}
	}
	if work.artifacts != nil {
		deps.Artifacts = ladoArtifacts{capturer: work.artifacts, store: store}
	}
	if work.sender != nil {
		deps.Sender = ladoVerifySender{sender: work.sender}
	}
	verify := ladoverify.New(deps)
	if work.ladoModes != nil {
		work.ladoModes.SetOnLadoModeOn(verify.RefreshProtection)
	}
	return verify
}

// ladoReviewRuns reads a worker's review runs.
type ladoReviewRuns interface {
	ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
}

// ladoReviews starts, reads and cancels AO's reviews of an execution's PR.
type ladoReviews struct {
	manager reviewsvc.Manager
	runs    ladoReviewRuns
}

// Trigger asks AO for a review of the PR at the head AO last saw, as a
// person's request that refuses a head reviewed already. AO's refusals
// (a review running, the head reviewed) come back as errors; the next pass
// reads the runs.
func (r ladoReviews) Trigger(ctx context.Context, worker domain.SessionID, prURL string, harness domain.ReviewerHarness) ([]string, error) {
	outcome, err := r.manager.TriggerRequested(ctx, worker, reviewsvc.TriggerRequest{
		Harness: harness, Source: domain.ReviewTriggerManual, PRURL: prURL, RejectReviewedHead: true,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(outcome.CreatedRuns))
	for _, run := range outcome.CreatedRuns {
		ids = append(ids, run.ID)
	}
	if len(ids) == 0 && outcome.Created && outcome.Run.ID != "" {
		ids = append(ids, outcome.Run.ID)
	}
	return ids, nil
}

func (r ladoReviews) Runs(ctx context.Context, worker domain.SessionID) ([]domain.ReviewRun, error) {
	return r.runs.ListReviewRunsBySession(ctx, worker)
}

func (r ladoReviews) Cancel(ctx context.Context, worker domain.SessionID) error {
	_, err := r.manager.Cancel(ctx, worker)
	return err
}

// ladoPlanRevisions reads LADO's pinned plan revisions.
type ladoPlanRevisions interface {
	LadoPlanRevision(ctx context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlanRevision, bool, error)
}

// ladoApprovedPlans reads the plan an approval pinned from LADO's blobs.
type ladoApprovedPlans struct {
	store ladoPlanRevisions
	blobs *ladoblob.Store
}

// ApprovedPlan parses the revision's pinned plan. Its problems were read
// before the approval, so only a plan without the front matter LADO reads
// fails.
func (p ladoApprovedPlans) ApprovedPlan(ctx context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlan, error) {
	rev, ok, err := p.store.LadoPlanRevision(ctx, id)
	if err != nil {
		return domain.LadoPlan{}, fmt.Errorf("read plan revision %s: %w", id, err)
	}
	if !ok {
		return domain.LadoPlan{}, fmt.Errorf("plan revision %s not found", id)
	}
	content, err := p.blobs.Get(rev.SHA256, domain.LadoPlanMaxBytes)
	if err != nil {
		return domain.LadoPlan{}, fmt.Errorf("read the pinned plan of revision %s: %w", id, err)
	}
	plan, _ := domain.ParseLadoPlan(content, domain.LadoPlanExpectations{})
	if len(plan.AffectedPaths) == 0 {
		return domain.LadoPlan{}, fmt.Errorf("the pinned plan of revision %s has no affected paths", id)
	}
	return plan, nil
}

// ladoVerifyClients reaches a repository's host on the host's token chain,
// stored tokens included, as LADO's tasks do.
type ladoVerifyClients struct {
	clients *ladoRepoClients
}

func (c ladoVerifyClients) ForRepo(ctx context.Context, repo domain.TrackerRepo) (ports.LadoRepoClient, error) {
	return c.clients.ForRepo(ctx, repo, "")
}

// ladoVerifySender tells a worker what failed as the daemon.
type ladoVerifySender struct {
	sender ladoMessageSender
}

func (s ladoVerifySender) Send(ctx context.Context, id domain.SessionID, message string) error {
	return s.sender.Send(ctx, id, message, nil)
}

// ladoMergeGated is AO's PR action service, whose merge asks LADO's gate.
type ladoMergeGated interface {
	SetMergeGate(ports.LadoMergeGate)
}

// installLadoMergeGate makes AO's merge route ask the verification loop's
// merge gate, so a LADO execution's PR merges only at a head whose
// integration the developer approved, and gives the loop AO's SCM reader,
// which integration approvals read PR heads with. The daemon calls it once
// the PR action service exists. Without an SCM provider AO merges nothing
// and LADO approves nothing.
func installLadoMergeGate(actions prsvc.ActionManager, reader *scmmulti.Provider, verify *ladoverify.Service) {
	if verify == nil {
		return
	}
	if gated, ok := actions.(ladoMergeGated); ok {
		gated.SetMergeGate(verify)
	}
	if reader != nil {
		verify.SetLiveHeads(ladoLiveHeads{reader: reader})
	}
}

// ladoPRReader reads PRs from their hosts: AO's SCM reader.
type ladoPRReader interface {
	FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error)
}

// ladoLiveHeads reads a PR's head from its host as AO's merge reads it.
type ladoLiveHeads struct {
	reader ladoPRReader
}

func (h ladoLiveHeads) LiveHead(ctx context.Context, pr domain.PullRequest) (string, error) {
	ref, ok := prsvc.LadoSCMRef(pr)
	if !ok {
		return "", fmt.Errorf("read pull request %s: its repository or number is unknown", pr.URL)
	}
	observations, err := h.reader.FetchPullRequests(ctx, []ports.SCMPRRef{ref})
	if err != nil {
		return "", fmt.Errorf("read pull request %s: %w", pr.URL, err)
	}
	if len(observations) != 1 || !observations[0].Fetched || observations[0].PR.Number != pr.Number || observations[0].PR.HeadSHA == "" {
		return "", fmt.Errorf("read pull request %s: the host did not return its head", pr.URL)
	}
	return observations[0].PR.HeadSHA, nil
}
