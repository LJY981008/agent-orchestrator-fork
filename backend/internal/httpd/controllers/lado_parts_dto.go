package controllers

// LADO fork: DTOs of a task's parts run (docs/LJY-PRD/TDD.md §5.4).

import "time"

// LadoTaskPartsSummaryResponse is how far a task's parts run got.
type LadoTaskPartsSummaryResponse struct {
	Total  int `json:"total"`
	Merged int `json:"merged" description:"The parts merged into the integration branch so far."`
}

// LadoTaskPartResponse is one part of a task's parts run.
type LadoTaskPartResponse struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Phase          int       `json:"phase" description:"The display phase, as the plan view shows it."`
	Steps          []int     `json:"steps"`
	AffectedPaths  []string  `json:"affectedPaths"`
	DependsOn      []string  `json:"dependsOn"`
	State          string    `json:"state" enum:"waiting,running,done,merging,merged,conflict,failed" description:"waiting until it can start; running in its worker; done once its worker reported done; merging while LADO merges it; merged once its commits are on the integration branch; conflict while a merge worker resolves its merge; failed once its attempts ran out."`
	WaitingReason  string    `json:"waitingReason,omitempty" enum:"part_dependency,part_overlap,slot_workers,memory_tight,disk_low,retry,account_limited,agent_unavailable" description:"Why a waiting part, or a conflict waiting for its merge worker, has not started; retry after a worker that did not start, until its next start; account_limited while the account a new worker would run on is at its usage limit (waitingDetail is the reset time), agent_unavailable while its agent cannot start (waitingDetail is the agent)."`
	WaitingDetail  string    `json:"waitingDetail,omitempty" description:"The parts it waits for (part_dependency) or the running part whose paths overlap (part_overlap)."`
	Attempts       int       `json:"attempts" description:"Workers started for the part so far; LADO gives up after two."`
	SessionID      string    `json:"sessionId,omitempty" description:"The part's newest worker session."`
	Branch         string    `json:"branch,omitempty"`
	StartSHA       string    `json:"startSha,omitempty" description:"The integration branch's commit the part's branch started from."`
	MergedSHA      string    `json:"mergedSha,omitempty" description:"The integration branch's commit once the part was merged."`
	MergeSessionID string    `json:"mergeSessionId,omitempty" description:"The merge worker resolving the part's conflict."`
	MergeRepo      string    `json:"mergeRepo,omitempty" description:"The repository, as its slash-separated folder under the workspace's, whose merge of the part conflicted and where its merge worker resolves it; omitted while no merge conflicts and for a run in one repository."`
	MergeAttempts  int       `json:"mergeAttempts"`
	Summary        string    `json:"summary,omitempty" description:"The part worker's done report, with the merge worker's note when it had one."`
	Error          string    `json:"error,omitempty" description:"Why the part's last worker did not finish it."`
	UpdatedAt      time.Time `json:"updatedAt"`
	// Repos is how the part went in each of the run's repositories (#64).
	Repos []LadoTaskPartRepoResponse `json:"repos" description:"How the part went in each of the run's repositories, in the run's order; one with an empty repo for a run in one repository."`
}

// LadoTaskPartRepoResponse is how one part went in one repository of its
// run.
type LadoTaskPartRepoResponse struct {
	Repo      string `json:"repo" description:"The repository's slash-separated folder under the workspace's; empty for a run in one repository."`
	StartSHA  string `json:"startSha,omitempty" description:"The repository's integration branch commit the part's branch started from there."`
	MergedSHA string `json:"mergedSha,omitempty" description:"The repository's integration branch commit once the part was merged there."`
	Empty     bool   `json:"empty" description:"The part left the repository as it found it: it has no commits there."`
}

// LadoPartRunRepoResponse is one repository of a parts run and its
// integration branch.
type LadoPartRunRepoResponse struct {
	Repo       string `json:"repo" description:"The repository's slash-separated folder under the workspace's; empty for a single-repository project, or a run in the issue's repository alone that started before runs had repositories."`
	Position   int    `json:"position" description:"The repository's place in the run's order, from 0: the order of the plan's repos, in which the PRs merge."`
	BaseSHA    string `json:"baseSha" description:"The commit the repository's integration branch started from."`
	HeadSHA    string `json:"headSha" description:"Where LADO last moved the repository's integration branch; its base while no part changed the repository."`
	BaseBranch string `json:"baseBranch,omitempty" description:"The repository's base branch the dispatch resolved."`
}

// LadoTaskPartsResponse is the parts run of a task's newest approval.
type LadoTaskPartsResponse struct {
	Branch    string                 `json:"branch,omitempty" description:"The integration branch, named the same in each of the run's repositories; omitted while the newest approval runs no parts."`
	BaseSHA   string                 `json:"baseSha,omitempty" description:"The approved base commit the integration branch started from."`
	HeadSHA   string                 `json:"headSha,omitempty" description:"Where LADO last moved the integration branch."`
	StartedAt *time.Time             `json:"startedAt,omitempty"`
	Parts     []LadoTaskPartResponse `json:"parts"`
	// Repos are the run's repositories (#64); the fields above are the
	// first's.
	Repos []LadoPartRunRepoResponse `json:"repos" description:"The repositories the run has an integration branch in, each named branch, in the order the plan's repos give; branch, baseSha and headSha above are the first's. Empty while the newest approval runs no parts."`
}
