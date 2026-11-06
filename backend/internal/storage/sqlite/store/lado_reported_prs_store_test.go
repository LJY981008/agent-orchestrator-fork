package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// mustReportLadoPRs writes a checkpoint report of session at at whose
// outputs say it created the PRs at urls.
func mustReportLadoPRs(t *testing.T, s *sqlite.Store, session domain.SessionRecord, at time.Time, urls ...string) {
	t.Helper()
	outputs := make([]domain.ReportOutput, 0, len(urls))
	for _, url := range urls {
		outputs = append(outputs, domain.ReportOutput{Kind: domain.ReportOutputPRCreated, Reference: url})
	}
	if _, err := s.CreateReport(context.Background(), domain.ReportRecord{
		ID: fmt.Sprintf("report-%s-%d", session.ID, at.UnixNano()), SessionID: session.ID, ProjectID: session.ProjectID,
		State: domain.ReportCheckpoint, Note: "opened the PR", Outputs: outputs,
		CreatedAt: at, DeliveryState: domain.ReportPending, AvailableAt: at, RepeatCount: 1,
	}); err != nil {
		t.Fatalf("CreateReport: %v", err)
	}
}

// mustLadoReportedSession returns the session record of an execution.
func mustLadoReportedSession(t *testing.T, s *sqlite.Store, exec domain.LadoExecution) domain.SessionRecord {
	t.Helper()
	rec, ok, err := s.GetSession(context.Background(), exec.SessionID)
	if err != nil || !ok {
		t.Fatalf("GetSession %s = (%v, %v)", exec.SessionID, ok, err)
	}
	return rec
}

func ladoReportedListed(t *testing.T, s *sqlite.Store, limit int) []domain.SessionID {
	t.Helper()
	executions, err := s.ListLadoExecutionsWithReportedPRs(context.Background(), limit)
	if err != nil {
		t.Fatalf("ListLadoExecutionsWithReportedPRs: %v", err)
	}
	var sessions []domain.SessionID
	for _, e := range executions {
		if len(e.PendingReportedPRs()) == 0 {
			t.Fatalf("listed %s without a pending reported PR: %+v", e.SessionID, e.ReportedPRs)
		}
		sessions = append(sessions, e.SessionID)
	}
	return sessions
}

// A reported PR the session does not have is a pending ReportedPRs entry
// once, however it is written; the session's own PR and a second PR in a
// repository where the session has an open one are not. The execution
// without a PR is Running for it, and both are listed, oldest first.
func TestLadoReportedPRReadsUntrackedReports(t *testing.T) {
	s, _ := openLadoMultiRepoStore(t)
	at := ladoVerifyAt
	withPR := mustLadoExecution(t, s, "1", at, false)
	back := mustWriteLadoRepoPR(t, s, withPR.SessionID, "back", 1, domain.PRStateOpen, ladoHead('a'), domain.MergeMergeable, at)
	mustReportLadoPRs(t, s, mustLadoReportedSession(t, s, withPR), at.Add(time.Minute),
		back, "https://github.com/acme/other/pull/5", "https://github.com/acme/back/pull/9")
	mustReportLadoPRs(t, s, mustLadoReportedSession(t, s, withPR), at.Add(2*time.Minute), "https://github.com/ACME/Other/pull/5")

	read := mustReadLadoExecution(t, s, withPR.SessionID)
	want := []domain.LadoReportedPR{{URL: "https://github.com/acme/other/pull/5"}}
	if !slices.Equal(read.ReportedPRs, want) {
		t.Fatalf("reported PRs = %+v, want %+v", read.ReportedPRs, want)
	}

	withoutPR := mustLadoExecution(t, s, "2", at.Add(time.Hour), false)
	mustReportLadoPRs(t, s, mustLadoReportedSession(t, s, withoutPR), at.Add(time.Hour), "https://github.com/acme/back/pull/2")
	read = mustReadLadoExecution(t, s, withoutPR.SessionID)
	if state, reason := read.State(); state != domain.LadoTaskRunning || reason != domain.LadoRunningReportedPRUntracked {
		t.Fatalf("state = %s (%q), want running with reported_pr_untracked", state, reason)
	}
	if got := ladoReportedListed(t, s, 10); !slices.Equal(got, []domain.SessionID{withPR.SessionID, withoutPR.SessionID}) {
		t.Fatalf("listed = %v, want both, oldest first", got)
	}
	if got := ladoReportedListed(t, s, 1); !slices.Equal(got, []domain.SessionID{withPR.SessionID}) {
		t.Fatalf("listed with limit 1 = %v, want the oldest", got)
	}

	// Once the session has the PR, the report is no longer one.
	mustWriteLadoRepoPR(t, s, withoutPR.SessionID, "back", 2, domain.PRStateOpen, ladoHead('b'), domain.MergeMergeable, at.Add(2*time.Hour))
	if read = mustReadLadoExecution(t, s, withoutPR.SessionID); len(read.ReportedPRs) != 0 {
		t.Fatalf("reported PRs after the claim = %+v, want none", read.ReportedPRs)
	}
	if got := ladoReportedListed(t, s, 10); !slices.Equal(got, []domain.SessionID{withPR.SessionID}) {
		t.Fatalf("listed after the claim = %v, want the first only", got)
	}
}

// A reported PR another session has, running or ended, is refused as owned
// elsewhere; one LADO refused carries its reason, the newer one when it is
// refused again. Neither is listed, and the execution without a PR stays
// Running with the reason.
func TestLadoReportedPRRefusals(t *testing.T) {
	s, raw := openLadoMultiRepoStore(t)
	ctx := context.Background()
	at := ladoVerifyAt
	active := mustLadoSession(t, s, "mer", false)
	ended := mustLadoSession(t, s, "mer", true)
	activePR := mustWriteLadoRepoPR(t, s, active.ID, "back", 3, domain.PRStateOpen, ladoHead('c'), domain.MergeMergeable, at)
	endedPR := mustWriteLadoRepoPR(t, s, ended.ID, "front", 4, domain.PRStateOpen, ladoHead('d'), domain.MergeMergeable, at)

	exec := mustLadoExecution(t, s, "1", at, false)
	closedPR := "https://github.com/acme/docs/pull/6"
	mustReportLadoPRs(t, s, mustLadoReportedSession(t, s, exec), at, activePR, endedPR, closedPR)
	read := mustReadLadoExecution(t, s, exec.SessionID)
	want := []domain.LadoReportedPR{
		{URL: activePR, Refused: true, Reason: domain.LadoReportedPROwnedElsewhere},
		{URL: endedPR, Refused: true, Reason: domain.LadoReportedPROwnedElsewhere},
		{URL: closedPR},
	}
	if !slices.Equal(read.ReportedPRs, want) {
		t.Fatalf("reported PRs = %+v, want %+v", read.ReportedPRs, want)
	}
	if got := ladoReportedListed(t, s, 10); !slices.Equal(got, []domain.SessionID{exec.SessionID}) {
		t.Fatalf("listed = %v, want the execution for its closed PR", got)
	}

	var seq int64
	if err := raw.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	changes := func() int {
		t.Helper()
		var n int
		if err := raw.QueryRow(`SELECT COUNT(*) FROM change_log WHERE seq > ? AND json_extract(payload, '$.kind') = 'lado_execution'
			AND json_extract(payload, '$.sessionId') = ? AND session_id = ?`, seq, string(exec.SessionID), string(exec.SessionID)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := s.RefuseLadoReportedPR(ctx, exec.SessionID, closedPR, domain.LadoReportedPRNotOpen, at.Add(time.Minute)); err != nil {
		t.Fatalf("RefuseLadoReportedPR: %v", err)
	}
	if n := changes(); n != 1 {
		t.Fatalf("the refusal published %d execution changes, want 1", n)
	}
	read = mustReadLadoExecution(t, s, exec.SessionID)
	if got := read.ReportedPRs[2]; got != (domain.LadoReportedPR{URL: closedPR, Refused: true, Reason: domain.LadoReportedPRNotOpen}) {
		t.Fatalf("refused PR = %+v", got)
	}
	if state, reason := read.State(); state != domain.LadoTaskRunning || reason != domain.LadoRunningReportedPRUntracked {
		t.Fatalf("state = %s (%q), want running with reported_pr_untracked", state, reason)
	}
	if got := ladoReportedListed(t, s, 10); len(got) != 0 {
		t.Fatalf("listed after the refusal = %v, want none", got)
	}
	if err := s.RefuseLadoReportedPR(ctx, exec.SessionID, closedPR, domain.LadoReportedPRUnreachable, at.Add(2*time.Minute)); err != nil {
		t.Fatalf("RefuseLadoReportedPR again: %v", err)
	}
	if got := mustReadLadoExecution(t, s, exec.SessionID).ReportedPRs[2].Reason; got != domain.LadoReportedPRUnreachable {
		t.Fatalf("reason after the second refusal = %q, want unreachable", got)
	}
	if n := changes(); n != 2 {
		t.Fatalf("the new reason published %d execution changes in all, want 2", n)
	}

	for name, err := range map[string]error{
		"an unknown reason": s.RefuseLadoReportedPR(ctx, exec.SessionID, closedPR, "nope", at),
		"no execution":      s.RefuseLadoReportedPR(ctx, active.ID, closedPR, domain.LadoReportedPRInvalid, at),
		"no PR":             s.RefuseLadoReportedPR(ctx, exec.SessionID, "", domain.LadoReportedPRInvalid, at),
		"no time":           s.RefuseLadoReportedPR(ctx, exec.SessionID, closedPR, domain.LadoReportedPRInvalid, time.Time{}),
	} {
		if err == nil {
			t.Fatalf("refusing with %s was accepted", name)
		}
	}
}

// Only a running, unsettled single or integration worker's execution is
// listed: a part worker's has no reported PRs, and an ended session's or a
// settled execution's are not tried.
func TestLadoReportedPRListSkipsOtherExecutions(t *testing.T) {
	s, raw := openLadoMultiRepoStore(t)
	at := ladoVerifyAt
	report := func(key string, terminated bool) domain.LadoExecution {
		t.Helper()
		exec := mustLadoExecution(t, s, key, at, terminated)
		mustReportLadoPRs(t, s, mustLadoReportedSession(t, s, exec), at, fmt.Sprintf("https://github.com/acme/back/pull/%s", key))
		return exec
	}
	ended := report("1", true)
	part := report("2", false)
	settled := report("3", false)
	integration := report("4", false)
	mustExecRaw(t, raw, `UPDATE lado_executions SET role = 'part', part_id = 'a' WHERE session_id = ?`, string(part.SessionID))
	mustExecRaw(t, raw, `UPDATE lado_executions SET settled_at = ?, settled_pr_url = 'https://github.com/acme/back/pull/30', settled_pr_state = 'merged' WHERE session_id = ?`,
		at, string(settled.SessionID))
	mustExecRaw(t, raw, `UPDATE lado_executions SET role = 'integration' WHERE session_id = ?`, string(integration.SessionID))

	read := mustReadLadoExecution(t, s, ended.SessionID)
	if len(read.ReportedPRs) != 1 || read.PendingReportedPRs() != nil {
		t.Fatalf("ended execution = reported %+v, pending %+v; want one reported, none pending", read.ReportedPRs, read.PendingReportedPRs())
	}
	if state, reason := read.State(); state != domain.LadoTaskFailed || reason != domain.LadoFailedEndedWithoutPR {
		t.Fatalf("ended execution state = %s (%q), want failed ended_without_pr", state, reason)
	}
	if read := mustReadLadoExecution(t, s, part.SessionID); len(read.ReportedPRs) != 0 {
		t.Fatalf("part execution reported PRs = %+v, want none", read.ReportedPRs)
	}
	if got := ladoReportedListed(t, s, 10); !slices.Equal(got, []domain.SessionID{integration.SessionID}) {
		t.Fatalf("listed = %v, want the integration execution only", got)
	}
}

func mustExecRaw(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}
