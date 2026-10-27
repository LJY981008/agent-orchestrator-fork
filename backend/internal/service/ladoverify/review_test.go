package ladoverify

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const reviewTestPR = "https://github.com/acme/demo/pull/7"

// reviewRunOn is a review run on a head of the test PR, by AO's Claude Code
// reviewer.
func reviewRunOn(id, head string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, created time.Time, source domain.ReviewTriggerSource) domain.ReviewRun {
	return domain.ReviewRun{ID: id, ReviewID: "review-1", SessionID: "lado-1", Harness: domain.ReviewerClaudeCode, TriggerSource: source, PRURL: reviewTestPR, TargetSHA: head, Status: status, Verdict: verdict, CreatedAt: created}
}

// ladoRoundFor is the review round LADO recorded for a run it started.
func ladoRoundFor(runID, head string, round int, created time.Time) domain.LadoExecutionReview {
	return domain.LadoExecutionReview{ReviewRunID: runID, ExecutionSessionID: "lado-1", HeadSHA: head, Round: round, CreatedAt: created}
}

func reviewDetailsOf(t *testing.T, item domain.LadoVerifyItem) domain.LadoReviewDetails {
	t.Helper()
	var details domain.LadoReviewDetails
	if err := json.Unmarshal(item.Details, &details); err != nil {
		t.Fatalf("review details %s: %v", item.Details, err)
	}
	return details
}

func TestReviewPolicyRounds(t *testing.T) {
	head, oldHead := strings.Repeat("a", 40), strings.Repeat("b", 40)
	now := t0.Add(3 * time.Hour)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	manual, auto := domain.ReviewTriggerManual, domain.ReviewTriggerAuto
	// Two rounds on the old head, both asking for changes.
	twoOldRounds := []domain.LadoExecutionReview{ladoRoundFor("rv1", oldHead, 1, ago(150*time.Minute)), ladoRoundFor("rv2", oldHead, 2, ago(100*time.Minute))}
	twoOldRuns := []domain.ReviewRun{
		reviewRunOn("rv1", oldHead, domain.ReviewRunDelivered, domain.VerdictChangesRequested, ago(150*time.Minute), manual),
		reviewRunOn("rv2", oldHead, domain.ReviewRunDelivered, domain.VerdictChangesRequested, ago(100*time.Minute), manual),
	}
	cancelled := ladoRoundFor("rv3", head, 3, ago(2*time.Hour))
	cancelled.CancelledAt = ago(time.Hour)

	cases := []struct {
		name    string
		edit    func(in *ReviewInput)
		outcome domain.LadoVerifyOutcome
		code    string
		action  ReviewAction
		details domain.LadoReviewDetails
	}{
		{
			name:    "not required",
			edit:    func(in *ReviewInput) { in.Required, in.Runs = false, twoOldRuns },
			outcome: domain.LadoVerifyInfo, code: domain.LadoCodeNotRequired,
		},
		{
			name:    "the other items are not ready",
			edit:    func(in *ReviewInput) { in.Ready = false },
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			details: domain.LadoReviewDetails{Harness: "codex"},
		},
		{
			name:    "ready without a run starts the first round",
			edit:    func(*ReviewInput) {},
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			action:  ReviewAction{Trigger: true, Harness: domain.ReviewerCodex},
			details: domain.LadoReviewDetails{Harness: "codex"},
		},
		{
			name: "an approval on the head passes, whoever asked and however ready",
			edit: func(in *ReviewInput) {
				in.Ready = false
				in.Runs = []domain.ReviewRun{reviewRunOn("auto1", head, domain.ReviewRunComplete, domain.VerdictApproved, ago(time.Hour), auto)}
			},
			outcome: domain.LadoVerifyPass,
			details: domain.LadoReviewDetails{RunID: "auto1", Verdict: "approved", Harness: "claude-code"},
		},
		{
			name: "a request for changes on the head fails",
			edit: func(in *ReviewInput) {
				in.Rounds = []domain.LadoExecutionReview{ladoRoundFor("rv1", head, 1, ago(time.Hour))}
				in.Runs = []domain.ReviewRun{reviewRunOn("rv1", head, domain.ReviewRunDelivered, domain.VerdictChangesRequested, ago(time.Hour), manual)}
			},
			outcome: domain.LadoVerifyFail, code: domain.LadoCodeChangesRequested,
			details: domain.LadoReviewDetails{Rounds: 1, RunID: "rv1", Verdict: "changes_requested", Harness: "claude-code"},
		},
		{
			name: "the newest verdict on the head decides",
			edit: func(in *ReviewInput) {
				in.Runs = []domain.ReviewRun{
					reviewRunOn("new", head, domain.ReviewRunComplete, domain.VerdictApproved, ago(time.Hour), manual),
					reviewRunOn("old", head, domain.ReviewRunDelivered, domain.VerdictChangesRequested, ago(2*time.Hour), manual),
				}
			},
			outcome: domain.LadoVerifyPass,
			details: domain.LadoReviewDetails{RunID: "new", Verdict: "approved", Harness: "claude-code"},
		},
		{
			name: "of two verdicts at once, the request for changes wins",
			edit: func(in *ReviewInput) {
				in.Runs = []domain.ReviewRun{
					reviewRunOn("yes", head, domain.ReviewRunComplete, domain.VerdictApproved, ago(time.Hour), manual),
					reviewRunOn("no", head, domain.ReviewRunComplete, domain.VerdictChangesRequested, ago(time.Hour), manual),
				}
			},
			outcome: domain.LadoVerifyFail, code: domain.LadoCodeChangesRequested,
			details: domain.LadoReviewDetails{RunID: "no", Verdict: "changes_requested", Harness: "claude-code"},
		},
		{
			name: "a failed run lets the next round start",
			edit: func(in *ReviewInput) {
				in.Rounds = []domain.LadoExecutionReview{ladoRoundFor("rv1", head, 1, ago(time.Hour))}
				in.Runs = []domain.ReviewRun{reviewRunOn("rv1", head, domain.ReviewRunFailed, domain.VerdictNone, ago(time.Hour), manual)}
			},
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			action:  ReviewAction{Trigger: true, Harness: domain.ReviewerCodex},
			details: domain.LadoReviewDetails{Rounds: 1, Harness: "codex"},
		},
		{
			name: "a cancelled run lets the next round start",
			edit: func(in *ReviewInput) {
				in.Rounds, in.Runs = twoOldRounds, slices.Concat([]domain.ReviewRun{reviewRunOn("m1", head, domain.ReviewRunCancelled, domain.VerdictNone, ago(time.Hour), manual)}, twoOldRuns)
			},
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			action:  ReviewAction{Trigger: true, Harness: domain.ReviewerCodex},
			details: domain.LadoReviewDetails{Rounds: 2, Harness: "codex"},
		},
		{
			name: "three rounds without an approval on the head are inconclusive",
			edit: func(in *ReviewInput) {
				in.Rounds = slices.Concat(twoOldRounds, []domain.LadoExecutionReview{ladoRoundFor("rv3", head, 3, ago(time.Hour))})
				in.Runs = slices.Concat([]domain.ReviewRun{reviewRunOn("rv3", head, domain.ReviewRunFailed, domain.VerdictNone, ago(time.Hour), manual)}, twoOldRuns)
			},
			outcome: domain.LadoVerifyConfirm, code: domain.LadoCodeReviewInconclusive,
			details: domain.LadoReviewDetails{Rounds: 3, Harness: "codex"},
		},
		{
			name: "a new head after the last round is inconclusive",
			edit: func(in *ReviewInput) {
				in.Rounds = slices.Concat(twoOldRounds, []domain.LadoExecutionReview{ladoRoundFor("rv3", oldHead, 3, ago(80*time.Minute))})
				in.Runs = slices.Concat(twoOldRuns, []domain.ReviewRun{reviewRunOn("rv3", oldHead, domain.ReviewRunDelivered, domain.VerdictChangesRequested, ago(80*time.Minute), manual)})
			},
			outcome: domain.LadoVerifyConfirm, code: domain.LadoCodeReviewInconclusive,
			details: domain.LadoReviewDetails{Rounds: 3, Harness: "codex"},
		},
		{
			name: "a LADO run going for over 30 minutes is cancelled",
			edit: func(in *ReviewInput) {
				in.Rounds = slices.Concat(twoOldRounds, []domain.LadoExecutionReview{ladoRoundFor("rv3", head, 3, ago(31*time.Minute))})
				in.Runs = slices.Concat([]domain.ReviewRun{reviewRunOn("rv3", head, domain.ReviewRunRunning, domain.VerdictNone, ago(31*time.Minute), manual)}, twoOldRuns)
			},
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			action:  ReviewAction{CancelRunID: "rv3"},
			details: domain.LadoReviewDetails{Rounds: 3, Harness: "codex"},
		},
		{
			name: "a LADO run going for 30 minutes goes on",
			edit: func(in *ReviewInput) {
				in.Rounds = []domain.LadoExecutionReview{ladoRoundFor("rv1", head, 1, ago(30*time.Minute))}
				in.Runs = []domain.ReviewRun{reviewRunOn("rv1", head, domain.ReviewRunRunning, domain.VerdictNone, ago(30*time.Minute), manual)}
			},
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			details: domain.LadoReviewDetails{Rounds: 1, Harness: "codex"},
		},
		{
			name: "a run LADO did not start is waited for, not cancelled",
			edit: func(in *ReviewInput) {
				in.Runs = []domain.ReviewRun{reviewRunOn("m1", head, domain.ReviewRunRunning, domain.VerdictNone, ago(2*time.Hour), manual)}
			},
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			details: domain.LadoReviewDetails{Harness: "codex"},
		},
		{
			name: "a round LADO cancelled already is not cancelled again",
			edit: func(in *ReviewInput) {
				in.Rounds = slices.Concat(twoOldRounds, []domain.LadoExecutionReview{cancelled})
				in.Runs = slices.Concat([]domain.ReviewRun{reviewRunOn("rv3", head, domain.ReviewRunRunning, domain.VerdictNone, ago(2*time.Hour), manual)}, twoOldRuns)
			},
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			details: domain.LadoReviewDetails{Rounds: 3, Harness: "codex"},
		},
		{
			name: "a verdict on another head says nothing",
			edit: func(in *ReviewInput) {
				in.Rounds = []domain.LadoExecutionReview{ladoRoundFor("rv1", oldHead, 1, ago(time.Hour))}
				in.Runs = []domain.ReviewRun{reviewRunOn("rv1", oldHead, domain.ReviewRunComplete, domain.VerdictApproved, ago(time.Hour), manual)}
			},
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			action:  ReviewAction{Trigger: true, Harness: domain.ReviewerCodex},
			details: domain.LadoReviewDetails{Rounds: 1, Harness: "codex"},
		},
		{
			name: "a verdict on another PR says nothing",
			edit: func(in *ReviewInput) {
				other := reviewRunOn("x1", head, domain.ReviewRunComplete, domain.VerdictApproved, ago(time.Hour), manual)
				other.PRURL = "https://github.com/acme/demo/pull/8"
				in.Runs = []domain.ReviewRun{other}
			},
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			action:  ReviewAction{Trigger: true, Harness: domain.ReviewerCodex},
			details: domain.LadoReviewDetails{Harness: "codex"},
		},
		{
			name:    "an unknown reviewer leaves the choice to AO",
			edit:    func(in *ReviewInput) { in.Reviewer = "gpt  reviewer" },
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			action:  ReviewAction{Trigger: true},
			details: domain.LadoReviewDetails{Note: "unknown reviewer gpt reviewer; AO chose"},
		},
		{
			name:    "a reviewer is matched without case",
			edit:    func(in *ReviewInput) { in.Reviewer = "Claude-Code" },
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			action:  ReviewAction{Trigger: true, Harness: domain.ReviewerClaudeCode},
			details: domain.LadoReviewDetails{Harness: "claude-code"},
		},
		{
			name:    "without a reviewer AO chooses",
			edit:    func(in *ReviewInput) { in.Reviewer = "" },
			outcome: domain.LadoVerifyWait, code: domain.LadoCodeReviewPending,
			action: ReviewAction{Trigger: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := ReviewInput{Required: true, Reviewer: "codex", PRURL: reviewTestPR, HeadSHA: head, Ready: true, Now: now}
			tc.edit(&in)
			item, action, err := EvaluateReview(in)
			if err != nil {
				t.Fatal(err)
			}
			expectItemOutcome(t, item, domain.LadoVerifyReview, tc.outcome, tc.code)
			if action != tc.action {
				t.Fatalf("action = %+v, want %+v", action, tc.action)
			}
			if tc.outcome == domain.LadoVerifyInfo {
				if string(item.Details) != "{}" {
					t.Fatalf("details = %s, want {}", item.Details)
				}
				return
			}
			if details := reviewDetailsOf(t, item); details != tc.details {
				t.Fatalf("details = %+v, want %+v", details, tc.details)
			}
		})
	}

	// A required review needs the PR's head.
	if _, _, err := EvaluateReview(ReviewInput{Required: true, PRURL: reviewTestPR, Now: now}); err == nil {
		t.Fatal("EvaluateReview took a required review without a head")
	}
}

func TestReviewReadyWaitsForTheOtherItems(t *testing.T) {
	item := func(kind domain.LadoVerifyKind, outcome domain.LadoVerifyOutcome, code string) domain.LadoVerifyItem {
		t.Helper()
		i, err := domain.NewLadoVerifyItem(kind, outcome, code, nil)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	ready := []domain.LadoVerifyItem{
		item(domain.LadoVerifyRequiredChecks, domain.LadoVerifyPass, domain.LadoCodeNoneReported),
		item(domain.LadoVerifyCommands, domain.LadoVerifyConfirm, domain.LadoCodeNoTests),
		item(domain.LadoVerifyCommitMessages, domain.LadoVerifyInfo, domain.LadoCodeNotRequired),
		item(domain.LadoVerifyPaths, domain.LadoVerifyConfirm, domain.LadoCodeOutsidePlan),
		// Items the review does not wait for.
		item(domain.LadoVerifyBaseBranch, domain.LadoVerifyConfirm, domain.LadoCodeBaseMoved),
		item(domain.LadoVerifyReview, domain.LadoVerifyWait, domain.LadoCodeReviewPending),
	}
	if !ReviewReady(ready) {
		t.Fatal("ReviewReady = false, want true")
	}
	for _, blocking := range []domain.LadoVerifyItem{
		item(domain.LadoVerifyRequiredChecks, domain.LadoVerifyWait, domain.LadoCodeChecksPending),
		item(domain.LadoVerifyCommands, domain.LadoVerifyFail, domain.LadoCodeCommandsFailed),
		item(domain.LadoVerifyCommitMessages, domain.LadoVerifyFail, domain.LadoCodeCommitMessagesFailed),
		item(domain.LadoVerifyPaths, domain.LadoVerifyFail, domain.LadoCodeProtectedPath),
	} {
		if ReviewReady(append([]domain.LadoVerifyItem{blocking}, ready...)) {
			t.Fatalf("ReviewReady = true with %s %s", blocking.Kind, blocking.Outcome)
		}
	}
}
