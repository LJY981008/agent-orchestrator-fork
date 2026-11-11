// Package ladodbchange ties the production database SQL of a LADO
// execution's PRs to the integration approval (#97,
// docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md).
//
// A project, or each child repository of a workspace project, may turn on
// database change settings: the globs of its SQL files, the command that
// applies one file, a time limit and the names of secrets the command gets
// as environment variables. Turning them on records the default branch's
// matching files as applied (the baseline). A PR head that adds such a file
// gets a db_changes item in its verification record, so the approval signs
// the files' blobs; LADO holds lado/integration back while the approved
// head's pre-merge files are not applied, applies them after a preflight,
// and only then posts the status and merges. Files whose first line is
// "-- lado:after-deploy" wait for the developer's button after the merge.
//
// This file holds the service and what it needs. settings.go, secrets.go,
// detect.go, git.go, redact.go and status_hold.go hold the settings, the
// secrets file, the detection, the git reads, the redaction and the status
// hold; apply.go, job.go, settle.go and views.go the application, the loop
// that applies and merges, the merge outside LADO and the views.
package ladodbchange

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
)

// Store is what the service reads and writes in LADO's store (*store.Store).
type Store interface {
	// The settings of a project's repositories, and one repository's.
	LadoDBChangeSettings(ctx context.Context, project domain.ProjectID) ([]domain.LadoDBChangeSettings, error)
	LadoDBChangeSetting(ctx context.Context, project domain.ProjectID, repo string) (domain.LadoDBChangeSettings, bool, error)
	// SetLadoDBChangeSettings saves s while the repository's settings still
	// hash to baseSHA256 ("" for none yet), with the baseline rows, the
	// proof and the audit events in one transaction. It fails with
	// domain.ErrLadoDBChangeSettingsChanged or domain.ErrLadoPresenceReplayed.
	SetLadoDBChangeSettings(ctx context.Context, s domain.LadoDBChangeSettings, baseSHA256 string, baseline []domain.LadoDBChangeApplication, proof *domain.LadoPresenceProof, audit []domain.LadoAuditEvent) error
	// LadoDBChangeAppliedPaths returns each path's last applied or baseline
	// row; a path without one is missing.
	LadoDBChangeAppliedPaths(ctx context.Context, project domain.ProjectID, repo string, paths []string) (map[string]domain.LadoDBChangeApplication, error)
	// ListLadoDBChangeApplications returns the paths' rows, oldest first.
	ListLadoDBChangeApplications(ctx context.Context, project domain.ProjectID, repo string, paths []string) ([]domain.LadoDBChangeApplication, error)
	StartLadoDBChangeApplication(ctx context.Context, a domain.LadoDBChangeApplication) error
	FinishLadoDBChangeApplication(ctx context.Context, id string, result domain.LadoDBChangeResult, exitCode *int, outputSHA256, message string, at time.Time, audit domain.LadoAuditEvent) error
	// InterruptLadoDBChangeApplications marks every running row interrupted.
	InterruptLadoDBChangeApplications(ctx context.Context, at time.Time) (int, error)
	// EnsureLadoDBChangeRun returns the run of the PR head and kind, made
	// from r when there was none (true).
	EnsureLadoDBChangeRun(ctx context.Context, r domain.LadoDBChangeRun) (domain.LadoDBChangeRun, bool, error)
	LadoDBChangeRun(ctx context.Context, id string) (domain.LadoDBChangeRun, bool, error)
	// TransitionLadoDBChangeRun moves a run in one of from to to, false when
	// it was in none of them.
	TransitionLadoDBChangeRun(ctx context.Context, id string, from []domain.LadoDBChangeRunState, to domain.LadoDBChangeRunState, attempts int, errMsg string, at time.Time, proof *domain.LadoPresenceProof, audit *domain.LadoAuditEvent) (bool, error)
	ListLadoDBChangeRunsByState(ctx context.Context, states []domain.LadoDBChangeRunState, limit int) ([]domain.LadoDBChangeRun, error)
	ListLadoDBChangeRunsForExecution(ctx context.Context, session domain.SessionID) ([]domain.LadoDBChangeRun, error)
	// ListLadoDBChangeRunsForProject leaves out the runs that are done.
	ListLadoDBChangeRunsForProject(ctx context.Context, project domain.ProjectID) ([]domain.LadoDBChangeRun, error)
	// The hold of an approval's lado/integration status.
	HoldLadoIntegrationStatus(ctx context.Context, approvalID string, at time.Time) error
	ReleaseLadoIntegrationStatus(ctx context.Context, approvalID string) error
	LadoIntegrationStatusHeld(ctx context.Context, approvalID string) (bool, error)

	// LADO's and AO's records the service reads.
	LadoExecution(ctx context.Context, session domain.SessionID) (domain.LadoExecution, bool, error)
	GetLadoVerification(ctx context.Context, session domain.SessionID, headSHA string) (domain.LadoVerification, bool, error)
	GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error)
	ListLadoIntegrationApprovals(ctx context.Context, session domain.SessionID, prURL, headSHA string) ([]domain.LadoIntegrationApproval, error)
	AddLadoOrchestratorNotice(ctx context.Context, n domain.LadoOrchestratorNotice) (bool, error)
	LadoPresenceProofUsed(ctx context.Context, nonce string) (bool, error)
	LadoProjectSettings(ctx context.Context, project domain.ProjectID) (domain.LadoProjectSettings, error)
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
	// AO's project registry.
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
}

// Blobs keeps the redacted outputs of the commands (LADO's blob store).
type Blobs interface {
	Put(data []byte) (string, error)
	Get(sum string, limit int64) ([]byte, error)
}

// Statuses posts lado/integration and checks AO's merge gate
// (*ladoverify.Service).
type Statuses interface {
	// RepostIntegrationStatusOf posts the status of the execution's
	// approval of the PR again, once its hold is released.
	RepostIntegrationStatusOf(ctx context.Context, session domain.SessionID, prURL string) (domain.LadoIntegrationApproval, error)
	// PreflightMerge runs the merge gate's checks of the approval, the base
	// and the merge order, without the database change hold.
	PreflightMerge(ctx context.Context, pr domain.PullRequest, head string) error
}

// MergeChecker reads a PR's merge readiness and merges it through AO's merge
// path (the daemon's adapter over AO's PR actions).
type MergeChecker interface {
	LadoMergeReadiness(ctx context.Context, prURL string, number int, head string) (domain.LadoMergeReadiness, error)
	MergeLadoPR(ctx context.Context, prURL string, number int, head string) error
}

// LiveHeads reads a PR as its host has it now (AO's SCM reader).
type LiveHeads interface {
	LivePR(ctx context.Context, pr domain.PullRequest) (ladoverify.LivePR, error)
}

// Deps wires a Service.
type Deps struct {
	Store Store
	// Git reads the repositories; nil uses the system git.
	Git Git
	// Blobs keeps the commands' redacted outputs.
	Blobs Blobs
	// Statuses, MergeChecker and LiveHeads serve the loop that applies and
	// merges; without them a run stops with an error.
	Statuses     Statuses
	MergeChecker MergeChecker
	LiveHeads    LiveHeads
	// StateDir is AO's state directory (~/.ao/data): the secrets live under
	// <StateDir>/lado/credentials/db and the commands' files under
	// <StateDir>/lado/tmp.
	StateDir string
	// Run runs one command and waits for it; nil uses ladoverify.RunCommand.
	Run func(ctx context.Context, spec ladoverify.CommandSpec) ladoverify.CommandResult
	// Env is the environment commands start from, before
	// ladoverify.CommandEnv takes AO's variables and the tokens out; nil
	// uses os.Environ.
	Env func() []string
	// Clock stamps records; nil uses time.Now.
	Clock func() time.Time
	// NewID returns a new unique id; nil uses uuid.NewString.
	NewID func() string
	Log   *slog.Logger
	// Interval is how often the loop runs; zero uses 15 seconds.
	Interval time.Duration
}

// Service is LADO's database change service.
type Service struct {
	deps Deps
	// settingsMu orders the changes of settings and secrets, so a baseline
	// and the secrets file follow the save they belong to.
	settingsMu sync.Mutex
	// secretsMu guards the secrets files.
	secretsMu sync.Mutex
	// kick wakes the loop.
	kick chan struct{}
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Git == nil {
		deps.Git = ExecGit{}
	}
	if deps.Run == nil {
		deps.Run = ladoverify.RunCommand
	}
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps, kick: make(chan struct{}, 1)}
}

// now is the clock's time in UTC.
func (s *Service) now() time.Time { return s.deps.Clock().UTC() }

// Kick wakes the loop without waiting for it.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}
