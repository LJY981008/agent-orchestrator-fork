package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	moderncsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var ladoRuleAt = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

func ladoRuleContent(name string) domain.LadoRuleContent {
	return domain.LadoRuleContent{
		Name:        name,
		Category:    "git",
		Instruction: "Write commit messages in Korean.",
		Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementPrompt},
	}
}

func ladoUserRule(id string, at time.Time) domain.LadoRule {
	return domain.LadoRule{
		ID: domain.LadoRuleID(id),
		Version: domain.LadoRuleVersion{
			ID:      domain.LadoRuleVersionID(id + "-v1"),
			Content: ladoRuleContent("Rule " + id),
			Actor:   "api",
		},
		Global:    domain.LadoBindingOff,
		CreatedAt: at,
	}
}

func ladoRuleEvent(action string, rule domain.LadoRuleID, at time.Time) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{At: at, Actor: "api", Action: action, SubjectKind: "rule", SubjectID: string(rule)}
}

// ladoNextVersion is the version an edit at base adds: the store numbers it.
func ladoNextVersion(rule domain.LadoRuleID, id string, instruction string, at time.Time) domain.LadoRuleVersion {
	content := ladoRuleContent("Rule " + string(rule))
	content.Instruction = instruction
	return domain.LadoRuleVersion{ID: domain.LadoRuleVersionID(id), RuleID: rule, Content: content, Actor: "api", CreatedAt: at}
}

func ladoRuleProof(nonce string, rule domain.LadoRuleID) *domain.LadoPresenceProof {
	return &domain.LadoPresenceProof{
		Nonce:     nonce,
		Action:    domain.LadoPresenceRuleChange,
		SubjectID: string(rule),
		ExpiresAt: ladoRuleAt.Add(2 * time.Minute),
		UsedAt:    ladoRuleAt.Add(time.Minute),
	}
}

func mustCreateLadoRule(t *testing.T, s *sqlite.Store, rule domain.LadoRule) domain.LadoRule {
	t.Helper()
	created, err := s.CreateLadoRule(context.Background(), rule, ladoRuleEvent("rule.created", rule.ID, rule.CreatedAt))
	if err != nil {
		t.Fatalf("CreateLadoRule %s: %v", rule.ID, err)
	}
	return created
}

func mustLadoRule(t *testing.T, s *sqlite.Store, id domain.LadoRuleID) domain.LadoRule {
	t.Helper()
	rule, ok, err := s.LadoRule(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("LadoRule %s = (%v, %v), want the rule", id, ok, err)
	}
	return rule
}

// ladoBuiltinSeeds mirrors the built-in rules LADO seeds at daemon start.
func ladoBuiltinSeeds() []domain.LadoRuleSeed {
	seed := func(id domain.LadoRuleID, protected bool, global domain.LadoBindingState, content domain.LadoRuleContent) domain.LadoRuleSeed {
		return domain.LadoRuleSeed{
			Rule: domain.LadoRule{
				ID:        id,
				Builtin:   true,
				Protected: protected,
				Version:   domain.LadoRuleVersion{ID: domain.LadoRuleVersionID(string(id) + "@1"), Content: content, Actor: "lado"},
				Global:    global,
				CreatedAt: ladoRuleAt,
			},
			Event: domain.LadoAuditEvent{At: ladoRuleAt, Actor: "lado", Action: "rule.created", SubjectKind: "rule", SubjectID: string(id)},
		}
	}
	return []domain.LadoRuleSeed{
		seed(domain.LadoRulePlanApproval, true, domain.LadoBindingOn, domain.LadoRuleContent{
			Name: "Plan approval required", Category: "workflow",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementWorkflowGate},
		}),
		seed(domain.LadoRulePermissionCeiling, true, domain.LadoBindingOn, domain.LadoRuleContent{
			Name: "Permission ceiling", Category: "tools",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementToolPolicy},
			Config:      json.RawMessage(`{"mode":"auto"}`),
		}),
		seed(domain.LadoRuleKoreanCommits, false, domain.LadoBindingOff, ladoRuleContent("Commit messages in Korean")),
	}
}

// ladoRuleRows counts the rows of the rule tables, the presence proofs and
// the audit log.
type ladoRuleRows struct {
	rules, versions, bindings, proofs, events int
}

func countLadoRuleRows(t *testing.T, raw *sql.DB) ladoRuleRows {
	t.Helper()
	var rows ladoRuleRows
	for table, n := range map[string]*int{
		"lado_rules":           &rows.rules,
		"lado_rule_versions":   &rows.versions,
		"lado_rule_bindings":   &rows.bindings,
		"lado_presence_proofs": &rows.proofs,
		"lado_audit_events":    &rows.events,
	} {
		if err := raw.QueryRow("SELECT COUNT(*) FROM " + table).Scan(n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	return rows
}

func latestLadoAuditAction(t *testing.T, raw *sql.DB) string {
	t.Helper()
	var action string
	if err := raw.QueryRow("SELECT action FROM lado_audit_events ORDER BY id DESC LIMIT 1").Scan(&action); err != nil {
		t.Fatalf("read the latest audit event: %v", err)
	}
	return action
}

func TestEnsureLadoBuiltinRulesIsIdempotent(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()

	added, err := s.EnsureLadoBuiltinRules(ctx, ladoBuiltinSeeds())
	if err != nil {
		t.Fatalf("EnsureLadoBuiltinRules: %v", err)
	}
	want := []domain.LadoRuleID{domain.LadoRulePlanApproval, domain.LadoRulePermissionCeiling, domain.LadoRuleKoreanCommits}
	if !reflect.DeepEqual(added, want) {
		t.Fatalf("first call added %v, want %v", added, want)
	}
	first := countLadoRuleRows(t, raw)
	if first != (ladoRuleRows{rules: 3, versions: 3, bindings: 3, events: 3}) {
		t.Fatalf("rows after seeding = %+v, want one rule, version, Global binding and event per seed", first)
	}

	added, err = s.EnsureLadoBuiltinRules(ctx, ladoBuiltinSeeds())
	if err != nil {
		t.Fatalf("EnsureLadoBuiltinRules again: %v", err)
	}
	if len(added) != 0 {
		t.Fatalf("second call added %v, want nothing", added)
	}
	if again := countLadoRuleRows(t, raw); again != first {
		t.Fatalf("rows after the second call = %+v, want %+v", again, first)
	}

	ceiling := mustLadoRule(t, s, domain.LadoRulePermissionCeiling)
	if !ceiling.Builtin || !ceiling.Protected || ceiling.Global != domain.LadoBindingOn || ceiling.Version.Number != 1 || ceiling.Version.Actor != "lado" {
		t.Fatalf("seeded ceiling = %+v", ceiling)
	}
	if string(ceiling.Version.Content.Config) != `{"mode":"auto"}` {
		t.Fatalf("ceiling config = %s, want the seeded mode", ceiling.Version.Content.Config)
	}
	if korean := mustLadoRule(t, s, domain.LadoRuleKoreanCommits); korean.Protected || korean.Global != domain.LadoBindingOff {
		t.Fatalf("seeded Korean commits rule = %+v, want unprotected and off", korean)
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = (%d, %v)", broken, err)
	}
}

func TestLadoRuleRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rule := ladoUserRule("lado-rule-b", ladoRuleAt)
	rule.Version.Number = 7 // the store numbers the first version 1
	rule.Version.Content = domain.LadoRuleContent{
		Name:        "  Commit messages in Korean ",
		Category:    "git",
		Instruction: "Write commit subjects in Korean.\n",
		Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementPrompt, domain.LadoEnforcementValidator},
		Config:      json.RawMessage(`{ "scope": "subject" }`),
		Conflicts:   []domain.LadoRuleID{"lado-rule-z", "lado-rule-a", "lado-rule-z"},
	}
	rule.Global = domain.LadoBindingOn
	rule.Overrides = []domain.LadoRuleBinding{
		{Scope: domain.LadoScopeProject, ScopeID: "mer", State: domain.LadoBindingForceOff},
		{Scope: domain.LadoScopeTask, ScopeID: "lado-task-1", State: domain.LadoBindingForceOn},
	}
	created := mustCreateLadoRule(t, s, rule)
	other := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-a", ladoRuleAt.Add(time.Minute)))

	wantContent := rule.Version.Content.Normalize()
	wantOverrides := []domain.LadoRuleBinding{
		{RuleID: rule.ID, Scope: domain.LadoScopeProject, ScopeID: "mer", State: domain.LadoBindingForceOff, UpdatedAt: ladoRuleAt},
		{RuleID: rule.ID, Scope: domain.LadoScopeTask, ScopeID: "lado-task-1", State: domain.LadoBindingForceOn, UpdatedAt: ladoRuleAt},
	}
	check := func(label string, got domain.LadoRule) {
		t.Helper()
		if !reflect.DeepEqual(got.Version.Content, wantContent) {
			t.Fatalf("%s content = %+v, want %+v", label, got.Version.Content, wantContent)
		}
		if got.Version.SHA256 != rule.Version.Content.SHA256() {
			t.Fatalf("%s sha256 = %s, want %s", label, got.Version.SHA256, rule.Version.Content.SHA256())
		}
		if got.Global != domain.LadoBindingOn || !reflect.DeepEqual(got.Overrides, wantOverrides) {
			t.Fatalf("%s bindings = %s %+v, want on %+v", label, got.Global, got.Overrides, wantOverrides)
		}
		if got.ID != rule.ID || got.Builtin || got.Protected || !got.CreatedAt.Equal(ladoRuleAt) || !got.ArchivedAt.IsZero() {
			t.Fatalf("%s rule = %+v", label, got)
		}
		v := got.Version
		if v.ID != rule.Version.ID || v.RuleID != rule.ID || v.Number != 1 || v.Actor != "api" || !v.CreatedAt.Equal(ladoRuleAt) {
			t.Fatalf("%s version = %+v, want number 1 made with the rule", label, v)
		}
	}
	check("CreateLadoRule", created)
	check("LadoRule", mustLadoRule(t, s, rule.ID))

	rules, err := s.ListLadoRules(ctx, false)
	if err != nil {
		t.Fatalf("ListLadoRules: %v", err)
	}
	if len(rules) != 2 || rules[0].ID != rule.ID || rules[1].ID != other.ID {
		t.Fatalf("ListLadoRules = %+v, want lado-rule-b then lado-rule-a by creation", rules)
	}
	check("ListLadoRules", rules[0])
	if len(rules[1].Overrides) != 0 || rules[1].Global != domain.LadoBindingOff {
		t.Fatalf("the other rule's bindings = %s %+v, want off and no overrides", rules[1].Global, rules[1].Overrides)
	}

	versions, err := s.ListLadoRuleVersions(ctx, rule.ID)
	if err != nil || len(versions) != 1 || !reflect.DeepEqual(versions[0], created.Version) {
		t.Fatalf("ListLadoRuleVersions = (%+v, %v), want the first version", versions, err)
	}
	if _, ok, err := s.LadoRule(ctx, "lado-rule-unknown"); err != nil || ok {
		t.Fatalf("LadoRule of an unknown id = (%v, %v), want not found", ok, err)
	}
}

func TestAddLadoRuleVersionRefusesStaleBase(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	rule := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-1", ladoRuleAt))

	v2 := ladoNextVersion(rule.ID, "lado-rule-1-v2", "Write commit bodies in Korean too.", ladoRuleAt.Add(time.Minute))
	updated, err := s.AddLadoRuleVersion(ctx, v2, 1, nil, ladoRuleEvent("rule.updated", rule.ID, v2.CreatedAt))
	if err != nil {
		t.Fatalf("AddLadoRuleVersion at the active base: %v", err)
	}
	if updated.Version.ID != v2.ID || updated.Version.Number != 2 || updated.Version.SHA256 != v2.Content.SHA256() {
		t.Fatalf("active version = %+v, want version 2", updated.Version)
	}
	before := countLadoRuleRows(t, raw)

	stale := ladoNextVersion(rule.ID, "lado-rule-1-v3", "Write commit subjects in English.", ladoRuleAt.Add(2*time.Minute))
	for _, base := range []int{1, 3} {
		_, err := s.AddLadoRuleVersion(ctx, stale, base, ladoRuleProof("nonce-stale", rule.ID), ladoRuleEvent("rule.updated", rule.ID, stale.CreatedAt))
		if !errors.Is(err, domain.ErrLadoRuleChanged) {
			t.Fatalf("AddLadoRuleVersion at base %d = %v, want ErrLadoRuleChanged", base, err)
		}
	}
	if _, err := s.AddLadoRuleVersion(ctx, ladoNextVersion("lado-rule-unknown", "x-v2", "x", stale.CreatedAt), 1, nil, ladoRuleEvent("rule.updated", "lado-rule-unknown", stale.CreatedAt)); !errors.Is(err, domain.ErrLadoRuleChanged) {
		t.Fatalf("AddLadoRuleVersion of an unknown rule = %v, want ErrLadoRuleChanged", err)
	}
	// Nothing of the refused edits was kept: not the versions, not the
	// proof, not the audit events.
	if after := countLadoRuleRows(t, raw); after != before {
		t.Fatalf("rows after refused edits = %+v, want %+v", after, before)
	}
	if got := mustLadoRule(t, s, rule.ID).Version; got.ID != v2.ID || got.Number != 2 {
		t.Fatalf("active version after refused edits = %+v, want version 2", got)
	}
	if _, err := s.AddLadoRuleVersion(ctx, stale, 2, ladoRuleProof("nonce-stale", rule.ID), ladoRuleEvent("rule.updated", rule.ID, stale.CreatedAt)); err != nil {
		t.Fatalf("AddLadoRuleVersion at the new base with the refused calls' nonce = %v, want success", err)
	}
}

func TestAddLadoRuleVersionRefusesReplayedProof(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	rule := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-1", ladoRuleAt))

	v2 := ladoNextVersion(rule.ID, "lado-rule-1-v2", "Second.", ladoRuleAt.Add(time.Minute))
	if _, err := s.AddLadoRuleVersion(ctx, v2, 1, ladoRuleProof("nonce-1", rule.ID), ladoRuleEvent("rule.protected_changed", rule.ID, v2.CreatedAt)); err != nil {
		t.Fatalf("AddLadoRuleVersion with a fresh proof: %v", err)
	}
	before := countLadoRuleRows(t, raw)
	if before.proofs != 1 {
		t.Fatalf("proofs = %d, want the used one", before.proofs)
	}

	v3 := ladoNextVersion(rule.ID, "lado-rule-1-v3", "Third.", ladoRuleAt.Add(2*time.Minute))
	_, err := s.AddLadoRuleVersion(ctx, v3, 2, ladoRuleProof("nonce-1", rule.ID), ladoRuleEvent("rule.protected_changed", rule.ID, v3.CreatedAt))
	if !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("AddLadoRuleVersion with a used nonce = %v, want ErrLadoPresenceReplayed", err)
	}
	if after := countLadoRuleRows(t, raw); after != before {
		t.Fatalf("rows after a replayed proof = %+v, want %+v", after, before)
	}
	versions, err := s.ListLadoRuleVersions(ctx, rule.ID)
	if err != nil || len(versions) != 2 || versions[0].Number != 1 || versions[1].ID != v2.ID {
		t.Fatalf("ListLadoRuleVersions = (%+v, %v), want versions 1 and 2, oldest first", versions, err)
	}
}

func TestSetLadoRuleArchivedHidesRule(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	kept := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-1", ladoRuleAt))
	archived := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-2", ladoRuleAt.Add(time.Minute)))
	listIDs := func(includeArchived bool) []domain.LadoRuleID {
		t.Helper()
		rules, err := s.ListLadoRules(ctx, includeArchived)
		if err != nil {
			t.Fatalf("ListLadoRules(%v): %v", includeArchived, err)
		}
		ids := []domain.LadoRuleID{}
		for _, rule := range rules {
			ids = append(ids, rule.ID)
		}
		return ids
	}

	at := ladoRuleAt.Add(time.Hour)
	rule, err := s.SetLadoRuleArchived(ctx, archived.ID, at, ladoRuleEvent("rule.archived", archived.ID, at))
	if err != nil || !rule.ArchivedAt.Equal(at) {
		t.Fatalf("SetLadoRuleArchived = (%+v, %v), want archived at %v", rule, err, at)
	}
	if got := listIDs(false); !reflect.DeepEqual(got, []domain.LadoRuleID{kept.ID}) {
		t.Fatalf("default list = %v, want only the rule that is not archived", got)
	}
	if got := listIDs(true); !reflect.DeepEqual(got, []domain.LadoRuleID{kept.ID, archived.ID}) {
		t.Fatalf("list with archived = %v, want both", got)
	}
	if got := mustLadoRule(t, s, archived.ID); !got.ArchivedAt.Equal(at) {
		t.Fatalf("LadoRule of the archived rule = %+v, want it with its archive time", got)
	}

	rule, err = s.SetLadoRuleArchived(ctx, archived.ID, time.Time{}, ladoRuleEvent("rule.unarchived", archived.ID, at.Add(time.Minute)))
	if err != nil || !rule.ArchivedAt.IsZero() {
		t.Fatalf("unarchive = (%+v, %v), want no archive time", rule, err)
	}
	if got := listIDs(false); !reflect.DeepEqual(got, []domain.LadoRuleID{kept.ID, archived.ID}) {
		t.Fatalf("default list after unarchiving = %v, want both", got)
	}
	if _, err := s.SetLadoRuleArchived(ctx, "lado-rule-unknown", at, ladoRuleEvent("rule.archived", "lado-rule-unknown", at)); err == nil {
		t.Fatal("archiving an unknown rule succeeded")
	}
}

func TestSetLadoRuleBindingInheritDeletes(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	rule := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-1", ladoRuleAt))
	project := domain.LadoRuleBinding{RuleID: rule.ID, Scope: domain.LadoScopeProject, ScopeID: "mer", UpdatedAt: ladoRuleAt.Add(time.Minute)}

	project.State = domain.LadoBindingForceOff
	if err := s.SetLadoRuleBinding(ctx, project, ladoRuleEvent("rule.binding_set", rule.ID, project.UpdatedAt)); err != nil {
		t.Fatalf("SetLadoRuleBinding force_off: %v", err)
	}
	if got := mustLadoRule(t, s, rule.ID).Overrides; len(got) != 1 || got[0] != project {
		t.Fatalf("overrides = %+v, want the force_off binding", got)
	}
	if n := countLadoRuleRows(t, raw).bindings; n != 2 {
		t.Fatalf("binding rows = %d, want Global and the project's", n)
	}

	project.State = domain.LadoBindingInherit
	project.UpdatedAt = ladoRuleAt.Add(2 * time.Minute)
	if err := s.SetLadoRuleBinding(ctx, project, ladoRuleEvent("rule.binding_set", rule.ID, project.UpdatedAt)); err != nil {
		t.Fatalf("SetLadoRuleBinding inherit: %v", err)
	}
	got := mustLadoRule(t, s, rule.ID)
	if len(got.Overrides) != 0 || got.Global != domain.LadoBindingOff {
		t.Fatalf("bindings after inherit = %s %+v, want Global off and no overrides", got.Global, got.Overrides)
	}
	if n := countLadoRuleRows(t, raw).bindings; n != 1 {
		t.Fatalf("binding rows after inherit = %d, want only Global", n)
	}
}

func TestLadoRuleBindingCheckRejectsBadStates(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	rule := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-1", ladoRuleAt))
	before := countLadoRuleRows(t, raw)

	for name, b := range map[string]domain.LadoRuleBinding{
		"global force_on": {Scope: domain.LadoScopeGlobal, State: domain.LadoBindingForceOn},
		"project on":      {Scope: domain.LadoScopeProject, ScopeID: "mer", State: domain.LadoBindingOn},
	} {
		t.Run(name, func(t *testing.T) {
			b.RuleID, b.UpdatedAt = rule.ID, ladoRuleAt.Add(time.Minute)
			err := s.SetLadoRuleBinding(ctx, b, ladoRuleEvent("rule.binding_set", rule.ID, b.UpdatedAt))
			var sqliteErr *moderncsqlite.Error
			if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_CONSTRAINT_CHECK {
				t.Fatalf("SetLadoRuleBinding = %v, want the table's CHECK to refuse it", err)
			}
		})
	}
	if after := countLadoRuleRows(t, raw); after != before {
		t.Fatalf("rows after refused bindings = %+v, want %+v", after, before)
	}
}

func TestLadoRuleChangesAppendAuditEvents(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	rule := ladoUserRule("lado-rule-1", ladoRuleAt)
	at := ladoRuleAt.Add(time.Minute)
	for _, change := range []struct {
		action string
		apply  func() error
	}{
		{"rule.created", func() error {
			_, err := s.CreateLadoRule(ctx, rule, ladoRuleEvent("rule.created", rule.ID, rule.CreatedAt))
			return err
		}},
		{"rule.updated", func() error {
			_, err := s.AddLadoRuleVersion(ctx, ladoNextVersion(rule.ID, "lado-rule-1-v2", "Second.", at), 1, nil, ladoRuleEvent("rule.updated", rule.ID, at))
			return err
		}},
		{"rule.archived", func() error {
			_, err := s.SetLadoRuleArchived(ctx, rule.ID, at, ladoRuleEvent("rule.archived", rule.ID, at))
			return err
		}},
		{"rule.unarchived", func() error {
			_, err := s.SetLadoRuleArchived(ctx, rule.ID, time.Time{}, ladoRuleEvent("rule.unarchived", rule.ID, at))
			return err
		}},
		{"rule.binding_set", func() error {
			b := domain.LadoRuleBinding{RuleID: rule.ID, Scope: domain.LadoScopeTask, ScopeID: "lado-task-1", State: domain.LadoBindingForceOn, UpdatedAt: at}
			return s.SetLadoRuleBinding(ctx, b, ladoRuleEvent("rule.binding_set", rule.ID, at))
		}},
		{"rule.deleted", func() error {
			return s.DeleteLadoRule(ctx, rule.ID, ladoRuleEvent("rule.deleted", rule.ID, at))
		}},
	} {
		before := countLadoRuleRows(t, raw).events
		if err := change.apply(); err != nil {
			t.Fatalf("%s: %v", change.action, err)
		}
		if after := countLadoRuleRows(t, raw).events; after != before+1 {
			t.Fatalf("%s appended %d audit events, want 1", change.action, after-before)
		}
		if got := latestLadoAuditAction(t, raw); got != change.action {
			t.Fatalf("latest audit action = %q, want %q", got, change.action)
		}
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = (%d, %v)", broken, err)
	}
}

func TestDeleteLadoRuleRemovesItsRows(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	kept := mustCreateLadoRule(t, s, ladoUserRule("lado-rule-1", ladoRuleAt))
	rule := ladoUserRule("lado-rule-2", ladoRuleAt.Add(time.Minute))
	rule.Overrides = []domain.LadoRuleBinding{{Scope: domain.LadoScopeProject, ScopeID: "mer", State: domain.LadoBindingForceOn}}
	mustCreateLadoRule(t, s, rule)
	at := ladoRuleAt.Add(2 * time.Minute)
	if _, err := s.AddLadoRuleVersion(ctx, ladoNextVersion(rule.ID, "lado-rule-2-v2", "Second.", at), 1, nil, ladoRuleEvent("rule.updated", rule.ID, at)); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteLadoRule(ctx, rule.ID, ladoRuleEvent("rule.deleted", rule.ID, at)); err != nil {
		t.Fatalf("DeleteLadoRule: %v", err)
	}
	if _, ok, err := s.LadoRule(ctx, rule.ID); err != nil || ok {
		t.Fatalf("LadoRule after delete = (%v, %v), want not found", ok, err)
	}
	if got := countLadoRuleRows(t, raw); got.rules != 1 || got.versions != 1 || got.bindings != 1 {
		t.Fatalf("rows after delete = %+v, want only the other rule's", got)
	}
	if got := mustLadoRule(t, s, kept.ID); got.Version.ID != kept.Version.ID {
		t.Fatalf("the other rule = %+v, want it untouched", got)
	}
	if err := s.DeleteLadoRule(ctx, rule.ID, ladoRuleEvent("rule.deleted", rule.ID, at)); err == nil {
		t.Fatal("deleting a deleted rule succeeded")
	}
}

func TestListLadoModeProjectIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for id, on := range map[domain.ProjectID]bool{"zed": true, "mer": false, "abc": true} {
		seedProject(t, s, string(id))
		if _, err := s.SetLadoMode(ctx, id, on, ladoRuleAt); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListLadoModeProjectIDs(ctx)
	if err != nil || !reflect.DeepEqual(got, []domain.ProjectID{"abc", "zed"}) {
		t.Fatalf("ListLadoModeProjectIDs = (%v, %v), want abc and zed", got, err)
	}
}
