package store

// LADO fork: the append-only audit log (docs/LJY-PRD/TDD.md §4.4).

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ladoAuditGenesisSHA256 is the prev_sha256 of the first entry.
var ladoAuditGenesisSHA256 = strings.Repeat("0", 64)

// listLadoAuditChainSQL streams the whole log; it is never pruned, so
// verification reads rows one at a time instead of loading them.
const listLadoAuditChainSQL = `SELECT id, at, actor, action, subject_kind, subject_id, payload_json, prev_sha256
FROM lado_audit_events
ORDER BY id`

// AppendLadoAuditEvent appends ev and returns it with ID and PrevSHA256 set.
// An empty payload is stored as {}.
func (s *Store) AppendLadoAuditEvent(ctx context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoAuditEvent{}, err
	}
	defer s.writeMu.Unlock()
	var appended domain.LadoAuditEvent
	err := s.inTx(ctx, "append LADO audit event", func(q *gen.Queries) error {
		var err error
		appended, err = appendLadoAuditEventTx(ctx, q, ev)
		return err
	})
	if err != nil {
		return domain.LadoAuditEvent{}, err
	}
	return appended, nil
}

// appendLadoAuditEventTx appends ev inside the caller's transaction, so an
// action and its audit event commit together. The caller holds writeMu, which
// keeps the read of the latest entry and the insert in order.
func appendLadoAuditEventTx(ctx context.Context, q *gen.Queries, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	if ev.At.IsZero() {
		return domain.LadoAuditEvent{}, errors.New("append LADO audit event: time is required")
	}
	payload := string(ev.Payload)
	if payload == "" {
		payload = "{}"
	}
	prev := ladoAuditGenesisSHA256
	latest, err := q.GetLatestLadoAuditEvent(ctx)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return domain.LadoAuditEvent{}, fmt.Errorf("read latest entry: %w", err)
	default:
		prev = ladoAuditSHA256(ladoAuditEventFromGen(latest))
	}
	row, err := q.InsertLadoAuditEvent(ctx, gen.InsertLadoAuditEventParams{
		At:          ev.At.UTC(),
		Actor:       ev.Actor,
		Action:      ev.Action,
		SubjectKind: ev.SubjectKind,
		SubjectID:   ev.SubjectID,
		PayloadJson: payload,
		PrevSha256:  prev,
	})
	if err != nil {
		return domain.LadoAuditEvent{}, err
	}
	return ladoAuditEventFromGen(row), nil
}

// VerifyLadoAuditChain returns the ID of the first entry whose prev_sha256 does
// not match the entry before it, or 0 when the chain is intact. Like any
// unkeyed chain it catches an edit, not a writer that also rewrites every later
// entry (T1 in docs/LJY-PRD/adr/0001-threat-model-t1-first.md).
func (s *Store) VerifyLadoAuditChain(ctx context.Context) (int64, error) {
	rows, err := s.readDB.QueryContext(ctx, listLadoAuditChainSQL)
	if err != nil {
		return 0, fmt.Errorf("read LADO audit log: %w", err)
	}
	defer func() { _ = rows.Close() }()
	want := ladoAuditGenesisSHA256
	for rows.Next() {
		var row gen.LadoAuditEvent
		if err := rows.Scan(&row.ID, &row.At, &row.Actor, &row.Action, &row.SubjectKind, &row.SubjectID, &row.PayloadJson, &row.PrevSha256); err != nil {
			return 0, fmt.Errorf("scan LADO audit entry: %w", err)
		}
		ev := ladoAuditEventFromGen(row)
		if ev.PrevSHA256 != want {
			return ev.ID, nil
		}
		want = ladoAuditSHA256(ev)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read LADO audit log: %w", err)
	}
	return 0, nil
}

// ladoAuditSHA256 hashes an entry's canonical form: each field as a netstring
// (<byte length>:<bytes>,) in the order id, at, actor, action, subject_kind,
// subject_id, payload_json, prev_sha256, with at in UTC RFC 3339 with
// nanoseconds and payload_json as stored. Changing it breaks every stored chain.
func ladoAuditSHA256(ev domain.LadoAuditEvent) string {
	h := sha256.New()
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
		h.Write([]byte(strconv.Itoa(len(field)) + ":" + field + ","))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func ladoAuditEventFromGen(row gen.LadoAuditEvent) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{
		ID:          row.ID,
		At:          row.At,
		Actor:       row.Actor,
		Action:      row.Action,
		SubjectKind: row.SubjectKind,
		SubjectID:   row.SubjectID,
		Payload:     json.RawMessage(row.PayloadJson),
		PrevSHA256:  row.PrevSha256,
	}
}
