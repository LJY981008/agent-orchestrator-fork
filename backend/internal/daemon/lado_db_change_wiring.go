package daemon

// LADO fork: wiring of the production database changes (issue #97,
// docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md). The
// database change service reads LADO's store, keeps the commands' redacted
// outputs in LADO's blobs and the secrets under
// <StateDir>/lado/credentials/db. The verification loop asks it about each
// head, holds the lado/integration status of an approved head whose
// pre-merge SQL is not applied, and tells it of approvals and merged PRs;
// the service applies the SQL, posts the status through the loop and merges
// through AO's PR actions, whose merge asks the loop's merge gate.

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladodbchange"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var (
	_ ladoverify.DBChanges            = (*ladodbchange.Service)(nil)
	_ ladodbchange.Store              = (*sqlite.Store)(nil)
	_ ladodbchange.Statuses           = (*ladoverify.Service)(nil)
	_ ladodbchange.MergeChecker       = ladoDBChangeMerger{}
	_ ladodbchange.LiveHeads          = ladoLiveHeads{}
	_ ladodbchange.Blobs              = (*ladoblob.Store)(nil)
	_ controllers.LadoDBChangeService = (*ladodbchange.Service)(nil)
	_ ladoDBChangePRActions           = (*prsvc.ActionService)(nil)
	_ interface {
		LadoDBChangeSettings(context.Context, domain.ProjectID) ([]domain.LadoDBChangeSettings, error)
	} = (*sqlite.Store)(nil)
)

// ladoDBChangePRActions is AO's PR action service as the database change
// service merges with it: the merge route's merge, which asks LADO's merge
// gate, and the read of what would stop that merge.
type ladoDBChangePRActions interface {
	Merge(ctx context.Context, request prsvc.MergeRequest) (prsvc.MergeResult, error)
	LadoMergeReadiness(ctx context.Context, prURL string, number int, head string) (domain.LadoMergeReadiness, error)
}

// errLadoDBChangeNoPRActions is what the merge adapter answers without AO's
// PR actions (no usable SCM provider): a run that would merge stops as
// merge_failed, and its preflight waits for the developer.
var errLadoDBChangeNoPRActions = errors.New("AO's pull request actions are not available: no usable SCM provider")

// ladoDBChangeMerger reads a PR's merge readiness and merges it through AO's
// PR actions, as the app's Merge button does.
type ladoDBChangeMerger struct {
	actions ladoDBChangePRActions
}

func (m ladoDBChangeMerger) LadoMergeReadiness(ctx context.Context, prURL string, number int, head string) (domain.LadoMergeReadiness, error) {
	if m.actions == nil {
		return domain.LadoMergeReadiness{}, errLadoDBChangeNoPRActions
	}
	return m.actions.LadoMergeReadiness(ctx, prURL, number, head)
}

func (m ladoDBChangeMerger) MergeLadoPR(ctx context.Context, prURL string, number int, head string) error {
	if m.actions == nil {
		return errLadoDBChangeNoPRActions
	}
	_, err := m.actions.Merge(ctx, prsvc.MergeRequest{PRID: strconv.Itoa(number), PRURL: prURL, ExpectedHeadSHA: head})
	return err
}

// ladoDBChangeLiveHeads reads PRs from their hosts with AO's SCM reader, nil
// without one.
func ladoDBChangeLiveHeads(reader ladoPRReader) ladodbchange.LiveHeads {
	if reader == nil {
		return nil
	}
	return ladoLiveHeads{reader: reader}
}

// wireLadoDBChanges builds the database change service and installs it in
// the verification loop. The daemon calls it once AO's PR actions exist,
// right after installLadoMergeGate, starts it with Start and waits for it
// with Wait at shutdown. Without AO's PR actions the service still serves
// its settings and records, but a run that would merge stops for the
// developer.
func wireLadoDBChanges(cfg config.Config, store *sqlite.Store, blobs *ladoblob.Store, verify *ladoverify.Service, actions prsvc.ActionManager, reader ladoPRReader, log *slog.Logger) *ladodbchange.Service {
	deps := ladodbchange.Deps{
		Store:     store,
		Blobs:     blobs,
		StateDir:  cfg.StateDir,
		LiveHeads: ladoDBChangeLiveHeads(reader),
		Log:       log,
	}
	merger := ladoDBChangeMerger{}
	if a, ok := actions.(ladoDBChangePRActions); ok {
		merger.actions = a
	}
	deps.MergeChecker = merger
	if verify != nil {
		deps.Statuses = verify
	}
	svc := ladodbchange.New(deps)
	if verify != nil {
		verify.SetDBChanges(svc)
	}
	return svc
}
