package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var ladoWatchAt = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

// openLadoWatchStore returns a store with task lado-task-1 in project mer.
func openLadoWatchStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, _ := openLadoQueueStore(t)
	seedLadoTaskWithPlan(t, s)
	return s
}

func TestLadoWatchSettingsLayerAndRefuseOutOfRange(t *testing.T) {
	s := openLadoWatchStore(t)
	ctx := context.Background()
	intp := func(v int) *int { return &v }
	centsp := func(v int64) *int64 { return &v }
	boolp := func(v bool) *bool { return &v }

	if _, ok, err := s.LadoWatchSettings(ctx, domain.LadoWatchGlobalScope); err != nil || ok {
		t.Fatalf("global row on a fresh database = %v, %v; want none", ok, err)
	}
	got, err := s.LadoEffectiveWatchSettings(ctx, "mer")
	if err != nil || got != domain.LadoWatchDefaults() || got.CompactWindow != 200_000 {
		t.Fatalf("effective settings without rows = %+v, %v; want the defaults", got, err)
	}

	if err := s.PutLadoWatchSettings(ctx, domain.LadoWatchGlobalScope, domain.LadoWatchOverrides{
		CompactWindow: intp(300_000), TaskCostCents: centsp(50_00), WakeEnabled: boolp(false), UpdatedAt: ladoWatchAt,
	}); err != nil {
		t.Fatalf("put global row: %v", err)
	}
	if err := s.PutLadoWatchSettings(ctx, "mer", domain.LadoWatchOverrides{
		CompactWindow: intp(150_000), StallMinutes: intp(30), WakeEnabled: boolp(true),
	}); err != nil {
		t.Fatalf("put project row: %v", err)
	}
	global, ok, err := s.LadoWatchSettings(ctx, domain.LadoWatchGlobalScope)
	if err != nil || !ok || *global.CompactWindow != 300_000 || *global.TaskCostCents != 50_00 || *global.WakeEnabled ||
		global.SessionCostCents != nil || global.StallMinutes != nil || !global.UpdatedAt.Equal(ladoWatchAt) {
		t.Fatalf("global row = %+v, %v, %v", global, ok, err)
	}
	want := domain.LadoWatchSettings{
		CompactWindow: 150_000, TaskCostCents: 50_00, SessionCostCents: domain.LadoDefaultSessionCostCents,
		SessionCompactions: domain.LadoDefaultSessionCompactions, StallMinutes: 30, WakeEnabled: true,
	}
	if got, err := s.LadoEffectiveWatchSettings(ctx, "mer"); err != nil || got != want {
		t.Fatalf("effective project settings = %+v, %v; want %+v", got, err, want)
	}
	// Another project follows the global row.
	if got, err := s.LadoEffectiveWatchSettings(ctx, "other"); err != nil || got.CompactWindow != 300_000 || got.WakeEnabled {
		t.Fatalf("effective settings of a project without a row = %+v, %v", got, err)
	}

	for name, o := range map[string]domain.LadoWatchOverrides{
		"a compact window below 100000":  {CompactWindow: intp(99_999)},
		"a compact window above 1000000": {CompactWindow: intp(1_000_001)},
		"a zero task cost":               {TaskCostCents: centsp(0)},
		"a negative session cost":        {SessionCostCents: centsp(-1)},
		"zero compactions":               {SessionCompactions: intp(0)},
		"a stall of one minute":          {StallMinutes: intp(1)},
	} {
		if err := s.PutLadoWatchSettings(ctx, "mer", o); !errors.Is(err, domain.ErrLadoWatchSettingOutOfRange) {
			t.Fatalf("%s: err = %v, want out of range", name, err)
		}
	}
	// A whole row is replaced: nil values clear what the row set before.
	if err := s.PutLadoWatchSettings(ctx, "mer", domain.LadoWatchOverrides{}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LadoEffectiveWatchSettings(ctx, "mer"); err != nil || got.CompactWindow != 300_000 || got.StallMinutes != domain.LadoDefaultStallMinutes {
		t.Fatalf("effective settings after clearing the project row = %+v, %v", got, err)
	}
}

func TestLadoSessionWatchGroupsDoNotOverwriteEachOther(t *testing.T) {
	s := openLadoWatchStore(t)
	ctx := context.Background()
	const session domain.SessionID = "mer-1"

	if ok, err := s.SetLadoSessionWatchLimit(ctx, session, domain.LadoWatchLimit{LimitState: domain.LadoLimitLimited}, ladoWatchAt); err != nil || ok {
		t.Fatalf("limit of a session without a row = %v, %v; want no row", ok, err)
	}
	if created, err := s.EnsureLadoSessionWatch(ctx, session, "lado-task-1", ladoWatchAt); err != nil || !created {
		t.Fatalf("EnsureLadoSessionWatch = %v, %v", created, err)
	}
	if created, err := s.EnsureLadoSessionWatch(ctx, session, "lado-task-1", ladoWatchAt.Add(time.Hour)); err != nil || created {
		t.Fatalf("EnsureLadoSessionWatch again = %v, %v; want the row kept", created, err)
	}
	if _, err := s.EnsureLadoSessionWatch(ctx, "mer-2", "lado-task-missing", ladoWatchAt); err == nil {
		t.Fatal("a watch of no task was made")
	}
	w, ok, err := s.LadoSessionWatch(ctx, session)
	if err != nil || !ok || w.TaskID != "lado-task-1" || len(w.ReadOffsets) != 0 || w.Compactions != 0 || w.LimitState != "" ||
		w.StallState != "" || !w.CreatedAt.Equal(ladoWatchAt) {
		t.Fatalf("new watch = %+v, %v, %v", w, ok, err)
	}

	at := func(minutes int) time.Time { return ladoWatchAt.Add(time.Duration(minutes) * time.Minute) }
	compactions := domain.LadoWatchCompactions{ReadOffsets: map[string]int64{"/t/main.jsonl": 4096}, Compactions: 2, CompactedAt: at(1)}
	limit := domain.LadoWatchLimit{
		LimitState: domain.LadoLimitLimited, LimitResetAt: at(60), LimitResetGuessed: true, LimitSeenAt: at(2), Resumes: 1, ResumedAt: at(3), Stops: 2,
	}
	stall := domain.LadoWatchStall{StallState: domain.LadoStallStalled, StalledAt: at(4), Nudges: 1, NudgedAt: at(5), Stalls: 3}
	mustSet := func(name string, set func() (bool, error)) {
		t.Helper()
		if ok, err := set(); err != nil || !ok {
			t.Fatalf("%s = %v, %v", name, ok, err)
		}
	}
	mustSet("compactions", func() (bool, error) { return s.SetLadoSessionWatchCompactions(ctx, session, compactions, at(1)) })
	mustSet("limit", func() (bool, error) { return s.SetLadoSessionWatchLimit(ctx, session, limit, at(2)) })
	mustSet("stall", func() (bool, error) { return s.SetLadoSessionWatchStall(ctx, session, stall, at(3)) })
	// Each writer writes its own group again from a stale read; nothing it
	// did not write changes.
	compactions.ReadOffsets["/t/main.jsonl"] = 8192
	compactions.Compactions = 3
	mustSet("compactions again", func() (bool, error) { return s.SetLadoSessionWatchCompactions(ctx, session, compactions, at(6)) })
	check := func(step string) {
		t.Helper()
		w, ok, err := s.LadoSessionWatch(ctx, session)
		if err != nil || !ok {
			t.Fatalf("%s: read = %v, %v", step, ok, err)
		}
		if !maps64Equal(w.ReadOffsets, compactions.ReadOffsets) || w.Compactions != compactions.Compactions || !w.CompactedAt.Equal(compactions.CompactedAt) {
			t.Fatalf("%s: compactions = %+v, want %+v", step, w.LadoWatchCompactions, compactions)
		}
		if w.LimitState != limit.LimitState || !w.LimitResetAt.Equal(limit.LimitResetAt) || w.LimitResetGuessed != limit.LimitResetGuessed ||
			!w.LimitSeenAt.Equal(limit.LimitSeenAt) || w.Resumes != limit.Resumes || !w.ResumedAt.Equal(limit.ResumedAt) || w.Stops != limit.Stops {
			t.Fatalf("%s: limit = %+v, want %+v", step, w.LadoWatchLimit, limit)
		}
		if w.StallState != stall.StallState || !w.StalledAt.Equal(stall.StalledAt) || w.Nudges != stall.Nudges || !w.NudgedAt.Equal(stall.NudgedAt) || w.Stalls != stall.Stalls {
			t.Fatalf("%s: stall = %+v, want %+v", step, w.LadoWatchStall, stall)
		}
	}
	check("in turn")

	// Concurrent writers of the three groups.
	var wg sync.WaitGroup
	errs := make(chan error, 300)
	for i := range 100 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			c := domain.LadoWatchCompactions{ReadOffsets: map[string]int64{"/t/main.jsonl": int64(10_000 + i)}, Compactions: 3, CompactedAt: compactions.CompactedAt}
			if _, err := s.SetLadoSessionWatchCompactions(ctx, session, c, at(7)); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := s.SetLadoSessionWatchLimit(ctx, session, limit, at(7)); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := s.SetLadoSessionWatchStall(ctx, session, stall, at(7)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write: %v", err)
	}
	w, _, err = s.LadoSessionWatch(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	compactions.ReadOffsets = w.ReadOffsets
	if off := w.ReadOffsets["/t/main.jsonl"]; off < 10_000 || off >= 10_100 {
		t.Fatalf("offset after concurrent writes = %d", off)
	}
	check("concurrently")

	for name, set := range map[string]func() (bool, error){
		"an unknown limit state": func() (bool, error) {
			return s.SetLadoSessionWatchLimit(ctx, session, domain.LadoWatchLimit{LimitState: "paused"}, at(8))
		},
		"an unknown stall state": func() (bool, error) {
			return s.SetLadoSessionWatchStall(ctx, session, domain.LadoWatchStall{StallState: "asleep"}, at(8))
		},
		"a negative offset": func() (bool, error) {
			return s.SetLadoSessionWatchCompactions(ctx, session, domain.LadoWatchCompactions{ReadOffsets: map[string]int64{"/t": -1}}, at(8))
		},
		"negative compactions": func() (bool, error) {
			return s.SetLadoSessionWatchCompactions(ctx, session, domain.LadoWatchCompactions{Compactions: -1}, at(8))
		},
	} {
		if _, err := set(); err == nil {
			t.Fatalf("%s was stored", name)
		}
	}

	if _, err := s.EnsureLadoSessionWatch(ctx, "mer-2", "lado-task-1", at(9)); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListLadoSessionWatch(ctx, "lado-task-1")
	if err != nil || len(list) != 2 || list[0].SessionID != session || list[1].SessionID != "mer-2" {
		t.Fatalf("ListLadoSessionWatch = %+v, %v", list, err)
	}
}

func maps64Equal(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestLadoAccountLimits(t *testing.T) {
	s := openLadoWatchStore(t)
	ctx := context.Background()
	key, ok := domain.LadoAccountKey(domain.HarnessClaudeCode, "")
	if !ok || key != "claude:device-claude" {
		t.Fatalf("LadoAccountKey = %q, %v", key, ok)
	}
	if _, ok, err := s.LadoAccountLimit(ctx, key); err != nil || ok {
		t.Fatalf("limit before any = %v, %v", ok, err)
	}
	if err := s.RecordLadoAccountLimit(ctx, domain.LadoAccountLimit{AccountKey: key}); err == nil {
		t.Fatal("a limit without a reset time was recorded")
	}
	first := domain.LadoAccountLimit{AccountKey: key, SessionID: "mer-1", ResetAt: ladoWatchAt.Add(time.Hour), DetectedAt: ladoWatchAt}
	if err := s.RecordLadoAccountLimit(ctx, first); err != nil {
		t.Fatal(err)
	}
	again := domain.LadoAccountLimit{AccountKey: key, SessionID: "mer-2", ResetAt: ladoWatchAt.Add(2 * time.Hour), ResetGuessed: true, DetectedAt: ladoWatchAt.Add(time.Minute)}
	if err := s.RecordLadoAccountLimit(ctx, again); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.LadoAccountLimit(ctx, key)
	if err != nil || !ok || got.SessionID != "mer-2" || !got.ResetAt.Equal(again.ResetAt) || !got.ResetGuessed ||
		!got.DetectedAt.Equal(ladoWatchAt) || !got.UpdatedAt.Equal(again.DetectedAt) {
		t.Fatalf("limit = %+v, %v, %v; want the later reset and the first detection", got, ok, err)
	}
	if !got.Active(ladoWatchAt.Add(time.Hour)) || got.Active(again.ResetAt) {
		t.Fatalf("Active around the reset is wrong for %+v", got)
	}
	if err := s.RecordLadoAccountLimit(ctx, domain.LadoAccountLimit{AccountKey: "codex:work", ResetAt: ladoWatchAt}); err != nil {
		t.Fatal(err)
	}
	if all, err := s.ListLadoAccountLimits(ctx); err != nil || len(all) != 2 || all[0].AccountKey != key || all[1].AccountKey != "codex:work" {
		t.Fatalf("ListLadoAccountLimits = %+v, %v", all, err)
	}
	if got.Identity != "" || got.Released() || got.ReleaseReason != "" || got.ReleasedWaiting != 0 {
		t.Fatalf("limit recorded without an identity = %+v", got)
	}
}

// A live limit keeps the first identity it knew; a released or expired one
// starts again with the new record's identity and detection time. A release
// is written once and keeps the row, with the waits it left.
func TestLadoAccountLimitIdentityAndRelease(t *testing.T) {
	s := openLadoWatchStore(t)
	ctx := context.Background()
	key := "claude:device-claude"
	read := func(key string) domain.LadoAccountLimit {
		t.Helper()
		got, ok, err := s.LadoAccountLimit(ctx, key)
		if err != nil || !ok {
			t.Fatalf("limit %s = %v, %v", key, ok, err)
		}
		return got
	}
	record := func(l domain.LadoAccountLimit) {
		t.Helper()
		if err := s.RecordLadoAccountLimit(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	reset := ladoWatchAt.Add(time.Hour)
	record(domain.LadoAccountLimit{AccountKey: key, SessionID: "mer-1", Identity: "sha256:a", ResetAt: reset, DetectedAt: ladoWatchAt})
	// Another worker hits the limit after the developer logged in elsewhere:
	// the live row keeps "a", so the next check sees the change.
	record(domain.LadoAccountLimit{AccountKey: key, SessionID: "mer-2", Identity: "sha256:b", ResetAt: reset, DetectedAt: ladoWatchAt.Add(time.Minute)})
	if got := read(key); got.Identity != "sha256:a" || got.SessionID != "mer-2" || !got.DetectedAt.Equal(ladoWatchAt) || !got.Active(ladoWatchAt.Add(2*time.Minute)) {
		t.Fatalf("live limit recorded again = %+v; want identity a and the first detection", got)
	}
	// A live row without an identity takes the first one known.
	record(domain.LadoAccountLimit{AccountKey: "claude:work", ResetAt: reset, DetectedAt: ladoWatchAt})
	record(domain.LadoAccountLimit{AccountKey: "claude:work", Identity: "sha256:c", ResetAt: reset, DetectedAt: ladoWatchAt.Add(time.Minute)})
	if got := read("claude:work"); got.Identity != "sha256:c" || !got.DetectedAt.Equal(ladoWatchAt) {
		t.Fatalf("live limit without an identity recorded again = %+v; want identity c", got)
	}

	// A release is written once, also when callers race for it.
	for _, bad := range []domain.LadoLimitReleaseReason{"", "expired"} {
		if _, err := s.MarkLadoAccountLimitReleased(ctx, key, bad, ladoWatchAt); err == nil {
			t.Fatalf("a release for reason %q was accepted", bad)
		}
	}
	if ok, err := s.MarkLadoAccountLimitReleased(ctx, "codex:none", domain.LadoLimitReleaseManual, ladoWatchAt); err != nil || ok {
		t.Fatalf("release of no limit = %v, %v", ok, err)
	}
	// Waits are not written on a limit that holds.
	if err := s.SetLadoAccountLimitReleasedWaiting(ctx, key, 4); err != nil {
		t.Fatal(err)
	}
	if got := read(key); got.ReleasedWaiting != 0 {
		t.Fatalf("waits on a live limit = %d, want 0", got.ReleasedWaiting)
	}
	released := ladoWatchAt.Add(2 * time.Minute)
	var wg sync.WaitGroup
	wins := make(chan bool, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.MarkLadoAccountLimitReleased(ctx, key, domain.LadoLimitReleaseAccountChanged, released)
			if err != nil {
				t.Error(err)
			}
			wins <- ok
		}()
	}
	wg.Wait()
	close(wins)
	won := 0
	for ok := range wins {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d of 4 racing releases won, want 1", won)
	}
	if ok, err := s.MarkLadoAccountLimitReleased(ctx, key, domain.LadoLimitReleaseManual, released.Add(time.Minute)); err != nil || ok {
		t.Fatalf("second release = %v, %v; want false", ok, err)
	}
	if err := s.SetLadoAccountLimitReleasedWaiting(ctx, key, 4); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLadoAccountLimitReleasedWaiting(ctx, key, -1); err == nil {
		t.Fatal("negative waits were accepted")
	}
	got := read(key)
	if !got.ReleasedAt.Equal(released) || got.ReleaseReason != domain.LadoLimitReleaseAccountChanged || got.ReleasedWaiting != 4 ||
		!got.UpdatedAt.Equal(released) || got.Identity != "sha256:a" || got.Active(released) || !got.ReleasedEarly() {
		t.Fatalf("released limit = %+v", got)
	}
	if all, err := s.ListLadoAccountLimits(ctx); err != nil || len(all) != 2 || all[0].AccountKey != "claude:device-claude" || !all[0].Released() || all[1].Released() {
		t.Fatalf("ListLadoAccountLimits = %+v, %v; want the released row kept", all, err)
	}

	// Recording the released limit again starts it over.
	again := released.Add(10 * time.Minute)
	record(domain.LadoAccountLimit{AccountKey: key, SessionID: "mer-3", Identity: "sha256:b", ResetAt: reset.Add(time.Hour), ResetGuessed: true, DetectedAt: again})
	got = read(key)
	if got.Identity != "sha256:b" || !got.DetectedAt.Equal(again) || got.Released() || got.ReleaseReason != "" || got.ReleasedWaiting != 0 ||
		!got.Active(again) || got.SessionID != "mer-3" || !got.ResetGuessed {
		t.Fatalf("released limit recorded again = %+v; want a new live limit with identity b", got)
	}

	// So does recording a limit whose reset passed before LADO released it.
	record(domain.LadoAccountLimit{AccountKey: "codex:work", Identity: "sha256:x", ResetAt: ladoWatchAt, DetectedAt: ladoWatchAt.Add(-time.Hour)})
	later := ladoWatchAt.Add(time.Minute)
	record(domain.LadoAccountLimit{AccountKey: "codex:work", Identity: "sha256:y", ResetAt: reset, DetectedAt: later})
	if got := read("codex:work"); got.Identity != "sha256:y" || !got.DetectedAt.Equal(later) {
		t.Fatalf("expired limit recorded again = %+v; want identity y detected at %s", got, later)
	}
}

// CountLadoAccountLimitedWaits counts the open queue entries no lease holds,
// the waiting parts and the plan worker requests not started that wait for an
// account limit, and nothing else.
func TestCountLadoAccountLimitedWaits(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	mustLadoMode(t, s, "mer", true)
	count := func(step string, want int) {
		t.Helper()
		if n, err := s.CountLadoAccountLimitedWaits(ctx); err != nil || n != want {
			t.Fatalf("%s: CountLadoAccountLimitedWaits = %d, %v; want %d", step, n, err, want)
		}
	}
	rawExec := func(query string, args ...any) {
		t.Helper()
		if _, err := raw.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	count("nothing", 0)

	limited := domain.LadoWaitingAccountLimited
	parts, held, leased, cancelled := mustQueuedLadoTask(t, s, "1", "mer", ladoQueueAt),
		mustQueuedLadoTask(t, s, "2", "mer", ladoQueueAt.Add(time.Minute)),
		mustQueuedLadoTask(t, s, "3", "mer", ladoQueueAt.Add(2*time.Minute)),
		mustQueuedLadoTask(t, s, "4", "mer", ladoQueueAt.Add(3*time.Minute))
	now := ladoQueueAt.Add(time.Hour)
	claimed := mustClaimLadoQueue(t, s, now, 4, ladoTokens("t"), parts, held, leased, cancelled)
	for _, i := range []int{1, 3} {
		if ok, err := s.HoldLadoQueueEntryFor(ctx, claimed[i].ID, claimed[i].LeaseToken, limited, "reset", now.Add(time.Minute)); err != nil || !ok {
			t.Fatalf("hold %s = %v, %v", claimed[i].ID, ok, err)
		}
	}
	// A leased entry is not waiting, whatever its reason; a cancelled one is
	// not open.
	rawExec(`UPDATE lado_queue_entries SET waiting_reason = ? WHERE id = ?`, limited, leased)
	rawExec(`UPDATE lado_queue_entries SET cancelled_at = ? WHERE id = ?`, now, cancelled)
	count("queue", 1)

	base := strings.Repeat("a", 40)
	run := domain.LadoPartRun{
		QueueEntryID: parts, TaskID: "lado-task-1", ProjectID: "mer", PlanRevisionID: "lado-plan-1", RepoDir: "/repo",
		Branch: "lado/task-1/integration", BaseSHA: base, HeadSHA: base, StartedAt: now,
	}
	if err := s.StartLadoPartRun(ctx, claimed[0].LeaseToken, run, []domain.LadoTaskPart{
		{PartID: "a", Title: "A", Phase: 1, Steps: []int{1}, AffectedPaths: []string{"a/**"}, WaitingReason: limited},
		{PartID: "b", Title: "B", Position: 1, Phase: 1, Steps: []int{2}, AffectedPaths: []string{"b/**"}, WaitingReason: domain.LadoWaitingPartDependency},
		{PartID: "c", Title: "C", Position: 2, Phase: 1, Steps: []int{3}, AffectedPaths: []string{"c/**"}, WaitingReason: limited},
	}, ladoPartsEvent("parts.started")); err != nil {
		t.Fatal(err)
	}
	count("parts started", 3)
	// A part that is no longer waiting does not count.
	rawExec(`UPDATE lado_task_parts SET state = 'failed' WHERE part_id = 'c'`)
	count("parts", 2)

	for _, id := range []string{"req-w1", "req-w2", "req-w3"} {
		mustLadoWorkerRequest(t, s, id, "lado-task-2", now)
		if err := s.SetLadoPlanRequestWaiting(ctx, domain.LadoPlanRequestID(id), limited); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetLadoPlanRequestWaiting(ctx, "req-w3", domain.LadoPlanWaitingCapacity); err != nil {
		t.Fatal(err)
	}
	rawExec(`UPDATE lado_plan_requests SET failed_at = ? WHERE id = 'req-w2'`, now)
	count("plan requests", 3)
}

func TestLadoAlertsAreRaisedOnceAndSeen(t *testing.T) {
	s := openLadoWatchStore(t)
	ctx := context.Background()
	session := mustLadoSession(t, s, "mer", false).ID
	at := func(minutes int) time.Time { return ladoWatchAt.Add(time.Duration(minutes) * time.Minute) }

	add := func(a domain.LadoAlert) (domain.LadoAlertID, bool) {
		t.Helper()
		id, added, err := s.AddLadoAlert(ctx, a)
		if err != nil {
			t.Fatalf("AddLadoAlert %+v: %v", a, err)
		}
		return id, added
	}
	// A task's own alert, with no session, raised on two ticks is one row.
	taskCost := domain.LadoAlert{TaskID: "lado-task-1", Kind: domain.LadoAlertTaskCost, Level: domain.LadoAlertLevelCrossed, Value: 120_00, Threshold: 100_00, CreatedAt: at(0)}
	first, added := add(taskCost)
	if !added || first == 0 {
		t.Fatalf("first task alert = %d, %v", first, added)
	}
	taskCost.CreatedAt = at(1)
	if _, added := add(taskCost); added {
		t.Fatal("the same task alert was raised twice")
	}
	taskCost.Level = domain.LadoAlertLevelDoubled
	doubled, added := add(taskCost)
	if !added {
		t.Fatal("the doubled task alert was not raised")
	}
	compact, added := add(domain.LadoAlert{TaskID: "lado-task-1", SessionID: session, Kind: domain.LadoAlertSessionCompactions, Level: 1, Value: 3, Threshold: 3, CreatedAt: at(2)})
	if !added {
		t.Fatal("the session alert was not raised")
	}
	for name, a := range map[string]domain.LadoAlert{
		"an alert without a task": {Kind: domain.LadoAlertTaskCost, Level: 1},
		"an unknown kind":         {TaskID: "lado-task-1", Kind: "weather", Level: 1},
		"level zero":              {TaskID: "lado-task-1", Kind: domain.LadoAlertStalled},
	} {
		if _, _, err := s.AddLadoAlert(ctx, a); err == nil {
			t.Fatalf("%s was raised", name)
		}
	}
	if _, _, err := s.AddLadoAlert(ctx, domain.LadoAlert{TaskID: "lado-task-missing", Kind: domain.LadoAlertStalled, Level: 1}); err == nil {
		t.Fatal("an alert of no task was raised")
	}

	ids := func(alerts []domain.LadoAlert) []domain.LadoAlertID {
		out := make([]domain.LadoAlertID, 0, len(alerts))
		for _, a := range alerts {
			out = append(out, a.ID)
		}
		return out
	}
	byProject, err := s.ListLadoAlertsByProject(ctx, "mer", false, 10)
	if err != nil || !slices.Equal(ids(byProject), []domain.LadoAlertID{compact, doubled, first}) {
		t.Fatalf("project alerts = %v, %v", ids(byProject), err)
	}
	if a := byProject[2]; a.TaskID != "lado-task-1" || a.SessionID != "" || a.ProjectID != "mer" || a.IssueRef != "github:acme/demo#12" ||
		a.Value != 120_00 || a.Threshold != 100_00 || !a.CreatedAt.Equal(at(0)) || a.Seen() {
		t.Fatalf("task alert = %+v", a)
	}
	if limited, err := s.ListLadoAlertsByProject(ctx, "mer", false, 1); err != nil || len(limited) != 1 {
		t.Fatalf("one project alert = %v, %v", ids(limited), err)
	}

	if ok, err := s.MarkLadoAlertSeen(ctx, first, at(10)); err != nil || !ok {
		t.Fatalf("MarkLadoAlertSeen = %v, %v", ok, err)
	}
	if ok, err := s.MarkLadoAlertSeen(ctx, first, at(11)); err != nil || ok {
		t.Fatalf("MarkLadoAlertSeen again = %v, %v; want it seen once", ok, err)
	}
	if ok, err := s.MarkLadoAlertSeen(ctx, 999, at(11)); err != nil || ok {
		t.Fatalf("MarkLadoAlertSeen of no alert = %v, %v", ok, err)
	}
	seen, ok, err := s.LadoAlert(ctx, first)
	if err != nil || !ok || !seen.SeenAt.Equal(at(10)) || seen.ProjectID != "mer" {
		t.Fatalf("seen alert = %+v, %v, %v", seen, ok, err)
	}
	if unseen, err := s.ListLadoAlertsByProject(ctx, "mer", true, 10); err != nil || !slices.Equal(ids(unseen), []domain.LadoAlertID{compact, doubled}) {
		t.Fatalf("unseen project alerts = %v, %v", ids(unseen), err)
	}
	if byTask, err := s.ListLadoAlertsByTask(ctx, "lado-task-1"); err != nil || len(byTask) != 3 {
		t.Fatalf("task alerts = %v, %v", ids(byTask), err)
	}
	since, err := s.ListLadoAlertsSince(ctx, at(1))
	if err != nil || !slices.Equal(ids(since), []domain.LadoAlertID{doubled, compact}) {
		t.Fatalf("alerts since = %v, %v; want the later two, oldest first", ids(since), err)
	}
	if since[1].IssueRef != "github:acme/demo#12" || since[1].SessionID != session {
		t.Fatalf("alert since = %+v", since[1])
	}
}

func TestLadoAlertChangesPublishEvents(t *testing.T) {
	s := openLadoWatchStore(t)
	ctx := context.Background()
	session := mustLadoSession(t, s, "mer", false).ID
	seq := lastChangeSeq(t, s)
	expect := func(step string, want ...string) {
		t.Helper()
		events, err := s.EventsAfter(ctx, seq, 1000)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		var got []string
		for _, ev := range events {
			seq = ev.Seq
			var payload struct {
				Kind      string `json:"kind"`
				AlertID   int64  `json:"alertId"`
				Op        string `json:"op"`
				SessionID string `json:"sessionId"`
				TaskID    string `json:"taskId"`
				ProjectID string `json:"projectId"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatalf("%s: payload %s: %v", step, ev.Payload, err)
			}
			if ev.Type != "session_updated" || ev.ProjectID != "mer" || payload.TaskID != "lado-task-1" || payload.ProjectID != "mer" {
				t.Fatalf("%s: event %+v payload %s", step, ev, ev.Payload)
			}
			got = append(got, fmt.Sprintf("%s %d %s %s/%s", payload.Kind, payload.AlertID, payload.Op, payload.SessionID, ev.SessionID))
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: events %q, want %q", step, got, want)
		}
	}

	taskAlert, _, err := s.AddLadoAlert(ctx, domain.LadoAlert{TaskID: "lado-task-1", Kind: domain.LadoAlertTaskCost, Level: 1, CreatedAt: ladoWatchAt})
	if err != nil {
		t.Fatal(err)
	}
	expect("task alert", fmt.Sprintf("lado_alert %d insert /", taskAlert))
	if _, added, err := s.AddLadoAlert(ctx, domain.LadoAlert{TaskID: "lado-task-1", Kind: domain.LadoAlertTaskCost, Level: 1, CreatedAt: ladoWatchAt}); err != nil || added {
		t.Fatalf("second raise = %v, %v", added, err)
	}
	expect("second raise", nil...)
	sessionAlert, _, err := s.AddLadoAlert(ctx, domain.LadoAlert{TaskID: "lado-task-1", SessionID: session, Kind: domain.LadoAlertStalled, Level: 1, CreatedAt: ladoWatchAt})
	if err != nil {
		t.Fatal(err)
	}
	expect("session alert", fmt.Sprintf("lado_alert %d insert %s/%s", sessionAlert, session, session))
	goneAlert, _, err := s.AddLadoAlert(ctx, domain.LadoAlert{TaskID: "lado-task-1", SessionID: "mer-gone", Kind: domain.LadoAlertStalled, Level: 1, CreatedAt: ladoWatchAt})
	if err != nil {
		t.Fatal(err)
	}
	expect("alert of a session AO no longer has", fmt.Sprintf("lado_alert %d insert mer-gone/", goneAlert))
	if _, err := s.MarkLadoAlertSeen(ctx, taskAlert, ladoWatchAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	expect("seen", fmt.Sprintf("lado_alert %d update /", taskAlert))
	if _, err := s.MarkLadoAlertSeen(ctx, taskAlert, ladoWatchAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	expect("seen again", nil...)

	// A session watch publishes when its limit state, stall state or
	// compactions change; a new row and a moved offset publish nothing.
	if _, err := s.EnsureLadoSessionWatch(ctx, session, "lado-task-1", ladoWatchAt); err != nil {
		t.Fatal(err)
	}
	expect("new watch", nil...)
	watchChange := "lado_watch 0  " + string(session) + "/" + string(session)
	offsets := func(off int64, n int) domain.LadoWatchCompactions {
		return domain.LadoWatchCompactions{ReadOffsets: map[string]int64{"/t/main.jsonl": off}, Compactions: n}
	}
	steps := []struct {
		name string
		set  func() (bool, error)
		want []string
	}{
		{"offset alone", func() (bool, error) {
			return s.SetLadoSessionWatchCompactions(ctx, session, offsets(100, 0), ladoWatchAt)
		}, nil},
		{"a compaction", func() (bool, error) {
			return s.SetLadoSessionWatchCompactions(ctx, session, offsets(200, 1), ladoWatchAt)
		}, []string{watchChange}},
		{"offset again", func() (bool, error) {
			return s.SetLadoSessionWatchCompactions(ctx, session, offsets(300, 1), ladoWatchAt)
		}, nil},
		{"limited", func() (bool, error) {
			return s.SetLadoSessionWatchLimit(ctx, session, domain.LadoWatchLimit{LimitState: domain.LadoLimitLimited, LimitResetAt: ladoWatchAt}, ladoWatchAt)
		}, []string{watchChange}},
		{"a resume", func() (bool, error) {
			return s.SetLadoSessionWatchLimit(ctx, session, domain.LadoWatchLimit{LimitState: domain.LadoLimitLimited, LimitResetAt: ladoWatchAt, Resumes: 1}, ladoWatchAt)
		}, nil},
		{"stalled", func() (bool, error) {
			return s.SetLadoSessionWatchStall(ctx, session, domain.LadoWatchStall{StallState: domain.LadoStallStalled}, ladoWatchAt)
		}, []string{watchChange}},
		{"a nudge", func() (bool, error) {
			return s.SetLadoSessionWatchStall(ctx, session, domain.LadoWatchStall{StallState: domain.LadoStallStalled, Nudges: 1}, ladoWatchAt)
		}, nil},
		{"moving again", func() (bool, error) {
			return s.SetLadoSessionWatchStall(ctx, session, domain.LadoWatchStall{Nudges: 1}, ladoWatchAt)
		}, []string{watchChange}},
	}
	for _, step := range steps {
		if ok, err := step.set(); err != nil || !ok {
			t.Fatalf("%s = %v, %v", step.name, ok, err)
		}
		expect(step.name, step.want...)
	}

	// Settings and account limits publish nothing.
	if err := s.PutLadoWatchSettings(ctx, "mer", domain.LadoWatchOverrides{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLadoAccountLimit(ctx, domain.LadoAccountLimit{AccountKey: "claude:a", ResetAt: ladoWatchAt}); err != nil {
		t.Fatal(err)
	}
	expect("settings and limits", nil...)
}

func lastChangeSeq(t *testing.T, s *sqlite.Store) int64 {
	t.Helper()
	events, err := s.EventsAfter(context.Background(), 0, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

func TestLadoPlanWorkerSessionsReadEveryAttempt(t *testing.T) {
	s := openLadoWatchStore(t)
	ctx := context.Background()
	mustLadoWorkerRequest(t, s, "req-w", "lado-task-1", ladoPlanAt.Add(time.Minute))
	mustLadoWorkerRequest(t, s, "req-w2", "lado-task-1", ladoPlanAt.Add(2*time.Minute))
	session := func(clientRequestID string, at time.Time) domain.SessionID {
		t.Helper()
		rec := sampleRecord("mer")
		rec.ClientRequestID, rec.ClientRequestHash = clientRequestID, "v1:x"
		rec.CreatedAt = at
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatalf("create session %s: %v", clientRequestID, err)
		}
		return created.ID
	}
	design1 := session(domain.LadoPlanWorkerClientRequestID("req-w", domain.LadoPlanRoleDesigner, 1), ladoPlanAt.Add(3*time.Minute))
	design2 := session(domain.LadoPlanWorkerClientRequestID("req-w", domain.LadoPlanRoleDesigner, 2), ladoPlanAt.Add(4*time.Minute))
	critic := session(domain.LadoPlanWorkerClientRequestID("req-w", domain.LadoPlanRoleCritic, 1), ladoPlanAt.Add(5*time.Minute))
	other := session(domain.LadoPlanWorkerClientRequestID("req-w2", domain.LadoPlanRoleDesigner, 1), ladoPlanAt.Add(6*time.Minute))
	// Neither a dispatch attempt nor a plain session belongs to the task's
	// plan workers.
	session(domain.LadoClientRequestID("lado-queue-1", 1), ladoPlanAt.Add(7*time.Minute))
	session("", ladoPlanAt.Add(8*time.Minute))

	got, err := s.ListLadoPlanWorkerSessions(ctx, "lado-task-1")
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		session domain.SessionID
		request domain.LadoPlanRequestID
		role    domain.LadoSessionRole
		attempt int
	}
	var rows []row
	for _, s := range got {
		rows = append(rows, row{s.SessionID, s.RequestID, s.SessionRole(), s.Attempt})
	}
	want := []row{
		{design1, "req-w", domain.LadoSessionDesigner, 1},
		{design2, "req-w", domain.LadoSessionDesigner, 2},
		{critic, "req-w", domain.LadoSessionCritic, 1},
		{other, "req-w2", domain.LadoSessionDesigner, 1},
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("plan worker sessions = %+v, want %+v", rows, want)
	}
	if none, err := s.ListLadoPlanWorkerSessions(ctx, "lado-task-missing"); err != nil || len(none) != 0 {
		t.Fatalf("plan worker sessions of no task = %+v, %v", none, err)
	}
}

func TestLadoSessionSubagentUsageAndTranscripts(t *testing.T) {
	s, raw := openLadoQueueStore(t)
	seedLadoTaskWithPlan(t, s)
	ctx := context.Background()
	session := mustLadoSession(t, s, "mer", false).ID
	exec := func(query string, args ...any) int64 {
		t.Helper()
		res, err := raw.Exec(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	binding := exec(`INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at)
		VALUES (?, 'claude-code', 'root', 'active', CURRENT_TIMESTAMP)`, string(session))
	source := func(kind, subagent, path string) int64 {
		return exec(`INSERT INTO usage_sources (binding_id, kind, native_session_id, subagent_id, artifact_path, state, updated_at)
			VALUES (?, ?, 'native', ?, ?, 'active', CURRENT_TIMESTAMP)`, binding, kind, subagent, path)
	}
	main := source("claude_main", "", "/t/main.jsonl")
	sub := source("claude_subagent", "agent-1", "/t/sub-1.jsonl")
	// The collector registers a Codex subagent thread's rollout under its
	// parent's binding; its compactions are not the session's own.
	source("codex_rollout", "thread-child", "/t/child-rollout.jsonl")
	event := func(src int64, key string, in, out int64, cost any) {
		exec(`INSERT INTO model_usage_events (binding_id, usage_source_id, provider_id, model_id, usage_measurement_kind,
			input_tokens, output_tokens, estimated_cost_nanos, source_event_key)
			VALUES (?, ?, 'anthropic', 'claude-test', 'native_reported', ?, ?, ?, ?)`, binding, src, in, out, cost, key)
	}
	event(main, "m1", 1000, 100, 5_000_000)
	event(sub, "s1", 200, 20, 1_000_000)
	event(sub, "s2", 300, 30, nil)

	usage, err := s.LadoSessionSubagentUsage(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if usage.SessionID != session || usage.Events != 2 || usage.InputTokens != 500 || usage.OutputTokens != 50 ||
		usage.CostNanos != 1_000_000 || usage.PricedEvents != 1 {
		t.Fatalf("subagent usage = %+v", usage)
	}
	if none, err := s.LadoSessionSubagentUsage(ctx, "mer-gone"); err != nil || none.Events != 0 || none.CostNanos != 0 {
		t.Fatalf("subagent usage of no session = %+v, %v", none, err)
	}

	sources, err := s.ListLadoSessionTranscriptSources(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if want := []domain.LadoTranscriptSource{{Kind: domain.UsageSourceClaudeMain, Path: "/t/main.jsonl"}}; !slices.Equal(sources, want) {
		t.Fatalf("transcript sources = %+v, want %+v", sources, want)
	}
}
