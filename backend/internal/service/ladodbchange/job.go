package ladodbchange

// The loop that applies an approved head's pre-merge SQL and merges it
// (ADR 0013). An integration approval whose record has pre-merge files not
// applied queues a run per PR (IntegrationApproved). The loop takes the
// queued runs in the execution's merge order, a PR only once the PRs before
// it merged, and checks first that the merge could follow: the host still
// has the approved head, AO's merge gate holds (approval, base, order) and
// no merge blocker that LADO's missing status does not explain stands
// (domain.LadoDBChangeMergeStoppers). A run that fails the check waits for
// the developer, with no SQL run. Then it applies the files that are not
// applied yet in path order, stops at the first failure, and once all are
// applied releases the status hold, posts lado/integration and merges
// through AO's merge path. A merge the host refuses right after the status
// was posted is tried again for mergeRetryFor while nothing but what the
// status explains stands in the way.
//
// Runs of a merged PR (merged outside LADO, or after-deploy files) only
// apply, when the developer asks with presence (ApplyDBChanges).

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

const (
	// defaultInterval is how often the loop runs without Deps.Interval.
	defaultInterval = 15 * time.Second
	// runBatch caps the runs one pass takes.
	runBatch = 50
	// mergeRetryFor is how long LADO tries a refused merge again while only
	// what its own status explains blocks it.
	mergeRetryFor = 15 * time.Minute
	// preflightGiveUp is how long a queued run waits for a preflight that
	// cannot read the host before it waits for the developer instead.
	preflightGiveUp = 15 * time.Minute
)

// LadoAuditDBChangeRequested is the audit action of the developer's request
// to apply a head's files.
const LadoAuditDBChangeRequested = "lado.db_change_requested"

// jobState is the loop's state.
type jobState struct {
	// applyMu lets one SQL command run at a time in the daemon.
	applyMu sync.Mutex
	wg      sync.WaitGroup

	mu sync.Mutex
	// recovered says the leftovers of a stopped daemon were marked.
	recovered bool
	// details are the db_changes details LADO computed for a merged head
	// without a record, or checked from the developer's request, by
	// "<pr> NUL <head>".
	details map[string]domain.LadoDBChangesDetails
	// actors are who queued a run, by run id; "lado" when not known.
	actors map[string]string
	// unknownSince is when Settled first failed for a PR head (settle.go).
	unknownSince map[string]time.Time
}

func headKey(prURL, head string) string { return prURL + "\x00" + strings.ToLower(head) }

func (j *jobState) setDetails(prURL, head string, details domain.LadoDBChangesDetails) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.details == nil {
		j.details = map[string]domain.LadoDBChangesDetails{}
	}
	j.details[headKey(prURL, head)] = details
}

func (j *jobState) cachedDetails(prURL, head string) (domain.LadoDBChangesDetails, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	d, ok := j.details[headKey(prURL, head)]
	return d, ok
}

func (j *jobState) setActor(run, actor string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.actors == nil {
		j.actors = map[string]string{}
	}
	j.actors[run] = actor
}

func (j *jobState) actor(run string) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return cmp.Or(j.actors[run], "lado")
}

// Start runs the loop until ctx ends: a pass every Deps.Interval (15
// seconds) or when kicked. The first pass marks what a stopped daemon left
// applying as interrupted.
func (s *Service) Start(ctx context.Context) {
	interval := s.deps.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	s.job.wg.Add(1)
	go func() {
		defer s.job.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				s.deps.Log.Warn("LADO database change pass failed", "err", RedactLine(err.Error(), nil))
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.kick:
			}
		}
	}()
}

// Wait waits for the loop Start began, and the command it runs, to end.
func (s *Service) Wait() { s.job.wg.Wait() }

// Tick is one pass of the loop.
func (s *Service) Tick(ctx context.Context) error {
	if err := s.recoverLeftovers(ctx); err != nil {
		return err
	}
	runs, err := s.deps.Store.ListLadoDBChangeRunsByState(ctx,
		[]domain.LadoDBChangeRunState{domain.LadoDBChangeRunQueued, domain.LadoDBChangeRunApplied, domain.LadoDBChangeRunMerging}, runBatch)
	if err != nil {
		return fmt.Errorf("list the database change runs: %w", err)
	}
	var problems []error
	for _, run := range runs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.advance(ctx, run); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			problems = append(problems, fmt.Errorf("run %s: %w", run.ID, err))
		}
	}
	return errors.Join(problems...)
}

// recoverLeftovers marks, once, the applications a stopped daemon left
// running as interrupted and their runs as failed: whether such a file is in
// the database the developer has to check.
func (s *Service) recoverLeftovers(ctx context.Context) error {
	s.job.mu.Lock()
	done := s.job.recovered
	s.job.mu.Unlock()
	if done {
		return nil
	}
	now := s.now()
	n, err := s.deps.Store.InterruptLadoDBChangeApplications(ctx, now)
	if err != nil {
		return fmt.Errorf("mark the interrupted SQL applications: %w", err)
	}
	runs, err := s.deps.Store.ListLadoDBChangeRunsByState(ctx, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunApplying}, 1000)
	if err != nil {
		return fmt.Errorf("list the interrupted database change runs: %w", err)
	}
	for _, run := range runs {
		if _, err := s.transitionAttempts(ctx, run, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunApplying}, domain.LadoDBChangeRunFailed,
			"interrupted: LADO stopped while the SQL ran; check the database before you retry"); err != nil {
			return err
		}
	}
	if n > 0 || len(runs) > 0 {
		s.deps.Log.Warn("LADO marked SQL applications a stopped daemon left running as interrupted", "applications", n, "runs", len(runs))
	}
	s.job.mu.Lock()
	s.job.recovered = true
	s.job.mu.Unlock()
	return nil
}

// advance moves one run as far as it goes in this pass.
func (s *Service) advance(ctx context.Context, run domain.LadoDBChangeRun) error {
	switch run.State {
	case domain.LadoDBChangeRunQueued:
		return s.startRun(ctx, run)
	case domain.LadoDBChangeRunApplied:
		return s.afterApplied(ctx, run)
	case domain.LadoDBChangeRunMerging:
		return s.merge(ctx, run)
	}
	return nil
}

// startRun checks a queued run and applies its files.
func (s *Service) startRun(ctx context.Context, run domain.LadoDBChangeRun) error {
	queued := []domain.LadoDBChangeRunState{domain.LadoDBChangeRunQueued}
	settings, on, err := s.Enabled(ctx, run.ProjectID, run.Repo)
	if err != nil {
		return err
	}
	if !on {
		_, err := s.transitionAttempts(ctx, run, queued, domain.LadoDBChangeRunAwaitingHuman, "the repository's database change settings are off")
		return err
	}
	details, ok, err := s.headDetails(ctx, run.ExecutionSessionID, run.PRURL, run.HeadSHA)
	if err != nil {
		return err
	}
	if !ok {
		_, err := s.transitionAttempts(ctx, run, queued, domain.LadoDBChangeRunAwaitingHuman, "LADO cannot tell the head's SQL files; apply them from the app")
		return err
	}
	pending, err := s.pendingOf(ctx, run.ProjectID, details, run.Kind.Phase())
	if err != nil {
		return err
	}
	pr, found, err := s.deps.Store.GetPR(ctx, run.PRURL)
	switch {
	case err != nil:
		return fmt.Errorf("read pull request %s: %w", run.PRURL, err)
	case !found:
		_, err := s.transitionAttempts(ctx, run, queued, domain.LadoDBChangeRunAwaitingHuman, "AO no longer knows the pull request")
		return err
	}

	if run.Kind == domain.LadoDBChangeRunPreMerge && pr.Merged && run.Trigger == domain.LadoDBChangeTriggerApproval && s.job.actor(run.ID) == "lado" {
		// Merged outside LADO before the loop applied the approved SQL: the
		// developer applies it from the app (Settled tells them).
		_, err := s.transitionAttempts(ctx, run, queued, domain.LadoDBChangeRunAwaitingHuman, "merged outside LADO before the SQL was applied")
		return err
	}
	needsMerge := run.Kind == domain.LadoDBChangeRunPreMerge && !pr.Merged
	if needsMerge {
		exec, ok, err := s.deps.Store.LadoExecution(ctx, run.ExecutionSessionID)
		switch {
		case err != nil:
			return fmt.Errorf("read execution %s: %w", run.ExecutionSessionID, err)
		case !ok:
			_, err := s.transitionAttempts(ctx, run, queued, domain.LadoDBChangeRunAwaitingHuman, "the execution is gone")
			return err
		}
		if len(domain.LadoMergeBlockers(exec, run.PRURL)) > 0 {
			// The PRs before it merge first; it waits queued.
			return nil
		}
		merged, reasons, err := s.preflight(ctx, run, pr)
		switch {
		case err != nil && s.now().Sub(run.UpdatedAt) < preflightGiveUp:
			s.deps.Log.Info("LADO could not check a pull request before applying its SQL; it tries again",
				"run", run.ID, "pr", run.PRURL, "err", RedactLine(err.Error(), nil))
			return nil
		case err != nil:
			reasons = []string{RedactLine(err.Error(), nil)}
		case merged:
			// Merged outside LADO before the SQL ran: Settled tells the
			// developer, who applies it from the app.
			_, err := s.transitionAttempts(ctx, run, queued, domain.LadoDBChangeRunAwaitingHuman, "merged outside LADO before the SQL was applied")
			return err
		}
		if len(reasons) > 0 {
			moved, err := s.transitionAttempts(ctx, run, queued, domain.LadoDBChangeRunAwaitingHuman, "preflight: "+strings.Join(reasons, ", "))
			if err == nil && moved {
				s.notice(ctx, run, "db_preflight", domain.LadoNoticeDBChangePreflightBlocked(s.issueOf(ctx, run.TaskID), reasons))
			}
			return err
		}
	}

	if len(pending) == 0 {
		next := domain.LadoDBChangeRunDone
		if needsMerge {
			next = domain.LadoDBChangeRunApplied
		}
		moved, err := s.transitionAttempts(ctx, run, queued, next, "")
		if err != nil || !moved || !needsMerge {
			return err
		}
		run.State = next
		return s.afterApplied(ctx, run)
	}

	run.Attempts++
	moved, err := s.transitionAttempts(ctx, run, queued, domain.LadoDBChangeRunApplying, "")
	if err != nil || !moved {
		return err
	}
	dir, err := s.RepoDir(ctx, run.ProjectID, run.Repo)
	if err != nil {
		return s.failRun(ctx, run, "", RedactLine(err.Error(), nil))
	}
	target := applyTarget{run: run, settings: settings, dir: dir, actor: s.job.actor(run.ID)}
	for _, file := range pending {
		if ctx.Err() != nil {
			// The next start marks the run interrupted.
			return ctx.Err()
		}
		res, err := s.applyFile(ctx, target, file)
		if err != nil {
			return s.failRun(ctx, run, file.Path, RedactLine(err.Error(), nil))
		}
		if !res.applied {
			return s.failRun(ctx, run, file.Path, res.message)
		}
	}
	applying := []domain.LadoDBChangeRunState{domain.LadoDBChangeRunApplying}
	if !needsMerge {
		_, err := s.transitionAttempts(ctx, run, applying, domain.LadoDBChangeRunDone, "")
		return err
	}
	if moved, err := s.transitionAttempts(ctx, run, applying, domain.LadoDBChangeRunApplied, ""); err != nil || !moved {
		return err
	}
	run.State = domain.LadoDBChangeRunApplied
	return s.afterApplied(ctx, run)
}

// failRun stops an applying run at a file that failed and tells the
// orchestrator. LADO does not merge.
func (s *Service) failRun(ctx context.Context, run domain.LadoDBChangeRun, path, message string) error {
	reason := message
	if path != "" {
		reason = path + ": " + message
	}
	moved, err := s.transitionAttempts(context.WithoutCancel(ctx), run, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunApplying}, domain.LadoDBChangeRunFailed, reason)
	if err == nil && moved {
		s.notice(ctx, run, "db_failed", domain.LadoNoticeDBChangeFailed(s.issueOf(ctx, run.TaskID), cmp.Or(path, "SQL")))
	}
	return err
}

// preflight checks that the approved head could merge once its SQL is
// applied: merged when the host merged it already, else the reasons it
// could not merge, none when it can. An error is a read that failed.
func (s *Service) preflight(ctx context.Context, run domain.LadoDBChangeRun, pr domain.PullRequest) (bool, []string, error) {
	if s.deps.LiveHeads == nil || s.deps.Statuses == nil || s.deps.MergeChecker == nil {
		return false, []string{"LADO cannot check pull requests on their host now"}, nil
	}
	live, err := s.deps.LiveHeads.LivePR(ctx, pr)
	if err != nil {
		return false, nil, fmt.Errorf("read the pull request from its host: %w", err)
	}
	switch {
	case live.Merged:
		return true, nil, nil
	case live.Closed:
		return false, []string{domain.MergeBlockerClosed}, nil
	case !strings.EqualFold(live.HeadSHA, run.HeadSHA):
		return false, []string{"head_changed"}, nil
	}
	if err := s.deps.Statuses.PreflightMerge(ctx, pr, run.HeadSHA); err != nil {
		var api *apierr.Error
		if errors.As(err, &api) && api.Kind != apierr.KindInternal && api.Kind != apierr.KindUnavailable {
			return false, []string{api.Code}, nil
		}
		return false, nil, fmt.Errorf("check AO's merge gate: %w", err)
	}
	readiness, err := s.deps.MergeChecker.LadoMergeReadiness(ctx, run.PRURL, pr.Number, run.HeadSHA)
	if err != nil {
		return false, nil, fmt.Errorf("read the pull request's merge readiness: %w", err)
	}
	if readiness.Merged {
		return true, nil, nil
	}
	return false, domain.LadoDBChangeMergeStoppers(readiness), nil
}

// afterApplied releases the status holds of an applied run's head, posts
// lado/integration and moves the run to merging.
func (s *Service) afterApplied(ctx context.Context, run domain.LadoDBChangeRun) error {
	approvals, err := s.deps.Store.ListLadoIntegrationApprovals(ctx, run.ExecutionSessionID, run.PRURL, run.HeadSHA)
	if err != nil {
		return fmt.Errorf("read the approvals of %s: %w", run.PRURL, err)
	}
	ids := []string{run.ApprovalID}
	for _, approval := range approvals {
		ids = append(ids, approval.ID)
	}
	for _, id := range slices.Compact(slices.Sorted(slices.Values(ids))) {
		if id == "" {
			continue
		}
		if err := s.deps.Store.ReleaseLadoIntegrationStatus(ctx, id); err != nil {
			return fmt.Errorf("release the hold of approval %s: %w", id, err)
		}
	}
	if s.deps.Statuses != nil {
		if _, err := s.deps.Statuses.RepostIntegrationStatusOf(ctx, run.ExecutionSessionID, run.PRURL); err != nil {
			// The verification loop posts it; a merge the host refuses
			// meanwhile is tried again.
			s.deps.Log.Warn("LADO applied a head's SQL but could not post its status yet", "run", run.ID, "pr", run.PRURL, "err", RedactLine(err.Error(), nil))
		}
	}
	moved, err := s.transitionAttempts(ctx, run, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunApplied}, domain.LadoDBChangeRunMerging, "")
	if err != nil || !moved {
		return err
	}
	run.State, run.UpdatedAt = domain.LadoDBChangeRunMerging, s.now()
	return s.merge(ctx, run)
}

// merge merges a run's PR through AO's merge path. A refusal is read again:
// a PR merged meanwhile is done, one that only lacks what LADO's status
// explains is tried again until mergeRetryFor after the run started merging,
// anything else stops the run as merge_failed.
func (s *Service) merge(ctx context.Context, run domain.LadoDBChangeRun) error {
	merging := []domain.LadoDBChangeRunState{domain.LadoDBChangeRunMerging}
	if s.deps.MergeChecker == nil {
		return s.mergeFailed(ctx, run, "LADO cannot merge pull requests now")
	}
	pr, found, err := s.deps.Store.GetPR(ctx, run.PRURL)
	switch {
	case err != nil:
		return fmt.Errorf("read pull request %s: %w", run.PRURL, err)
	case !found:
		return s.mergeFailed(ctx, run, "AO no longer knows the pull request")
	case pr.Merged:
		_, err := s.transitionAttempts(ctx, run, merging, domain.LadoDBChangeRunDone, "")
		return err
	}
	mergeErr := s.deps.MergeChecker.MergeLadoPR(ctx, run.PRURL, pr.Number, run.HeadSHA)
	if mergeErr == nil {
		s.deps.Log.Info("LADO applied a head's SQL and merged its pull request", "run", run.ID, "pr", run.PRURL, "head", run.HeadSHA)
		_, err := s.transitionAttempts(ctx, run, merging, domain.LadoDBChangeRunDone, "")
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	readiness, readErr := s.deps.MergeChecker.LadoMergeReadiness(ctx, run.PRURL, pr.Number, run.HeadSHA)
	if readErr == nil && readiness.Merged {
		_, err := s.transitionAttempts(ctx, run, merging, domain.LadoDBChangeRunDone, "")
		return err
	}
	reason := RedactLine(mergeErr.Error(), nil)
	if (readErr != nil || len(domain.LadoDBChangeMergeStoppers(readiness)) == 0) && s.now().Sub(run.UpdatedAt) < mergeRetryFor {
		s.deps.Log.Info("LADO's merge after applying SQL was refused; it tries again", "run", run.ID, "pr", run.PRURL, "err", reason)
		return nil
	}
	if readErr == nil {
		if stoppers := domain.LadoDBChangeMergeStoppers(readiness); len(stoppers) > 0 {
			reason += " (" + strings.Join(stoppers, ", ") + ")"
		}
	}
	return s.mergeFailed(ctx, run, reason)
}

// mergeFailed stops a merging run and tells the orchestrator.
func (s *Service) mergeFailed(ctx context.Context, run domain.LadoDBChangeRun, reason string) error {
	moved, err := s.transitionAttempts(ctx, run, []domain.LadoDBChangeRunState{domain.LadoDBChangeRunMerging}, domain.LadoDBChangeRunMergeFailed, reason)
	if err == nil && moved {
		s.notice(ctx, run, "db_merge_failed", domain.LadoNoticeDBChangeMergeFailed(s.issueOf(ctx, run.TaskID)))
	}
	return err
}

// transitionAttempts moves a run to to with run.Attempts and reason, false
// when it was no longer in from.
func (s *Service) transitionAttempts(ctx context.Context, run domain.LadoDBChangeRun, from []domain.LadoDBChangeRunState, to domain.LadoDBChangeRunState, reason string) (bool, error) {
	moved, err := s.deps.Store.TransitionLadoDBChangeRun(ctx, run.ID, from, to, run.Attempts, RedactLine(reason, nil), s.now(), nil, nil)
	if err != nil {
		return false, fmt.Errorf("move run %s to %s: %w", run.ID, to, err)
	}
	return moved, nil
}

// notice tells the project's orchestrator of a run, once per key and moment.
func (s *Service) notice(ctx context.Context, run domain.LadoDBChangeRun, kind, text string) {
	now := s.now()
	key := fmt.Sprintf("%s:%s:%d", kind, run.ID, now.UnixMilli())
	s.addNotice(ctx, run.ProjectID, key, text, now)
}

// addNotice adds an orchestrator notice; a failure is logged.
func (s *Service) addNotice(ctx context.Context, project domain.ProjectID, key, text string, now time.Time) {
	if _, err := s.deps.Store.AddLadoOrchestratorNotice(context.WithoutCancel(ctx), domain.LadoOrchestratorNotice{
		ProjectID: project, Key: key, Text: text, OccurredAt: now, CreatedAt: now,
	}); err != nil {
		s.deps.Log.Warn("LADO could not tell the orchestrator of a database change", "project", project, "key", key, "err", err)
	}
}

// issueOf is the issue number of a task, 0 when it has none.
func (s *Service) issueOf(ctx context.Context, id domain.LadoTaskID) int {
	task, ok, err := s.deps.Store.LadoTask(ctx, id)
	if err != nil || !ok {
		return 0
	}
	ref, err := domain.ParseLadoIssueRef(task.IssueRef)
	if err != nil {
		return 0
	}
	return domain.LadoIssueNumber(ref)
}

// headDetails returns the db_changes details of a head: its stored record's,
// else those LADO computed or checked for it since it started.
func (s *Service) headDetails(ctx context.Context, session domain.SessionID, prURL, head string) (domain.LadoDBChangesDetails, bool, error) {
	record, found, err := s.deps.Store.GetLadoVerification(ctx, session, head)
	if err != nil {
		return domain.LadoDBChangesDetails{}, false, fmt.Errorf("read the record of %s: %w", head, err)
	}
	if found {
		details, has, err := record.DBChanges()
		if err != nil || has {
			return details, has, err
		}
	}
	details, ok := s.job.cachedDetails(prURL, head)
	return details, ok, nil
}

// IntegrationApproved queues a pre-merge run for each approved head whose
// record has pre-merge files not applied yet, and wakes the loop. What
// fails, it logs: the developer can apply from the app.
func (s *Service) IntegrationApproved(ctx context.Context, exec domain.LadoExecution, approvals []domain.LadoIntegrationApproval) {
	queued := false
	for _, approval := range approvals {
		if approval.Revoked() {
			continue
		}
		created, err := s.queueApproved(ctx, exec, approval)
		if err != nil {
			s.deps.Log.Warn("LADO could not queue an approved head's SQL", "session", exec.SessionID, "pr", approval.PRURL,
				"head", approval.HeadSHA, "err", RedactLine(err.Error(), nil))
			continue
		}
		queued = queued || created
	}
	if queued {
		s.Kick()
	}
}

// queueApproved makes the pre-merge run of an approved head, true when it
// is new.
func (s *Service) queueApproved(ctx context.Context, exec domain.LadoExecution, approval domain.LadoIntegrationApproval) (bool, error) {
	record, found, err := s.deps.Store.GetLadoVerification(ctx, approval.ExecutionSessionID, approval.HeadSHA)
	if err != nil || !found {
		return false, err
	}
	details, has, err := record.DBChanges()
	if err != nil || !has || len(details.PreMerge()) == 0 {
		return false, err
	}
	task, ok, err := s.deps.Store.LadoTask(ctx, exec.TaskID)
	switch {
	case err != nil:
		return false, err
	case !ok:
		return false, fmt.Errorf("task %s not found", exec.TaskID)
	}
	pending, err := s.PendingPreMerge(ctx, task.ProjectID, details)
	if err != nil || len(pending) == 0 {
		return false, err
	}
	now := s.now()
	_, created, err := s.deps.Store.EnsureLadoDBChangeRun(ctx, domain.LadoDBChangeRun{
		ID: s.deps.NewID(), ProjectID: task.ProjectID, Repo: details.Repo, ExecutionSessionID: exec.SessionID,
		TaskID: exec.TaskID, PRURL: approval.PRURL, HeadSHA: approval.HeadSHA, Kind: domain.LadoDBChangeRunPreMerge,
		Trigger: domain.LadoDBChangeTriggerApproval, State: domain.LadoDBChangeRunQueued, ApprovalID: approval.ID,
		CreatedAt: now, UpdatedAt: now,
	})
	return created, err
}

// ApplyDBChanges queues, with the developer's presence (db_change.apply,
// subject "<session>@<head>:<phase>"), a head's run of one phase that waits
// for the developer or stopped: a preflight that stopped, a failed file, a
// failed merge, files merged outside LADO, or after-deploy files. The files
// the developer confirmed must cover every file the run applies. Files
// applied already are skipped.
func (s *Service) ApplyDBChanges(ctx context.Context, req domain.LadoDBChangeApplyRequest, presence func(action, subject string) (domain.LadoPresenceProof, error)) (domain.LadoDBChangeRun, error) {
	none := domain.LadoDBChangeRun{}
	head := strings.ToLower(strings.TrimSpace(req.HeadSHA))
	var problems []domain.LadoDBChangeProblem
	if req.Phase != domain.LadoDBChangePreMerge && req.Phase != domain.LadoDBChangeAfterDeploy {
		problems = append(problems, domain.LadoDBChangeProblem{Field: "phase", Reason: domain.LadoDBChangeProblemInvalid})
	}
	if !IsObjectID(head) {
		problems = append(problems, domain.LadoDBChangeProblem{Field: "headSha", Reason: domain.LadoDBChangeProblemInvalid})
	}
	if strings.TrimSpace(req.PRURL) == "" {
		problems = append(problems, domain.LadoDBChangeProblem{Field: "prUrl", Reason: domain.LadoDBChangeProblemRequired})
	}
	if len(problems) > 0 {
		return none, invalid(problems)
	}
	kind := domain.LadoDBChangeRunPreMerge
	if req.Phase == domain.LadoDBChangeAfterDeploy {
		kind = domain.LadoDBChangeRunAfterDeploy
	}

	exec, tracked, project, err := s.trackedPR(ctx, req.SessionID, req.PRURL)
	if err != nil {
		return none, err
	}
	settings, on, err := s.Enabled(ctx, project, tracked.Repo)
	if err != nil {
		s.deps.Log.Error("LADO could not read database change settings", "project", project, "err", err)
		return none, loadFailed()
	}
	if !on {
		return none, apierr.Conflict("LADO_DB_CHANGE_DISABLED", "The repository's database change settings are off.",
			map[string]any{"repo": tracked.Repo})
	}
	runs, err := s.deps.Store.ListLadoDBChangeRunsForExecution(ctx, req.SessionID)
	if err != nil {
		s.deps.Log.Error("LADO could not read database change runs", "session", req.SessionID, "err", err)
		return none, loadFailed()
	}
	at := slices.IndexFunc(runs, func(r domain.LadoDBChangeRun) bool {
		return r.PRURL == tracked.PR.URL && strings.EqualFold(r.HeadSHA, head) && r.Kind == kind
	})
	merged := tracked.PR.Merged
	if pr, found, err := s.deps.Store.GetPR(ctx, tracked.PR.URL); err == nil && found {
		merged = merged || pr.Merged
	}
	var run domain.LadoDBChangeRun
	if at >= 0 {
		run = runs[at]
		if !run.State.Retryable() {
			return none, runState(run)
		}
	} else {
		switch {
		case !merged && kind == domain.LadoDBChangeRunPreMerge:
			return none, apierr.Conflict("LADO_DB_CHANGE_RUN_STATE",
				"Approve the pull request's integration: LADO applies its SQL and then merges it.", map[string]any{"prUrl": tracked.PR.URL})
		case !merged:
			return none, apierr.Conflict("LADO_DB_CHANGE_RUN_STATE",
				"After-deploy SQL waits until the pull request merged and deployed.", map[string]any{"prUrl": tracked.PR.URL})
		case !strings.EqualFold(tracked.PR.HeadSHA, head):
			return none, apierr.Conflict("LADO_DB_CHANGE_RUN_STATE",
				"The pull request merged at another head.", map[string]any{"prUrl": tracked.PR.URL, "headSha": tracked.PR.HeadSHA})
		}
	}

	details, ok, err := s.headDetails(ctx, req.SessionID, tracked.PR.URL, head)
	if err != nil {
		s.deps.Log.Error("LADO could not read a head's database changes", "session", req.SessionID, "err", err)
		return none, loadFailed()
	}
	if !ok {
		if details, err = s.checkRequestedFiles(ctx, project, settings, head, req); err != nil {
			return none, err
		}
	}
	pending, err := s.pendingOf(ctx, project, details, req.Phase)
	if err != nil {
		s.deps.Log.Error("LADO could not read the applied SQL files", "session", req.SessionID, "err", err)
		return none, loadFailed()
	}
	if err := coversPending(req.Files, pending); err != nil {
		return none, err
	}
	if len(pending) == 0 && (at < 0 || run.State != domain.LadoDBChangeRunMergeFailed) &&
		(kind != domain.LadoDBChangeRunPreMerge || merged) {
		return none, apierr.Conflict("LADO_DB_CHANGE_RUN_STATE", "Every SQL file of this head is applied already.",
			map[string]any{"prUrl": tracked.PR.URL, "headSha": head})
	}

	proof, err := s.presence(ctx, presence, domain.LadoPresenceDBChangeApply, domain.LadoDBChangeApplySubject(req.SessionID, head, req.Phase),
		"Apply the database changes in the AO desktop app. It asks you to confirm, then sends the request itself.")
	if err != nil {
		return none, err
	}
	now := s.now()
	if at < 0 {
		trigger := domain.LadoDBChangeTriggerMergedUnapplied
		if kind == domain.LadoDBChangeRunAfterDeploy {
			trigger = domain.LadoDBChangeTriggerAfterMerge
		}
		if run, _, err = s.deps.Store.EnsureLadoDBChangeRun(ctx, domain.LadoDBChangeRun{
			ID: s.deps.NewID(), ProjectID: project, Repo: tracked.Repo, ExecutionSessionID: exec.SessionID, TaskID: exec.TaskID,
			PRURL: tracked.PR.URL, HeadSHA: head, Kind: kind, Trigger: trigger, State: domain.LadoDBChangeRunAwaitingHuman,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			s.deps.Log.Error("LADO could not make a database change run", "session", req.SessionID, "err", err)
			return none, applyFailed()
		}
		if !run.State.Retryable() {
			return none, runState(run)
		}
	}
	if !ok {
		s.job.setDetails(tracked.PR.URL, head, details)
	}

	actor := "human"
	if proof.Dev {
		actor = "human:dev-presence"
	}
	files := make([]map[string]string, len(pending))
	for i, file := range pending {
		files[i] = map[string]string{"path": file.Path, "blobSha": file.BlobSHA}
	}
	payload, _ := json.Marshal(map[string]any{
		"run": run.ID, "repo": run.Repo, "pr": run.PRURL, "head": run.HeadSHA, "phase": req.Phase, "from": run.State,
		"files": files, "proof": proof.Nonce, "dev": proof.Dev,
	})
	audit := domain.LadoAuditEvent{
		At: now, Actor: actor, Action: LadoAuditDBChangeRequested, SubjectKind: "execution",
		SubjectID: string(run.ExecutionSessionID), Payload: payload,
	}
	moved, err := s.deps.Store.TransitionLadoDBChangeRun(ctx, run.ID,
		[]domain.LadoDBChangeRunState{domain.LadoDBChangeRunAwaitingHuman, domain.LadoDBChangeRunFailed, domain.LadoDBChangeRunMergeFailed},
		domain.LadoDBChangeRunQueued, run.Attempts, "", now, &proof, &audit)
	switch {
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return none, ladopresence.Replayed()
	case err != nil:
		s.deps.Log.Error("LADO could not queue a database change run", "run", run.ID, "err", err)
		return none, applyFailed()
	case !moved:
		latest, _, _ := s.deps.Store.LadoDBChangeRun(ctx, run.ID)
		return none, runState(latest)
	}
	s.job.setActor(run.ID, actor)
	s.Kick()
	run.State, run.Error, run.UpdatedAt = domain.LadoDBChangeRunQueued, "", now
	return run, nil
}

// trackedPR returns an execution, its tracked PR at prURL and its project.
func (s *Service) trackedPR(ctx context.Context, session domain.SessionID, prURL string) (domain.LadoExecution, domain.LadoExecutionTrackedPR, domain.ProjectID, error) {
	exec, ok, err := s.deps.Store.LadoExecution(ctx, session)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read an execution", "session", session, "err", err)
		return exec, domain.LadoExecutionTrackedPR{}, "", loadFailed()
	case !ok:
		return exec, domain.LadoExecutionTrackedPR{}, "", apierr.NotFound("LADO_EXECUTION_NOT_FOUND", "This session is not a LADO execution")
	}
	tracked := exec.TrackedPRs()
	at := slices.IndexFunc(tracked, func(pr domain.LadoExecutionTrackedPR) bool { return pr.PR.URL == prURL })
	if at < 0 {
		return exec, domain.LadoExecutionTrackedPR{}, "", apierr.NotFound("LADO_DB_CHANGE_NOT_FOUND", "The execution tracks no such pull request")
	}
	task, ok, err := s.deps.Store.LadoTask(ctx, exec.TaskID)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read a task", "task", exec.TaskID, "err", err)
		return exec, domain.LadoExecutionTrackedPR{}, "", loadFailed()
	case !ok:
		return exec, domain.LadoExecutionTrackedPR{}, "", apierr.NotFound("LADO_TASK_NOT_FOUND", "The execution's task is gone")
	}
	return exec, tracked[at], task.ProjectID, nil
}

// checkRequestedFiles checks the files of a request for a head LADO has no
// details of: each must match the settings' paths and be the blob the head
// has at that path, in the phase its content says.
func (s *Service) checkRequestedFiles(ctx context.Context, project domain.ProjectID, settings domain.LadoDBChangeSettings, head string, req domain.LadoDBChangeApplyRequest) (domain.LadoDBChangesDetails, error) {
	details := domain.LadoDBChangesDetails{Repo: settings.Repo, Files: []domain.LadoDBChangeFileRef{}, Modified: []domain.LadoDBChangeModified{}}
	if len(req.Files) == 0 {
		return details, filesChanged(nil, "LADO does not know this head's SQL files: open them in the app and confirm them.")
	}
	dir, err := s.RepoDir(ctx, project, settings.Repo)
	if err != nil {
		s.deps.Log.Error("LADO could not find a repository's folder", "project", project, "err", err)
		return details, loadFailed()
	}
	for _, file := range req.Files {
		if file.Phase != req.Phase || !matchesAny(settings.Paths, file.Path) || !IsObjectID(file.BlobSHA) {
			return details, filesChanged(nil, "A confirmed file is not one of the head's SQL files.")
		}
		entries, err := s.deps.Git.Tree(ctx, dir, head, file.Path)
		if err != nil || len(entries) != 1 || entries[0].Path != file.Path || entries[0].Blob != file.BlobSHA || !regularFile(entries[0].Mode) {
			return details, filesChanged(nil, "A confirmed file is not at that blob in the head.")
		}
		content, _, err := s.deps.Git.Blob(ctx, dir, file.BlobSHA, BlobReadMax)
		if err != nil || domain.LadoDBChangePhaseOf(string(content)) != file.Phase {
			return details, filesChanged(nil, "A confirmed file's phase is not the one it says.")
		}
		details.Files = append(details.Files, file)
	}
	slices.SortFunc(details.Files, func(a, b domain.LadoDBChangeFileRef) int { return strings.Compare(a.Path, b.Path) })
	details.Files = slices.CompactFunc(details.Files, func(a, b domain.LadoDBChangeFileRef) bool { return a.Path == b.Path })
	return details, nil
}

// coversPending checks that the confirmed files include every pending file
// at its blob.
func coversPending(confirmed, pending []domain.LadoDBChangeFileRef) error {
	for _, file := range pending {
		if !slices.ContainsFunc(confirmed, func(c domain.LadoDBChangeFileRef) bool {
			return c.Path == file.Path && strings.EqualFold(c.BlobSHA, file.BlobSHA) && c.Phase == file.Phase
		}) {
			return filesChanged(pending, "The SQL files to apply are not the ones you confirmed. Review them, then apply again.")
		}
	}
	return nil
}

func filesChanged(pending []domain.LadoDBChangeFileRef, message string) error {
	files := make([]map[string]string, len(pending))
	for i, file := range pending {
		files[i] = map[string]string{"path": file.Path, "blobSha": file.BlobSHA, "phase": string(file.Phase)}
	}
	return apierr.Conflict("LADO_DB_CHANGE_FILES_CHANGED", message, map[string]any{"files": files})
}

func runState(run domain.LadoDBChangeRun) error {
	return apierr.Conflict("LADO_DB_CHANGE_RUN_STATE", "The database change run is "+string(run.State)+" and cannot be applied again now.",
		map[string]any{"runId": run.ID, "state": string(run.State)})
}

func applyFailed() error {
	return apierr.Internal("LADO_DB_CHANGE_APPLY_FAILED", "Failed to queue the database changes")
}
