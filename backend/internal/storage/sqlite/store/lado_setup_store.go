package store

// LADO fork: adopted local setup — adoptions, setup items, their versions,
// bindings and accepted conflicts (docs/LJY-PRD/HARNESS.md §9 and §10). Every
// write commits together with its audit event and, when the desktop app
// confirmed it, its presence proof.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// RecordLadoSetupAdoption stores an adoption with the items it adopted, the
// first version of each, its presence proof and its audit event in one
// transaction: BeginLadoSetupAdoption and CompleteLadoSetupAdoption at once.
// Each item takes the adoption's id and each version number 1; every item
// needs exactly one of the versions. An item whose tool, kind and name a live
// item holds is refused with domain.ErrLadoSetupItemExists, and a proof whose
// nonce was used before with domain.ErrLadoPresenceReplayed. When any part
// fails, nothing is kept.
func (s *Store) RecordLadoSetupAdoption(ctx context.Context, adoption domain.LadoSetupAdoption, items []domain.LadoSetupItem, versions []domain.LadoSetupItemVersion, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error {
	if err := checkLadoSetupFirstVersions(items, versions); err != nil {
		return fmt.Errorf("record LADO setup adoption: %w", err)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "record LADO setup adoption", func(q *gen.Queries) error {
		state := adoption.State
		adoption.State = domain.LadoSetupAdoptionPending
		if err := beginLadoSetupAdoptionTx(ctx, q, adoption, proof); err != nil {
			return err
		}
		adoption.State = state
		return completeLadoSetupAdoptionTx(ctx, q, adoption, items, versions, event)
	})
	return unwrapLadoSentinel(err)
}

// BeginLadoSetupAdoption records a pending adoption with its presence proof
// in one transaction, before it removes the first original. A proof whose
// nonce was used before is refused with domain.ErrLadoPresenceReplayed, and
// nothing is kept.
func (s *Store) BeginLadoSetupAdoption(ctx context.Context, adoption domain.LadoSetupAdoption, proof *domain.LadoPresenceProof) error {
	if adoption.State != domain.LadoSetupAdoptionPending {
		return fmt.Errorf("begin LADO setup adoption: state %q, want pending", adoption.State)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "begin LADO setup adoption", func(q *gen.Queries) error {
		return beginLadoSetupAdoptionTx(ctx, q, adoption, proof)
	})
	return unwrapLadoSentinel(err)
}

// CompleteLadoSetupAdoption ends a pending adoption as adoption.State,
// completed or partial with its error message, and stores the items whose
// originals it removed, their first versions and its audit event in one
// transaction, as RecordLadoSetupAdoption does. An adoption that is not
// pending is refused.
func (s *Store) CompleteLadoSetupAdoption(ctx context.Context, adoption domain.LadoSetupAdoption, items []domain.LadoSetupItem, versions []domain.LadoSetupItemVersion, event domain.LadoAuditEvent) error {
	if err := checkLadoSetupFirstVersions(items, versions); err != nil {
		return fmt.Errorf("complete LADO setup adoption: %w", err)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "complete LADO setup adoption", func(q *gen.Queries) error {
		return completeLadoSetupAdoptionTx(ctx, q, adoption, items, versions, event)
	})
	return unwrapLadoSentinel(err)
}

// AbandonLadoSetupAdoption removes a pending adoption that removed nothing.
// Its proof stays used.
func (s *Store) AbandonLadoSetupAdoption(ctx context.Context, id domain.LadoSetupAdoptionID) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.DeletePendingLadoSetupAdoption(ctx, string(id))
	if err != nil {
		return fmt.Errorf("abandon LADO setup adoption: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("LADO setup adoption %s is not pending", id)
	}
	return nil
}

// MarkInterruptedLadoSetupAdoptions marks every pending adoption partial with
// message: a daemon stopped while it removed originals. The daemon calls it
// when it starts, before any adoption can run. It returns how many it marked.
func (s *Store) MarkInterruptedLadoSetupAdoptions(ctx context.Context, message string) (int64, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return 0, err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.MarkPendingLadoSetupAdoptionsInterrupted(ctx, message)
	if err != nil {
		return 0, fmt.Errorf("mark interrupted LADO setup adoptions: %w", err)
	}
	return n, nil
}

// LadoPresenceProofUsed reports whether a presence proof with nonce was
// stored, so a reused proof can be refused before the work it would allow.
// Storing it later refuses a reuse in any case.
func (s *Store) LadoPresenceProofUsed(ctx context.Context, nonce string) (bool, error) {
	n, err := s.qr.CountLadoPresenceProofsByNonce(ctx, nonce)
	if err != nil {
		return false, fmt.Errorf("read LADO presence proof: %w", err)
	}
	return n > 0, nil
}

func beginLadoSetupAdoptionTx(ctx context.Context, q *gen.Queries, adoption domain.LadoSetupAdoption, proof *domain.LadoPresenceProof) error {
	if err := insertLadoPresenceProofIfAny(ctx, q, proof); err != nil {
		return err
	}
	return q.InsertLadoSetupAdoption(ctx, gen.InsertLadoSetupAdoptionParams{
		ID:             string(adoption.ID),
		CreatedAt:      adoption.CreatedAt.UTC(),
		Actor:          adoption.Actor,
		Os:             adoption.OS,
		PlanSha256:     adoption.PlanSHA256,
		ManifestSha256: adoption.ManifestSHA256,
		State:          string(adoption.State),
		ErrorMessage:   adoption.ErrorMessage,
	})
}

func completeLadoSetupAdoptionTx(ctx context.Context, q *gen.Queries, adoption domain.LadoSetupAdoption, items []domain.LadoSetupItem, versions []domain.LadoSetupItemVersion, event domain.LadoAuditEvent) error {
	if adoption.State != domain.LadoSetupAdoptionCompleted && adoption.State != domain.LadoSetupAdoptionPartial {
		return fmt.Errorf("an adoption ends completed or partial, not %q", adoption.State)
	}
	n, err := q.CompleteLadoSetupAdoption(ctx, gen.CompleteLadoSetupAdoptionParams{
		State:        string(adoption.State),
		ErrorMessage: adoption.ErrorMessage,
		ID:           string(adoption.ID),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("LADO setup adoption %s is not pending", adoption.ID)
	}
	for _, item := range items {
		item.AdoptionID = adoption.ID
		if err := insertLadoSetupItemTx(ctx, q, item); err != nil {
			return err
		}
	}
	for _, v := range versions {
		v.Version = 1
		if err := insertLadoSetupItemVersionTx(ctx, q, v); err != nil {
			return err
		}
	}
	_, err = appendLadoAuditEventTx(ctx, q, event)
	return err
}

// MarkLadoSetupAdoptionPartial records that removing an adoption's originals
// stopped with message. Its items stay live, and restore stays available.
func (s *Store) MarkLadoSetupAdoptionPartial(ctx context.Context, id domain.LadoSetupAdoptionID, message string) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.MarkLadoSetupAdoptionPartial(ctx, gen.MarkLadoSetupAdoptionPartialParams{ErrorMessage: message, ID: string(id)})
	if err != nil {
		return fmt.Errorf("mark LADO setup adoption partial: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("LADO setup adoption %s does not exist", id)
	}
	return nil
}

// RestoreLadoSetupAdoption records that an adoption was restored at at, with
// its JSON report, the presence proof that allowed it when there is one and
// its audit event, in one transaction. Every item the adoption recorded,
// deleted ones included, gets the same time, so none of them is delivered
// again; their versions stay. An adoption restored before is refused with
// domain.ErrLadoSetupAlreadyRestored, and a proof whose nonce was used before
// with domain.ErrLadoPresenceReplayed.
func (s *Store) RestoreLadoSetupAdoption(ctx context.Context, id domain.LadoSetupAdoptionID, report json.RawMessage, at time.Time, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error {
	if at.IsZero() {
		return errors.New("restore LADO setup adoption: time is required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "restore LADO setup adoption", func(q *gen.Queries) error {
		if err := insertLadoPresenceProofIfAny(ctx, q, proof); err != nil {
			return err
		}
		n, err := q.MarkLadoSetupAdoptionRestored(ctx, gen.MarkLadoSetupAdoptionRestoredParams{
			RestoredAt:    ladoNullTime(at),
			RestoreReport: string(report),
			ID:            string(id),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			_, err := q.GetLadoSetupAdoption(ctx, string(id))
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("LADO setup adoption %s does not exist", id)
			}
			if err != nil {
				return err
			}
			return domain.ErrLadoSetupAlreadyRestored
		}
		if _, err := q.MarkLadoSetupItemsRestoredForAdoption(ctx, gen.MarkLadoSetupItemsRestoredForAdoptionParams{
			RestoredAt: ladoNullTime(at),
			AdoptionID: sql.NullString{String: string(id), Valid: true},
		}); err != nil {
			return err
		}
		_, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return unwrapLadoSentinel(err)
}

// CreateLadoSetupItem stores an item made in LADO with version as its version
// 1, the presence proof that allowed it when there is one, and its audit event
// in one transaction. A live item with the same tool, kind and name is refused
// with domain.ErrLadoSetupItemExists, and a proof whose nonce was used before
// with domain.ErrLadoPresenceReplayed.
func (s *Store) CreateLadoSetupItem(ctx context.Context, item domain.LadoSetupItem, version domain.LadoSetupItemVersion, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "create LADO setup item", func(q *gen.Queries) error {
		if err := insertLadoPresenceProofIfAny(ctx, q, proof); err != nil {
			return err
		}
		if err := insertLadoSetupItemTx(ctx, q, item); err != nil {
			return err
		}
		version.ItemID, version.Version = item.ID, 1
		if err := insertLadoSetupItemVersionTx(ctx, q, version); err != nil {
			return err
		}
		_, err := appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return unwrapLadoSentinel(err)
}

// AddLadoSetupItemVersion stores version as the item's next version, the
// presence proof that allowed it when there is one, and its audit event in one
// transaction. baseVersion is the latest version number the change started
// from: when the item has another one by now, nothing is stored and the error
// is domain.ErrLadoSetupItemChanged. An item that is not live is refused with
// domain.ErrLadoSetupItemNotLive, and a proof whose nonce was used before with
// domain.ErrLadoPresenceReplayed.
func (s *Store) AddLadoSetupItemVersion(ctx context.Context, itemID domain.LadoSetupItemID, baseVersion int, version domain.LadoSetupItemVersion, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "add LADO setup item version", func(q *gen.Queries) error {
		if err := requireLiveLadoSetupItemTx(ctx, q, itemID); err != nil {
			return err
		}
		latest, err := q.LatestLadoSetupItemVersion(ctx, string(itemID))
		if err != nil {
			return err
		}
		if int(latest.Version) != baseVersion {
			return domain.ErrLadoSetupItemChanged
		}
		if err := insertLadoPresenceProofIfAny(ctx, q, proof); err != nil {
			return err
		}
		version.ItemID, version.Version = itemID, baseVersion+1
		if err := insertLadoSetupItemVersionTx(ctx, q, version); err != nil {
			return err
		}
		_, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return unwrapLadoSentinel(err)
}

// DeleteLadoSetupItem ends a live item at at together with its audit event.
// It is no longer resolved or delivered, and its versions and bindings stay.
// An item that is not live is refused with domain.ErrLadoSetupItemNotLive.
func (s *Store) DeleteLadoSetupItem(ctx context.Context, id domain.LadoSetupItemID, at time.Time, event domain.LadoAuditEvent) error {
	return s.endLadoSetupRow(ctx, "delete LADO setup item", at, event, domain.ErrLadoSetupItemNotLive, func(q *gen.Queries, end sql.NullTime) (int64, error) {
		return q.SoftDeleteLadoSetupItem(ctx, gen.SoftDeleteLadoSetupItemParams{DeletedAt: end, ID: string(id)})
	})
}

// SetLadoSetupBinding stores a binding of a live item, the presence proof that
// allowed it when there is one, and its audit event in one transaction.
// inherit removes the scope's binding instead: a Project or Task scope then
// inherits, and an item without a Global binding is on. A binding that cannot
// be stored is refused with domain.ErrLadoSetupBindingInvalid, an item that
// is not live with domain.ErrLadoSetupItemNotLive, and a proof whose nonce
// was used before with domain.ErrLadoPresenceReplayed.
func (s *Store) SetLadoSetupBinding(ctx context.Context, b domain.LadoSetupBinding, inherit bool, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error {
	if !inherit {
		if err := b.Validate(); err != nil {
			return err
		}
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "set LADO setup binding", func(q *gen.Queries) error {
		if err := requireLiveLadoSetupItemTx(ctx, q, b.ItemID); err != nil {
			return err
		}
		if err := insertLadoPresenceProofIfAny(ctx, q, proof); err != nil {
			return err
		}
		if inherit {
			if _, err := q.DeleteLadoSetupBinding(ctx, gen.DeleteLadoSetupBindingParams{
				ItemID:  string(b.ItemID),
				Scope:   string(b.Scope),
				ScopeID: b.ScopeID,
			}); err != nil {
				return err
			}
		} else if err := q.UpsertLadoSetupBinding(ctx, gen.UpsertLadoSetupBindingParams{
			ItemID:    string(b.ItemID),
			Scope:     string(b.Scope),
			ScopeID:   b.ScopeID,
			State:     string(b.State),
			UpdatedAt: b.UpdatedAt.UTC(),
		}); err != nil {
			return err
		}
		_, err := appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return unwrapLadoSentinel(err)
}

// AcceptLadoConflict stores the acceptance of a conflict warning on a live
// item together with its audit event. The service decides which results can
// be accepted; an item that is not live is refused with
// domain.ErrLadoSetupItemNotLive.
func (s *Store) AcceptLadoConflict(ctx context.Context, a domain.LadoConflictAcceptance, event domain.LadoAuditEvent) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "accept LADO setup conflict", func(q *gen.Queries) error {
		if err := requireLiveLadoSetupItemTx(ctx, q, a.ItemID); err != nil {
			return err
		}
		if err := q.InsertLadoConflictAcceptance(ctx, gen.InsertLadoConflictAcceptanceParams{
			ID:            string(a.ID),
			Code:          a.Code,
			ItemID:        string(a.ItemID),
			Detail:        a.Detail,
			ContentSha256: a.ContentSHA256,
			Scope:         string(a.Scope),
			ScopeID:       a.ScopeID,
			Actor:         a.Actor,
			CreatedAt:     a.CreatedAt.UTC(),
		}); err != nil {
			return err
		}
		_, err := appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return unwrapLadoSentinel(err)
}

// RevokeLadoConflictAcceptance revokes an acceptance at at together with its
// audit event. One that does not exist or was revoked before is refused with
// domain.ErrLadoConflictAcceptanceNotLive.
func (s *Store) RevokeLadoConflictAcceptance(ctx context.Context, id domain.LadoConflictAcceptanceID, at time.Time, event domain.LadoAuditEvent) error {
	return s.endLadoSetupRow(ctx, "revoke LADO setup conflict acceptance", at, event, domain.ErrLadoConflictAcceptanceNotLive, func(q *gen.Queries, end sql.NullTime) (int64, error) {
		return q.RevokeLadoConflictAcceptance(ctx, gen.RevokeLadoConflictAcceptanceParams{RevokedAt: end, ID: string(id)})
	})
}

// ListLiveLadoSetupItems returns every live item with its latest version,
// ordered by tool, kind and name.
func (s *Store) ListLiveLadoSetupItems(ctx context.Context) ([]domain.LadoSetupItemWithVersion, error) {
	rows, err := s.qr.ListLiveLadoSetupItemsWithLatestVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO setup items: %w", err)
	}
	items := make([]domain.LadoSetupItemWithVersion, 0, len(rows))
	for _, row := range rows {
		items = append(items, ladoSetupItemWithVersionFromGen(row.LadoSetupItem, row.LadoSetupItemVersion))
	}
	return items, nil
}

// LadoSetupItem returns one item, live or not, with its versions, oldest
// first.
func (s *Store) LadoSetupItem(ctx context.Context, id domain.LadoSetupItemID) (domain.LadoSetupItem, []domain.LadoSetupItemVersion, bool, error) {
	row, err := s.qr.GetLadoSetupItem(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoSetupItem{}, nil, false, nil
	}
	if err != nil {
		return domain.LadoSetupItem{}, nil, false, fmt.Errorf("read LADO setup item: %w", err)
	}
	rows, err := s.qr.ListLadoSetupItemVersions(ctx, string(id))
	if err != nil {
		return domain.LadoSetupItem{}, nil, false, fmt.Errorf("list LADO setup item versions: %w", err)
	}
	versions := make([]domain.LadoSetupItemVersion, 0, len(rows))
	for _, v := range rows {
		versions = append(versions, ladoSetupItemVersionFromGen(v))
	}
	return ladoSetupItemFromGen(row), versions, true, nil
}

// CountLadoSetupItemPins counts the rule snapshot pins that name a version of
// an item, live or not. A pinned version stays readable after the item ends.
func (s *Store) CountLadoSetupItemPins(ctx context.Context, id domain.LadoSetupItemID) (int, error) {
	n, err := s.qr.CountLadoPolicySnapshotSetupForItem(ctx, string(id))
	if err != nil {
		return 0, fmt.Errorf("count LADO setup item pins: %w", err)
	}
	return int(n), nil
}

// ListLadoSetupItemsForAdoption returns every item an adoption recorded,
// deleted and restored ones included, ordered by tool, kind and name.
func (s *Store) ListLadoSetupItemsForAdoption(ctx context.Context, id domain.LadoSetupAdoptionID) ([]domain.LadoSetupItem, error) {
	rows, err := s.qr.ListLadoSetupItemsForAdoption(ctx, sql.NullString{String: string(id), Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list LADO setup items of an adoption: %w", err)
	}
	items := make([]domain.LadoSetupItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ladoSetupItemFromGen(row))
	}
	return items, nil
}

// LadoSetupItemVersion returns one item version.
func (s *Store) LadoSetupItemVersion(ctx context.Context, id domain.LadoSetupItemVersionID) (domain.LadoSetupItemVersion, bool, error) {
	row, err := s.qr.GetLadoSetupItemVersion(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoSetupItemVersion{}, false, nil
	}
	if err != nil {
		return domain.LadoSetupItemVersion{}, false, fmt.Errorf("read LADO setup item version: %w", err)
	}
	return ladoSetupItemVersionFromGen(row), true, nil
}

// ListLadoSetupBindings returns the bindings of one Project or Task scope,
// ordered by item.
func (s *Store) ListLadoSetupBindings(ctx context.Context, scope domain.LadoRuleScope, scopeID string) ([]domain.LadoSetupBinding, error) {
	rows, err := s.qr.ListLadoSetupBindingsForScope(ctx, gen.ListLadoSetupBindingsForScopeParams{Scope: string(scope), ScopeID: scopeID})
	if err != nil {
		return nil, fmt.Errorf("list LADO setup bindings: %w", err)
	}
	return ladoSetupBindingsFromGen(rows), nil
}

// ListLadoSetupGlobalBindings returns the Global bindings, ordered by item.
// An item without one is on.
func (s *Store) ListLadoSetupGlobalBindings(ctx context.Context) ([]domain.LadoSetupBinding, error) {
	rows, err := s.qr.ListLadoSetupGlobalBindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO setup Global bindings: %w", err)
	}
	return ladoSetupBindingsFromGen(rows), nil
}

// ListLiveLadoConflictAcceptances returns the acceptances that were not
// revoked, oldest first.
func (s *Store) ListLiveLadoConflictAcceptances(ctx context.Context) ([]domain.LadoConflictAcceptance, error) {
	rows, err := s.qr.ListLiveLadoConflictAcceptances(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO setup conflict acceptances: %w", err)
	}
	acceptances := make([]domain.LadoConflictAcceptance, 0, len(rows))
	for _, row := range rows {
		acceptances = append(acceptances, domain.LadoConflictAcceptance{
			ID:            domain.LadoConflictAcceptanceID(row.ID),
			Code:          row.Code,
			ItemID:        domain.LadoSetupItemID(row.ItemID),
			Detail:        row.Detail,
			ContentSHA256: row.ContentSha256,
			Scope:         domain.LadoRuleScope(row.Scope),
			ScopeID:       row.ScopeID,
			Actor:         row.Actor,
			CreatedAt:     row.CreatedAt.UTC(),
			RevokedAt:     ladoTimeFromNull(row.RevokedAt),
		})
	}
	return acceptances, nil
}

// ListLadoSetupAdoptions returns every adoption, oldest first.
func (s *Store) ListLadoSetupAdoptions(ctx context.Context) ([]domain.LadoSetupAdoption, error) {
	rows, err := s.qr.ListLadoSetupAdoptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO setup adoptions: %w", err)
	}
	adoptions := make([]domain.LadoSetupAdoption, 0, len(rows))
	for _, row := range rows {
		adoptions = append(adoptions, ladoSetupAdoptionFromGen(row))
	}
	return adoptions, nil
}

// LadoSetupAdoption returns one adoption.
func (s *Store) LadoSetupAdoption(ctx context.Context, id domain.LadoSetupAdoptionID) (domain.LadoSetupAdoption, bool, error) {
	row, err := s.qr.GetLadoSetupAdoption(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoSetupAdoption{}, false, nil
	}
	if err != nil {
		return domain.LadoSetupAdoption{}, false, fmt.Errorf("read LADO setup adoption: %w", err)
	}
	return ladoSetupAdoptionFromGen(row), true, nil
}

// LadoSetupVersionsForClientRequest returns the setup item versions that the
// rule snapshot of the entry a client request id "lado:<entry>/<attempt>"
// names pinned, with their items, ordered by tool, kind and name. A worker's
// sessions get these, so a later edit never reaches a running execution. Any
// other id, and an unknown entry, report false.
func (s *Store) LadoSetupVersionsForClientRequest(ctx context.Context, clientRequestID string) ([]domain.LadoSetupItemWithVersion, bool, error) {
	entryID, _, ok := domain.ParseLadoClientRequestID(clientRequestID)
	if !ok {
		return nil, false, nil
	}
	entry, err := s.qr.GetLadoQueueEntry(ctx, string(entryID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read LADO queue entry: %w", err)
	}
	rows, err := s.qr.ListLadoPolicySnapshotSetupVersions(ctx, entry.PolicySnapshotID)
	if err != nil {
		return nil, false, fmt.Errorf("read LADO snapshot setup versions: %w", err)
	}
	pinned := make([]domain.LadoSetupItemWithVersion, 0, len(rows))
	for _, row := range rows {
		pinned = append(pinned, ladoSetupItemWithVersionFromGen(row.LadoSetupItem, row.LadoSetupItemVersion))
	}
	return pinned, true, nil
}

// endLadoSetupRow runs end, an update that ends one live row at the given time
// and counts the rows it changed, together with its audit event. A row that
// does not exist or ended before changes none and is refused with notLive. A
// zero time is refused: it would read back as never ended.
func (s *Store) endLadoSetupRow(ctx context.Context, what string, at time.Time, event domain.LadoAuditEvent, notLive error, end func(*gen.Queries, sql.NullTime) (int64, error)) error {
	if at.IsZero() {
		return fmt.Errorf("%s: time is required", what)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, what, func(q *gen.Queries) error {
		n, err := end(q, ladoNullTime(at))
		if err != nil {
			return err
		}
		if n == 0 {
			return notLive
		}
		_, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return unwrapLadoSentinel(err)
}

// checkLadoSetupFirstVersions checks that each item has exactly one of the
// versions and that each version names one of the items.
func checkLadoSetupFirstVersions(items []domain.LadoSetupItem, versions []domain.LadoSetupItemVersion) error {
	count := make(map[domain.LadoSetupItemID]int, len(items))
	for _, item := range items {
		count[item.ID] = 0
	}
	for _, v := range versions {
		n, ok := count[v.ItemID]
		if !ok {
			return fmt.Errorf("version %s names item %s, which the adoption does not record", v.ID, v.ItemID)
		}
		count[v.ItemID] = n + 1
	}
	for id, n := range count {
		if n != 1 {
			return fmt.Errorf("item %s has %d first versions, want 1", id, n)
		}
	}
	return nil
}

// insertLadoSetupItemTx inserts an item. The live index refuses a second
// live item with the same tool, kind and name as
// domain.ErrLadoSetupItemExists.
func insertLadoSetupItemTx(ctx context.Context, q *gen.Queries, item domain.LadoSetupItem) error {
	err := q.InsertLadoSetupItem(ctx, gen.InsertLadoSetupItemParams{
		ID:         string(item.ID),
		Tool:       string(item.Tool),
		Kind:       string(item.Kind),
		Name:       item.Name,
		OriginPath: item.OriginPath,
		AdoptionID: sql.NullString{String: string(item.AdoptionID), Valid: item.AdoptionID != ""},
		CreatedAt:  item.CreatedAt.UTC(),
	})
	if isSQLiteUnique(err) {
		return domain.ErrLadoSetupItemExists
	}
	return err
}

func insertLadoSetupItemVersionTx(ctx context.Context, q *gen.Queries, v domain.LadoSetupItemVersion) error {
	return q.InsertLadoSetupItemVersion(ctx, gen.InsertLadoSetupItemVersionParams{
		ID:          string(v.ID),
		ItemID:      string(v.ItemID),
		Version:     int64(v.Version),
		ContentKind: string(v.ContentKind),
		Sha256:      v.SHA256,
		SizeBytes:   v.Size,
		Executable:  boolInt(v.Executable),
		Actor:       v.Actor,
		CreatedAt:   v.CreatedAt.UTC(),
	})
}

// requireLiveLadoSetupItemTx refuses an item that does not exist or is not
// live with domain.ErrLadoSetupItemNotLive.
func requireLiveLadoSetupItemTx(ctx context.Context, q *gen.Queries, id domain.LadoSetupItemID) error {
	row, err := q.GetLadoSetupItem(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrLadoSetupItemNotLive
	}
	if err != nil {
		return err
	}
	if !ladoSetupItemFromGen(row).Live() {
		return domain.ErrLadoSetupItemNotLive
	}
	return nil
}

func insertLadoPresenceProofIfAny(ctx context.Context, q *gen.Queries, proof *domain.LadoPresenceProof) error {
	if proof == nil {
		return nil
	}
	return insertLadoPresenceProof(ctx, q, *proof)
}

func ladoSetupItemWithVersionFromGen(item gen.LadoSetupItem, version gen.LadoSetupItemVersion) domain.LadoSetupItemWithVersion {
	return domain.LadoSetupItemWithVersion{Item: ladoSetupItemFromGen(item), Version: ladoSetupItemVersionFromGen(version)}
}

func ladoSetupItemFromGen(row gen.LadoSetupItem) domain.LadoSetupItem {
	return domain.LadoSetupItem{
		ID:         domain.LadoSetupItemID(row.ID),
		Tool:       domain.LadoSetupTool(row.Tool),
		Kind:       domain.LadoSetupKind(row.Kind),
		Name:       row.Name,
		OriginPath: row.OriginPath,
		AdoptionID: domain.LadoSetupAdoptionID(row.AdoptionID.String),
		CreatedAt:  row.CreatedAt.UTC(),
		DeletedAt:  ladoTimeFromNull(row.DeletedAt),
		RestoredAt: ladoTimeFromNull(row.RestoredAt),
	}
}

func ladoSetupItemVersionFromGen(row gen.LadoSetupItemVersion) domain.LadoSetupItemVersion {
	return domain.LadoSetupItemVersion{
		ID:          domain.LadoSetupItemVersionID(row.ID),
		ItemID:      domain.LadoSetupItemID(row.ItemID),
		Version:     int(row.Version),
		ContentKind: domain.LadoSetupContentKind(row.ContentKind),
		SHA256:      row.Sha256,
		Size:        row.SizeBytes,
		Executable:  row.Executable == 1,
		Actor:       row.Actor,
		CreatedAt:   row.CreatedAt.UTC(),
	}
}

func ladoSetupBindingsFromGen(rows []gen.LadoSetupBinding) []domain.LadoSetupBinding {
	bindings := make([]domain.LadoSetupBinding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, domain.LadoSetupBinding{
			ItemID:    domain.LadoSetupItemID(row.ItemID),
			Scope:     domain.LadoRuleScope(row.Scope),
			ScopeID:   row.ScopeID,
			State:     domain.LadoBindingState(row.State),
			UpdatedAt: row.UpdatedAt.UTC(),
		})
	}
	return bindings
}

func ladoSetupAdoptionFromGen(row gen.LadoSetupAdoption) domain.LadoSetupAdoption {
	adoption := domain.LadoSetupAdoption{
		ID:             domain.LadoSetupAdoptionID(row.ID),
		CreatedAt:      row.CreatedAt.UTC(),
		Actor:          row.Actor,
		OS:             row.Os,
		PlanSHA256:     row.PlanSha256,
		ManifestSHA256: row.ManifestSha256,
		State:          domain.LadoSetupAdoptionState(row.State),
		ErrorMessage:   row.ErrorMessage,
		RestoredAt:     ladoTimeFromNull(row.RestoredAt),
	}
	if row.RestoreReport != "" {
		adoption.RestoreReport = json.RawMessage(row.RestoreReport)
	}
	return adoption
}
