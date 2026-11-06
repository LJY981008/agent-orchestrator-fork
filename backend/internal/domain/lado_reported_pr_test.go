package domain_test

import (
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var (
	ladoReportedPending = domain.LadoReportedPR{URL: "https://gitlab.example.com/team/back/-/merge_requests/7"}
	ladoReportedRefused = domain.LadoReportedPR{
		URL: "https://gitlab.example.com/team/front/-/merge_requests/8", Refused: true, Reason: domain.LadoReportedPROwnedElsewhere,
	}
)

// Only reported PRs that are not refused are pending, and only while the
// session runs (#71).
func TestLadoReportedPRPending(t *testing.T) {
	e := domain.LadoExecution{ReportedPRs: []domain.LadoReportedPR{ladoReportedRefused, ladoReportedPending}}
	if got := e.PendingReportedPRs(); !slices.Equal(got, []domain.LadoReportedPR{ladoReportedPending}) {
		t.Fatalf("pending = %+v, want the one not refused", got)
	}
	e.SessionTerminated = true
	if got := e.PendingReportedPRs(); got != nil {
		t.Fatalf("pending after the session ended = %+v, want none", got)
	}
	if got := (domain.LadoExecution{ReportedPRs: []domain.LadoReportedPR{ladoReportedRefused}}).PendingReportedPRs(); got != nil {
		t.Fatalf("pending of refused only = %+v, want none", got)
	}
	if !slices.Contains(domain.LadoReportedPRRefusalReasons, domain.LadoReportedPRUnreachable) || len(domain.LadoReportedPRRefusalReasons) != 6 {
		t.Fatalf("refusal reasons = %v", domain.LadoReportedPRRefusalReasons)
	}
}

// An execution without a PR whose worker reported one is Running with the
// reason, refused or not; a timeout and an ended session still fail it.
func TestLadoReportedPRStateWithoutAPR(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(e *domain.LadoExecution)
		state  domain.LadoTaskState
		reason string
	}{
		{"nothing reported", func(*domain.LadoExecution) {}, domain.LadoTaskRunning, ""},
		{"a pending report", func(e *domain.LadoExecution) {
			e.ReportedPRs = []domain.LadoReportedPR{ladoReportedPending}
		}, domain.LadoTaskRunning, domain.LadoRunningReportedPRUntracked},
		{"a refused report", func(e *domain.LadoExecution) {
			e.ReportedPRs = []domain.LadoReportedPR{ladoReportedRefused}
		}, domain.LadoTaskRunning, domain.LadoRunningReportedPRUntracked},
		{"timed out", func(e *domain.LadoExecution) {
			e.ReportedPRs, e.TimedOutAt = []domain.LadoReportedPR{ladoReportedPending}, time.Now()
		}, domain.LadoTaskFailed, domain.LadoFailedTimedOut},
		{"the session ended", func(e *domain.LadoExecution) {
			e.ReportedPRs, e.SessionTerminated = []domain.LadoReportedPR{ladoReportedPending}, true
		}, domain.LadoTaskFailed, domain.LadoFailedEndedWithoutPR},
		{"several repositories expected", func(e *domain.LadoExecution) {
			e.ReportedPRs, e.PRRepos = []domain.LadoReportedPR{ladoReportedPending}, []string{"back", "front"}
		}, domain.LadoTaskRunning, domain.LadoRunningReportedPRUntracked},
		{"an open PR", func(e *domain.LadoExecution) {
			e.ReportedPRs = []domain.LadoReportedPR{ladoReportedPending}
			e.PR = domain.LadoExecutionPR{URL: "https://gitlab.example.com/team/back/-/merge_requests/1", HeadSHA: "h"}
		}, domain.LadoTaskValidating, domain.LadoValidatingVerifying},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := domain.LadoExecution{SessionID: "s1"}
			tc.edit(&e)
			if state, reason := e.State(); state != tc.state || reason != tc.reason {
				t.Fatalf("state = %s (%q), want %s (%q)", state, reason, tc.state, tc.reason)
			}
		})
	}
}

// An execution with several PRs is held Validating by a pending reported PR
// instead of waiting for or holding its approval; a refused one or an ended
// session leaves the state as it was.
func TestLadoReportedPRStateWithSeveralPRs(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(e *domain.LadoExecution)
		state  domain.LadoTaskState
		reason string
	}{
		{"a pending report", func(e *domain.LadoExecution) {
			e.ReportedPRs = []domain.LadoReportedPR{ladoReportedPending}
		}, domain.LadoTaskValidating, domain.LadoRunningReportedPRUntracked},
		{"a pending report of approved heads", func(e *domain.LadoExecution) {
			e.ReportedPRs = []domain.LadoReportedPR{ladoReportedPending}
			for i := range e.PRs {
				e.PRs[i].Integration.HeadApproved = true
			}
		}, domain.LadoTaskValidating, domain.LadoRunningReportedPRUntracked},
		{"a refused report", func(e *domain.LadoExecution) {
			e.ReportedPRs = []domain.LadoReportedPR{ladoReportedRefused}
		}, domain.LadoTaskIntegrationPending, ""},
		{"a pending report after the session ended", func(e *domain.LadoExecution) {
			e.ReportedPRs, e.SessionTerminated = []domain.LadoReportedPR{ladoReportedPending}, true
		}, domain.LadoTaskIntegrationPending, ""},
		{"a validating PR comes first", func(e *domain.LadoExecution) {
			e.ReportedPRs = []domain.LadoReportedPR{ladoReportedPending}
			e.PRs[0].PR.HeadSHA = "f2"
		}, domain.LadoTaskValidating, domain.LadoValidatingVerifying},
		{"a missing repository comes first", func(e *domain.LadoExecution) {
			e.ReportedPRs, e.PRs = []domain.LadoReportedPR{ladoReportedPending}, e.PRs[1:]
		}, domain.LadoTaskValidating, domain.LadoValidatingPRMissing},
		{"no PR yet", func(e *domain.LadoExecution) {
			e.ReportedPRs = []domain.LadoReportedPR{ladoReportedPending}
			e.PRs, e.PR, e.Verification = nil, domain.LadoExecutionPR{}, domain.LadoExecutionVerification{}
		}, domain.LadoTaskRunning, domain.LadoRunningReportedPRUntracked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := multiExec()
			tc.edit(&e)
			if state, reason := e.State(); state != tc.state || reason != tc.reason {
				t.Fatalf("state = %s (%q), want %s (%q)", state, reason, tc.state, tc.reason)
			}
		})
	}
}
