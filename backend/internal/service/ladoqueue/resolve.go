package ladoqueue

// Resolve workers (issue #53): when an execution's open PR conflicts with its
// base, or the base moved under its head with a change to the same paths
// (base_overlap), and no session works on the PR any more (the execution's
// own session ended or its agent exited), the dispatcher starts a resolve
// worker: a merge worker without a part, on its own branch made at the PR's
// head. It merges the base, runs the checks and pushes to the PR's source
// branch without force, then reports done. LADO reads the source branch on
// the remote to judge it: the attempt resolved the PR once the branch moved
// from the head the worker started at and contains the base tip of then.
// The PR stays the execution's; an exited original session is left as it is.
// Workers that fail LadoMaxResolveAttempts times in a row, or one that asks
// for a decision, leave the PR to a person (needs_human) with one alert.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

const (
	// resolveListLimit bounds the executions one pass looks at.
	resolveListLimit = 50
	// resolveRecheckAfter is how long the resolver leaves a PR alone after a
	// look at its branches found nothing to do yet.
	resolveRecheckAfter = time.Minute
	// defaultResolveRemote is the remote read when neither the execution nor
	// the project names one.
	defaultResolveRemote = "origin"
)

// RemoteGit reads branch tips on a remote.
type RemoteGit interface {
	// FetchBranch fetches branch from remote into the repository at dir and
	// returns the commit it points at on the remote.
	FetchBranch(ctx context.Context, dir, remote, branch string) (string, error)
}

// execRemoteGit fetches with the system git, every prompt off. It writes only
// the branch's remote-tracking ref, as a plain fetch does.
type execRemoteGit struct{}

func (execRemoteGit) FetchBranch(ctx context.Context, dir, remote, branch string) (string, error) {
	if !plainRefPart(remote) || !plainRefPart(branch) {
		return "", fmt.Errorf("fetch: %q of remote %q is not a plain branch name", branch, remote)
	}
	tracking := "refs/remotes/" + remote + "/" + branch
	if _, err := runPartGit(ctx, dir, nil, "fetch", "--no-tags", "--quiet", "--end-of-options", remote, "+refs/heads/"+branch+":"+tracking); err != nil {
		return "", fmt.Errorf("fetch %s from %s: %w", branch, remote, err)
	}
	sha, err := runPartGit(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", tracking+"^{commit}")
	if err != nil || !commitID.MatchString(sha) {
		return "", fmt.Errorf("read %s of %s after the fetch: %w", branch, remote, errors.Join(err, errors.New("no commit")))
	}
	return sha, nil
}

// plainRefPart reports whether name can go into a fetch refspec as it is: no
// option, no refspec or revision syntax.
func plainRefPart(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.HasSuffix(name, ".lock") {
		return false
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f || strings.ContainsRune("~^:?*[\\", r) {
			return false
		}
	}
	return true
}

// resolveRound is what one resolve pass shares between its executions.
type resolveRound struct {
	settings domain.LadoDispatchSettings
	load     *domain.LadoRunLoad
	tight    bool
	holds    launchHolds
	now      time.Time
}

// resolvePass looks at every execution whose PR needs a resolve worker or has
// one: it starts workers within the worker cap, follows the running ones, and
// clears the resolve of a PR that merged or closed.
func (s *Service) resolvePass(ctx context.Context, now time.Time, settings domain.LadoDispatchSettings) error {
	candidates, err := s.deps.Store.ListLadoExecutionsToResolve(ctx, resolveListLimit)
	if err != nil {
		return fmt.Errorf("list the PRs to resolve: %w", err)
	}
	if len(candidates) == 0 {
		return nil
	}
	load, err := s.deps.Store.LadoRunLoad(ctx, now)
	if err != nil {
		return err
	}
	round := &resolveRound{settings: settings, load: &load, holds: launchHolds{}, now: now}
	round.tight = settings.HoldOnMemoryTight && s.memoryTight(ctx)
	var problems []error
	for _, c := range candidates {
		if ctx.Err() != nil {
			break
		}
		if err := s.resolveOne(ctx, c, round); err != nil {
			problems = append(problems, fmt.Errorf("resolve the PR of execution %s: %w", c.Execution.SessionID, err))
		}
	}
	return errors.Join(problems...)
}

func (s *Service) resolveOne(ctx context.Context, c domain.LadoResolveCandidate, round *resolveRound) error {
	r, pr := c.Resolve, c.Execution.PR
	if r.SpawnRequest != "" {
		// A start is in progress, or a stopped pass left it: recovery settles
		// it once its grant expired.
		return nil
	}
	if pr.URL == "" || pr.Merged || pr.Closed {
		return s.endResolve(ctx, c, round.now)
	}
	switch r.State {
	case domain.LadoResolveResolving:
		return s.followResolve(ctx, c, round.now)
	case domain.LadoResolveNeedsHuman:
		return s.reopenResolve(ctx, c, round.now)
	}
	if !c.Conflicting() || c.SessionLive || c.ResolveSessionLive {
		// The execution's own session gets AO's conflict notice; a resolve
		// worker still running is told by followResolve.
		return nil
	}
	return s.startResolve(ctx, c, round)
}

// endResolve stops the resolve worker of a PR that merged or closed and
// clears its resolve. A worker that could not be stopped keeps the state, so
// that the next pass stops it again.
func (s *Service) endResolve(ctx context.Context, c domain.LadoResolveCandidate, now time.Time) error {
	r := c.Resolve
	if c.ResolveSessionLive {
		if _, err := s.deps.Sessions.RequestKill(ctx, r.SessionID); err != nil {
			s.deps.Log.Warn("LADO could not stop the resolve worker of a settled PR", "task", r.TaskID, "session", r.SessionID,
				"problem", problem(err, "the session could not be stopped"))
			return nil
		}
	}
	s.forgetResolve(c.Execution.SessionID)
	if r.Version == 0 || (r.State == domain.LadoResolveNone && r.Attempts == 0 && r.Error == "" && r.DoneSeenAt.IsZero()) {
		return nil
	}
	next := r
	next.State, next.Attempts, next.DoneSeenAt, next.Error = domain.LadoResolveNone, 0, time.Time{}, ""
	if ok, err := s.writeResolve(ctx, next, now); err != nil || !ok {
		return err
	}
	s.appendAudit(ctx, s.audit("resolve.ended", r.QueueEntryID, map[string]any{"task": r.TaskID, "execution": r.ExecutionSessionID, "session": r.SessionID}))
	return nil
}

// reopenResolve clears a resolve a person took over once the PR no longer
// conflicts: its count starts again.
func (s *Service) reopenResolve(ctx context.Context, c domain.LadoResolveCandidate, now time.Time) error {
	switch c.Execution.PR.Mergeability {
	case domain.MergeMergeable, domain.MergeBlocked, domain.MergeUnstable:
	default:
		return nil
	}
	if c.Conflicting() {
		return nil
	}
	next := c.Resolve
	next.State, next.Attempts, next.DoneSeenAt, next.Error = domain.LadoResolveNone, 0, time.Time{}, ""
	if ok, err := s.writeResolve(ctx, next, now); err != nil || !ok {
		return err
	}
	s.appendAudit(ctx, s.audit("resolve.cleared", next.QueueEntryID, map[string]any{"task": next.TaskID, "execution": next.ExecutionSessionID}))
	return nil
}

// resolveRepo is where an execution's PR is read: the task's repository, the
// remote and the PR's target branch.
type resolveRepo struct {
	task       domain.LadoTask
	dir        string
	remote     string
	baseBranch string
}

func (s *Service) readResolveRepo(ctx context.Context, exec domain.LadoExecution) (resolveRepo, bool, error) {
	task, ok, err := s.deps.Store.LadoTask(ctx, exec.TaskID)
	if err != nil {
		return resolveRepo{}, false, fmt.Errorf("read task %s: %w", exec.TaskID, err)
	}
	if !ok {
		return resolveRepo{}, false, nil
	}
	dir, base, err := s.workerBase(ctx, task)
	if err != nil {
		return resolveRepo{}, false, fmt.Errorf("resolve the task's repository: %w", err)
	}
	repo := resolveRepo{task: task, dir: dir, remote: exec.BaseRemote, baseBranch: exec.PR.TargetBranch}
	if repo.remote == "" {
		repo.remote = base.Remote
	}
	if repo.remote == "" {
		repo.remote = defaultResolveRemote
	}
	if repo.baseBranch == "" {
		repo.baseBranch = exec.BaseBranch
	}
	if repo.baseBranch == "" {
		repo.baseBranch = base.Branch
	}
	return repo, true, nil
}

// startResolve starts a resolve worker for a PR no session works on, within
// the worker cap and while memory, the agent and the disk allow. It first
// reads the PR's source and target branches on the remote: a source branch
// AO has not observed at its tip yet, or one that already contains the
// target, waits.
func (s *Service) startResolve(ctx context.Context, c domain.LadoResolveCandidate, round *resolveRound) error {
	exec, now := c.Execution, round.now
	if round.load.Workers >= round.settings.MaxWorkers || round.tight || s.resolveWaits(exec.SessionID, now) {
		return nil
	}
	repo, ok, err := s.readResolveRepo(ctx, exec)
	if err != nil || !ok {
		return err
	}
	task := repo.task
	if unwanted(task, exec.PlanApprovalID) != "" {
		// The task's sweep stops its run; nothing resolves it.
		return nil
	}
	in, err := s.deps.Tasks.RunInput(ctx, task)
	if err != nil {
		return fmt.Errorf("read the task's inputs: %w", err)
	}
	if in.Approval.ID != exec.PlanApprovalID {
		return nil
	}
	agent, err := snapshotAgent(in.Snapshot)
	if err != nil {
		return err
	}
	if hold := s.launchHold(ctx, task.ID, agent.Harness, now, round.holds); hold.reason != "" {
		return nil
	}
	if s.diskLow(round.settings, repo.dir) {
		return nil
	}
	headTip, baseTip, ready := s.resolveTips(ctx, c, repo, now)
	if !ready {
		return nil
	}
	checks, _, err := s.deps.Store.LadoProjectChecks(ctx, task.ProjectID)
	if err != nil {
		return err
	}
	r := c.Resolve
	next := r
	next.ExecutionSessionID, next.TaskID, next.QueueEntryID = exec.SessionID, exec.TaskID, exec.QueueEntryID
	next.StartHeadSHA, next.BaseTipSHA, next.NotifiedHeadSHA, next.DoneSeenAt = headTip, baseTip, "", time.Time{}
	if ok, err := s.writeResolve(ctx, next, now); err != nil || !ok {
		return err
	}
	next.Version = r.Version + 1
	request, held, err := s.deps.Store.SetLadoResolveSpawnRequest(ctx, exec.SessionID, now, s.leaseEnd(now))
	if err != nil || !held {
		return err
	}
	req, _ := domain.ParseLadoRunClientRequestID(request)
	branch := domain.LadoResolveBranch(task.ID, exec.QueueEntryID, req.Attempt)
	if err := s.deps.PartGit.CreateBranch(ctx, repo.dir, branch, headTip); err != nil {
		s.deps.Log.Warn("LADO could not make a resolve worker's branch", "task", task.ID, "branch", branch, "err", err)
		if _, clearErr := s.deps.Store.ClearLadoResolveSpawnRequest(context.WithoutCancel(ctx), exec.SessionID, request); clearErr != nil {
			return clearErr
		}
		return s.resolveFailed(ctx, c, next, false, now, "the resolve worker's branch could not be made")
	}
	cfg := ports.SpawnConfig{
		ProjectID:         task.ProjectID,
		IssueID:           task.IssueRef,
		ClientRequestID:   request,
		ClientRequestHash: domain.LadoDispatchHash(in.Revision.SHA256, in.Snapshot.SHA256),
		IssueContext:      in.IssueContext,
		Kind:              domain.KindWorker,
		Harness:           agent.Harness,
		Prompt:            resolvePrompt(task, in, checks, c, repo, branch, headTip, baseTip),
		AgentConfig:       ports.AgentConfig{Model: agent.Model, Permissions: agent.PermissionMode},
		RequestedMode:     domain.SessionModeChat,
		DisplayName:       displayName("[resolve] " + task.Issue.Title),
		Branch:            branch,
	}
	session, _, _, err := s.deps.Sessions.Spawn(ctx, cfg)
	id := session.ID
	if err != nil {
		rec, found, lookupErr := s.deps.Store.GetSessionByClientRequestID(context.WithoutCancel(ctx), request)
		if lookupErr != nil || !found || !rec.ClientRequestCommitted || !startedFor(rec, task.ProjectID, cfg.ClientRequestHash) {
			message := problem(err, "the resolve worker could not start")
			s.deps.Log.Warn("LADO could not start a resolve worker", "task", task.ID, "request", request, "problem", message)
			if found && !rec.IsTerminated {
				// A half-started session is stopped before its grant goes;
				// if it cannot be, the request stays and recovery settles it
				// once it expired.
				if _, killErr := s.deps.Sessions.RequestKill(context.WithoutCancel(ctx), rec.ID); killErr != nil {
					return fmt.Errorf("stop the half-started resolve worker %s: %s", rec.ID, problem(killErr, "the session could not be stopped"))
				}
			}
			if _, clearErr := s.deps.Store.ClearLadoResolveSpawnRequest(context.WithoutCancel(ctx), exec.SessionID, request); clearErr != nil {
				return clearErr
			}
			return s.resolveFailed(ctx, c, next, false, now, "the resolve worker could not start")
		}
		id = rec.ID
	}
	if err := s.linkResolve(ctx, request, exec, id, s.deps.Clock().UTC(), branch, headTip, round.settings); err != nil {
		return err
	}
	round.load.Workers++
	s.forgetResolve(exec.SessionID)
	s.appendAudit(ctx, s.audit("resolve.dispatched", exec.QueueEntryID, map[string]any{
		"task": task.ID, "execution": exec.SessionID, "session": id, "attempt": req.Attempt, "branch": branch,
		"head": headTip, "base": baseTip, "pr": exec.PR.URL,
	}))
	return nil
}

// resolveTips reads the PR's source branch and target branch tips on the
// remote. It reports false, and looks again a minute later, while they
// cannot be read, while AO's PR observation is behind the source branch, and
// while the source branch already contains the target.
func (s *Service) resolveTips(ctx context.Context, c domain.LadoResolveCandidate, repo resolveRepo, now time.Time) (string, string, bool) {
	exec := c.Execution
	wait := func(why string, err error) (string, string, bool) {
		s.resolveLater(exec.SessionID, now)
		if err != nil {
			s.deps.Log.Warn("LADO could not read a PR's branches to resolve it", "task", exec.TaskID, "execution", exec.SessionID, "problem", why, "err", err)
		} else {
			s.deps.Log.Debug("LADO waits to resolve a PR", "task", exec.TaskID, "execution", exec.SessionID, "reason", why)
		}
		return "", "", false
	}
	if c.PRSourceBranch == "" || repo.baseBranch == "" {
		return wait("the PR's branches are unknown", nil)
	}
	headTip, err := s.deps.Remote.FetchBranch(ctx, repo.dir, repo.remote, c.PRSourceBranch)
	if err != nil {
		return wait("the PR's source branch could not be fetched", err)
	}
	if headTip != exec.PR.HeadSHA {
		return wait("AO has not observed the PR's head yet", nil)
	}
	baseTip, err := s.deps.Remote.FetchBranch(ctx, repo.dir, repo.remote, repo.baseBranch)
	if err != nil {
		return wait("the PR's target branch could not be fetched", err)
	}
	contains, err := s.deps.PartGit.IsAncestor(ctx, repo.dir, baseTip, headTip)
	if err != nil {
		return wait("the branches could not be compared", err)
	}
	if contains {
		return wait("the PR's head already contains its target", nil)
	}
	return headTip, baseTip, true
}

// linkResolve records session id as the resolve worker of request. A link
// that fails stops the session and ends the request; a session that cannot
// be stopped keeps the request, which recovery settles once it expired.
func (s *Service) linkResolve(ctx context.Context, request string, exec domain.LadoExecution, id domain.SessionID, started time.Time,
	branch, startSHA string, settings domain.LadoDispatchSettings) error {
	ctx = context.WithoutCancel(ctx)
	link := domain.LadoExecution{
		SessionID: id, TaskID: exec.TaskID, QueueEntryID: exec.QueueEntryID,
		Role: domain.LadoRoleMerge, Branch: branch, StartSHA: startSHA, StartedAt: started,
	}
	if settings.MaxRunMinutes > 0 {
		link.DeadlineAt = started.Add(time.Duration(settings.MaxRunMinutes) * time.Minute)
	}
	err := s.deps.Store.LinkLadoResolveExecution(ctx, request, link)
	if err == nil {
		s.deps.Log.Info("LADO started a resolve worker", "task", exec.TaskID, "execution", exec.SessionID, "session", id, "branch", branch)
		return nil
	}
	s.deps.Log.Warn("LADO started a resolve worker but could not link it, so it stops it", "task", exec.TaskID, "session", id, "err", err)
	if _, killErr := s.deps.Sessions.RequestKill(ctx, id); killErr != nil {
		return fmt.Errorf("link session %s: %w; stop it: %s", id, err, problem(killErr, "the session could not be stopped"))
	}
	if _, clearErr := s.deps.Store.ClearLadoResolveSpawnRequest(ctx, exec.SessionID, request); clearErr != nil {
		return errors.Join(fmt.Errorf("link session %s: %w", id, err), clearErr)
	}
	if errors.Is(err, domain.ErrLadoLeaseLost) {
		return nil
	}
	return fmt.Errorf("link session %s: %w", id, err)
}

// followResolve follows a running resolve: a worker that asked for a
// decision leaves the PR to a person; once it reported done, or ended
// without, the PR's source branch on the remote tells whether it resolved
// the PR. After done LADO waits LadoResolveDoneGrace for the push to show
// before it counts the attempt failed. A worker still at work is told once
// about each new conflicting head.
func (s *Service) followResolve(ctx context.Context, c domain.LadoResolveCandidate, now time.Time) error {
	r, exec := c.Resolve, c.Execution
	if r.SessionID == "" {
		return s.resolveFailed(ctx, c, r, false, now, "the resolve worker is unknown")
	}
	state, note, found, err := s.deps.Store.LatestSessionReport(ctx, r.SessionID)
	if err != nil {
		return fmt.Errorf("read the reports of session %s: %w", r.SessionID, err)
	}
	if found && state == domain.ReportNeedsInput {
		return s.resolveNeedsHuman(ctx, c, now, "the resolve worker asked for a decision: "+oneLine(note))
	}
	done := found && state == domain.ReportDone
	if !done && c.ResolveSessionLive {
		return s.notifyResolver(ctx, c, now)
	}
	if done && r.DoneSeenAt.IsZero() {
		next := r
		next.DoneSeenAt = now
		if ok, err := s.writeResolve(ctx, next, now); err != nil || !ok {
			return err
		}
		next.Version++
		r = next
	}
	inGrace := done && now.Before(r.DoneSeenAt.Add(domain.LadoResolveDoneGrace))
	if inGrace && s.resolveWaits(exec.SessionID, now) {
		return nil
	}
	resolved, readErr := s.resolvedRemotely(ctx, c)
	if readErr == nil && resolved {
		return s.resolveSucceeded(ctx, c, r, now)
	}
	if inGrace {
		// The push may not show yet: look again later, without a failure.
		s.resolveLater(exec.SessionID, now)
		return nil
	}
	message := "the resolve worker ended without reporting done"
	if done {
		message = "the resolve worker reported done, but the PR's branch did not take in the base"
	}
	if readErr != nil {
		message += " (" + problem(readErr, "the PR's branch could not be read") + ")"
	}
	return s.resolveFailed(ctx, c, r, c.ResolveSessionLive, now, message)
}

// resolvedRemotely reads the PR's source branch on the remote: the worker
// resolved the PR when the branch moved from the head it started at and
// contains the base tip of then.
func (s *Service) resolvedRemotely(ctx context.Context, c domain.LadoResolveCandidate) (bool, error) {
	r := c.Resolve
	if !commitID.MatchString(r.StartHeadSHA) || !commitID.MatchString(r.BaseTipSHA) || c.PRSourceBranch == "" {
		return false, errors.New("the resolve's start is unknown")
	}
	repo, ok, err := s.readResolveRepo(ctx, c.Execution)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("task %s not found", c.Execution.TaskID)
	}
	tip, err := s.deps.Remote.FetchBranch(ctx, repo.dir, repo.remote, c.PRSourceBranch)
	if err != nil {
		return false, err
	}
	if tip == r.StartHeadSHA {
		return false, nil
	}
	return s.deps.PartGit.IsAncestor(ctx, repo.dir, r.BaseTipSHA, tip)
}

// notifyResolver tells a running resolve worker, once per head, that the
// PR's new head conflicts again (the base moved while it worked). A new head
// whose record turned base_overlap without a conflict is not told here: the
// verification loop sends its fix request, with the files, to the PR's
// owner, which is this worker while the run's own session does not live.
func (s *Service) notifyResolver(ctx context.Context, c domain.LadoResolveCandidate, now time.Time) error {
	r, pr := c.Resolve, c.Execution.PR
	if s.deps.Sender == nil || pr.Mergeability != domain.MergeConflicting || !c.Conflicting() ||
		pr.HeadSHA == "" || pr.HeadSHA == r.StartHeadSHA || pr.HeadSHA == r.NotifiedHeadSHA {
		return nil
	}
	message := fmt.Sprintf("The PR %s conflicts with its base branch again at its new head %s: the base moved while you worked. "+
		"Fetch the base, merge it into your branch again, run the checks and push to the PR's source branch `%s` without force, then run `ao report --done` again.",
		pr.URL, shortSHA(pr.HeadSHA), c.PRSourceBranch)
	if err := s.deps.Sender.Send(ctx, r.SessionID, message); err != nil {
		s.deps.Log.Warn("LADO could not tell a resolve worker about a new conflict", "task", r.TaskID, "session", r.SessionID, "err", err)
		return nil
	}
	next := r
	next.NotifiedHeadSHA = pr.HeadSHA
	_, err := s.writeResolve(ctx, next, now)
	return err
}

// resolveSucceeded records a resolved PR: its count starts again, and the
// worker, done, is stopped.
func (s *Service) resolveSucceeded(ctx context.Context, c domain.LadoResolveCandidate, r domain.LadoPRResolve, now time.Time) error {
	next := r
	next.State, next.Attempts, next.DoneSeenAt, next.Error = domain.LadoResolveNone, 0, time.Time{}, ""
	if ok, err := s.writeResolve(ctx, next, now); err != nil || !ok {
		return err
	}
	s.forgetResolve(c.Execution.SessionID)
	if c.ResolveSessionLive {
		s.stopFinished(ctx, r.TaskID, r.SessionID)
	}
	s.deps.Log.Info("LADO's resolve worker resolved a PR", "task", r.TaskID, "execution", r.ExecutionSessionID, "session", r.SessionID)
	s.appendAudit(ctx, s.audit("resolve.succeeded", r.QueueEntryID, map[string]any{"task": r.TaskID, "execution": r.ExecutionSessionID, "session": r.SessionID}))
	return nil
}

// resolveFailed counts a failed attempt: the PR waits for the next resolve
// worker, or, after LadoMaxResolveAttempts failures in a row, for a person.
// stop stops the attempt's worker, still running.
func (s *Service) resolveFailed(ctx context.Context, c domain.LadoResolveCandidate, r domain.LadoPRResolve, stop bool, now time.Time, message string) error {
	next := r
	next.Attempts, next.DoneSeenAt, next.Error = r.Attempts+1, time.Time{}, message
	next.State = domain.LadoResolveNone
	if next.Attempts >= domain.LadoMaxResolveAttempts {
		next.State = domain.LadoResolveNeedsHuman
	}
	if ok, err := s.writeResolve(ctx, next, now); err != nil || !ok {
		return err
	}
	s.forgetResolve(c.Execution.SessionID)
	if stop && r.SessionID != "" {
		s.stopFinished(ctx, r.TaskID, r.SessionID)
	}
	s.deps.Log.Warn("LADO's resolve attempt failed", "task", r.TaskID, "execution", r.ExecutionSessionID, "session", r.SessionID,
		"attempts", next.Attempts, "problem", message)
	s.appendAudit(ctx, s.audit("resolve.failed", r.QueueEntryID, map[string]any{
		"task": r.TaskID, "execution": r.ExecutionSessionID, "session": r.SessionID, "attempts": next.Attempts, "error": message,
	}))
	if next.State == domain.LadoResolveNeedsHuman {
		s.raiseResolveAlert(ctx, next, now)
	}
	return nil
}

// resolveNeedsHuman leaves the PR to a person at once: the worker asked for
// a decision. The worker keeps running, so the person can answer it.
func (s *Service) resolveNeedsHuman(ctx context.Context, c domain.LadoResolveCandidate, now time.Time, message string) error {
	next := c.Resolve
	next.State, next.DoneSeenAt, next.Error = domain.LadoResolveNeedsHuman, time.Time{}, message
	if ok, err := s.writeResolve(ctx, next, now); err != nil || !ok {
		return err
	}
	s.forgetResolve(c.Execution.SessionID)
	s.appendAudit(ctx, s.audit("resolve.needs_human", next.QueueEntryID, map[string]any{
		"task": next.TaskID, "execution": next.ExecutionSessionID, "session": next.SessionID, "error": message,
	}))
	s.raiseResolveAlert(ctx, next, now)
	return nil
}

// raiseResolveAlert raises the alert of a PR LADO could not resolve, once
// for the execution.
func (s *Service) raiseResolveAlert(ctx context.Context, r domain.LadoPRResolve, now time.Time) {
	if _, _, err := s.deps.Store.AddLadoAlert(context.WithoutCancel(ctx), domain.LadoAlert{
		TaskID: r.TaskID, SessionID: r.ExecutionSessionID, Kind: domain.LadoAlertConflictUnresolved,
		Level: domain.LadoAlertLevelCrossed, Value: int64(r.Attempts), CreatedAt: now,
	}); err != nil {
		s.deps.Log.Warn("LADO could not raise an alert", "task", r.TaskID, "kind", domain.LadoAlertConflictUnresolved, "err", err)
	}
}

// writeResolve writes r over the version it was read at. It reports false
// when the row changed meanwhile; the next pass reads it again.
func (s *Service) writeResolve(ctx context.Context, r domain.LadoPRResolve, now time.Time) (bool, error) {
	r.UpdatedAt = now
	ok, err := s.deps.Store.UpsertLadoPRResolve(context.WithoutCancel(ctx), r)
	if err != nil {
		return false, fmt.Errorf("write the resolve of execution %s: %w", r.ExecutionSessionID, err)
	}
	if !ok {
		s.deps.Log.Debug("LADO's resolve changed meanwhile", "execution", r.ExecutionSessionID)
	}
	return ok, nil
}

// resolveWaits reports whether the resolver leaves an execution's PR alone
// at now after an earlier look.
func (s *Service) resolveWaits(id domain.SessionID, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.resolveRecheck[id]
	if ok && now.Before(at) {
		return true
	}
	delete(s.resolveRecheck, id)
	return false
}

func (s *Service) resolveLater(id domain.SessionID, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolveRecheck[id] = now.Add(resolveRecheckAfter)
}

func (s *Service) forgetResolve(id domain.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.resolveRecheck, id)
}

// recoverResolveSpawns settles the resolve worker starts a stopped pass left:
// once a start's grant expired, a session AO finished starting for it is
// linked, one it never finished is stopped, and the grant is cleared.
func (s *Service) recoverResolveSpawns(ctx context.Context, now time.Time, settings *domain.LadoDispatchSettings) error {
	rows, err := s.deps.Store.ListExpiredLadoResolveSpawns(ctx, now)
	if err != nil {
		return fmt.Errorf("list the resolve worker starts: %w", err)
	}
	var problems []error
	for _, row := range rows {
		if err := s.recoverResolveSpawn(ctx, row, settings); err != nil {
			problems = append(problems, fmt.Errorf("recover the start %s: %w", row.SpawnRequest, err))
		}
	}
	return errors.Join(problems...)
}

func (s *Service) recoverResolveSpawn(ctx context.Context, row domain.LadoPRResolve, settings *domain.LadoDispatchSettings) error {
	ctx = context.WithoutCancel(ctx)
	request := row.SpawnRequest
	req, ok := domain.ParseLadoRunClientRequestID(request)
	rec, found, err := s.deps.Store.GetSessionByClientRequestID(ctx, request)
	if err != nil {
		return err
	}
	if ok && req.Resolve() && found && rec.ClientRequestCommitted && settings != nil {
		grant, err := s.deps.Store.LadoPartSpawnGrant(ctx, request, row.SpawnExpiresAt.Add(-time.Nanosecond))
		if err != nil {
			return err
		}
		if startedFor(rec, grant.ProjectID, grant.Hash) {
			s.deps.Log.Info("LADO links a resolve worker a stopped pass had started", "task", row.TaskID, "session", rec.ID, "request", request)
			exec := domain.LadoExecution{SessionID: row.ExecutionSessionID, TaskID: row.TaskID, QueueEntryID: row.QueueEntryID}
			branch := domain.LadoResolveBranch(row.TaskID, row.QueueEntryID, req.Attempt)
			if err := s.linkResolve(ctx, request, exec, rec.ID, rec.CreatedAt.UTC(), branch, row.StartHeadSHA, *settings); err != nil {
				return err
			}
			s.appendAudit(ctx, s.audit("resolve.dispatched", row.QueueEntryID, map[string]any{
				"task": row.TaskID, "execution": row.ExecutionSessionID, "session": rec.ID, "attempt": req.Attempt, "branch": branch, "recovered": true,
			}))
			return nil
		}
	}
	if found && !rec.IsTerminated {
		if _, err := s.deps.Sessions.RequestKill(ctx, rec.ID); err != nil {
			return fmt.Errorf("stop session %s: %s", rec.ID, problem(err, "the session could not be stopped"))
		}
	}
	_, err = s.deps.Store.ClearLadoResolveSpawnRequest(ctx, row.ExecutionSessionID, request)
	return err
}

// resolvePrompt is the task of a resolve worker: merge the PR's target into
// its branch, keep both sides, run the checks and push to the PR's source
// branch without force.
func resolvePrompt(task domain.LadoTask, in ladotask.RunInput, checks []domain.LadoCheckCommand, c domain.LadoResolveCandidate,
	repo resolveRepo, branch, headTip, baseTip string) string {
	pr := c.Execution.PR
	why := fmt.Sprintf("conflicts with its base branch `%s`", repo.baseBranch)
	if pr.Mergeability != domain.MergeConflicting {
		why = fmt.Sprintf("is behind its base branch `%s`, which merged changes to the same paths since the PR's branch was made", repo.baseBranch)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are resolving the pull request of a plan the developer approved in LADO for %s: %s.\n", task.IssueRef, oneLine(task.Issue.Title))
	fmt.Fprintf(&b, "The pull request %s (source branch `%s`) %s. The worker that opened it no longer runs, so LADO started you to take the base in.\n\n", pr.URL, c.PRSourceBranch, why)
	b.WriteString("## Your task\n\n")
	fmt.Fprintf(&b, "- You work on branch `%s`, which starts at the pull request's head `%s`.\n", branch, headTip)
	fmt.Fprintf(&b, "- Fetch the base and merge it: `git fetch %s %s` and `git merge --no-ff %s/%s`. The merge must contain the base commit `%s`. Write the merge commit's title in Korean.\n",
		repo.remote, repo.baseBranch, repo.remote, repo.baseBranch, baseTip)
	b.WriteString("- Resolve every conflict so that both sides keep what they did: the plan's change must still be complete after the merge, and the base's changes must stay. Do not drop either side's change to make a conflict go away. Where both sides changed the same paths without a text conflict, check that the result still works.\n")
	b.WriteString("- Run the build and the tests that cover the merged files, and fix what the merge broke, following the repository's commit rules.\n")
	if len(checks) > 0 {
		names := make([]string, 0, len(checks))
		for _, check := range checks {
			names = append(names, fmt.Sprintf("%s (`%s`)", check.Name, check.Command))
		}
		fmt.Fprintf(&b, "- Run the project's checks before you push: %s.\n", joinAnd(names))
	}
	fmt.Fprintf(&b, "- Push to the pull request's source branch without force: `git push %s HEAD:%s`. Never force-push. If the push is rejected, fetch `%s/%s`, merge it as well and push again.\n",
		repo.remote, c.PRSourceBranch, repo.remote, c.PRSourceBranch)
	b.WriteString("- Do not open, close or claim a pull request, and do not do any other step of the plan.\n")
	b.WriteString("- When the push went through, run `ao report --done --note \"<one paragraph: the conflicts and how you resolved them, the checks you ran>\"`. LADO then reads the pull request's branch to confirm it.\n")
	b.WriteString("- If resolving needs a decision the plan does not make, run `ao report --needs-input --note \"<the decision>\"` and wait.\n\n")
	b.WriteString(planSection(in))
	return b.String()
}
