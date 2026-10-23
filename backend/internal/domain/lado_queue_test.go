package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLadoQueueEntryState(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Minute)
	open := domain.LadoQueueEntry{
		ID:               "lado-queue-1",
		TaskID:           "lado-task-1",
		ProjectID:        "demo",
		PlanApprovalID:   "lado-approval-1",
		PolicySnapshotID: "lado-snapshot-1",
		EnqueuedAt:       now.Add(-time.Hour),
	}
	lease := func(e *domain.LadoQueueEntry, until time.Time) {
		e.AttemptCount = 1
		e.LeaseToken, e.ClaimedAt, e.LeaseExpiresAt = "token-1", until.Add(-2*time.Minute), until
	}
	clearLease := func(e *domain.LadoQueueEntry) {
		e.LeaseToken, e.ClaimedAt, e.LeaseExpiresAt = "", time.Time{}, time.Time{}
	}

	// An entry with every deciding fact set. Clearing the fact that wins each
	// time walks the whole precedence order.
	entry := open
	entry.SupersededAt = past
	entry.CancelledAt = past
	entry.DispatchedSessionID = "demo-7"
	entry.FailedAt = past
	entry.ErrorMessage = "spawn failed"
	entry.ReviewRequiredReason = domain.LadoReviewIssueEdited
	entry.BlockedReason = domain.LadoBlockedBaseRefMismatch
	lease(&entry, future)
	entry.WaitingReason = domain.LadoWaitingSlotGlobal
	entry.NextAttemptAt = future
	for _, step := range []struct {
		want  domain.LadoQueueState
		clear func(*domain.LadoQueueEntry)
	}{
		{domain.LadoQueueSuperseded, func(e *domain.LadoQueueEntry) { e.SupersededAt = time.Time{} }},
		{domain.LadoQueueCancelled, func(e *domain.LadoQueueEntry) { e.CancelledAt = time.Time{} }},
		{domain.LadoQueueDispatched, func(e *domain.LadoQueueEntry) { e.DispatchedSessionID = "" }},
		{domain.LadoQueueFailed, func(e *domain.LadoQueueEntry) { e.FailedAt = time.Time{} }},
		{domain.LadoQueueReviewRequired, func(e *domain.LadoQueueEntry) { e.ReviewRequiredReason = "" }},
		{domain.LadoQueueBlocked, func(e *domain.LadoQueueEntry) { e.BlockedReason = "" }},
		{domain.LadoQueueDispatching, clearLease},
		{domain.LadoQueueWaiting, func(e *domain.LadoQueueEntry) { e.WaitingReason = "" }},
		{domain.LadoQueueWaiting, func(e *domain.LadoQueueEntry) { e.NextAttemptAt = time.Time{} }},
		{domain.LadoQueueQueued, nil},
	} {
		if got := entry.State(now); got != step.want {
			t.Fatalf("entry %+v: State = %q, want %q", entry, got, step.want)
		}
		if step.clear != nil {
			step.clear(&entry)
		}
	}

	for _, tc := range []struct {
		name string
		edit func(*domain.LadoQueueEntry)
		want domain.LadoQueueState
	}{
		{"new entry", func(*domain.LadoQueueEntry) {}, domain.LadoQueueQueued},
		{"active lease", func(e *domain.LadoQueueEntry) { lease(e, future) }, domain.LadoQueueDispatching},
		// A dispatcher that stopped mid-dispatch leaves its lease behind; once it
		// expires the entry can be claimed again.
		{"expired lease", func(e *domain.LadoQueueEntry) { lease(e, past) }, domain.LadoQueueQueued},
		{"lease expiring now", func(e *domain.LadoQueueEntry) { lease(e, now) }, domain.LadoQueueQueued},
		{"lease token without an expiry", func(e *domain.LadoQueueEntry) { e.LeaseToken = "token-1" }, domain.LadoQueueQueued},
		{"waiting for a slot in the project", func(e *domain.LadoQueueEntry) { e.WaitingReason = domain.LadoWaitingSlotProject }, domain.LadoQueueWaiting},
		{"waiting while LADO mode is off", func(e *domain.LadoQueueEntry) { e.WaitingReason = domain.LadoWaitingModeOff }, domain.LadoQueueWaiting},
		{"waiting to retry", func(e *domain.LadoQueueEntry) {
			e.AttemptCount, e.WaitingReason, e.NextAttemptAt = 1, domain.LadoWaitingRetry, future
		}, domain.LadoQueueWaiting},
		{"future next attempt", func(e *domain.LadoQueueEntry) { e.NextAttemptAt = future }, domain.LadoQueueWaiting},
		{"next attempt due now", func(e *domain.LadoQueueEntry) { e.NextAttemptAt = now }, domain.LadoQueueQueued},
		{"next attempt past due", func(e *domain.LadoQueueEntry) { e.NextAttemptAt = past }, domain.LadoQueueQueued},
		{"blocked while leased", func(e *domain.LadoQueueEntry) {
			lease(e, future)
			e.BlockedReason = domain.LadoBlockedAgentUnsupported
		}, domain.LadoQueueBlocked},
		{"failed after its attempts", func(e *domain.LadoQueueEntry) {
			e.AttemptCount, e.FailedAt, e.ErrorMessage = 3, past, "spawn failed"
		}, domain.LadoQueueFailed},
		{"dispatched", func(e *domain.LadoQueueEntry) { e.AttemptCount, e.DispatchedSessionID = 1, "demo-7" }, domain.LadoQueueDispatched},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := open
			tc.edit(&e)
			if got := e.State(now); got != tc.want {
				t.Fatalf("State = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLadoDispatchSettingsValidate(t *testing.T) {
	defaults := domain.DefaultLadoDispatchSettings()
	if defaults.MaxConcurrent != 2 || defaults.MaxRunMinutes != 480 || !defaults.HoldOnMemoryTight || !defaults.UpdatedAt.IsZero() {
		t.Fatalf("defaults = %+v, want 2 runs, 480 minutes and a hold while memory is tight", defaults)
	}
	if err := defaults.Validate(); err != nil {
		t.Fatalf("defaults: Validate = %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*domain.LadoDispatchSettings)
		// field names the setting Validate refuses, empty when it accepts.
		field string
	}{
		{"one run", func(s *domain.LadoDispatchSettings) { s.MaxConcurrent = 1 }, ""},
		{"eight runs", func(s *domain.LadoDispatchSettings) { s.MaxConcurrent = 8 }, ""},
		{"no runs", func(s *domain.LadoDispatchSettings) { s.MaxConcurrent = 0 }, "maxConcurrent"},
		{"nine runs", func(s *domain.LadoDispatchSettings) { s.MaxConcurrent = 9 }, "maxConcurrent"},
		{"negative runs", func(s *domain.LadoDispatchSettings) { s.MaxConcurrent = -1 }, "maxConcurrent"},
		{"no time limit", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = 0 }, ""},
		{"one hour", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = 60 }, ""},
		{"two hours", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = 120 }, ""},
		{"four hours", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = 240 }, ""},
		{"twelve hours", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = 720 }, ""},
		{"a day", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = 1440 }, ""},
		{"half an hour", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = 30 }, "maxRunMinutes"},
		{"just under an hour", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = 59 }, "maxRunMinutes"},
		{"just over a day", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = 1441 }, "maxRunMinutes"},
		{"two days", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = 2880 }, "maxRunMinutes"},
		{"negative minutes", func(s *domain.LadoDispatchSettings) { s.MaxRunMinutes = -60 }, "maxRunMinutes"},
		{"no hold", func(s *domain.LadoDispatchSettings) { s.HoldOnMemoryTight = false }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := domain.DefaultLadoDispatchSettings()
			tc.edit(&settings)
			err := settings.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("Validate(%+v) = %v, want nil", settings, err)
				}
				return
			}
			if !errors.Is(err, domain.ErrLadoDispatchSettingsInvalid) || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Validate(%+v) = %v, want ErrLadoDispatchSettingsInvalid naming %s", settings, err, tc.field)
			}
		})
	}
}

func TestLadoClientRequestID(t *testing.T) {
	if got := domain.LadoClientRequestID("lado-queue-7f3a", 2); got != "lado:lado-queue-7f3a/2" {
		t.Fatalf("LadoClientRequestID = %q, want lado:lado-queue-7f3a/2", got)
	}
	for _, entry := range []domain.LadoQueueEntryID{"lado-queue-7f3a", "q"} {
		for _, attempt := range []int{1, 2, 3, 10, 120} {
			id := domain.LadoClientRequestID(entry, attempt)
			gotEntry, gotAttempt, ok := domain.ParseLadoClientRequestID(id)
			if !ok || gotEntry != entry || gotAttempt != attempt {
				t.Fatalf("ParseLadoClientRequestID(%q) = %q, %d, %v; want %q, %d", id, gotEntry, gotAttempt, ok, entry, attempt)
			}
		}
	}
	for _, id := range []string{
		"",
		"lado:",
		"lado:/1",
		"lado:lado-queue-1",
		"lado:lado-queue-1/",
		"lado:lado-queue-1/0",
		"lado:lado-queue-1/-1",
		"lado:lado-queue-1/+1",
		"lado:lado-queue-1/01",
		"lado:lado-queue-1/1.5",
		"lado:lado-queue-1/x",
		"lado:lado-queue-1/ 1",
		"lado:lado-queue-1/1 ",
		"lado:lado-queue-1/99999999999999999999",
		"lado:a/b/1",
		"lado:lado-queue-1/1/2",
		"lado:lado-queue-1//1",
		"LADO:lado-queue-1/1",
		" lado:lado-queue-1/1",
		"lado-queue-1/1",
		"manual:lado-queue-1/1",
	} {
		if entry, attempt, ok := domain.ParseLadoClientRequestID(id); ok {
			t.Errorf("ParseLadoClientRequestID(%q) = %q, %d, true; want it refused", id, entry, attempt)
		}
	}

	plan, snapshot := strings.Repeat("a", 64), strings.Repeat("b", 64)
	sum := sha256.Sum256([]byte(plan + "\n" + snapshot))
	if got := domain.LadoDispatchHash(plan, snapshot); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("LadoDispatchHash = %s, want the hex sha256 of plan, newline, snapshot", got)
	}
	if domain.LadoDispatchHash(plan, snapshot) == domain.LadoDispatchHash(snapshot, plan) {
		t.Fatal("LadoDispatchHash ignores the order of its inputs")
	}
	if domain.LadoDispatchHash("ab", "c") == domain.LadoDispatchHash("a", "bc") {
		t.Fatal("LadoDispatchHash lets the plan hash run into the snapshot hash")
	}
}
