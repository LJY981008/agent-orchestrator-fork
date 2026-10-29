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
	WaitingReason  string    `json:"waitingReason,omitempty" enum:"part_dependency,part_overlap,slot_workers,memory_tight,disk_low" description:"Why a waiting part, or a conflict waiting for its merge worker, has not started."`
	WaitingDetail  string    `json:"waitingDetail,omitempty" description:"The parts it waits for (part_dependency) or the running part whose paths overlap (part_overlap)."`
	Attempts       int       `json:"attempts" description:"Workers started for the part so far; LADO gives up after two."`
	SessionID      string    `json:"sessionId,omitempty" description:"The part's newest worker session."`
	Branch         string    `json:"branch,omitempty"`
	StartSHA       string    `json:"startSha,omitempty" description:"The integration branch's commit the part's branch started from."`
	MergedSHA      string    `json:"mergedSha,omitempty" description:"The integration branch's commit once the part was merged."`
	MergeSessionID string    `json:"mergeSessionId,omitempty" description:"The merge worker resolving the part's conflict."`
	MergeAttempts  int       `json:"mergeAttempts"`
	Summary        string    `json:"summary,omitempty" description:"The part worker's done report, with the merge worker's note when it had one."`
	Error          string    `json:"error,omitempty" description:"Why the part's last worker did not finish it."`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// LadoTaskPartsResponse is the parts run of a task's newest approval.
type LadoTaskPartsResponse struct {
	Branch    string                 `json:"branch,omitempty" description:"The integration branch; omitted while the newest approval runs no parts."`
	BaseSHA   string                 `json:"baseSha,omitempty" description:"The approved base commit the integration branch started from."`
	HeadSHA   string                 `json:"headSha,omitempty" description:"Where LADO last moved the integration branch."`
	StartedAt *time.Time             `json:"startedAt,omitempty"`
	Parts     []LadoTaskPartResponse `json:"parts"`
}
