package ladoqueue

// Parts runs in several repositories of a workspace (#64,
// docs/LJY-PRD/TDD.md §5.4): the run keeps an integration branch of the same
// name in each repository its plan runs in, every part's branch starts in
// each of them from that repository's integration head, and a done part
// merges repository by repository, in the plan's order. A merge that
// conflicts goes to a merge worker in that repository alone; what merged in
// the others stays. A run in one repository does none of this and runs as
// parts.go always did.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// branchAt is a branch LADO makes in the repository at dir, at sha.
type branchAt struct {
	dir string
	sha string
}

// loadRepos reads the run's repositories and, for a run in several, how each
// part went in each.
func (s *Service) loadRepos(ctx context.Context, r *partRun) error {
	repos, err := s.deps.Store.LadoPartRunRepos(ctx, r.entry.ID)
	if err != nil {
		return err
	}
	r.repos = repos
	if !r.multi() {
		return nil
	}
	r.partRepos, err = s.deps.Store.LadoTaskPartRepos(ctx, r.entry.ID)
	return err
}

// repoIndex returns the place of the repository whose folder is relative in
// the run's order, -1 when the run has none such.
func (r *partRun) repoIndex(relative string) int {
	relative = strings.Trim(relative, "/")
	for k, repo := range r.repos {
		if strings.Trim(repo.Relative, "/") == relative {
			return k
		}
	}
	return -1
}

// partRepo returns how part went in the run's repository k so far.
func (r *partRun) partRepo(partID string, k int) domain.LadoTaskPartRepo {
	relative := r.repos[k].Relative
	for _, row := range r.partRepos[partID] {
		if strings.Trim(row.Relative, "/") == strings.Trim(relative, "/") {
			return row
		}
	}
	return domain.LadoTaskPartRepo{Relative: relative}
}

// setPartRepos keeps rows as how part went in their repositories.
func (r *partRun) setPartRepos(partID string, rows ...domain.LadoTaskPartRepo) {
	if r.partRepos == nil {
		r.partRepos = map[string][]domain.LadoTaskPartRepo{}
	}
	for _, row := range rows {
		kept := r.partRepos[partID]
		found := false
		for j := range kept {
			if strings.Trim(kept[j].Relative, "/") == strings.Trim(row.Relative, "/") {
				kept[j], found = row, true
			}
		}
		if !found {
			kept = append(kept, row)
		}
		r.partRepos[partID] = kept
	}
}

// folders names the run's repository folders, as "back/", for a run in
// several; nil for a run in one.
func (r *partRun) folders() []string {
	if !r.multi() {
		return nil
	}
	names := make([]string, len(r.repos))
	for k, repo := range r.repos {
		names[k] = strings.Trim(repo.Relative, "/") + "/"
	}
	return names
}

// allBranchesAt is where a part's branch starts: the integration head of
// each repository of the run.
func (r *partRun) allBranchesAt() []branchAt {
	if !r.multi() {
		return []branchAt{{dir: r.run.RepoDir, sha: r.run.HeadSHA}}
	}
	at := make([]branchAt, len(r.repos))
	for k, repo := range r.repos {
		at[k] = branchAt{dir: repo.Dir, sha: repo.HeadSHA}
	}
	return at
}

// changedRepos lists, in the run's order, the repositories whose integration
// branch moved past its base: those the parts changed, where the
// integration worker opens a PR each.
func (r *partRun) changedRepos() []string {
	changed := []string{}
	for _, repo := range r.repos {
		if repo.Changed() {
			changed = append(changed, strings.Trim(repo.Relative, "/"))
		}
	}
	return changed
}

// withFirstRepo gives part the SHAs of how it went in the run's first
// repository, as the store keeps them in the part's own fields.
func withFirstRepo(part domain.LadoTaskPart, row domain.LadoTaskPartRepo) domain.LadoTaskPart {
	part.StartSHA, part.MergeFromSHA, part.MergeTipSHA, part.MergedSHA = row.StartSHA, row.MergeFromSHA, row.MergeTipSHA, row.MergedSHA
	return part
}

// writePartRepos writes next over part, as writePart does, with rows as how
// it went in their repositories.
func (s *Service) writePartRepos(ctx context.Context, r *partRun, i int, next domain.LadoTaskPart, rows []domain.LadoTaskPartRepo) error {
	part := &r.parts[i]
	next.UpdatedAt = s.deps.Clock().UTC()
	for _, row := range rows {
		if r.repoIndex(row.Relative) == 0 {
			next = withFirstRepo(next, row)
		}
	}
	ok, err := s.deps.Store.SetLadoTaskPartRepos(context.WithoutCancel(ctx), next, part.State, rows)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("part %s changed under the pass", part.PartID)
	}
	*part = next
	r.setPartRepos(part.PartID, rows...)
	return nil
}

// recordPartStarts records, before a part's new worker starts, the
// integration head its branch starts from in each repository; the merges of
// its last attempt are forgotten.
func (s *Service) recordPartStarts(ctx context.Context, r *partRun, i int) error {
	rows := make([]domain.LadoTaskPartRepo, len(r.repos))
	for k, repo := range r.repos {
		rows[k] = domain.LadoTaskPartRepo{Relative: repo.Relative, StartSHA: repo.HeadSHA}
	}
	return s.writePartRepos(ctx, r, i, r.parts[i], rows)
}

// mergeDoneRepos merges a done part of a run in several repositories, as
// mergeDone does in one. It reads the part's tip in each repository first: a
// part whose branch has no commit after its start in any of them merges
// nothing and fails the run unless the plan marks it allow_empty. Otherwise
// the tips are recorded and the part merges repository by repository.
func (s *Service) mergeDoneRepos(ctx context.Context, r *partRun, i int) error {
	part := &r.parts[i]
	rows := make([]domain.LadoTaskPartRepo, len(r.repos))
	empty := true
	for k, repo := range r.repos {
		tip, err := s.deps.PartGit.BranchSHA(ctx, repo.Dir, part.Branch)
		if err != nil {
			return err
		}
		if tip == "" {
			return s.partAttemptFailed(ctx, r, i, fmt.Sprintf("the part's branch is gone in %s/", strings.Trim(repo.Relative, "/")))
		}
		row := r.partRepo(part.PartID, k)
		if row.StartSHA == "" {
			return s.failRun(ctx, r, part.PartID, fmt.Sprintf("the part's start in %s/ is unknown", strings.Trim(repo.Relative, "/")))
		}
		row.MergeFromSHA, row.MergeTipSHA, row.MergedSHA = "", tip, ""
		if tip != row.StartSHA {
			empty = false
		}
		rows[k] = row
	}
	if empty {
		allowed, err := s.allowsEmpty(ctx, r, part.PartID)
		if err != nil {
			return err
		}
		if !allowed {
			return s.failRun(ctx, r, part.PartID, "the part's worker reported done, but its branch has no commit after its start")
		}
	}
	next := *part
	next.State = domain.LadoPartMerging
	if err := s.writePartRepos(ctx, r, i, next, rows); err != nil {
		return err
	}
	return s.finishMergeRepos(ctx, r, i)
}

// finishMergeRepos merges a merging part into each repository it has not
// merged in yet, in the run's order. In a repository its branch did not
// change the part merges as it is. Elsewhere LADO records the integration
// head it merges onto, then merges and moves the branch; a branch that
// already holds that merge, as after a restart between the two, is recorded
// without merging again. A conflict hands the part to a merge worker in that
// repository and stops there; the repositories merged so far keep their
// merges.
func (s *Service) finishMergeRepos(ctx context.Context, r *partRun, i int) error {
	part := &r.parts[i]
	for k := range r.repos {
		repo := r.repos[k]
		folder := strings.Trim(repo.Relative, "/") + "/"
		row := r.partRepo(part.PartID, k)
		if row.MergedSHA != "" {
			continue
		}
		if row.MergeTipSHA == "" {
			return s.failRun(ctx, r, part.PartID, "the part's tip in "+folder+" is unknown")
		}
		if row.Empty() {
			row.MergeFromSHA, row.MergedSHA = repo.HeadSHA, repo.HeadSHA
			if err := s.recordRepoMerge(ctx, r, i, k, *part, row, repo.HeadSHA, nil); err != nil {
				return err
			}
			continue
		}
		current, err := s.deps.PartGit.BranchSHA(ctx, repo.Dir, r.run.Branch)
		if err != nil {
			return err
		}
		if row.MergeFromSHA != "" && current != row.MergeFromSHA {
			parents, err := s.deps.PartGit.Parents(ctx, repo.Dir, current)
			if err == nil && len(parents) == 2 && parents[0] == row.MergeFromSHA && parents[1] == row.MergeTipSHA {
				row.MergedSHA = current
				if err := s.recordRepoMerge(ctx, r, i, k, *part, row, row.MergeFromSHA, map[string]any{"recovered": true}); err != nil {
					return err
				}
				continue
			}
			// The branch is where LADO last left it: the head recorded for
			// this merge went stale, as when another part merged first after
			// a restart. The merge starts again from there.
			row.MergeFromSHA = ""
		}
		if row.MergeFromSHA == "" {
			if current != repo.HeadSHA {
				return s.failRun(ctx, r, part.PartID, "the integration branch in "+folder+" moved outside LADO")
			}
			row.MergeFromSHA = current
			if err := s.writePartRepos(ctx, r, i, *part, []domain.LadoTaskPartRepo{row}); err != nil {
				return err
			}
		}
		message := fmt.Sprintf("chore(lado): 파트 %s(%s)를 %s 통합 브랜치에 병합", part.PartID, oneLine(part.Title), folder)
		merged, err := s.deps.PartGit.Merge(ctx, repo.Dir, row.MergeFromSHA, row.MergeTipSHA, message)
		if errors.Is(err, errMergeConflict) {
			next := *part
			next.State, next.MergeSessionID, next.MergeBranch, next.MergeRepo = domain.LadoPartConflict, "", "", strings.Trim(repo.Relative, "/")
			// This repository's merge workers count from here (#64).
			next.MergeAttemptsBase = part.MergeAttempts
			if err := s.writePart(ctx, part, next); err != nil {
				return err
			}
			s.deps.Log.Info("LADO found a merge conflict and hands it to a merge worker", "task", r.task.ID, "part", part.PartID, "repo", repo.Relative)
			s.appendAudit(ctx, s.audit("part.conflict", r.entry.ID, map[string]any{
				"task": r.task.ID, "part": part.PartID, "repo": repo.Relative, "head": row.MergeFromSHA, "tip": row.MergeTipSHA,
			}))
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.deps.PartGit.MoveBranch(ctx, repo.Dir, r.run.Branch, merged, row.MergeFromSHA); err != nil {
			return err
		}
		row.MergedSHA = merged
		if err := s.recordRepoMerge(ctx, r, i, k, *part, row, row.MergeFromSHA, nil); err != nil {
			return err
		}
	}
	return nil
}

// fastForwardRepo moves the integration branch of the repository whose merge
// conflicted to the merge worker's branch there, as fastForward does in a run
// in one repository, then merges the part into the repositories after it.
func (s *Service) fastForwardRepo(ctx context.Context, r *partRun, i int, note string) error {
	part := &r.parts[i]
	k := r.repoIndex(part.MergeRepo)
	if k < 0 {
		return s.failRun(ctx, r, part.PartID, "the repository whose merge conflicted is not the run's")
	}
	repo := r.repos[k]
	folder := strings.Trim(repo.Relative, "/") + "/"
	row := r.partRepo(part.PartID, k)
	tip, err := s.deps.PartGit.BranchSHA(ctx, repo.Dir, part.MergeBranch)
	if err != nil {
		return err
	}
	current, err := s.deps.PartGit.BranchSHA(ctx, repo.Dir, r.run.Branch)
	if err != nil {
		return err
	}
	holdsPart := tip != "" && commitID.MatchString(row.MergeTipSHA)
	if holdsPart {
		if holdsPart, err = s.deps.PartGit.IsAncestor(ctx, repo.Dir, row.MergeTipSHA, tip); err != nil {
			return err
		}
	}
	if !holdsPart {
		return s.mergeAttemptFailed(ctx, r, i, "the merge worker's branch does not hold the part")
	}
	next := *part
	next.MergeRepo = ""
	if current != repo.HeadSHA {
		// LADO moved the branch and stopped before recording it, as
		// fastForward finds; anything else moved outside LADO.
		parents, err := s.deps.PartGit.Parents(ctx, repo.Dir, current)
		if err == nil && (current == tip || (len(parents) == 2 && parents[0] == repo.HeadSHA && parents[1] == tip)) {
			row.MergedSHA = current
			if err := s.recordRepoMerge(ctx, r, i, k, next, row, repo.HeadSHA, map[string]any{"mergeWorker": part.MergeSessionID, "recovered": true}); err != nil {
				return err
			}
			return s.mergeRest(ctx, r, i)
		}
		return s.failRun(ctx, r, part.PartID, "the integration branch in "+folder+" moved outside LADO")
	}
	head := tip
	ahead, err := s.deps.PartGit.IsAncestor(ctx, repo.Dir, current, tip)
	if err != nil {
		return err
	}
	switch {
	case ahead && tip != current:
		if err := s.deps.PartGit.MoveBranch(ctx, repo.Dir, r.run.Branch, tip, current); err != nil {
			return err
		}
	case !ahead:
		message := fmt.Sprintf("chore(lado): 파트 %s(%s)의 충돌 해결을 %s 통합 브랜치에 병합", part.PartID, oneLine(part.Title), folder)
		merged, err := s.deps.PartGit.Merge(ctx, repo.Dir, current, tip, message)
		if errors.Is(err, errMergeConflict) {
			return s.mergeAttemptFailed(ctx, r, i, "the merge worker's branch conflicts with what merged meanwhile")
		}
		if err != nil {
			return err
		}
		if err := s.deps.PartGit.MoveBranch(ctx, repo.Dir, r.run.Branch, merged, current); err != nil {
			return err
		}
		head = merged
	}
	row.MergedSHA = head
	if note = oneLine(note); note != "" {
		// The integration worker reads how the conflict was resolved.
		next.Summary = strings.TrimSpace(next.Summary + " Merge worker: " + note)
	}
	if err := s.recordRepoMerge(ctx, r, i, k, next, row, current, map[string]any{"mergeWorker": part.MergeSessionID}); err != nil {
		return err
	}
	return s.mergeRest(ctx, r, i)
}

// mergeRest merges a part whose conflict was resolved into the repositories
// it has not merged in yet.
func (s *Service) mergeRest(ctx context.Context, r *partRun, i int) error {
	if r.parts[i].State != domain.LadoPartMerging {
		return nil
	}
	return s.finishMergeRepos(ctx, r, i)
}

// recordRepoMerge stores the part's merge in the run's repository k, row,
// which moved that repository's integration head from oldHead to
// row.MergedSHA, with next as the part's progress: merging while another
// repository is still to merge, merged once none is, with the part.merged
// audit event then.
func (s *Service) recordRepoMerge(ctx context.Context, r *partRun, i, k int, next domain.LadoTaskPart, row domain.LadoTaskPartRepo, oldHead string, extra map[string]any) error {
	part := &r.parts[i]
	remaining := false
	for j := range r.repos {
		if j != k && r.partRepo(part.PartID, j).MergedSHA == "" {
			remaining = true
		}
	}
	next.State = domain.LadoPartMerging
	if !remaining {
		next.State, next.WaitingReason, next.WaitingDetail = domain.LadoPartMerged, "", ""
	}
	if k == 0 {
		next = withFirstRepo(next, row)
	}
	next.UpdatedAt = s.deps.Clock().UTC()
	var event *domain.LadoAuditEvent
	if !remaining {
		heads := map[string]string{}
		empty := row.Empty()
		for j, repo := range r.repos {
			merged := row
			if j != k {
				merged = r.partRepo(part.PartID, j)
			}
			heads[repo.Relative] = merged.MergedSHA
			empty = empty && merged.Empty()
		}
		// next carries the first repository's merge as its own (withFirstRepo).
		payload := map[string]any{"task": r.task.ID, "part": next.PartID, "head": next.MergedSHA, "repos": heads}
		if empty {
			payload["empty"] = true
		}
		for key, v := range extra {
			payload[key] = v
		}
		ev := s.audit("part.merged", r.entry.ID, payload)
		event = &ev
	}
	ok, err := s.deps.Store.MergeLadoTaskPartRepo(context.WithoutCancel(ctx), next, part.State, row, oldHead, event)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("part %s changed under the pass", part.PartID)
	}
	*part = next
	r.setPartRepos(part.PartID, row)
	r.repos[k].HeadSHA = row.MergedSHA
	if k == 0 {
		r.run.HeadSHA = row.MergedSHA
	}
	if !remaining {
		s.deps.Log.Info("LADO merged a part into its task's integration branches", "task", r.task.ID, "part", next.PartID, "repos", len(r.repos))
	}
	return nil
}
