package ladoverify

// LADO fork: the review item (decision j of the step 8 plan; PRD VER-002,
// VER-003; docs/LJY-PRD/TDD.md §8.5). AO's code review runs only when the
// approved plan says review: required. LADO starts it through AO's review
// service once the head's checks, commands, commit messages and paths
// neither wait nor fail, at most LadoReviewRoundCap rounds per execution,
// and a head still without an approval after the last round goes to the
// developer as review_inconclusive. The newest verdict on the head decides,
// whoever asked for the review.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Reviews is AO's review service as LADO uses it.
type Reviews interface {
	// Trigger starts a review of the worker's PR at its head and returns the
	// ids of the runs it created. An empty harness leaves the reviewer to AO.
	Trigger(ctx context.Context, worker domain.SessionID, prURL string, harness domain.ReviewerHarness) ([]string, error)
	// Runs returns the worker's review runs, from any source.
	Runs(ctx context.Context, worker domain.SessionID) ([]domain.ReviewRun, error)
	// Cancel cancels the worker's running review.
	Cancel(ctx context.Context, worker domain.SessionID) error
}

// ReviewInput is what the review item reads.
type ReviewInput struct {
	// Required and Reviewer are the approved plan's review: required and its
	// reviewer as written.
	Required bool
	Reviewer string
	PRURL    string
	HeadSHA  string
	// Rounds are the review rounds LADO started for the execution, on any
	// head; Runs are the worker's review runs.
	Rounds []domain.LadoExecutionReview
	Runs   []domain.ReviewRun
	// Ready says whether the head's required checks, commands, commit
	// messages and paths neither wait nor fail (ReviewReady).
	Ready bool
	Now   time.Time
}

// ReviewAction is what the loop does for the review: start a round with
// Harness, AO's choice when empty, or cancel the stalled LADO run
// CancelRunID. The zero action does nothing.
type ReviewAction struct {
	Trigger     bool
	Harness     domain.ReviewerHarness
	CancelRunID string
}

// ReviewReady reports whether a head's items let its review start: none of
// required_checks, commands, commit_messages and paths waits or fails.
func ReviewReady(items []domain.LadoVerifyItem) bool {
	for _, item := range items {
		switch item.Kind {
		case domain.LadoVerifyRequiredChecks, domain.LadoVerifyCommands, domain.LadoVerifyCommitMessages, domain.LadoVerifyPaths:
			if item.Outcome == domain.LadoVerifyWait || item.Outcome == domain.LadoVerifyFail {
				return false
			}
		}
	}
	return true
}

// EvaluateReview computes the review item and what to do next. Without a
// required review it is info not_required. Otherwise the newest verdict
// among the runs on the head (the PR's URL and head commit) decides, from
// any source: an approval passes and a request for changes fails as
// changes_requested. Without one the item waits as review_pending: for the
// other items while the head is not ready, for a run still going on the
// head, which LADO cancels when it is its own round and has run longer than
// LadoReviewStallAfter, and for a new round, which it starts while fewer
// than LadoReviewRoundCap were started. With every round used, the developer
// confirms review_inconclusive.
//
// The plan's reviewer, compared without case, picks the reviewer of a new
// round when AO knows it; AO chooses otherwise, and the details note an
// unknown one. The details count the rounds and name the deciding run, its
// verdict and its reviewer, or else the reviewer LADO asks for.
func EvaluateReview(in ReviewInput) (domain.LadoVerifyItem, ReviewAction, error) {
	if !in.Required {
		item, err := domain.NewLadoVerifyItem(domain.LadoVerifyReview, domain.LadoVerifyInfo, domain.LadoCodeNotRequired, nil)
		return item, ReviewAction{}, err
	}
	if in.PRURL == "" || in.HeadSHA == "" {
		return domain.LadoVerifyItem{}, ReviewAction{}, errors.New("evaluate the review: no PR head")
	}
	harness, note := planReviewer(in.Reviewer)
	details := domain.LadoReviewDetails{Rounds: reviewRoundsUsed(in.Rounds), Harness: string(harness), Note: note}
	answer := func(outcome domain.LadoVerifyOutcome, code string, action ReviewAction) (domain.LadoVerifyItem, ReviewAction, error) {
		item, err := domain.NewLadoVerifyItem(domain.LadoVerifyReview, outcome, code, details)
		if err != nil {
			return domain.LadoVerifyItem{}, ReviewAction{}, err
		}
		return item, action, nil
	}

	var decided *domain.ReviewRun
	var running []domain.ReviewRun
	for i, run := range in.Runs {
		if run.PRURL != in.PRURL || run.TargetSHA != in.HeadSHA {
			continue
		}
		switch {
		case run.Verdict == domain.VerdictApproved || run.Verdict == domain.VerdictChangesRequested:
			if decided == nil || newerVerdict(run, *decided) {
				decided = &in.Runs[i]
			}
		case run.Status == domain.ReviewRunRunning:
			running = append(running, run)
		}
	}
	switch {
	case decided != nil:
		details.RunID, details.Verdict, details.Harness = decided.ID, string(decided.Verdict), string(decided.Harness)
		if decided.Verdict == domain.VerdictApproved {
			return answer(domain.LadoVerifyPass, "", ReviewAction{})
		}
		return answer(domain.LadoVerifyFail, domain.LadoCodeChangesRequested, ReviewAction{})
	case !in.Ready:
		return answer(domain.LadoVerifyWait, domain.LadoCodeReviewPending, ReviewAction{})
	case len(running) > 0:
		return answer(domain.LadoVerifyWait, domain.LadoCodeReviewPending, ReviewAction{CancelRunID: stalledRound(in.Rounds, running, in.Now)})
	case details.Rounds < domain.LadoReviewRoundCap:
		return answer(domain.LadoVerifyWait, domain.LadoCodeReviewPending, ReviewAction{Trigger: true, Harness: harness})
	default:
		return answer(domain.LadoVerifyConfirm, domain.LadoCodeReviewInconclusive, ReviewAction{})
	}
}

// planReviewer maps the plan's reviewer to AO's reviewer of that id,
// compared without case. Without a reviewer AO chooses; for one AO does not
// know it chooses too, and the note says so.
func planReviewer(reviewer string) (domain.ReviewerHarness, string) {
	reviewer = strings.Join(strings.Fields(reviewer), " ")
	if reviewer == "" {
		return "", ""
	}
	if harness := domain.ReviewerHarness(strings.ToLower(reviewer)); harness.IsKnown() {
		return harness, ""
	}
	return "", "unknown reviewer " + reviewer + "; AO chose"
}

// reviewRoundsUsed counts the rounds LADO started.
func reviewRoundsUsed(rounds []domain.LadoExecutionReview) int {
	seen := map[int]bool{}
	for _, round := range rounds {
		seen[round.Round] = true
	}
	return len(seen)
}

// newerVerdict reports whether run's verdict is newer than than's. Of two
// verdicts given at the same time, a request for changes wins.
func newerVerdict(run, than domain.ReviewRun) bool {
	if !run.CreatedAt.Equal(than.CreatedAt) {
		return run.CreatedAt.After(than.CreatedAt)
	}
	return run.Verdict == domain.VerdictChangesRequested && than.Verdict != domain.VerdictChangesRequested
}

// stalledRound returns the oldest running run that is a LADO round not yet
// cancelled and has run longer than LadoReviewStallAfter, empty when none
// has.
func stalledRound(rounds []domain.LadoExecutionReview, running []domain.ReviewRun, now time.Time) string {
	var oldest *domain.ReviewRun
	for i, run := range running {
		ours := false
		for _, round := range rounds {
			if round.ReviewRunID == run.ID && round.CancelledAt.IsZero() {
				ours = true
			}
		}
		if ours && now.Sub(run.CreatedAt) > domain.LadoReviewStallAfter && (oldest == nil || run.CreatedAt.Before(oldest.CreatedAt)) {
			oldest = &running[i]
		}
	}
	if oldest == nil {
		return ""
	}
	return oldest.ID
}
