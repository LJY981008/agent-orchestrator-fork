package ladodbchange

// A merged PR, as the verification loop settles it (ladoverify calls
// Settled before it records the settlement). A head merged with pre-merge
// SQL that is not applied, as when someone merged it on the host, gets a
// pre-merge run waiting for the developer; a head with after-deploy SQL gets
// an after-deploy run waiting for the developer's button once the deploy is
// done. Each tells the orchestrator once. An error makes the loop tell the
// PR again on its next pass; after unknownGiveUp of errors LADO tells the
// orchestrator it cannot tell and lets the PR settle.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// unknownGiveUp is how long Settled fails for a PR head before LADO lets it
// settle without knowing its SQL.
const unknownGiveUp = time.Hour

// Settled looks at an execution's merged PR (exec.PR) before LADO settles
// it: the stored record of its head, else what LADO knows of the head (in).
// It returns nil when the repository's settings are off.
func (s *Service) Settled(ctx context.Context, exec domain.LadoExecution, record *domain.LadoVerification, in *domain.LadoDBChangeInput) error {
	if !exec.PR.Merged || exec.PR.URL == "" {
		return nil
	}
	key := headKey(exec.PR.URL, exec.PR.HeadSHA)
	err := s.settled(ctx, exec, record, in)
	if err == nil {
		s.forgetUnknown(key)
		return nil
	}
	since := s.unknownSince(key)
	if s.now().Sub(since) < unknownGiveUp {
		return err
	}
	s.deps.Log.Warn("LADO could not tell for an hour whether a merged pull request's SQL is applied; it lets it settle",
		"session", exec.SessionID, "pr", exec.PR.URL, "err", RedactLine(err.Error(), nil))
	project, perr := s.projectOf(ctx, exec.SessionID)
	if perr == nil {
		now := s.now()
		s.addNotice(ctx, project, "db_unknown:"+exec.PR.URL+":"+exec.PR.HeadSHA, domain.LadoNoticeDBChangeUnknown(s.issueOf(ctx, exec.TaskID)), now)
	}
	s.forgetUnknown(key)
	return nil
}

// settled is Settled without the give-up.
func (s *Service) settled(ctx context.Context, exec domain.LadoExecution, record *domain.LadoVerification, in *domain.LadoDBChangeInput) error {
	task, ok, err := s.deps.Store.LadoTask(ctx, exec.TaskID)
	switch {
	case err != nil:
		return fmt.Errorf("read task %s: %w", exec.TaskID, err)
	case !ok:
		return nil
	}
	project := task.ProjectID
	repo := repoOf(exec, exec.PR.URL)

	var details domain.LadoDBChangesDetails
	has := false
	if record != nil {
		if details, has, err = record.DBChanges(); err != nil {
			return err
		}
		if has {
			repo = details.Repo
		}
	}
	if in != nil && !has {
		repo = in.Repo
	}
	if _, on, err := s.Enabled(ctx, project, repo); err != nil || !on {
		return err
	}
	if !has {
		if in == nil {
			return fmt.Errorf("LADO knows neither the record nor the inputs of merged head %s", exec.PR.HeadSHA)
		}
		item, ok, err := s.Item(ctx, *in)
		if err != nil || !ok {
			return err
		}
		if err := json.Unmarshal(item.Details, &details); err != nil {
			return fmt.Errorf("read the db_changes details: %w", err)
		}
		s.job.setDetails(exec.PR.URL, exec.PR.HeadSHA, details)
	}

	issue := s.issueOf(ctx, exec.TaskID)
	preMerge, err := s.pendingOf(ctx, project, details, domain.LadoDBChangePreMerge)
	if err != nil {
		return err
	}
	if len(preMerge) > 0 {
		if err := s.settleRun(ctx, exec, project, details.Repo, domain.LadoDBChangeRunPreMerge, domain.LadoDBChangeTriggerMergedUnapplied,
			"db_merged_unapplied", domain.LadoNoticeDBChangeMergedOutside(issue, len(preMerge))); err != nil {
			return err
		}
	}
	afterDeploy, err := s.pendingOf(ctx, project, details, domain.LadoDBChangeAfterDeploy)
	if err != nil {
		return err
	}
	if len(afterDeploy) > 0 {
		if err := s.settleRun(ctx, exec, project, details.Repo, domain.LadoDBChangeRunAfterDeploy, domain.LadoDBChangeTriggerAfterMerge,
			"db_after_deploy", domain.LadoNoticeDBChangeAfterDeploy(issue, len(afterDeploy))); err != nil {
			return err
		}
	}
	return nil
}

// settleRun makes the merged head's run of kind wait for the developer and
// tells the orchestrator once per head and kind. A run of the approval that
// had not applied everything when the PR merged waits for the developer
// too; one applying is left to finish.
func (s *Service) settleRun(ctx context.Context, exec domain.LadoExecution, project domain.ProjectID, repo string, kind domain.LadoDBChangeRunKind, trigger domain.LadoDBChangeTrigger, noticeKind, text string) error {
	now := s.now()
	run, _, err := s.deps.Store.EnsureLadoDBChangeRun(ctx, domain.LadoDBChangeRun{
		ID: s.deps.NewID(), ProjectID: project, Repo: repo, ExecutionSessionID: exec.SessionID, TaskID: exec.TaskID,
		PRURL: exec.PR.URL, HeadSHA: exec.PR.HeadSHA, Kind: kind, Trigger: trigger,
		State: domain.LadoDBChangeRunAwaitingHuman, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return fmt.Errorf("make the %s run of %s: %w", kind, exec.PR.URL, err)
	}
	stale := []domain.LadoDBChangeRunState{
		domain.LadoDBChangeRunQueued, domain.LadoDBChangeRunApplied, domain.LadoDBChangeRunMerging, domain.LadoDBChangeRunMergeFailed,
	}
	if slices.Contains(stale, run.State) {
		if _, err := s.transitionAttempts(ctx, run, stale, domain.LadoDBChangeRunAwaitingHuman, "merged outside LADO before the SQL was applied"); err != nil {
			return err
		}
	}
	s.addNotice(ctx, project, noticeKind+":"+exec.PR.URL+":"+exec.PR.HeadSHA, text, now)
	return nil
}

// repoOf is the repository of an execution's tracked PR, "" when it tracks
// none at that URL.
func repoOf(exec domain.LadoExecution, prURL string) string {
	for _, pr := range exec.TrackedPRs() {
		if pr.PR.URL == prURL {
			return pr.Repo
		}
	}
	return ""
}

func (s *Service) unknownSince(key string) time.Time {
	s.job.mu.Lock()
	defer s.job.mu.Unlock()
	if s.job.unknownSince == nil {
		s.job.unknownSince = map[string]time.Time{}
	}
	since, ok := s.job.unknownSince[key]
	if !ok {
		since = s.now()
		s.job.unknownSince[key] = since
	}
	return since
}

func (s *Service) forgetUnknown(key string) {
	s.job.mu.Lock()
	defer s.job.mu.Unlock()
	delete(s.job.unknownSince, key)
}
