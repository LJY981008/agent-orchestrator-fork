package store

// LADO fork: one task changing several repositories of a workspace (issue
// #64, docs/LJY-PRD/TDD.md §5.4, §7 and §8). An execution tracks one PR per
// repository in lado_execution_prs, pinned by the verification loop rather
// than read from the session's newest PR, and settles each on its own row;
// a parts run keeps an integration branch in each repository it runs in
// (lado_part_run_repos) and records each part's merge there
// (lado_task_part_repos). The first repository's values repeat in the
// columns lado_part_runs and lado_task_parts had before, so a run in one
// repository reads and writes as it did.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListLadoExecutionPRsToTrack returns up to limit executions, newest first,
// whose session has a PR LADO does not track for them yet: executions that
// ran a PR (role empty or integration) with an open untracked PR, or a merged
// or closed one while the execution is not settled or still has a live
// tracked PR that is not. Each carries its tracked PRs as PRs and, as
// UntrackedPRs, every PR of its session without a tracking row, newest
// first, merged and closed ones too; an execution without tracked PRs has
// its session's newest PR as PR, which is among them. Whether its session
// ended and whether its worker reported done are read too.
func (s *Store) ListLadoExecutionPRsToTrack(ctx context.Context, limit int) ([]domain.LadoExecution, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.qr.ListLadoExecutionsWithUntrackedPRs(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("list LADO executions with untracked PRs: %w", err)
	}
	executions := make([]domain.LadoExecution, 0, len(rows))
	for _, row := range rows {
		execution := ladoExecutionFromGen(row.LadoExecution)
		execution.SessionTerminated = row.SessionTerminated
		executions = append(executions, execution)
	}
	if _, err := s.readLadoExecutionPRs(ctx, executions, true); err != nil {
		return nil, err
	}
	return executions, nil
}

// TrackLadoExecutionPR starts tracking the PR prURL for the execution of
// session at at: the PR of repository repo (its slash-separated folder under
// the workspace's, "" for a single-repository project) at position in the
// merge order. A PR the execution settled before LADO tracked PRs
// (lado_executions.settled_pr_url) is tracked as settled in that state. It
// reports false, writing nothing, when the execution tracks the PR already
// or tracks a live PR of that repository. A session that is not an
// execution is an error.
func (s *Store) TrackLadoExecutionPR(ctx context.Context, session domain.SessionID, prURL, repo string, position int, at time.Time) (bool, error) {
	if session == "" || prURL == "" || at.IsZero() || position < 0 {
		return false, errors.New("track LADO execution PR: a session, a PR, a position and a time are required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	tracked := false
	err := s.inTx(ctx, "track LADO execution PR", func(q *gen.Queries) error {
		row, err := q.GetLadoExecution(ctx, string(session))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("session %s is not a LADO execution", session)
		}
		if err != nil {
			return err
		}
		params := gen.InsertLadoExecutionPRParams{
			ExecutionSessionID: string(session),
			PRURL:              prURL,
			RepoKey:            strings.Trim(repo, "/"),
			Position:           int64(position),
			TrackedAt:          at.UTC(),
		}
		e := row.LadoExecution
		if e.SettledAt.Valid && e.SettledPRURL == prURL && e.SettledPRState != "" {
			params.SettledAt, params.SettledPRState = e.SettledAt, e.SettledPRState
		}
		n, err := q.InsertLadoExecutionPR(ctx, params)
		tracked = n > 0
		return err
	})
	if err != nil {
		return false, err
	}
	return tracked, nil
}

// ReplaceLadoExecutionPR tracks newURL instead of oldURL for the execution
// of session at at: the worker closed the PR it opened, or that PR merged and
// verification settled it, and the worker opened a newer one in the same
// repository. The new PR takes the old one's repository and position, the
// old one keeps its row as replaced, and audit, when given, is appended in
// the same transaction. It reports false, writing nothing, when oldURL is not
// a live tracked PR of the execution, AO sees it neither closed nor merged
// and settled merged, or newURL is tracked already.
func (s *Store) ReplaceLadoExecutionPR(ctx context.Context, session domain.SessionID, oldURL, newURL string, at time.Time, audit *domain.LadoAuditEvent) (bool, error) {
	if session == "" || oldURL == "" || newURL == "" || oldURL == newURL || at.IsZero() {
		return false, errors.New("replace LADO execution PR: a session, two PRs and a time are required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	errKept := errors.New("kept")
	err := s.inTx(ctx, "replace LADO execution PR", func(q *gen.Queries) error {
		old, err := q.GetLadoExecutionTrackedPR(ctx, gen.GetLadoExecutionTrackedPRParams{ExecutionSessionID: string(session), PRURL: oldURL})
		if errors.Is(err, sql.ErrNoRows) {
			return errKept
		}
		if err != nil {
			return err
		}
		if old.ReplacedAt.Valid {
			return errKept
		}
		pr, err := q.GetPR(ctx, oldURL)
		if errors.Is(err, sql.ErrNoRows) {
			return errKept
		}
		if err != nil {
			return err
		}
		settledMerged := pr.PRState == domain.PRStateMerged && old.SettledAt.Valid && old.SettledPRState == string(domain.PRStateMerged)
		if pr.PRState != domain.PRStateClosed && !settledMerged {
			return errKept
		}
		if _, err := q.MarkLadoExecutionPRReplaced(ctx, gen.MarkLadoExecutionPRReplacedParams{
			ReplacedAt: ladoNullTime(at), ExecutionSessionID: string(session), PRURL: oldURL,
		}); err != nil {
			return err
		}
		n, err := q.InsertLadoExecutionPR(ctx, gen.InsertLadoExecutionPRParams{
			ExecutionSessionID: string(session),
			PRURL:              newURL,
			RepoKey:            old.RepoKey,
			Position:           old.Position,
			TrackedAt:          at.UTC(),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return errKept
		}
		if audit == nil {
			return nil
		}
		_, err = appendLadoAuditEventTx(ctx, q, *audit)
		return err
	})
	if errors.Is(err, errKept) {
		return false, nil
	}
	return err == nil, err
}

// StartLadoPartRunRepos records that the leased entry starts its parts run in
// the repositories repos, as StartLadoPartRun does with one: one transaction
// writes the run, its waiting parts, the entry's parts_started_at, one row
// per repository with its integration branch's base and head, one empty row
// per part and repository, and the run's audit event. The repository at
// position 0 is the run's own: its folder, base and head are written to the
// run's columns too, whatever run gives. Positions and folders must not
// repeat, and one must be at position 0. When token no longer holds the entry
// nothing is written and the error is domain.ErrLadoLeaseLost.
func (s *Store) StartLadoPartRunRepos(ctx context.Context, token string, run domain.LadoPartRun, repos []domain.LadoPartRunRepo, parts []domain.LadoTaskPart, event domain.LadoAuditEvent) error {
	ordered, err := ladoOrderedRunRepos(repos)
	if err != nil {
		return fmt.Errorf("start LADO parts run: %w", err)
	}
	first := ordered[0]
	run.RepoDir = first.Dir
	run.BaseSHA = first.BaseSHA
	run.HeadSHA = first.HeadSHA
	run.BaseBranch = first.BaseBranch
	run.BaseRemote = first.BaseRemote
	run.DispatchLocalBaseSHA = first.DispatchLocalBaseSHA
	run.BaseCommitsAhead = first.BaseCommitsAhead
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err = s.inTx(ctx, "start LADO parts run", func(q *gen.Queries) error {
		if err := startLadoPartRunTx(ctx, q, token, run, parts); err != nil {
			return err
		}
		for _, repo := range ordered {
			if err := q.InsertLadoPartRunRepo(ctx, gen.InsertLadoPartRunRepoParams{
				QueueEntryID:         string(run.QueueEntryID),
				RelativePath:         repo.Relative,
				Position:             int64(repo.Position),
				RepoDir:              repo.Dir,
				BaseSha:              repo.BaseSHA,
				HeadSha:              repo.HeadSHA,
				BaseBranch:           repo.BaseBranch,
				BaseRemote:           repo.BaseRemote,
				DispatchLocalBaseSha: repo.DispatchLocalBaseSHA,
				BaseCommitsAhead:     ladoNullInt(repo.BaseCommitsAhead),
			}); err != nil {
				return err
			}
			for _, part := range parts {
				if err := q.UpsertLadoTaskPartRepo(ctx, gen.UpsertLadoTaskPartRepoParams{
					QueueEntryID: string(run.QueueEntryID), PartID: part.PartID, RelativePath: repo.Relative,
				}); err != nil {
					return err
				}
			}
		}
		_, err := appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return unwrapLadoSentinel(err)
}

// ladoOrderedRunRepos returns repos by position, each folder without a
// leading or trailing "/", after checking that positions and folders do not
// repeat and that one is at position 0.
func ladoOrderedRunRepos(repos []domain.LadoPartRunRepo) ([]domain.LadoPartRunRepo, error) {
	if len(repos) == 0 {
		return nil, errors.New("a repository is required")
	}
	ordered := slices.Clone(repos)
	for i := range ordered {
		ordered[i].Relative = strings.Trim(ordered[i].Relative, "/")
	}
	slices.SortStableFunc(ordered, func(a, b domain.LadoPartRunRepo) int { return a.Position - b.Position })
	if ordered[0].Position != 0 {
		return nil, errors.New("no repository is at position 0")
	}
	for i := 1; i < len(ordered); i++ {
		if ordered[i].Position == ordered[i-1].Position {
			return nil, fmt.Errorf("two repositories are at position %d", ordered[i].Position)
		}
		if slices.ContainsFunc(ordered[:i], func(r domain.LadoPartRunRepo) bool { return r.Relative == ordered[i].Relative }) {
			return nil, fmt.Errorf("repository %q repeats", ordered[i].Relative)
		}
	}
	return ordered, nil
}

// LadoPartRunRepos returns the repositories of a queue entry's parts run in
// order. A run started before runs had repositories reads as one, at
// position 0 with an empty folder, from the run's own columns; an entry
// without a parts run has none.
func (s *Store) LadoPartRunRepos(ctx context.Context, entryID domain.LadoQueueEntryID) ([]domain.LadoPartRunRepo, error) {
	rows, err := s.qr.ListLadoPartRunRepos(ctx, string(entryID))
	if err != nil {
		return nil, fmt.Errorf("list LADO parts run repositories: %w", err)
	}
	if len(rows) == 0 {
		run, ok, err := s.LadoPartRun(ctx, entryID)
		if err != nil || !ok {
			return nil, err
		}
		return []domain.LadoPartRunRepo{{
			Dir:                  run.RepoDir,
			BaseSHA:              run.BaseSHA,
			HeadSHA:              run.HeadSHA,
			BaseBranch:           run.BaseBranch,
			BaseRemote:           run.BaseRemote,
			DispatchLocalBaseSHA: run.DispatchLocalBaseSHA,
			BaseCommitsAhead:     run.BaseCommitsAhead,
		}}, nil
	}
	repos := make([]domain.LadoPartRunRepo, 0, len(rows))
	for _, row := range rows {
		repos = append(repos, ladoPartRunRepoFromGen(row))
	}
	return repos, nil
}

// LadoTaskPartRepos returns how each part of a queue entry's parts run went
// in each repository, by part id and in the run's repository order. A run
// started before runs had repositories reads each part as one repository
// with an empty folder, from the part's own columns.
func (s *Store) LadoTaskPartRepos(ctx context.Context, entryID domain.LadoQueueEntryID) (map[string][]domain.LadoTaskPartRepo, error) {
	rows, err := s.qr.ListLadoTaskPartRepos(ctx, string(entryID))
	if err != nil {
		return nil, fmt.Errorf("list LADO task part repositories: %w", err)
	}
	repos := map[string][]domain.LadoTaskPartRepo{}
	for _, row := range rows {
		repos[row.PartID] = append(repos[row.PartID], domain.LadoTaskPartRepo{
			Relative:     row.RelativePath,
			StartSHA:     row.StartSha,
			MergeFromSHA: row.MergeFromSha,
			MergeTipSHA:  row.MergeTipSha,
			MergedSHA:    row.MergedSha,
		})
	}
	if len(rows) > 0 {
		return repos, nil
	}
	runRepos, err := s.qr.ListLadoPartRunRepos(ctx, string(entryID))
	if err != nil {
		return nil, fmt.Errorf("list LADO parts run repositories: %w", err)
	}
	if len(runRepos) > 0 {
		return repos, nil
	}
	parts, err := s.LadoTaskParts(ctx, entryID)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		repos[part.PartID] = []domain.LadoTaskPartRepo{{
			StartSHA: part.StartSHA, MergeFromSHA: part.MergeFromSHA, MergeTipSHA: part.MergeTipSHA, MergedSHA: part.MergedSHA,
		}}
	}
	return repos, nil
}

// SetLadoTaskPartRepos writes a part's progress, as UpdateLadoTaskPart does,
// with how it went in each of repos (the start, merge-from, merge tip and
// merged commits), in one transaction. The run's first repository among
// repos gives the part's own SHA fields, whatever part has. It reports false,
// writing nothing, when the part's stored state is no longer oldState; a
// repository that is not the run's is an error.
func (s *Store) SetLadoTaskPartRepos(ctx context.Context, part domain.LadoTaskPart, oldState domain.LadoPartState, repos []domain.LadoTaskPartRepo) (bool, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	errStale := errors.New("stale")
	err := s.inTx(ctx, "set LADO task part repositories", func(q *gen.Queries) error {
		runRepos, err := q.ListLadoPartRunRepos(ctx, string(part.QueueEntryID))
		if err != nil {
			return err
		}
		for _, repo := range repos {
			relative := strings.Trim(repo.Relative, "/")
			at := slices.IndexFunc(runRepos, func(r gen.LadoPartRunRepo) bool { return r.RelativePath == relative })
			if at < 0 {
				return fmt.Errorf("repository %q is not one of the parts run's", relative)
			}
			if at == 0 {
				part = ladoPartWithRepo(part, repo)
			}
		}
		n, err := q.UpdateLadoTaskPart(ctx, ladoTaskPartParams(part, oldState))
		if err != nil {
			return err
		}
		if n == 0 {
			return errStale
		}
		for _, repo := range repos {
			if err := upsertLadoTaskPartRepoTx(ctx, q, part, repo); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errStale) {
		return false, nil
	}
	return err == nil, err
}

// MergeLadoTaskPartRepo records a merge of a part LADO made or found made in
// one repository of its parts run: the part's progress (whatever state the
// caller gives it, as the part merges repository by repository), that
// repository's merge commits and its integration head moved from oldHead to
// repo.MergedSHA, in one transaction with event when it is given. In the
// run's first repository the run's own head moves too, under the same check,
// and the part's own SHA fields take repo's. It reports false, writing
// nothing, when the part is no longer in oldState or the repository's head is
// no longer oldHead; a repository that is not the run's is an error.
func (s *Store) MergeLadoTaskPartRepo(ctx context.Context, part domain.LadoTaskPart, oldState domain.LadoPartState, repo domain.LadoTaskPartRepo, oldHead string, event *domain.LadoAuditEvent) (bool, error) {
	if repo.MergedSHA == "" || oldHead == "" {
		return false, errors.New("merge LADO task part: a merged commit and the head it moved from are required")
	}
	repo.Relative = strings.Trim(repo.Relative, "/")
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	errStale := errors.New("stale")
	err := s.inTx(ctx, "merge LADO task part", func(q *gen.Queries) error {
		runRepos, err := q.ListLadoPartRunRepos(ctx, string(part.QueueEntryID))
		if err != nil {
			return err
		}
		at := slices.IndexFunc(runRepos, func(r gen.LadoPartRunRepo) bool { return r.RelativePath == repo.Relative })
		if at < 0 {
			return fmt.Errorf("repository %q is not one of the parts run's", repo.Relative)
		}
		if at == 0 {
			part = ladoPartWithRepo(part, repo)
		}
		n, err := q.UpdateLadoTaskPart(ctx, ladoTaskPartParams(part, oldState))
		if err != nil {
			return err
		}
		if n == 0 {
			return errStale
		}
		// An empty part leaves the head where it is, and still checks it.
		n, err = q.SetLadoPartRunRepoHead(ctx, gen.SetLadoPartRunRepoHeadParams{
			HeadSha: repo.MergedSHA, QueueEntryID: string(part.QueueEntryID), RelativePath: repo.Relative, OldHeadSha: oldHead,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return errStale
		}
		if at == 0 {
			n, err = q.SetLadoPartRunHead(ctx, gen.SetLadoPartRunHeadParams{HeadSha: repo.MergedSHA, QueueEntryID: string(part.QueueEntryID), OldHeadSha: oldHead})
			if err != nil {
				return err
			}
			if n == 0 {
				return errStale
			}
		}
		if err := upsertLadoTaskPartRepoTx(ctx, q, part, repo); err != nil {
			return err
		}
		if event == nil {
			return nil
		}
		_, err = appendLadoAuditEventTx(ctx, q, *event)
		return err
	})
	if errors.Is(err, errStale) {
		return false, nil
	}
	return err == nil, err
}

// mirrorLadoFirstRepoTx keeps the first repository of a parts run with
// repository rows the same as the run's and the part's own columns after
// MergeLadoTaskPart moved them; a run without them has nothing to keep.
func mirrorLadoFirstRepoTx(ctx context.Context, q *gen.Queries, part domain.LadoTaskPart) error {
	runRepos, err := q.ListLadoPartRunRepos(ctx, string(part.QueueEntryID))
	if err != nil || len(runRepos) == 0 {
		return err
	}
	if err := q.SetLadoPartRunFirstRepoHead(ctx, gen.SetLadoPartRunFirstRepoHeadParams{
		HeadSha: part.MergedSHA, QueueEntryID: string(part.QueueEntryID),
	}); err != nil {
		return err
	}
	return upsertLadoTaskPartRepoTx(ctx, q, part, domain.LadoTaskPartRepo{
		Relative:     runRepos[0].RelativePath,
		StartSHA:     part.StartSHA,
		MergeFromSHA: part.MergeFromSHA,
		MergeTipSHA:  part.MergeTipSHA,
		MergedSHA:    part.MergedSHA,
	})
}

func upsertLadoTaskPartRepoTx(ctx context.Context, q *gen.Queries, part domain.LadoTaskPart, repo domain.LadoTaskPartRepo) error {
	return q.UpsertLadoTaskPartRepo(ctx, gen.UpsertLadoTaskPartRepoParams{
		QueueEntryID: string(part.QueueEntryID),
		PartID:       part.PartID,
		RelativePath: strings.Trim(repo.Relative, "/"),
		StartSha:     repo.StartSHA,
		MergeFromSha: repo.MergeFromSHA,
		MergeTipSha:  repo.MergeTipSHA,
		MergedSha:    repo.MergedSHA,
	})
}

// ladoPartWithRepo gives the part the SHAs of its run's first repository.
func ladoPartWithRepo(part domain.LadoTaskPart, repo domain.LadoTaskPartRepo) domain.LadoTaskPart {
	part.StartSHA = repo.StartSHA
	part.MergeFromSHA = repo.MergeFromSHA
	part.MergeTipSHA = repo.MergeTipSHA
	part.MergedSHA = repo.MergedSHA
	return part
}

func ladoPartRunRepoFromGen(row gen.LadoPartRunRepo) domain.LadoPartRunRepo {
	var ahead *int
	if row.BaseCommitsAhead.Valid {
		n := int(row.BaseCommitsAhead.Int64)
		ahead = &n
	}
	return domain.LadoPartRunRepo{
		Relative:             row.RelativePath,
		Position:             int(row.Position),
		Dir:                  row.RepoDir,
		BaseSHA:              row.BaseSha,
		HeadSHA:              row.HeadSha,
		BaseBranch:           row.BaseBranch,
		BaseRemote:           row.BaseRemote,
		DispatchLocalBaseSHA: row.DispatchLocalBaseSha,
		BaseCommitsAhead:     ahead,
	}
}
