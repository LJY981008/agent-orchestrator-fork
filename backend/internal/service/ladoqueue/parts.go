package ladoqueue

// Parallel parts (docs/LJY-PRD/TDD.md §5.4): a plan that splits its steps into
// parts runs one fresh worker session per part on its own branch, each
// started from the integration branch's tip once the parts it depends on
// are merged there and no running part of the task may touch its files.
// When a part's worker reports done, LADO merges its branch into the
// integration branch; a merge that conflicts goes to a fresh merge worker.
// Once every part is merged, a fresh integration worker reviews the merged
// result, does the steps no part took and opens the task's pull request. No
// worker is ever resumed or reused: a retry is a new session.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

// startParts starts the parts run of a claimed entry whose approved plan has
// parts: it makes the integration branch at the dispatch's base commit and
// records the run and its waiting parts, which frees the entry's lease. The
// parts' workers start in the parts pass. It reports whether the run started,
// which takes the task's slot.
func (s *Service) startParts(ctx context.Context, entry domain.LadoQueueEntry, task domain.LadoTask, in ladotask.RunInput, plan domain.LadoPlan,
	dir string, base ports.WorkspaceDefaultBranch, dispatchSHA string, ahead *int, localSHA string, now time.Time) (bool, error) {
	if !commitID.MatchString(dispatchSHA) {
		// Without the base commit there is nothing to branch from: try again.
		return false, s.attemptFailed(ctx, entry, "the base commit could not be read")
	}
	branch := domain.LadoIntegrationBranch(task.ID)
	if err := s.deps.PartGit.CreateBranch(ctx, dir, branch, dispatchSHA); err != nil {
		s.deps.Log.Warn("LADO could not make a task's integration branch", "entry", entry.ID, "task", task.ID, "branch", branch, "err", err)
		return false, s.attemptFailed(ctx, entry, "the integration branch could not be made")
	}
	at := s.deps.Clock().UTC()
	run := domain.LadoPartRun{
		QueueEntryID: entry.ID, TaskID: task.ID, ProjectID: task.ProjectID, RepoDir: dir,
		Branch: branch, BaseSHA: dispatchSHA, HeadSHA: dispatchSHA,
		PlanRevisionID: in.Revision.ID, PlanBaseSHA: in.Revision.BaseSHA, BaseCommitsAhead: ahead,
		BaseBranch: base.Branch, BaseRemote: base.Remote, DispatchLocalBaseSHA: localSHA, StartedAt: at,
	}
	phases := domain.LadoPartPhases(plan.Parts)
	parts := make([]domain.LadoTaskPart, 0, len(plan.Parts))
	for i, p := range plan.Parts {
		waiting := ""
		if len(p.DependsOn) > 0 {
			waiting = domain.LadoWaitingPartDependency
		}
		parts = append(parts, domain.LadoTaskPart{
			PartID: p.ID, Title: p.Title, Position: i, Phase: phases[p.ID],
			Steps: p.Steps, AffectedPaths: p.AffectedPaths, DependsOn: p.DependsOn, WaitingReason: waiting,
		})
	}
	err := s.deps.Store.StartLadoPartRun(context.WithoutCancel(ctx), entry.LeaseToken, run, parts,
		s.audit("parts.started", entry.ID, map[string]any{"task": task.ID, "branch": branch, "base": dispatchSHA, "parts": len(parts)}))
	switch {
	case errors.Is(err, domain.ErrLadoLeaseLost):
		s.deps.Log.Warn("LADO lost the claim of a queue entry before starting its parts", "entry", entry.ID, "task", task.ID)
		return false, nil
	case err != nil:
		return false, fmt.Errorf("start the parts run: %w", err)
	}
	s.deps.Log.Info("LADO started a task's parts run", "entry", entry.ID, "task", task.ID, "branch", branch, "parts", len(parts))
	return true, nil
}

// audit builds an audit event of the dispatcher about a queue entry.
func (s *Service) audit(action string, entry domain.LadoQueueEntryID, payload map[string]any) domain.LadoAuditEvent {
	data, _ := json.Marshal(payload)
	return domain.LadoAuditEvent{
		At: s.deps.Clock().UTC(), Actor: "lado", Action: action,
		SubjectKind: "queue_entry", SubjectID: string(entry), Payload: data,
	}
}

// partsPass moves every parts run in progress on, oldest approval first:
// parts whose workers reported done are merged, workers that ended without
// done are retried, and the parts and integration workers that can start
// do, within the worker cap. load is what runs now; each start adds to it.
func (s *Service) partsPass(ctx context.Context, now time.Time, settings domain.LadoDispatchSettings) error {
	runs, err := s.deps.Store.ActiveLadoPartRuns(ctx)
	if err != nil {
		return fmt.Errorf("list the parts runs: %w", err)
	}
	if len(runs) == 0 {
		return nil
	}
	load, err := s.deps.Store.LadoRunLoad(ctx, now)
	if err != nil {
		return err
	}
	tight := settings.HoldOnMemoryTight && s.memoryTight(ctx)
	var problems []error
	for _, active := range runs {
		if ctx.Err() != nil {
			break
		}
		if err := s.advanceRun(ctx, active, settings, &load, tight, now); err != nil {
			problems = append(problems, fmt.Errorf("parts run %s: %w", active.Entry.ID, err))
		}
	}
	return errors.Join(problems...)
}

// partRun is what one pass knows of a parts run.
type partRun struct {
	run   domain.LadoPartRun
	entry domain.LadoQueueEntry
	task  domain.LadoTask
	parts []domain.LadoTaskPart
	execs map[domain.SessionID]domain.LadoExecution
	// in is read once, when a worker starts.
	in *ladotask.RunInput
}

func (s *Service) advanceRun(ctx context.Context, active domain.LadoActivePartRun, settings domain.LadoDispatchSettings, load *domain.LadoRunLoad, tight bool, now time.Time) error {
	task, ok, err := s.deps.Store.LadoTask(ctx, active.Entry.TaskID)
	if err != nil || !ok {
		return fmt.Errorf("read task %s: %v", active.Entry.TaskID, err)
	}
	if reason := unwanted(task, active.Entry.PlanApprovalID); reason != "" {
		// The pass that stops unwanted runs (stopUnwanted) stops its workers.
		_, err := s.deps.Store.EndLadoPartRun(ctx, active.Entry.ID, reason == stopApprovalSuperseded, s.deps.Clock().UTC())
		return err
	}
	if active.Entry.PartSpawnRequest != "" {
		// A start is in progress or left by a stopped pass; recovery settles
		// it first.
		return nil
	}
	parts, err := s.deps.Store.LadoTaskParts(ctx, active.Entry.ID)
	if err != nil {
		return err
	}
	execs, err := s.deps.Store.LadoExecutionsForEntry(ctx, active.Entry.ID)
	if err != nil {
		return err
	}
	r := &partRun{run: active.Run, entry: active.Entry, task: task, parts: parts, execs: map[domain.SessionID]domain.LadoExecution{}}
	for _, exec := range execs {
		r.execs[exec.SessionID] = exec
	}
	for i := range r.parts {
		if err := s.followPart(ctx, r, i); err != nil {
			return err
		}
		if !r.entry.FailedAt.IsZero() {
			return nil
		}
	}
	return s.startReady(ctx, r, settings, load, tight, now)
}

// followPart moves one part on from what its workers did.
func (s *Service) followPart(ctx context.Context, r *partRun, i int) error {
	part := &r.parts[i]
	switch part.State {
	case domain.LadoPartRunning:
		done, note, ended, err := s.workerOutcome(ctx, r, part.SessionID)
		if err != nil || (!done && !ended) {
			return err
		}
		if !done {
			return s.partAttemptFailed(ctx, r, i, "the part's worker ended without reporting done")
		}
		s.stopFinished(ctx, r.task.ID, part.SessionID)
		next := *part
		next.State, next.Summary, next.WaitingReason, next.WaitingDetail = domain.LadoPartDone, note, "", ""
		if err := s.writePart(ctx, part, next); err != nil {
			return err
		}
		return s.mergeDone(ctx, r, i)
	case domain.LadoPartDone:
		return s.mergeDone(ctx, r, i)
	case domain.LadoPartMerging:
		return s.finishMerge(ctx, r, i)
	case domain.LadoPartConflict:
		if part.MergeSessionID == "" {
			return nil
		}
		done, _, ended, err := s.workerOutcome(ctx, r, part.MergeSessionID)
		if err != nil || (!done && !ended) {
			return err
		}
		if !done {
			return s.mergeAttemptFailed(ctx, r, i, "the merge worker ended without reporting done")
		}
		s.stopFinished(ctx, r.task.ID, part.MergeSessionID)
		return s.fastForward(ctx, r, i)
	}
	return nil
}

// workerOutcome reads whether a worker reported done, with its note, and
// whether its session ended. A done report wins over an ended session: a
// worker that reported done and then stopped did its part.
func (s *Service) workerOutcome(ctx context.Context, r *partRun, session domain.SessionID) (bool, string, bool, error) {
	state, note, found, err := s.deps.Store.LatestSessionReport(ctx, session)
	if err != nil {
		return false, "", false, err
	}
	if found && state == domain.ReportDone {
		return true, note, true, nil
	}
	exec, ok := r.execs[session]
	return false, "", !ok || exec.SessionTerminated, nil
}

// stopFinished stops a part or merge worker that reported done, so that it
// frees its worker slot and adds nothing after its branch was read. A stop
// that failed is logged: the branch is read now either way.
func (s *Service) stopFinished(ctx context.Context, task domain.LadoTaskID, session domain.SessionID) {
	if _, err := s.deps.Sessions.RequestKill(ctx, session); err != nil {
		s.deps.Log.Warn("LADO could not stop a worker that reported done", "task", task, "session", session,
			"problem", problem(err, "the session could not be stopped"))
	}
}

// writePart writes next over part when the stored state is still part's.
func (s *Service) writePart(ctx context.Context, part *domain.LadoTaskPart, next domain.LadoTaskPart) error {
	next.UpdatedAt = s.deps.Clock().UTC()
	ok, err := s.deps.Store.UpdateLadoTaskPart(context.WithoutCancel(ctx), next, part.State)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("part %s changed under the pass", part.PartID)
	}
	*part = next
	return nil
}

// mergeDone merges a done part's branch into the integration branch. A part
// whose branch has no commit after its start is merged as it is. Otherwise
// LADO records the merge it is about to make, then makes it.
func (s *Service) mergeDone(ctx context.Context, r *partRun, i int) error {
	part := &r.parts[i]
	tip, err := s.deps.PartGit.BranchSHA(ctx, r.run.RepoDir, part.Branch)
	if err != nil {
		return err
	}
	if tip == "" {
		return s.partAttemptFailed(ctx, r, i, "the part's branch is gone")
	}
	if tip == part.StartSHA {
		next := *part
		next.State, next.MergedSHA = domain.LadoPartMerged, r.run.HeadSHA
		return s.recordMerged(ctx, r, i, next, domain.LadoPartDone, map[string]any{"empty": true})
	}
	next := *part
	next.State, next.MergeFromSHA, next.MergeTipSHA = domain.LadoPartMerging, r.run.HeadSHA, tip
	if err := s.writePart(ctx, part, next); err != nil {
		return err
	}
	return s.finishMerge(ctx, r, i)
}

// finishMerge makes or finds the merge a part records. While the integration
// branch is still where the merge started, LADO merges and moves it; when
// the branch already holds that merge, as after a restart between the two,
// the merge is recorded without being made again.
func (s *Service) finishMerge(ctx context.Context, r *partRun, i int) error {
	part := &r.parts[i]
	current, err := s.deps.PartGit.BranchSHA(ctx, r.run.RepoDir, r.run.Branch)
	if err != nil {
		return err
	}
	if current != part.MergeFromSHA {
		parents, err := s.deps.PartGit.Parents(ctx, r.run.RepoDir, current)
		if err == nil && len(parents) == 2 && parents[0] == part.MergeFromSHA && parents[1] == part.MergeTipSHA {
			next := *part
			next.State, next.MergedSHA = domain.LadoPartMerged, current
			return s.recordMerged(ctx, r, i, next, domain.LadoPartMerging, map[string]any{"recovered": true})
		}
		return s.failRun(ctx, r, part.PartID, "the integration branch moved outside LADO")
	}
	message := fmt.Sprintf("chore(lado): 파트 %s(%s)를 통합 브랜치에 병합", part.PartID, oneLine(part.Title))
	merged, err := s.deps.PartGit.Merge(ctx, r.run.RepoDir, part.MergeFromSHA, part.MergeTipSHA, message)
	if errors.Is(err, errMergeConflict) {
		next := *part
		next.State, next.MergeSessionID, next.MergeBranch = domain.LadoPartConflict, "", ""
		if err := s.writePart(ctx, part, next); err != nil {
			return err
		}
		s.deps.Log.Info("LADO found a merge conflict and hands it to a merge worker", "task", r.task.ID, "part", part.PartID)
		s.appendAudit(ctx, s.audit("part.conflict", r.entry.ID, map[string]any{"task": r.task.ID, "part": part.PartID, "head": part.MergeFromSHA, "tip": part.MergeTipSHA}))
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.deps.PartGit.MoveBranch(ctx, r.run.RepoDir, r.run.Branch, merged, part.MergeFromSHA); err != nil {
		return err
	}
	next := *part
	next.State, next.MergedSHA = domain.LadoPartMerged, merged
	return s.recordMerged(ctx, r, i, next, domain.LadoPartMerging, nil)
}

// fastForward moves the integration branch to a merge worker's branch, which
// must hold both the integration head and the part's tip. Anything else is a
// merge that is still to be done.
func (s *Service) fastForward(ctx context.Context, r *partRun, i int) error {
	part := &r.parts[i]
	tip, err := s.deps.PartGit.BranchSHA(ctx, r.run.RepoDir, part.MergeBranch)
	if err != nil {
		return err
	}
	current, err := s.deps.PartGit.BranchSHA(ctx, r.run.RepoDir, r.run.Branch)
	if err != nil {
		return err
	}
	ok := tip != ""
	if ok {
		if ok, err = s.deps.PartGit.IsAncestor(ctx, r.run.RepoDir, current, tip); err != nil {
			return err
		}
	}
	if ok {
		if ok, err = s.deps.PartGit.IsAncestor(ctx, r.run.RepoDir, part.MergeTipSHA, tip); err != nil {
			return err
		}
	}
	if !ok {
		return s.mergeAttemptFailed(ctx, r, i, "the merge worker's branch does not hold both the integration branch and the part")
	}
	if tip != current {
		if err := s.deps.PartGit.MoveBranch(ctx, r.run.RepoDir, r.run.Branch, tip, current); err != nil {
			return err
		}
	}
	next := *part
	next.State, next.MergedSHA = domain.LadoPartMerged, tip
	return s.recordMerged(ctx, r, i, next, domain.LadoPartConflict, map[string]any{"mergeWorker": part.MergeSessionID})
}

// recordMerged stores a part as merged and the integration head it left,
// with the part.merged audit event.
func (s *Service) recordMerged(ctx context.Context, r *partRun, i int, next domain.LadoTaskPart, old domain.LadoPartState, extra map[string]any) error {
	payload := map[string]any{"task": r.task.ID, "part": next.PartID, "head": next.MergedSHA}
	for k, v := range extra {
		payload[k] = v
	}
	next.UpdatedAt = s.deps.Clock().UTC()
	next.WaitingReason, next.WaitingDetail = "", ""
	ok, err := s.deps.Store.MergeLadoTaskPart(context.WithoutCancel(ctx), next, old, r.run.HeadSHA, s.audit("part.merged", r.entry.ID, payload))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("part %s changed under the pass", next.PartID)
	}
	r.parts[i] = next
	r.run.HeadSHA = next.MergedSHA
	s.deps.Log.Info("LADO merged a part into its task's integration branch", "task", r.task.ID, "part", next.PartID, "head", next.MergedSHA)
	return nil
}

// partAttemptFailed records a part worker that ended without its part done:
// the part waits for a new worker, or the run fails once the part spent its
// attempts.
func (s *Service) partAttemptFailed(ctx context.Context, r *partRun, i int, message string) error {
	part := &r.parts[i]
	if part.Attempts >= domain.LadoMaxPartAttempts {
		return s.failRun(ctx, r, part.PartID, message)
	}
	next := *part
	next.State, next.Error, next.WaitingReason, next.WaitingDetail = domain.LadoPartWaiting, message, "", ""
	s.deps.Log.Warn("LADO starts a part again in a new worker", "task", r.task.ID, "part", part.PartID, "attempt", part.Attempts, "problem", message)
	return s.writePart(ctx, part, next)
}

// mergeAttemptFailed records a merge worker that did not finish the merge:
// the part waits for a new merge worker, or the run fails once its merges
// spent their attempts.
func (s *Service) mergeAttemptFailed(ctx context.Context, r *partRun, i int, message string) error {
	part := &r.parts[i]
	if part.MergeAttempts >= domain.LadoMaxPartAttempts {
		return s.failRun(ctx, r, part.PartID, message)
	}
	next := *part
	next.MergeSessionID, next.Error = "", message
	return s.writePart(ctx, part, next)
}

// failRun fails the parts run because of part: the task shows Failed with
// attempts_exhausted, and the part shows why. Parts still running finish;
// no new worker starts.
func (s *Service) failRun(ctx context.Context, r *partRun, partID, message string) error {
	ctx = context.WithoutCancel(ctx)
	for i := range r.parts {
		if r.parts[i].PartID == partID && r.parts[i].State != domain.LadoPartFailed {
			next := r.parts[i]
			next.State, next.Error = domain.LadoPartFailed, message
			if err := s.writePart(ctx, &r.parts[i], next); err != nil {
				return err
			}
		}
	}
	text := "part " + partID + ": " + message
	failed, err := s.deps.Store.FailLadoPartRun(ctx, r.entry.ID, text, s.audit("part.failed", r.entry.ID, map[string]any{"task": r.task.ID, "part": partID, "reason": message}))
	if err != nil {
		return err
	}
	if failed {
		r.entry.FailedAt = s.deps.Clock().UTC()
		s.deps.Log.Warn("LADO gave up a task's parts run", "task", r.task.ID, "part", partID, "problem", message)
	}
	return nil
}

func (s *Service) appendAudit(ctx context.Context, ev domain.LadoAuditEvent) {
	if _, err := s.deps.Store.AppendLadoAuditEvent(context.WithoutCancel(ctx), ev); err != nil {
		s.deps.Log.Warn("LADO could not audit a parts run event", "action", ev.Action, "err", err)
	}
}

// startReady starts the workers the run can start now, in the plan's order:
// the integration worker once every part is merged; otherwise a merge worker
// for each conflict and a worker for each waiting part whose dependencies are
// merged and whose paths no active part of the task may touch. A start needs
// a free worker slot, memory and disk; what a part waits for is recorded on
// it.
func (s *Service) startReady(ctx context.Context, r *partRun, settings domain.LadoDispatchSettings, load *domain.LadoRunLoad, tight bool, now time.Time) error {
	if !r.entry.FailedAt.IsZero() {
		return nil
	}
	merged := 0
	for _, part := range r.parts {
		if part.State == domain.LadoPartMerged {
			merged++
		}
	}
	diskLow := s.diskLow(settings, r.run.RepoDir)
	if merged == len(r.parts) {
		if load.Workers >= settings.MaxWorkers || tight || diskLow {
			return nil
		}
		return s.startIntegration(ctx, r, settings, load)
	}
	ignore := domain.LadoDefaultOverlapIgnore
	if ps, err := s.deps.Store.LadoProjectSettings(ctx, r.run.ProjectID); err == nil {
		ignore = ps.OverlapIgnoreOrDefault()
	}
	var problems []error
	for i := range r.parts {
		part := &r.parts[i]
		if ctx.Err() != nil {
			break
		}
		reason, detail := "", ""
		switch {
		case part.State == domain.LadoPartConflict && part.MergeSessionID == "":
		case part.State == domain.LadoPartWaiting:
			if missing := r.unmerged(part.DependsOn); len(missing) > 0 {
				reason, detail = domain.LadoWaitingPartDependency, strings.Join(missing, ", ")
			} else if other, ok := r.overlapping(*part, ignore); ok {
				reason, detail = domain.LadoWaitingPartOverlap, other
			}
		default:
			continue
		}
		switch {
		case reason != "":
		case tight:
			reason = domain.LadoWaitingMemoryTight
		case diskLow:
			reason = domain.LadoWaitingDiskLow
		case load.Workers >= settings.MaxWorkers:
			reason = domain.LadoWaitingSlotWorkers
		}
		if reason != "" {
			if part.WaitingReason != reason || part.WaitingDetail != detail {
				next := *part
				next.WaitingReason, next.WaitingDetail = reason, detail
				if err := s.writePart(ctx, part, next); err != nil {
					problems = append(problems, err)
				}
			}
			continue
		}
		var err error
		if part.State == domain.LadoPartConflict {
			err = s.startMergeWorker(ctx, r, i, settings)
		} else {
			err = s.startPart(ctx, r, i, settings)
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("part %s: %w", part.PartID, err))
			continue
		}
		load.Workers++
		if !r.entry.FailedAt.IsZero() {
			break
		}
	}
	return errors.Join(problems...)
}

// unmerged returns the ids of the parts in ids that are not merged yet.
func (r *partRun) unmerged(ids []string) []string {
	var missing []string
	for _, id := range ids {
		for _, part := range r.parts {
			if part.PartID == id && part.State != domain.LadoPartMerged {
				missing = append(missing, id)
			}
		}
	}
	return missing
}

// overlapping returns an active part of the run whose paths may name a file
// part's paths name, leaving out the overlaps ignore allows.
func (r *partRun) overlapping(part domain.LadoTaskPart, ignore []string) (string, bool) {
	for _, other := range r.parts {
		if other.PartID == part.PartID || !other.State.Active() {
			continue
		}
		if _, _, ok := domain.LadoPathsOverlap(part.AffectedPaths, other.AffectedPaths, ignore); ok {
			return other.PartID, true
		}
	}
	return "", false
}

// input reads the run's approved inputs once a pass needs them.
func (s *Service) input(ctx context.Context, r *partRun) (ladotask.RunInput, error) {
	if r.in != nil {
		return *r.in, nil
	}
	in, err := s.deps.Tasks.RunInput(ctx, r.task)
	if err != nil {
		return ladotask.RunInput{}, err
	}
	if in.Approval.ID != r.entry.PlanApprovalID {
		return ladotask.RunInput{}, fmt.Errorf("the task's newest approval is not the run's")
	}
	r.in = &in
	return in, nil
}

// startPart starts a new worker for a waiting part, on a new branch made at
// the integration branch's tip.
func (s *Service) startPart(ctx context.Context, r *partRun, i int, settings domain.LadoDispatchSettings) error {
	part := &r.parts[i]
	attempt := part.Attempts + 1
	branch := domain.LadoPartBranch(r.task.ID, part.PartID, attempt)
	in, err := s.input(ctx, r)
	if err != nil {
		return err
	}
	next := *part
	next.State, next.Attempts, next.Branch, next.StartSHA = domain.LadoPartRunning, attempt, branch, r.run.HeadSHA
	next.WaitingReason, next.WaitingDetail, next.MergeFromSHA, next.MergeTipSHA = "", "", "", ""
	req := domain.LadoRunRequest{Entry: r.entry.ID, Role: domain.LadoRolePart, Part: part.PartID, Attempt: attempt}
	prompt := partPrompt(r.task, in, *part, r.parts, branch)
	started, err := s.startRunWorker(ctx, r, req, in, branch, r.run.HeadSHA, prompt, settings, &next, part.State)
	if err != nil || started {
		if started {
			*part = next
			s.appendAudit(ctx, s.audit("part.dispatched", r.entry.ID, map[string]any{"task": r.task.ID, "part": part.PartID, "attempt": attempt, "session": part.SessionID, "branch": branch}))
		}
		return err
	}
	// The worker did not start: the attempt is spent.
	failed := *part
	failed.Attempts = attempt
	if err := s.writePart(ctx, part, failed); err != nil {
		return err
	}
	return s.partAttemptFailed(ctx, r, i, "the part's worker could not start")
}

// startMergeWorker starts a new merge worker for a part whose merge
// conflicts, on a new branch made at the integration branch's tip.
func (s *Service) startMergeWorker(ctx context.Context, r *partRun, i int, settings domain.LadoDispatchSettings) error {
	part := &r.parts[i]
	attempt := part.MergeAttempts + 1
	branch := domain.LadoMergeBranch(r.task.ID, part.PartID, attempt)
	in, err := s.input(ctx, r)
	if err != nil {
		return err
	}
	next := *part
	next.MergeAttempts, next.MergeBranch, next.WaitingReason, next.WaitingDetail = attempt, branch, "", ""
	req := domain.LadoRunRequest{Entry: r.entry.ID, Role: domain.LadoRoleMerge, Part: part.PartID, Attempt: attempt}
	prompt := mergePrompt(r.task, in, *part, branch)
	started, err := s.startRunWorker(ctx, r, req, in, branch, r.run.HeadSHA, prompt, settings, &next, part.State)
	if err != nil || started {
		if started {
			*part = next
			s.appendAudit(ctx, s.audit("part.merge_dispatched", r.entry.ID, map[string]any{"task": r.task.ID, "part": part.PartID, "attempt": attempt, "session": part.MergeSessionID, "branch": branch}))
		}
		return err
	}
	failed := *part
	failed.MergeAttempts = attempt
	if err := s.writePart(ctx, part, failed); err != nil {
		return err
	}
	return s.mergeAttemptFailed(ctx, r, i, "the merge worker could not start")
}

// startIntegration starts the integration worker on the integration branch,
// which no worktree has checked out: LADO never checks it out, and every
// part and merge worker worked on its own branch.
func (s *Service) startIntegration(ctx context.Context, r *partRun, settings domain.LadoDispatchSettings, load *domain.LadoRunLoad) error {
	attempt := r.run.IntegrationAttempts + 1
	if attempt > domain.LadoMaxPartAttempts {
		return s.failRun(ctx, r, domain.LadoRoleIntegration, "the integration worker could not start")
	}
	in, err := s.input(ctx, r)
	if err != nil {
		return err
	}
	checks, _, err := s.deps.Store.LadoProjectChecks(ctx, r.task.ProjectID)
	if err != nil {
		return err
	}
	if ok, err := s.deps.Store.BumpLadoPartRunIntegrationAttempts(ctx, r.entry.ID, r.run.IntegrationAttempts); err != nil || !ok {
		return err
	}
	r.run.IntegrationAttempts = attempt
	req := domain.LadoRunRequest{Entry: r.entry.ID, Role: domain.LadoRoleIntegration, Attempt: attempt}
	prompt := integrationPrompt(r.task, in, checks, r.run, r.parts)
	started, err := s.startRunWorker(ctx, r, req, in, r.run.Branch, r.run.HeadSHA, prompt, settings, nil, "")
	if started {
		load.Workers++
		s.appendAudit(ctx, s.audit("integration.dispatched", r.entry.ID, map[string]any{"task": r.task.ID, "attempt": attempt, "branch": r.run.Branch, "head": r.run.HeadSHA}))
	}
	return err
}

// startRunWorker starts one fresh worker session of a parts run on branch,
// which it first makes at startSHA unless it is the integration branch. The
// entry's spawn request lets the worker gate grant that one spawn; the
// session is linked as an execution of the entry with its role, part and
// branch, and part, when given, is written with it. It reports whether a
// worker started; a start that failed spends the attempt.
func (s *Service) startRunWorker(ctx context.Context, r *partRun, req domain.LadoRunRequest, in ladotask.RunInput, branch, startSHA, prompt string,
	settings domain.LadoDispatchSettings, part *domain.LadoTaskPart, oldState domain.LadoPartState) (bool, error) {
	agent, err := snapshotAgent(in.Snapshot)
	if err != nil {
		return false, err
	}
	request := domain.LadoRunClientRequestID(req)
	if req.Role != domain.LadoRoleIntegration {
		if err := s.deps.PartGit.CreateBranch(ctx, r.run.RepoDir, branch, startSHA); err != nil {
			return false, err
		}
	}
	held, err := s.deps.Store.SetLadoPartSpawnRequest(ctx, r.entry.ID, request, s.leaseEnd(s.deps.Clock().UTC()))
	if err != nil || !held {
		return false, err
	}
	cfg := ports.SpawnConfig{
		ProjectID:         r.task.ProjectID,
		IssueID:           r.task.IssueRef,
		ClientRequestID:   request,
		ClientRequestHash: domain.LadoDispatchHash(in.Revision.SHA256, in.Snapshot.SHA256),
		IssueContext:      in.IssueContext,
		Kind:              domain.KindWorker,
		Harness:           agent.Harness,
		Prompt:            prompt,
		AgentConfig:       ports.AgentConfig{Model: agent.Model, Permissions: agent.PermissionMode},
		RequestedMode:     domain.SessionModeChat,
		DisplayName:       displayName(roleLabel(req) + " " + r.task.Issue.Title),
		Branch:            branch,
	}
	session, _, _, err := s.deps.Sessions.Spawn(ctx, cfg)
	id := session.ID
	if err != nil {
		rec, found, lookupErr := s.deps.Store.GetSessionByClientRequestID(context.WithoutCancel(ctx), request)
		if lookupErr != nil || !found || !rec.ClientRequestCommitted || !startedFor(rec, r.task.ProjectID, cfg.ClientRequestHash) {
			s.deps.Log.Warn("LADO could not start a parts run worker", "task", r.task.ID, "request", request,
				"problem", problem(err, "the worker session could not start"))
			if _, clearErr := s.deps.Store.ClearLadoPartSpawnRequest(context.WithoutCancel(ctx), r.entry.ID, request); clearErr != nil {
				return false, clearErr
			}
			return false, nil
		}
		id = rec.ID
	}
	return true, s.linkRunWorker(ctx, r, req, request, id, s.deps.Clock().UTC(), branch, startSHA, settings, part, oldState)
}

// linkRunWorker records session id as the execution of a parts run worker.
func (s *Service) linkRunWorker(ctx context.Context, r *partRun, req domain.LadoRunRequest, request string, id domain.SessionID, started time.Time,
	branch, startSHA string, settings domain.LadoDispatchSettings, part *domain.LadoTaskPart, oldState domain.LadoPartState) error {
	ctx = context.WithoutCancel(ctx)
	exec := domain.LadoExecution{
		SessionID: id, TaskID: r.task.ID, QueueEntryID: r.entry.ID, Attempt: req.Attempt,
		Role: req.Role, PartID: req.Part, Branch: branch, StartSHA: startSHA, StartedAt: started,
	}
	if req.Role == domain.LadoRoleIntegration {
		exec.DispatchBaseSHA, exec.BaseCommitsAhead = r.run.BaseSHA, r.run.BaseCommitsAhead
		exec.BaseBranch, exec.BaseRemote, exec.DispatchLocalBaseSHA = r.run.BaseBranch, r.run.BaseRemote, r.run.DispatchLocalBaseSHA
	}
	if settings.MaxRunMinutes > 0 {
		exec.DeadlineAt = started.Add(time.Duration(settings.MaxRunMinutes) * time.Minute)
	}
	if part != nil {
		switch req.Role {
		case domain.LadoRolePart:
			part.SessionID = id
		case domain.LadoRoleMerge:
			part.MergeSessionID = id
		}
		part.UpdatedAt = started
	}
	err := s.deps.Store.LinkLadoPartExecution(ctx, request, exec, part, oldState)
	if errors.Is(err, domain.ErrLadoLeaseLost) {
		s.deps.Log.Warn("LADO started a parts run worker but its start was settled meanwhile", "task", r.task.ID, "session", id)
		return nil
	}
	if err != nil {
		return fmt.Errorf("link session %s: %w", id, err)
	}
	s.deps.Log.Info("LADO started a parts run worker", "task", r.task.ID, "role", req.Role, "part", req.Part, "attempt", req.Attempt, "session", id)
	return nil
}

// roleLabel names a parts run worker in its session's display name.
func roleLabel(req domain.LadoRunRequest) string {
	switch req.Role {
	case domain.LadoRolePart:
		return "[" + req.Part + "]"
	case domain.LadoRoleMerge:
		return "[merge " + req.Part + "]"
	case domain.LadoRoleIntegration:
		return "[integration]"
	}
	return ""
}

// recoverPartSpawns settles the parts run starts a stopped pass left: once a
// start's grant expired, a session AO finished starting for it is linked, one
// it never finished is stopped, and the grant is cleared. An unexpired grant
// may still be starting its worker and is left alone.
func (s *Service) recoverPartSpawns(ctx context.Context, now time.Time, settings *domain.LadoDispatchSettings) error {
	entries, err := s.deps.Store.LadoPartSpawnRequests(ctx)
	if err != nil {
		return fmt.Errorf("list the parts run starts: %w", err)
	}
	var problems []error
	for _, entry := range entries {
		if entry.PartSpawnExpiresAt.After(now) {
			continue
		}
		if err := s.recoverPartSpawn(ctx, entry, settings); err != nil {
			problems = append(problems, fmt.Errorf("recover the start %s: %w", entry.PartSpawnRequest, err))
		}
	}
	return errors.Join(problems...)
}

func (s *Service) recoverPartSpawn(ctx context.Context, entry domain.LadoQueueEntry, settings *domain.LadoDispatchSettings) error {
	ctx = context.WithoutCancel(ctx)
	request := entry.PartSpawnRequest
	req, ok := domain.ParseLadoRunClientRequestID(request)
	rec, found, err := s.deps.Store.GetSessionByClientRequestID(ctx, request)
	if err != nil {
		return err
	}
	if ok && found && rec.ClientRequestCommitted && settings != nil {
		grant, err := s.deps.Store.LadoPartSpawnGrant(ctx, request, entry.PartSpawnExpiresAt.Add(-time.Nanosecond))
		if err != nil {
			return err
		}
		if startedFor(rec, entry.ProjectID, grant.Hash) {
			return s.relinkRunWorker(ctx, entry, req, request, rec, *settings)
		}
	}
	if found && !rec.IsTerminated {
		if _, err := s.deps.Sessions.RequestKill(ctx, rec.ID); err != nil {
			return fmt.Errorf("stop session %s: %s", rec.ID, problem(err, "the session could not be stopped"))
		}
	}
	_, err = s.deps.Store.ClearLadoPartSpawnRequest(ctx, entry.ID, request)
	return err
}

// relinkRunWorker links the session AO started for a parts run worker before
// a stop, with the part's progress as that start would have written it.
func (s *Service) relinkRunWorker(ctx context.Context, entry domain.LadoQueueEntry, req domain.LadoRunRequest, request string, rec domain.SessionRecord, settings domain.LadoDispatchSettings) error {
	run, ok, err := s.deps.Store.LadoPartRun(ctx, entry.ID)
	if err != nil || !ok {
		return fmt.Errorf("read the parts run: %v", err)
	}
	task, ok, err := s.deps.Store.LadoTask(ctx, entry.TaskID)
	if err != nil || !ok {
		return fmt.Errorf("read task %s: %v", entry.TaskID, err)
	}
	r := &partRun{run: run, entry: entry, task: task}
	branch, startSHA := run.Branch, run.HeadSHA
	var part *domain.LadoTaskPart
	var oldState domain.LadoPartState
	if req.Role != domain.LadoRoleIntegration {
		parts, err := s.deps.Store.LadoTaskParts(ctx, entry.ID)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(parts, func(p domain.LadoTaskPart) bool { return p.PartID == req.Part })
		if i < 0 {
			return fmt.Errorf("the run has no part %s", req.Part)
		}
		next := parts[i]
		oldState = next.State
		if req.Role == domain.LadoRolePart {
			branch = domain.LadoPartBranch(task.ID, req.Part, req.Attempt)
			next.State, next.Attempts, next.Branch, next.StartSHA = domain.LadoPartRunning, req.Attempt, branch, run.HeadSHA
			next.WaitingReason, next.WaitingDetail = "", ""
		} else {
			branch = domain.LadoMergeBranch(task.ID, req.Part, req.Attempt)
			next.MergeAttempts, next.MergeBranch, next.WaitingReason, next.WaitingDetail = req.Attempt, branch, "", ""
		}
		part = &next
	}
	s.deps.Log.Info("LADO links a parts run worker a stopped pass had started", "task", task.ID, "session", rec.ID, "request", request)
	return s.linkRunWorker(ctx, r, req, request, rec.ID, rec.CreatedAt.UTC(), branch, startSHA, settings, part, oldState)
}

// diskLow reports whether the free disk space, on the AO data folder's file
// system and on dir's, is under the settings' threshold while they hold work
// then. Space that cannot be read holds nothing; the first such failure is
// logged.
func (s *Service) diskLow(settings domain.LadoDispatchSettings, dir string) bool {
	if !settings.HoldOnDiskLow || s.deps.FreeDisk == nil {
		return false
	}
	limit := uint64(settings.MinFreeDiskGB) << 30 //nolint:gosec // MinFreeDiskGB is validated positive.
	for _, path := range []string{s.deps.DataDir, dir} {
		if path == "" {
			continue
		}
		free, err := s.deps.FreeDisk(path)
		if err != nil {
			if !s.diskUnreadable.Swap(true) {
				s.deps.Log.Warn("LADO cannot read the free disk space, so work does not wait for disk", "path", path, "err", err)
			}
			continue
		}
		if free < limit {
			return true
		}
	}
	return false
}
