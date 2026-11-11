package ladowatch

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const sessionLimit = "You've hit your session limit · resets 8:20pm (Asia/Seoul)"

// reset is when sessionLimit lifts after t0: 20:20 in Seoul.
var reset = time.Date(2026, 10, 29, 11, 20, 0, 0, time.UTC)

func TestTickCountsCompactionsBeforeItWatches(t *testing.T) {
	f := newFixture(t)
	f.limitAt(t0, sessionLimit)
	f.tick(reset.Add(domain.LadoWakeResumeDelay))
	if f.counter.calls != 1 {
		t.Fatalf("the counter ran %d times", f.counter.calls)
	}
	if len(f.trace) < 2 || f.trace[0] != "count" || f.trace[1] != "send" {
		t.Fatalf("the tick ran %v; the count must come before the watch", f.trace)
	}
}

func TestLimitIsRecordedWithItsResetAndTheAccount(t *testing.T) {
	f := newFixture(t)
	f.svc.deps.Accounts = fakeAccounts{ids: map[domain.SessionID]string{worker1: "acct-2"}}
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))

	w := f.watch()
	if w.LimitState != domain.LadoLimitLimited || !w.LimitResetAt.Equal(reset) || w.LimitResetGuessed || !w.LimitSeenAt.Equal(t0) {
		t.Fatalf("limit watch = %+v", w.LadoWatchLimit)
	}
	l, ok := f.store.limits["claude:acct-2"]
	if !ok || !l.ResetAt.Equal(reset) || l.SessionID != worker1 {
		t.Fatalf("account limits = %+v", f.store.limits)
	}
	if !slices.Equal(f.store.alertKinds(), []domain.LadoAlertKind{domain.LadoAlertLimited}) {
		t.Fatalf("alerts = %v", f.store.alertKinds())
	}
	if f.store.auditCount("lado.limit_detected") != 1 {
		t.Fatalf("audits = %+v", f.store.audits)
	}
	// A second tick over the same turn records nothing new.
	f.tick(t0.Add(2 * time.Minute))
	if len(f.store.alerts) != 1 || f.store.auditCount("lado.limit_detected") != 1 {
		t.Fatalf("the same stop was recorded again: %v", f.store.alertKinds())
	}
}

func TestLimitedWorkerIsResumedFiveMinutesAfterTheReset(t *testing.T) {
	f := newFixture(t)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	f.tick(reset)
	f.tick(reset.Add(domain.LadoWakeResumeDelay - time.Second))
	if len(f.msgs.texts()) != 0 {
		t.Fatalf("resumed before the reset's delay: %v", f.msgs.texts())
	}
	f.tick(reset.Add(domain.LadoWakeResumeDelay))
	if !slices.Equal(f.msgs.texts(), []string{domain.LadoWakeLimitResumeText}) {
		t.Fatalf("sent %v", f.msgs.texts())
	}
	w := f.watch()
	if w.Resumes != 1 || !w.ResumedAt.Equal(reset.Add(domain.LadoWakeResumeDelay)) {
		t.Fatalf("limit watch = %+v", w.LadoWatchLimit)
	}
	if f.store.auditCount("lado.nudge_sent") != 1 {
		t.Fatalf("audits = %+v", f.store.audits)
	}
	if !slices.Contains(f.store.alertKinds(), domain.LadoAlertLimitResumed) {
		t.Fatalf("alerts = %v", f.store.alertKinds())
	}
	// The resume of one stop goes once, even while its turn has not come.
	f.tick(reset.Add(domain.LadoWakeResumeDelay + time.Minute))
	if len(f.msgs.texts()) != 1 {
		t.Fatalf("sent %v", f.msgs.texts())
	}
}

func TestLimitResumesStopAtTheSessionCap(t *testing.T) {
	f := newFixture(t)
	at := t0
	for i := 1; i <= domain.LadoWakeMaxResumes+1; i++ {
		// Each stop happens after the resume before it, and resets in an
		// hour (8:20pm, then each hour after as a guess would).
		f.limitAt(at, "You've hit your session limit")
		f.tick(at.Add(time.Minute))
		f.tick(at.Add(domain.LadoLimitGuessedReset + domain.LadoWakeResumeDelay))
		want := min(i, domain.LadoWakeMaxResumes)
		if got := len(f.msgs.texts()); got != want {
			t.Fatalf("after stop %d: sent %d resumes, want %d", i, got, want)
		}
		at = at.Add(2 * time.Hour)
	}
	if w := f.watch(); w.Resumes != domain.LadoWakeMaxResumes || w.LimitState != domain.LadoLimitLimited {
		t.Fatalf("limit watch = %+v", w.LadoWatchLimit)
	}
}

func TestNoResumeWhileATurnRunsOrTheWorkerAsked(t *testing.T) {
	f := newFixture(t)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	due := reset.Add(domain.LadoWakeResumeDelay)

	// The owner typed into the worker: its new turn runs. It is not past the
	// limit until that turn ends.
	f.work(due)
	f.tick(due)
	if len(f.msgs.texts()) != 0 || f.watch().LimitState != domain.LadoLimitLimited {
		t.Fatalf("resumed a busy worker: %v %+v", f.msgs.texts(), f.watch().LadoWatchLimit)
	}

	// The worker asked the owner a question before its limit.
	f2 := newFixture(t)
	f2.store.reports[worker1] = domain.ReportNeedsInput
	f2.limitAt(t0, sessionLimit)
	f2.tick(t0.Add(time.Minute))
	f2.tick(due)
	if len(f2.msgs.texts()) != 0 {
		t.Fatalf("answered for the owner: %v", f2.msgs.texts())
	}

	// A queued turn is a turn on its way.
	f3 := newFixture(t)
	f3.limitAt(t0, sessionLimit)
	f3.tick(t0.Add(time.Minute))
	f3.convs.set(worker1, domain.ConversationTurn{ID: "queued", State: domain.TurnStateQueued, RequestedAt: due})
	f3.tick(due)
	if len(f3.msgs.texts()) != 0 {
		t.Fatalf("resumed with a queued turn: %v", f3.msgs.texts())
	}
}

func TestBusyAtTheResetIsResumedOnALaterTick(t *testing.T) {
	f := newFixture(t)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	due := reset.Add(domain.LadoWakeResumeDelay)
	// Busy only in AO's activity reading: the turn is still the failed one.
	rec, _, _ := f.sessions.Session(context.Background(), worker1)
	rec.Activity.State = domain.ActivityActive
	f.sessions.set(rec)
	f.tick(due)
	if len(f.msgs.texts()) != 0 {
		t.Fatalf("resumed a busy worker: %v", f.msgs.texts())
	}
	f.rest(due)
	f.tick(due.Add(time.Minute))
	if len(f.msgs.texts()) != 1 {
		t.Fatalf("sent %v", f.msgs.texts())
	}
}

func TestLimitLiftsWhenALaterTurnCompletes(t *testing.T) {
	f := newFixture(t)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	key := "claude:device-claude"
	if _, ok := f.store.limits[key]; !ok {
		t.Fatalf("account limits = %+v", f.store.limits)
	}
	due := reset.Add(domain.LadoWakeResumeDelay)
	f.tick(due)
	f.endTurn(domain.TurnStateCompleted, "", due.Add(10*time.Minute))
	f.rest(due.Add(10 * time.Minute))
	f.tick(due.Add(11 * time.Minute))
	w := f.watch()
	if w.LimitState != domain.LadoLimitNone || w.Resumes != 1 {
		t.Fatalf("limit watch = %+v", w.LadoWatchLimit)
	}
	// The reset released the account's limit before that turn.
	if l, ok := f.store.limit(key); !ok || l.ReleaseReason != domain.LadoLimitReleaseReset {
		t.Fatalf("account limit = %+v", l)
	}

	// A turn that completes before the reset releases the account's limit.
	f2 := newFixture(t)
	f2.limitAt(t0, sessionLimit)
	f2.tick(t0.Add(time.Minute))
	back := t0.Add(30 * time.Minute)
	f2.endTurn(domain.TurnStateCompleted, "", back)
	f2.rest(back)
	f2.tick(back.Add(time.Minute))
	if l, ok := f2.store.limit(key); !ok || l.ReleaseReason != domain.LadoLimitReleaseTurnCompleted || l.Active(back.Add(time.Minute)) {
		t.Fatalf("the account's limit was not released after a completed turn: %+v", l)
	}
	if _, ok, _ := f2.svc.LaunchAccountLimit(context.Background(), domain.HarnessClaudeCode, back.Add(time.Minute)); ok {
		t.Fatal("new workers are still held after the completed turn")
	}
}

func TestResumeThatDidNotGoIsGivenBack(t *testing.T) {
	f := newFixture(t)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	f.msgs.fail = errors.New("send failed")
	due := reset.Add(domain.LadoWakeResumeDelay)
	f.tick(due) // the error is logged, not returned
	if w := f.watch(); w.Resumes != 0 || !w.ResumedAt.IsZero() {
		t.Fatalf("limit watch = %+v", w.LadoWatchLimit)
	}
	f.msgs.fail = nil
	f.tick(due.Add(time.Minute))
	if len(f.msgs.texts()) != 1 || f.watch().Resumes != 1 {
		t.Fatalf("sent %v, watch %+v", f.msgs.texts(), f.watch().LadoWatchLimit)
	}
}

func TestStalledWorkerIsNudgedOnceThenOnlyAlerted(t *testing.T) {
	f := newFixture(t)
	stall := domain.LadoWatchDefaults().Stall()
	f.tick(t0.Add(stall - time.Minute))
	if len(f.msgs.texts()) != 0 || f.watch().StallState != domain.LadoStallNone {
		t.Fatalf("stalled too early: %v", f.msgs.texts())
	}
	f.tick(t0.Add(stall))
	if !slices.Equal(f.msgs.texts(), []string{domain.LadoWakeStallNudgeText}) {
		t.Fatalf("sent %v", f.msgs.texts())
	}
	if w := f.watch(); w.StallState != domain.LadoStallStalled || w.Nudges != 1 {
		t.Fatalf("stall watch = %+v", w.LadoWatchStall)
	}
	f.tick(t0.Add(stall + time.Hour))
	if len(f.msgs.texts()) != 1 {
		t.Fatalf("nudged twice in one stall: %v", f.msgs.texts())
	}

	// The worker answered the nudge, then went quiet again.
	back := t0.Add(stall + 2*time.Hour)
	f.endTurn(domain.TurnStateCompleted, "", back)
	f.rest(back)
	f.tick(back.Add(time.Minute))
	if f.watch().StallState != domain.LadoStallNone {
		t.Fatalf("stall watch = %+v", f.watch().LadoWatchStall)
	}
	f.tick(back.Add(stall))
	if len(f.msgs.texts()) != 1 {
		t.Fatalf("nudged a second time: %v", f.msgs.texts())
	}
	if !slices.Equal(f.store.alertKinds(), []domain.LadoAlertKind{domain.LadoAlertStallNudged, domain.LadoAlertStalled}) {
		t.Fatalf("alerts = %v", f.store.alertKinds())
	}
	if f.store.auditCount("lado.nudge_sent") != 1 {
		t.Fatalf("audits = %+v", f.store.audits)
	}
}

func TestStallLeavesOutOpenPRsReportsAndPlanWorkers(t *testing.T) {
	stall := domain.LadoWatchDefaults().Stall()
	cases := map[string]func(*fixture){
		"a run with a PR": func(f *fixture) {
			f.store.prs[worker1] = domain.LadoExecutionPR{URL: "https://github.com/o/r/pull/9", Number: 9}
		},
		"a question to the owner": func(f *fixture) { f.store.reports[worker1] = domain.ReportNeedsInput },
		"a done report":           func(f *fixture) { f.store.reports[worker1] = domain.ReportDone },
		"an unknown role":         func(f *fixture) { f.store.execs[0].Role = "reviewer" },
		"a running turn":          func(f *fixture) { f.work(t0) },
		"an unread conversation":  func(f *fixture) { delete(f.convs.turns, worker1) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			change(f)
			f.tick(t0.Add(stall + time.Hour))
			if len(f.msgs.texts()) != 0 || len(f.store.alerts) != 0 {
				t.Fatalf("sent %v, alerts %v", f.msgs.texts(), f.store.alertKinds())
			}
		})
	}
	// The one worker of a task without parts is watched like a part.
	f := newFixture(t)
	f.store.execs[0].Role, f.store.execs[0].PartID = domain.LadoRoleSingle, ""
	f.tick(t0.Add(stall))
	if len(f.msgs.texts()) != 1 {
		t.Fatalf("the single worker was not nudged: %v", f.msgs.texts())
	}
}

func TestWakeOffSendsNothing(t *testing.T) {
	f := newFixture(t)
	settings := domain.LadoWatchDefaults()
	settings.WakeEnabled = false
	f.store.settings[proj1] = settings
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	f.tick(reset.Add(domain.LadoWakeResumeDelay))
	f.tick(reset.Add(24 * time.Hour))
	if len(f.msgs.texts()) != 0 {
		t.Fatalf("sent %v", f.msgs.texts())
	}
	// A stall it may not wake only alerts.
	f2 := newFixture(t)
	f2.store.settings[proj1] = settings
	f2.tick(t0.Add(settings.Stall()))
	if len(f2.msgs.texts()) != 0 || !slices.Equal(f2.store.alertKinds(), []domain.LadoAlertKind{domain.LadoAlertStalled}) {
		t.Fatalf("sent %v, alerts %v", f2.msgs.texts(), f2.store.alertKinds())
	}
}

func TestLimitComesBeforeTheStall(t *testing.T) {
	f := newFixture(t)
	f.limitAt(t0, "You've hit your weekly limit · resets Oct 31, 11am (Asia/Seoul)")
	// Two days later the weekly limit has not reset yet.
	f.tick(t0.Add(time.Minute))
	f.tick(t0.Add(40 * time.Hour))
	if len(f.msgs.texts()) != 0 || f.watch().StallState != domain.LadoStallNone {
		t.Fatalf("a limited worker was taken as stalled: %v %+v", f.msgs.texts(), f.watch().LadoWatchStall)
	}
}

func TestProjectStallTimeApplies(t *testing.T) {
	f := newFixture(t)
	settings := domain.LadoWatchDefaults()
	settings.StallMinutes = 30
	f.store.settings[proj1] = settings
	f.tick(t0.Add(30 * time.Minute))
	if len(f.msgs.texts()) != 1 {
		t.Fatalf("sent %v", f.msgs.texts())
	}
}

func TestEndedSessionsAreLeftAlone(t *testing.T) {
	f := newFixture(t)
	rec, _, _ := f.sessions.Session(context.Background(), worker1)
	rec.IsTerminated = true
	f.sessions.set(rec)
	f.limitAt(t0, sessionLimit)
	rec.IsTerminated = true
	f.sessions.set(rec)
	f.tick(t0.Add(time.Minute))
	if _, ok := f.store.watches[worker1]; ok {
		t.Fatalf("watched an ended session")
	}
}

func TestLaunchAccountLimitReadsTheDefaultAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.store.limits["claude:device-claude"] = domain.LadoAccountLimit{AccountKey: "claude:device-claude", ResetAt: reset}

	l, ok, err := f.svc.LaunchAccountLimit(ctx, domain.HarnessClaudeCode, t0)
	if err != nil || !ok || !l.ResetAt.Equal(reset) {
		t.Fatalf("got %+v %v %v", l, ok, err)
	}
	if _, ok, _ := f.svc.LaunchAccountLimit(ctx, domain.HarnessClaudeCode, reset); ok {
		t.Fatalf("a limit held at its reset")
	}
	// A saved default account is its own account.
	f.store.defaults[domain.AgentAccountProviderClaude] = "acct-2"
	if _, ok, _ := f.svc.LaunchAccountLimit(ctx, domain.HarnessClaudeCode, t0); ok {
		t.Fatalf("another account's limit held")
	}
	// Codex's accounts are apart from Claude's.
	if _, ok, _ := f.svc.LaunchAccountLimit(ctx, domain.HarnessCodex, t0); ok {
		t.Fatalf("Claude's limit held Codex")
	}
	// An agent without accounts never holds.
	if _, ok, err := f.svc.LaunchAccountLimit(ctx, domain.HarnessAider, t0); ok || err != nil {
		t.Fatalf("got %v %v", ok, err)
	}
}

func TestTickReleasesLimitsPastTheirReset(t *testing.T) {
	f := newFixture(t)
	f.store.limits["a"] = domain.LadoAccountLimit{AccountKey: "a", ResetAt: t0}
	f.store.limits["b"] = domain.LadoAccountLimit{AccountKey: "b", ResetAt: t0.Add(time.Hour)}
	f.tick(t0)
	if l, _ := f.store.limit("a"); l.ReleaseReason != domain.LadoLimitReleaseReset || !l.ReleasedAt.Equal(t0) {
		t.Fatalf("kept a limit past its reset: %+v", l)
	}
	if l, _ := f.store.limit("b"); l.Released() {
		t.Fatalf("released a limit before its reset: %+v", l)
	}
	// A released limit is not released or counted again.
	f.tick(t0.Add(time.Minute))
	if f.store.auditCount("lado.limit_released") != 1 || f.store.waitCounts != 1 {
		t.Fatalf("released again: audits %+v, counted %d times", f.store.audits, f.store.waitCounts)
	}
}

func TestLimitRecordFailureIsLeftForTheNextTick(t *testing.T) {
	f := newFixture(t)
	f.limitAt(t0, sessionLimit)
	f.store.failLimitWrite = 1
	f.tick(t0.Add(time.Minute))
	if f.watch().LimitState != domain.LadoLimitNone {
		t.Fatalf("limit watch = %+v", f.watch().LadoWatchLimit)
	}
	f.tick(t0.Add(2 * time.Minute))
	if f.watch().LimitState != domain.LadoLimitLimited {
		t.Fatalf("limit watch = %+v", f.watch().LadoWatchLimit)
	}
}

// countAlerts counts the alerts of kind.
func (f *fixture) countAlerts(kind domain.LadoAlertKind) int {
	n := 0
	for _, k := range f.store.alertKinds() {
		if k == kind {
			n++
		}
	}
	return n
}

// Every limit stop is its own alert: with waking off, and past the resume cap
// when LADO no longer resumes and the owner most needs to know.
func TestEveryLimitStopRaisesItsOwnAlert(t *testing.T) {
	for _, wake := range []bool{true, false} {
		f := newFixture(t)
		settings := domain.LadoWatchDefaults()
		settings.WakeEnabled = wake
		f.store.settings[proj1] = settings
		at := t0
		stops := domain.LadoWakeMaxResumes + 2
		for range stops {
			f.limitAt(at, "You've hit your session limit")
			f.tick(at.Add(time.Minute))
			f.tick(at.Add(domain.LadoLimitGuessedReset + domain.LadoWakeResumeDelay))
			at = at.Add(2 * time.Hour)
		}
		if got := f.countAlerts(domain.LadoAlertLimited); got != stops {
			t.Fatalf("wake %v: %d limit alerts for %d stops", wake, got, stops)
		}
		if w := f.watch(); w.Stops != stops {
			t.Fatalf("wake %v: limit watch = %+v, want %d stops", wake, w.LadoWatchLimit, stops)
		}
	}
}

// A stall after the one nudge alerts, and so does each stall after it.
func TestEveryLaterStallRaisesItsOwnAlert(t *testing.T) {
	f := newFixture(t)
	stall := domain.LadoWatchDefaults().Stall()
	at := t0
	for range 3 {
		f.tick(at.Add(stall))
		// The worker moves again, then rests.
		at = at.Add(stall + time.Hour)
		f.endTurn(domain.TurnStateCompleted, "", at)
		f.rest(at)
		f.tick(at.Add(time.Minute))
	}
	if got := len(f.msgs.texts()); got != domain.LadoWakeMaxNudges {
		t.Fatalf("sent %d nudges, want %d", got, domain.LadoWakeMaxNudges)
	}
	if nudged, stalled := f.countAlerts(domain.LadoAlertStallNudged), f.countAlerts(domain.LadoAlertStalled); nudged != 1 || stalled != 2 {
		t.Fatalf("alerts = %v, want one nudge alert and a stall alert for each later stall", f.store.alertKinds())
	}
	if w := f.watch(); w.Stalls != 3 {
		t.Fatalf("stall watch = %+v, want 3 stalls", w.LadoWatchStall)
	}
}

// A worker whose newest turn the owner stopped (interrupted) or took back
// (cancelled) is never nudged: LADO only alerts.
func TestOwnerStoppedWorkerIsNotNudged(t *testing.T) {
	for _, state := range []domain.TurnState{domain.TurnStateInterrupted, domain.TurnStateCancelled} {
		f := newFixture(t)
		f.endTurn(state, "", t0)
		f.rest(t0)
		stall := domain.LadoWatchDefaults().Stall()
		f.tick(t0.Add(stall))
		f.tick(t0.Add(stall + time.Hour))
		if len(f.msgs.texts()) != 0 {
			t.Fatalf("%s: sent %v to a worker the owner stopped", state, f.msgs.texts())
		}
		if !slices.Equal(f.store.alertKinds(), []domain.LadoAlertKind{domain.LadoAlertStalled}) {
			t.Fatalf("%s: alerts = %v, want one stall alert", state, f.store.alertKinds())
		}
	}
}
