package ladowatch

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	deviceKey = "claude:device-claude"
	deviceID  = "device-claude"
	fpA       = "sha256:aaaa"
	fpB       = "sha256:bbbb"

	worker2 domain.SessionID  = "s-2"
	task2   domain.LadoTaskID = "task-2"
	proj2   domain.ProjectID  = "proj-2"
)

// withIdentities lets the fixture read logins: the device account answers fp.
func (f *fixture) withIdentities(fp string) *fakeIdentities {
	ids := &fakeIdentities{fps: map[string]string{deviceID: fp}}
	f.svc.deps.Identities = ids
	return ids
}

// sentTo lists the messages that went to session id.
func (f *fixture) sentTo(id domain.SessionID) []string {
	f.msgs.mu.Lock()
	defer f.msgs.mu.Unlock()
	var out []string
	for _, s := range f.msgs.sent {
		if s.to == id {
			out = append(out, s.text)
		}
	}
	return out
}

func (f *fixture) launchHeld(at time.Time) bool {
	f.t.Helper()
	_, ok, err := f.svc.LaunchAccountLimit(context.Background(), domain.HarnessClaudeCode, at)
	if err != nil {
		f.t.Fatalf("launch account limit: %v", err)
	}
	return ok
}

func TestLimitRecordsTheLoginAndLiftsWhenItChanges(t *testing.T) {
	f := newFixture(t)
	ids := f.withIdentities(fpA)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	if l, _ := f.store.limit(deviceKey); l.Identity != fpA || !l.Active(t0.Add(time.Minute)) {
		t.Fatalf("account limit = %+v", l)
	}

	ids.set(deviceID, fpB)
	f.tick(t0.Add(2 * time.Minute))
	l, _ := f.store.limit(deviceKey)
	if l.ReleaseReason != domain.LadoLimitReleaseAccountChanged || !l.ReleasedAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("account limit = %+v", l)
	}
	events := f.store.audited("lado.limit_released")
	if len(events) != 1 || events[0].SubjectKind != "account" || events[0].SubjectID != deviceKey || events[0].Actor != "lado" {
		t.Fatalf("release audits = %+v", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil || payload["reason"] != "account_changed" {
		t.Fatalf("release payload = %s (%v)", events[0].Payload, err)
	}
	if f.launchHeld(t0.Add(2 * time.Minute)) {
		t.Fatal("new workers are held after the login changed")
	}
}

func TestLaunchAccountLimitLiftsWhenTheLoginChanged(t *testing.T) {
	f := newFixture(t)
	ids := f.withIdentities(fpA)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	if !f.launchHeld(t0.Add(time.Minute)) {
		t.Fatal("the limit did not hold new workers")
	}
	ids.set(deviceID, fpB)
	if f.launchHeld(t0.Add(2 * time.Minute)) {
		t.Fatal("a changed login still held new workers")
	}
	if l, _ := f.store.limit(deviceKey); l.ReleaseReason != domain.LadoLimitReleaseAccountChanged {
		t.Fatalf("account limit = %+v", l)
	}
	if f.store.auditCount("lado.limit_released") != 1 {
		t.Fatalf("audits = %+v", f.store.audits)
	}
}

func TestLimitHoldsWhenTheLoginIsTheSameOrUnknown(t *testing.T) {
	cases := map[string]func(*fakeIdentities){
		"the same login":   func(ids *fakeIdentities) {},
		"a failed read":    func(ids *fakeIdentities) { ids.fail = errors.New("claude auth status timed out") },
		"an unknown login": func(ids *fakeIdentities) { ids.set(deviceID, "") },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ids := f.withIdentities(fpA)
			f.limitAt(t0, sessionLimit)
			f.tick(t0.Add(time.Minute))
			change(ids)
			f.tick(t0.Add(2 * time.Minute))
			if !f.launchHeld(t0.Add(4 * time.Minute)) {
				t.Fatal("the limit did not hold")
			}
			if l, _ := f.store.limit(deviceKey); l.Released() || l.Identity != fpA {
				t.Fatalf("account limit = %+v", l)
			}
			if ids.callCount() < 3 {
				t.Fatalf("the login was read %d times", ids.callCount())
			}
		})
	}

	// A limit recorded without a login is never checked.
	f := newFixture(t)
	ids := f.withIdentities("")
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	recorded := ids.callCount()
	ids.set(deviceID, fpB)
	f.tick(t0.Add(3 * time.Minute))
	if !f.launchHeld(t0.Add(5*time.Minute)) || ids.callCount() != recorded {
		t.Fatalf("a limit without a login was checked: %d reads after %d", ids.callCount(), recorded)
	}
}

func TestLoginIsCheckedOnceAMinuteAKey(t *testing.T) {
	f := newFixture(t)
	ids := f.withIdentities(fpA)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	base := ids.callCount() // the read the record made
	start := t0.Add(90 * time.Second)
	for _, at := range []time.Duration{0, time.Second, 20 * time.Second, 59 * time.Second} {
		f.launchHeld(start.Add(at))
	}
	f.tick(start.Add(30 * time.Second))
	if got := ids.callCount() - base; got != 1 {
		t.Fatalf("the login was read %d times in one minute", got)
	}
	f.launchHeld(start.Add(time.Minute))
	if got := ids.callCount() - base; got != 2 {
		t.Fatalf("the login was read %d times in two minutes", got)
	}
}

func TestLaunchDoesNotWaitForARunningCheck(t *testing.T) {
	f := newFixture(t)
	ids := f.withIdentities(fpA)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	base := ids.callCount()
	ids.mu.Lock()
	ids.block, ids.entered = make(chan struct{}), make(chan struct{}, 1)
	ids.fps[deviceID] = fpB
	ids.mu.Unlock()

	done := make(chan bool)
	go func() {
		_, ok, _ := f.svc.LaunchAccountLimit(context.Background(), domain.HarnessClaudeCode, t0.Add(2*time.Minute))
		done <- ok
	}()
	<-ids.entered
	// A second launch while the read runs answers at once: the limit holds.
	if !f.launchHeld(t0.Add(3 * time.Minute)) {
		t.Fatal("the second launch did not get the limit as it was")
	}
	close(ids.block)
	if <-done {
		t.Fatal("the launch that read the changed login still held")
	}
	if got := ids.callCount() - base; got != 1 {
		t.Fatalf("the login was read %d times", got)
	}
}

func TestLiveLimitKeepsItsFirstLoginOverALaterRecord(t *testing.T) {
	f := newFixture(t)
	ids := f.withIdentities(fpA)
	f.addWorker(worker2, task2, proj1, t0.Add(-time.Hour))
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	f.launchHeld(t0.Add(70 * time.Second)) // the first check: the same login

	// The login changes; within the minute another worker hits the limit.
	ids.set(deviceID, fpB)
	f.limitOf(worker2, t0.Add(80*time.Second), sessionLimit)
	f.tick(t0.Add(100 * time.Second))
	l, _ := f.store.limit(deviceKey)
	if l.Released() || l.Identity != fpA || f.store.records != 2 {
		t.Fatalf("account limit after the second record = %+v (%d records)", l, f.store.records)
	}
	f.tick(t0.Add(130 * time.Second))
	if l, _ := f.store.limit(deviceKey); l.ReleaseReason != domain.LadoLimitReleaseAccountChanged {
		t.Fatalf("account limit = %+v", l)
	}
}

func TestEarlyReleaseResumesLimitedWorkersOfEveryProject(t *testing.T) {
	f := newFixture(t)
	f.addWorker(worker2, task2, proj2, t0.Add(-time.Hour))
	f.limitAt(t0, sessionLimit)
	f.limitOf(worker2, t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	f.tick(t0.Add(5 * time.Minute))
	if len(f.msgs.texts()) != 0 {
		t.Fatalf("resumed before any release: %v", f.msgs.texts())
	}
	if _, _, err := f.svc.ReleaseAccountLimit(context.Background(), deviceKey, "switched to the sub account"); err != nil {
		t.Fatal(err)
	}
	f.tick(t0.Add(6 * time.Minute))
	want := []string{domain.LadoWakeLimitResumeText}
	if !slices.Equal(f.sentTo(worker1), want) || !slices.Equal(f.sentTo(worker2), want) {
		t.Fatalf("sent %v to s-1 and %v to s-2", f.sentTo(worker1), f.sentTo(worker2))
	}
	// One stop gets one resume.
	f.tick(t0.Add(7 * time.Minute))
	if len(f.msgs.texts()) != 2 {
		t.Fatalf("sent %v", f.msgs.texts())
	}
}

func TestEarlyReleaseKeepsTheResumeCap(t *testing.T) {
	f := newFixture(t)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	f.store.mu.Lock()
	w := f.store.watches[worker1]
	w.Resumes = domain.LadoWakeMaxResumes
	f.store.watches[worker1] = w
	f.store.mu.Unlock()
	if _, _, err := f.svc.ReleaseAccountLimit(context.Background(), deviceKey, "switched"); err != nil {
		t.Fatal(err)
	}
	f.tick(t0.Add(2 * time.Minute))
	if len(f.msgs.texts()) != 0 {
		t.Fatalf("resumed past the cap: %v", f.msgs.texts())
	}
}

func TestReleaseAtTheResetWaitsForTheResumeDelay(t *testing.T) {
	f := newFixture(t)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	f.tick(reset)
	if l, _ := f.store.limit(deviceKey); l.ReleaseReason != domain.LadoLimitReleaseReset {
		t.Fatalf("account limit = %+v", l)
	}
	f.tick(reset.Add(domain.LadoWakeResumeDelay - time.Second))
	if len(f.msgs.texts()) != 0 {
		t.Fatalf("resumed before the reset's delay: %v", f.msgs.texts())
	}
	f.tick(reset.Add(domain.LadoWakeResumeDelay))
	if len(f.msgs.texts()) != 1 {
		t.Fatalf("sent %v", f.msgs.texts())
	}
}

func TestOlderSessionDoesNotHoldAnEarlyReleasedAccountAgain(t *testing.T) {
	releases := map[string]func(*fixture, *fakeIdentities, time.Time){
		"account_changed": func(f *fixture, ids *fakeIdentities, at time.Time) {
			ids.set(deviceID, fpB)
			f.tick(at)
		},
		"manual": func(f *fixture, _ *fakeIdentities, at time.Time) {
			f.now = at
			if _, _, err := f.svc.ReleaseAccountLimit(context.Background(), deviceKey, "switched"); err != nil {
				f.t.Fatal(err)
			}
		},
	}
	for reason, release := range releases {
		t.Run(reason, func(t *testing.T) {
			f := newFixture(t)
			ids := f.withIdentities(fpA)
			rec := f.record(worker1)
			rec.CreatedAt = t0.Add(-time.Hour)
			f.sessions.set(rec)
			f.limitAt(t0, sessionLimit)
			f.tick(t0.Add(time.Minute))
			releasedAt := t0.Add(10 * time.Minute)
			release(f, ids, releasedAt)
			if l, _ := f.store.limit(deviceKey); string(l.ReleaseReason) != reason {
				t.Fatalf("account limit = %+v", l)
			}

			// The older worker, still on its old login, hits the limit again.
			f.limitAt(t0.Add(20*time.Minute), sessionLimit)
			f.tick(t0.Add(21 * time.Minute))
			if w := f.watch(); w.LimitState != domain.LadoLimitLimited || w.Stops != 2 {
				t.Fatalf("limit watch = %+v", w.LadoWatchLimit)
			}
			if l, _ := f.store.limit(deviceKey); l.Active(t0.Add(21*time.Minute)) || string(l.ReleaseReason) != reason {
				t.Fatalf("an older session held the account again: %+v", l)
			}
			if f.launchHeld(t0.Add(21 * time.Minute)) {
				t.Fatal("new workers are held again")
			}

			// A worker made after the release that hits the limit holds it again.
			f.addWorker(worker2, task2, proj1, releasedAt.Add(time.Minute))
			f.limitOf(worker2, t0.Add(30*time.Minute), sessionLimit)
			f.tick(t0.Add(31 * time.Minute))
			l, _ := f.store.limit(deviceKey)
			if !l.Active(t0.Add(31*time.Minute)) || l.SessionID != worker2 {
				t.Fatalf("a newer session did not hold the account: %+v", l)
			}
			if !f.launchHeld(t0.Add(31 * time.Minute)) {
				t.Fatal("new workers are not held")
			}
		})
	}
}

func TestLimitAndReleaseTellEveryLadoProject(t *testing.T) {
	f := newFixture(t)
	f.store.ladoProjects = []domain.ProjectID{proj1, proj2}
	f.store.waiting = 3
	f.addWorker(worker2, task2, proj1, t0.Add(-time.Hour))
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	limited := "LADO 알림: Claude 계정 한도 — 새 워커 대기, 20:20 리셋"
	if got := f.store.noticeTexts(); !slices.Equal(got, []string{"proj-1 " + limited, "proj-2 " + limited}) {
		t.Fatalf("notices = %q", got)
	}
	// Another worker at the same live limit tells nothing new.
	f.limitOf(worker2, t0.Add(2*time.Minute), sessionLimit)
	f.tick(t0.Add(3 * time.Minute))
	if got := len(f.store.noticeTexts()); got != 2 {
		t.Fatalf("notices = %q", f.store.noticeTexts())
	}

	l, waiting, err := f.svc.ReleaseAccountLimit(context.Background(), deviceKey, "switched")
	if err != nil || waiting != 3 || l.ReleasedWaiting != 3 || l.ReleaseReason != domain.LadoLimitReleaseManual {
		t.Fatalf("release = %+v, %d, %v", l, waiting, err)
	}
	released := "LADO 알림: Claude 계정 한도 해제(수동 해제) — 계정 한도 대기 3건 다시 확인"
	if got := f.store.noticeTexts()[2:]; !slices.Equal(got, []string{"proj-1 " + released, "proj-2 " + released}) {
		t.Fatalf("notices = %q", got)
	}
	// A released limit is not counted again on later ticks.
	f.tick(t0.Add(10 * time.Minute))
	f.tick(reset.Add(time.Hour))
	if f.store.waitCounts != 1 || len(f.store.noticeTexts()) != 4 {
		t.Fatalf("counted %d times, notices %q", f.store.waitCounts, f.store.noticeTexts())
	}
}

func TestReleaseAccountLimitByHand(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.svc.ReleaseAccountLimit(ctx, deviceKey, "x"); !errors.Is(err, domain.ErrLadoAccountLimitNotFound) {
		t.Fatalf("release of no limit: %v", err)
	}
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	f.now = t0.Add(2 * time.Minute)
	l, _, err := f.svc.ReleaseAccountLimit(ctx, deviceKey, "switched to the sub account")
	if err != nil || l.ReleaseReason != domain.LadoLimitReleaseManual || !l.ReleasedAt.Equal(f.now) {
		t.Fatalf("release = %+v, %v", l, err)
	}
	events := f.store.audited("lado.limit_released")
	if len(events) != 1 || events[0].Actor != "api" || events[0].SubjectKind != "account" || events[0].SubjectID != deviceKey {
		t.Fatalf("release audits = %+v", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil ||
		payload["reason"] != "manual" || payload["note"] != "switched to the sub account" || payload["waiting"] != float64(0) {
		t.Fatalf("release payload = %s (%v)", events[0].Payload, err)
	}
	if _, _, err := f.svc.ReleaseAccountLimit(ctx, deviceKey, "again"); !errors.Is(err, domain.ErrLadoAccountLimitNotActive) {
		t.Fatalf("second release: %v", err)
	}
	if f.store.auditCount("lado.limit_released") != 1 {
		t.Fatalf("audits = %+v", f.store.audits)
	}

	// A limit whose reset passed is not active either.
	f2 := newFixture(t)
	f2.store.limits["codex:device-codex"] = domain.LadoAccountLimit{AccountKey: "codex:device-codex", ResetAt: t0}
	f2.now = t0.Add(time.Minute)
	if _, _, err := f2.svc.ReleaseAccountLimit(ctx, "codex:device-codex", "x"); !errors.Is(err, domain.ErrLadoAccountLimitNotActive) {
		t.Fatalf("release past the reset: %v", err)
	}

	limits, err := f.svc.AccountLimits(ctx)
	if err != nil || len(limits) != 1 || limits[0].AccountKey != deviceKey || !limits[0].Released() {
		t.Fatalf("account limits = %+v, %v", limits, err)
	}
}

func TestReleaseRecordsNoEmail(t *testing.T) {
	f := newFixture(t)
	ids := f.withIdentities(fpA)
	f.limitAt(t0, sessionLimit)
	f.tick(t0.Add(time.Minute))
	ids.set(deviceID, fpB)
	f.tick(t0.Add(2 * time.Minute))
	for _, ev := range f.store.audits {
		if strings.Contains(string(ev.Payload), "@") || strings.Contains(string(ev.Payload), "sha256:") {
			t.Fatalf("audit %s holds a login: %s", ev.Action, ev.Payload)
		}
	}
}
