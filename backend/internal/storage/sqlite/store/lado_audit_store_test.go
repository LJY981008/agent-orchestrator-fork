package store_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

var ladoAuditGenesis = strings.Repeat("0", 64)

func sampleLadoAuditEvent(action string) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{
		At:          time.Date(2026, 10, 19, 9, 30, 0, 123456789, time.UTC),
		Actor:       "human:presence-1",
		Action:      action,
		SubjectKind: "plan_revision",
		SubjectID:   "plan-1",
		Payload:     json.RawMessage(`{"sha256":"abc","note":"a<b&c>"}`),
	}
}

// expectedLadoAuditSHA256 restates the documented canonical form: each field as
// a netstring, in column order. A change here means every stored chain breaks.
func expectedLadoAuditSHA256(ev domain.LadoAuditEvent) string {
	var canonical strings.Builder
	for _, field := range []string{
		strconv.FormatInt(ev.ID, 10),
		ev.At.UTC().Format(time.RFC3339Nano),
		ev.Actor,
		ev.Action,
		ev.SubjectKind,
		ev.SubjectID,
		string(ev.Payload),
		ev.PrevSHA256,
	} {
		fmt.Fprintf(&canonical, "%d:%s,", len(field), field)
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:])
}

// openLadoAuditStore returns a store and a raw connection to the same file, for
// writes the store refuses to make.
func openLadoAuditStore(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return s, raw
}

func TestAppendLadoAuditEventChainsEachEntryToThePreviousOne(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var appended []domain.LadoAuditEvent
	for _, action := range []string{"plan.approved", "queue.admitted", "execution.dispatched"} {
		ev, err := s.AppendLadoAuditEvent(ctx, sampleLadoAuditEvent(action))
		if err != nil {
			t.Fatalf("append %s: %v", action, err)
		}
		appended = append(appended, ev)
	}

	if appended[0].PrevSHA256 != ladoAuditGenesis {
		t.Fatalf("first entry prev_sha256 = %q, want the genesis value", appended[0].PrevSHA256)
	}
	for i := 1; i < len(appended); i++ {
		if appended[i].ID <= appended[i-1].ID {
			t.Fatalf("entry %d id %d does not follow %d", i, appended[i].ID, appended[i-1].ID)
		}
		if want := expectedLadoAuditSHA256(appended[i-1]); appended[i].PrevSHA256 != want {
			t.Fatalf("entry %d prev_sha256 = %s, want %s", i, appended[i].PrevSHA256, want)
		}
	}
	want := sampleLadoAuditEvent("plan.approved")
	if got := appended[0]; !got.At.Equal(want.At) || got.Actor != want.Actor || got.SubjectKind != want.SubjectKind ||
		got.SubjectID != want.SubjectID || string(got.Payload) != string(want.Payload) {
		t.Fatalf("stored entry %+v does not match appended %+v", got, want)
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = %d, %v; want an intact chain", broken, err)
	}
}

func TestAppendLadoAuditEventDefaultsAnEmptyPayloadToAnObject(t *testing.T) {
	s := newTestStore(t)
	ev := sampleLadoAuditEvent("rule.toggled")
	ev.Payload = nil
	got, err := s.AppendLadoAuditEvent(context.Background(), ev)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if string(got.Payload) != "{}" {
		t.Fatalf("payload = %q, want {}", got.Payload)
	}
}

func TestAppendLadoAuditEventRejectsIncompleteEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for name, edit := range map[string]func(*domain.LadoAuditEvent){
		"no time":         func(ev *domain.LadoAuditEvent) { ev.At = time.Time{} },
		"no actor":        func(ev *domain.LadoAuditEvent) { ev.Actor = "" },
		"no action":       func(ev *domain.LadoAuditEvent) { ev.Action = "" },
		"no subject kind": func(ev *domain.LadoAuditEvent) { ev.SubjectKind = "" },
		"invalid payload": func(ev *domain.LadoAuditEvent) { ev.Payload = json.RawMessage(`{"unterminated"`) },
	} {
		t.Run(name, func(t *testing.T) {
			ev := sampleLadoAuditEvent("plan.approved")
			edit(&ev)
			if _, err := s.AppendLadoAuditEvent(ctx, ev); err == nil {
				t.Fatal("incomplete audit event was appended")
			}
		})
	}
	// Nothing was written, so the next entry is still the first.
	first, err := s.AppendLadoAuditEvent(ctx, sampleLadoAuditEvent("plan.approved"))
	if err != nil {
		t.Fatalf("append after rejections: %v", err)
	}
	if first.PrevSHA256 != ladoAuditGenesis {
		t.Fatalf("a rejected event entered the chain: prev_sha256 = %s", first.PrevSHA256)
	}
}

func TestLadoAuditEventsRefuseUpdatesAndDeletes(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	if _, err := s.AppendLadoAuditEvent(context.Background(), sampleLadoAuditEvent("plan.approved")); err != nil {
		t.Fatalf("append: %v", err)
	}
	for _, statement := range []string{
		`UPDATE lado_audit_events SET actor = 'agent:session-1'`,
		`DELETE FROM lado_audit_events`,
	} {
		_, err := raw.Exec(statement)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: err = %v, want the append-only refusal", statement, err)
		}
	}
}

func TestVerifyLadoAuditChainFindsTheEntryAfterAnEdit(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("empty log: VerifyLadoAuditChain = %d, %v", broken, err)
	}
	var ids []int64
	for _, action := range []string{"plan.approved", "queue.admitted", "execution.dispatched"} {
		ev, err := s.AppendLadoAuditEvent(ctx, sampleLadoAuditEvent(action))
		if err != nil {
			t.Fatalf("append %s: %v", action, err)
		}
		ids = append(ids, ev.ID)
	}
	// An edit outside the store, with the guard trigger removed first.
	if _, err := raw.Exec(`DROP TRIGGER lado_audit_events_no_update`); err != nil {
		t.Fatalf("drop guard: %v", err)
	}
	if _, err := raw.Exec(`UPDATE lado_audit_events SET payload_json = '{"sha256":"forged"}' WHERE id = ?`, ids[1]); err != nil {
		t.Fatalf("edit entry: %v", err)
	}
	broken, err := s.VerifyLadoAuditChain(ctx)
	if err != nil {
		t.Fatalf("VerifyLadoAuditChain: %v", err)
	}
	if broken != ids[2] {
		t.Fatalf("VerifyLadoAuditChain = %d, want %d, the entry after the edited one", broken, ids[2])
	}
}

func TestAppendLadoAuditEventKeepsOneChainUnderConcurrentWriters(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Go(func() {
			if _, err := s.AppendLadoAuditEvent(ctx, sampleLadoAuditEvent(fmt.Sprintf("writer.%d", i))); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent append: %v", err)
	}
	var entries, distinctPrev int
	if err := raw.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT prev_sha256) FROM lado_audit_events`).Scan(&entries, &distinctPrev); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if entries != writers || distinctPrev != writers {
		t.Fatalf("%d entries with %d distinct prev values, want %d of each", entries, distinctPrev, writers)
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = %d, %v; want an intact chain", broken, err)
	}
}
