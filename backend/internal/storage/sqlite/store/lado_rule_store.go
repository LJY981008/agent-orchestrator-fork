package store

// LADO fork: rules, rule versions and rule bindings (docs/LJY-PRD/HARNESS.md
// §3-§5 and §8). Every write commits together with its audit event.

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

// EnsureLadoBuiltinRules creates the seeded rules that do not exist yet, each
// as CreateLadoRule does and with its seed's audit event, in one transaction.
// It returns the ids it added; a rule that exists is left as it is.
func (s *Store) EnsureLadoBuiltinRules(ctx context.Context, seeds []domain.LadoRuleSeed) ([]domain.LadoRuleID, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer s.writeMu.Unlock()
	added := []domain.LadoRuleID{}
	err := s.inTx(ctx, "ensure LADO built-in rules", func(q *gen.Queries) error {
		for _, seed := range seeds {
			_, err := q.GetLadoRuleRow(ctx, string(seed.Rule.ID))
			if err == nil {
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err := insertLadoRuleTx(ctx, q, seed.Rule); err != nil {
				return err
			}
			if _, err := appendLadoAuditEventTx(ctx, q, seed.Event); err != nil {
				return err
			}
			added = append(added, seed.Rule.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return added, nil
}

// ListLadoRules returns the rules with their active versions and bindings,
// ordered by creation time, then id. Archived rules are left out unless
// includeArchived.
func (s *Store) ListLadoRules(ctx context.Context, includeArchived bool) ([]domain.LadoRule, error) {
	rows, err := s.qr.ListLadoRuleRows(ctx, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("list LADO rules: %w", err)
	}
	versions, err := s.qr.ListLatestLadoRuleVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO rule versions: %w", err)
	}
	bindings, err := s.qr.ListLadoRuleBindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO rule bindings: %w", err)
	}
	active := make(map[string]gen.LadoRuleVersion, len(versions))
	for _, row := range versions {
		active[row.RuleID] = row
	}
	byRule := map[string][]gen.LadoRuleBinding{}
	for _, row := range bindings {
		byRule[row.RuleID] = append(byRule[row.RuleID], row)
	}
	rules := make([]domain.LadoRule, 0, len(rows))
	for _, row := range rows {
		version, ok := active[row.ID]
		if !ok {
			// Only a rule deleted between the reads has no version.
			continue
		}
		rules = append(rules, ladoRuleFromGen(row, version, byRule[row.ID]))
	}
	return rules, nil
}

// LadoRule returns one rule with its active version and bindings, archived
// or not.
func (s *Store) LadoRule(ctx context.Context, id domain.LadoRuleID) (domain.LadoRule, bool, error) {
	rule, ok, err := readLadoRule(ctx, s.qr, id)
	if err != nil {
		return domain.LadoRule{}, false, fmt.Errorf("read LADO rule: %w", err)
	}
	return rule, ok, nil
}

// ListLadoRuleVersions returns a rule's versions, oldest first.
func (s *Store) ListLadoRuleVersions(ctx context.Context, id domain.LadoRuleID) ([]domain.LadoRuleVersion, error) {
	rows, err := s.qr.ListLadoRuleVersions(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list LADO rule versions: %w", err)
	}
	versions := make([]domain.LadoRuleVersion, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, ladoRuleVersionFromGen(row))
	}
	return versions, nil
}

// CreateLadoRule stores a new rule with rule.Version as version 1, its Global
// binding, its Overrides and its audit event in one transaction. The version
// and the bindings take the rule's id and creation time, an Override that
// inherits stores nothing, and the version's content is stored normalized
// with its sha256.
func (s *Store) CreateLadoRule(ctx context.Context, rule domain.LadoRule, event domain.LadoAuditEvent) (domain.LadoRule, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoRule{}, err
	}
	defer s.writeMu.Unlock()
	var created domain.LadoRule
	err := s.inTx(ctx, "create LADO rule", func(q *gen.Queries) error {
		if err := insertLadoRuleTx(ctx, q, rule); err != nil {
			return err
		}
		if _, err := appendLadoAuditEventTx(ctx, q, event); err != nil {
			return err
		}
		var err error
		created, err = mustReadLadoRuleTx(ctx, q, rule.ID)
		return err
	})
	if err != nil {
		return domain.LadoRule{}, err
	}
	return created, nil
}

// AddLadoRuleVersion stores v as the rule's next version, the presence proof
// that allowed it when there is one, and its audit event in one transaction.
// base is the active version number the edit started from: when the rule has
// another one by now, or no longer exists, nothing is stored and the error is
// domain.ErrLadoRuleChanged. A proof whose nonce was used before is refused
// with domain.ErrLadoPresenceReplayed. It returns the rule with v active.
func (s *Store) AddLadoRuleVersion(ctx context.Context, v domain.LadoRuleVersion, base int, proof *domain.LadoPresenceProof, event domain.LadoAuditEvent) (domain.LadoRule, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoRule{}, err
	}
	defer s.writeMu.Unlock()
	var updated domain.LadoRule
	err := s.inTx(ctx, "add LADO rule version", func(q *gen.Queries) error {
		latest, err := q.GetLatestLadoRuleVersion(ctx, string(v.RuleID))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrLadoRuleChanged
		}
		if err != nil {
			return err
		}
		if int(latest.Number) != base {
			return domain.ErrLadoRuleChanged
		}
		if proof != nil {
			if err := insertLadoPresenceProof(ctx, q, *proof); err != nil {
				return err
			}
		}
		v.Number = base + 1
		if err := insertLadoRuleVersionTx(ctx, q, v); err != nil {
			return err
		}
		if _, err := appendLadoAuditEventTx(ctx, q, event); err != nil {
			return err
		}
		updated, err = mustReadLadoRuleTx(ctx, q, v.RuleID)
		return err
	})
	if err != nil {
		return domain.LadoRule{}, unwrapLadoSentinel(err)
	}
	return updated, nil
}

// SetLadoRuleArchived archives the rule at archivedAt, or unarchives it when
// archivedAt is zero, together with its audit event.
func (s *Store) SetLadoRuleArchived(ctx context.Context, id domain.LadoRuleID, archivedAt time.Time, event domain.LadoAuditEvent) (domain.LadoRule, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoRule{}, err
	}
	defer s.writeMu.Unlock()
	var rule domain.LadoRule
	err := s.inTx(ctx, "archive LADO rule", func(q *gen.Queries) error {
		n, err := q.SetLadoRuleArchivedAt(ctx, gen.SetLadoRuleArchivedAtParams{
			ArchivedAt: ladoNullTime(archivedAt),
			ID:         string(id),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("LADO rule %s does not exist", id)
		}
		if _, err := appendLadoAuditEventTx(ctx, q, event); err != nil {
			return err
		}
		rule, err = mustReadLadoRuleTx(ctx, q, id)
		return err
	})
	if err != nil {
		return domain.LadoRule{}, err
	}
	return rule, nil
}

// DeleteLadoRule removes the rule with its versions and bindings, and appends
// its audit event, in one transaction. A rule that a rule snapshot used is
// refused with domain.ErrLadoRuleReferenced; it can be archived instead.
func (s *Store) DeleteLadoRule(ctx context.Context, id domain.LadoRuleID, event domain.LadoAuditEvent) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "delete LADO rule", func(q *gen.Queries) error {
		used, err := q.CountLadoSnapshotRulesForRule(ctx, string(id))
		if err != nil {
			return err
		}
		if used > 0 {
			return domain.ErrLadoRuleReferenced
		}
		if err := q.DeleteLadoRuleBindingsForRule(ctx, string(id)); err != nil {
			return err
		}
		if err := q.DeleteLadoRuleVersions(ctx, string(id)); err != nil {
			return err
		}
		n, err := q.DeleteLadoRuleRow(ctx, string(id))
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("LADO rule %s does not exist", id)
		}
		_, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	return unwrapLadoSentinel(err)
}

// SetLadoRuleBinding stores a binding together with its audit event. Inherit
// removes the scope's binding instead, since a scope without one inherits. A
// state the scope does not take, such as force_on at Global, fails the
// table's CHECK; the service validates before.
func (s *Store) SetLadoRuleBinding(ctx context.Context, b domain.LadoRuleBinding, event domain.LadoAuditEvent) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "set LADO rule binding", func(q *gen.Queries) error {
		if err := setLadoRuleBindingTx(ctx, q, b); err != nil {
			return err
		}
		_, err := appendLadoAuditEventTx(ctx, q, event)
		return err
	})
}

// ListLadoModeProjectIDs returns the projects that have LADO mode on.
func (s *Store) ListLadoModeProjectIDs(ctx context.Context) ([]domain.ProjectID, error) {
	rows, err := s.qr.ListLadoModeProjectIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO mode projects: %w", err)
	}
	ids := make([]domain.ProjectID, 0, len(rows))
	for _, id := range rows {
		ids = append(ids, domain.ProjectID(id))
	}
	return ids, nil
}

// insertLadoRuleTx inserts a new rule with its first version and bindings.
func insertLadoRuleTx(ctx context.Context, q *gen.Queries, rule domain.LadoRule) error {
	if err := q.InsertLadoRule(ctx, gen.InsertLadoRuleParams{
		ID:        string(rule.ID),
		Builtin:   boolInt(rule.Builtin),
		Protected: boolInt(rule.Protected),
		CreatedAt: rule.CreatedAt.UTC(),
	}); err != nil {
		return err
	}
	version := rule.Version
	version.RuleID, version.Number, version.CreatedAt = rule.ID, 1, rule.CreatedAt
	if err := insertLadoRuleVersionTx(ctx, q, version); err != nil {
		return err
	}
	bindings := append([]domain.LadoRuleBinding{{Scope: domain.LadoScopeGlobal, State: rule.Global}}, rule.Overrides...)
	for _, b := range bindings {
		b.RuleID, b.UpdatedAt = rule.ID, rule.CreatedAt
		if err := setLadoRuleBindingTx(ctx, q, b); err != nil {
			return err
		}
	}
	return nil
}

// insertLadoRuleVersionTx stores a version's content normalized, with the
// sha256 of that content.
func insertLadoRuleVersionTx(ctx context.Context, q *gen.Queries, v domain.LadoRuleVersion) error {
	content := v.Content.Normalize()
	enforcement, err := json.Marshal(content.Enforcement)
	if err != nil {
		return err
	}
	conflicts, err := json.Marshal(content.Conflicts)
	if err != nil {
		return err
	}
	_, err = q.InsertLadoRuleVersion(ctx, gen.InsertLadoRuleVersionParams{
		ID:              string(v.ID),
		RuleID:          string(v.RuleID),
		Number:          int64(v.Number),
		Name:            content.Name,
		Category:        content.Category,
		Instruction:     content.Instruction,
		EnforcementJson: string(enforcement),
		ConfigJson:      string(content.Config),
		ConflictsJson:   string(conflicts),
		Sha256:          content.SHA256(),
		Actor:           v.Actor,
		CreatedAt:       v.CreatedAt.UTC(),
	})
	return err
}

func setLadoRuleBindingTx(ctx context.Context, q *gen.Queries, b domain.LadoRuleBinding) error {
	if b.State == domain.LadoBindingInherit {
		_, err := q.DeleteLadoRuleBinding(ctx, gen.DeleteLadoRuleBindingParams{
			RuleID:  string(b.RuleID),
			Scope:   string(b.Scope),
			ScopeID: b.ScopeID,
		})
		return err
	}
	return q.UpsertLadoRuleBinding(ctx, gen.UpsertLadoRuleBindingParams{
		RuleID:    string(b.RuleID),
		Scope:     string(b.Scope),
		ScopeID:   b.ScopeID,
		State:     string(b.State),
		UpdatedAt: b.UpdatedAt.UTC(),
	})
}

// readLadoRule reads one rule through q, the read pool or a transaction.
func readLadoRule(ctx context.Context, q *gen.Queries, id domain.LadoRuleID) (domain.LadoRule, bool, error) {
	row, err := q.GetLadoRuleRow(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoRule{}, false, nil
	}
	if err != nil {
		return domain.LadoRule{}, false, err
	}
	version, err := q.GetLatestLadoRuleVersion(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		// Only a rule deleted between the reads has no version.
		return domain.LadoRule{}, false, nil
	}
	if err != nil {
		return domain.LadoRule{}, false, err
	}
	bindings, err := q.ListLadoRuleBindingsForRule(ctx, string(id))
	if err != nil {
		return domain.LadoRule{}, false, err
	}
	return ladoRuleFromGen(row, version, bindings), true, nil
}

// mustReadLadoRuleTx reads back a rule the transaction just wrote.
func mustReadLadoRuleTx(ctx context.Context, q *gen.Queries, id domain.LadoRuleID) (domain.LadoRule, error) {
	rule, ok, err := readLadoRule(ctx, q, id)
	if err == nil && !ok {
		err = fmt.Errorf("LADO rule %s does not exist", id)
	}
	return rule, err
}

// ladoRuleFromGen assembles a rule from its row, its active version and its
// bindings. A rule without a Global binding reads as off.
func ladoRuleFromGen(row gen.LadoRule, version gen.LadoRuleVersion, bindings []gen.LadoRuleBinding) domain.LadoRule {
	rule := domain.LadoRule{
		ID:         domain.LadoRuleID(row.ID),
		Builtin:    row.Builtin == 1,
		Protected:  row.Protected == 1,
		Version:    ladoRuleVersionFromGen(version),
		Global:     domain.LadoBindingOff,
		Overrides:  []domain.LadoRuleBinding{},
		CreatedAt:  row.CreatedAt.UTC(),
		ArchivedAt: ladoTimeFromNull(row.ArchivedAt),
	}
	for _, b := range bindings {
		if b.Scope == string(domain.LadoScopeGlobal) {
			rule.Global = domain.LadoBindingState(b.State)
			continue
		}
		rule.Overrides = append(rule.Overrides, domain.LadoRuleBinding{
			RuleID:    domain.LadoRuleID(b.RuleID),
			Scope:     domain.LadoRuleScope(b.Scope),
			ScopeID:   b.ScopeID,
			State:     domain.LadoBindingState(b.State),
			UpdatedAt: b.UpdatedAt.UTC(),
		})
	}
	return rule
}

func ladoRuleVersionFromGen(row gen.LadoRuleVersion) domain.LadoRuleVersion {
	content := domain.LadoRuleContent{
		Name:        row.Name,
		Category:    row.Category,
		Instruction: row.Instruction,
		Config:      json.RawMessage(row.ConfigJson),
	}
	// The JSON columns only ever hold what insertLadoRuleVersionTx wrote.
	_ = json.Unmarshal([]byte(row.EnforcementJson), &content.Enforcement)
	_ = json.Unmarshal([]byte(row.ConflictsJson), &content.Conflicts)
	return domain.LadoRuleVersion{
		ID:        domain.LadoRuleVersionID(row.ID),
		RuleID:    domain.LadoRuleID(row.RuleID),
		Number:    int(row.Number),
		Content:   content,
		SHA256:    row.Sha256,
		Actor:     row.Actor,
		CreatedAt: row.CreatedAt.UTC(),
	}
}
