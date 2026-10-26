package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var ladoSetupAt = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

// ladoSetupTables are the tables a setup write touches, with the proofs and
// the audit log.
var ladoSetupTables = []string{
	"lado_setup_adoptions", "lado_setup_items", "lado_setup_item_versions", "lado_setup_bindings",
	"lado_conflict_acceptances", "lado_presence_proofs", "lado_audit_events",
}

func ladoSetupItem(id string, kind domain.LadoSetupKind, name string) domain.LadoSetupItem {
	return domain.LadoSetupItem{
		ID:         domain.LadoSetupItemID(id),
		Tool:       domain.LadoSetupToolClaudeCode,
		Kind:       kind,
		Name:       name,
		OriginPath: "/home/dev/.claude/" + name,
		CreatedAt:  ladoSetupAt,
	}
}

// ladoSetupVersion is a file version; the store numbers it.
func ladoSetupVersion(item domain.LadoSetupItemID, id string, sum byte, at time.Time) domain.LadoSetupItemVersion {
	return domain.LadoSetupItemVersion{
		ID:          domain.LadoSetupItemVersionID(id),
		ItemID:      item,
		ContentKind: domain.LadoSetupContentFile,
		SHA256:      ladoSHA(sum),
		Size:        12,
		Actor:       "human",
		CreatedAt:   at,
	}
}

func ladoSetupEvent(action, subject string, at time.Time) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{At: at, Actor: "human", Action: action, SubjectKind: "setup_item", SubjectID: subject}
}

func ladoSetupProof(nonce, action, subject string) *domain.LadoPresenceProof {
	return &domain.LadoPresenceProof{
		Nonce:     nonce,
		Action:    action,
		SubjectID: subject,
		ExpiresAt: ladoSetupAt.Add(2 * time.Minute),
		UsedAt:    ladoSetupAt.Add(time.Minute),
	}
}

func ladoSetupAdoption(id string) domain.LadoSetupAdoption {
	return domain.LadoSetupAdoption{
		ID:         domain.LadoSetupAdoptionID(id),
		CreatedAt:  ladoSetupAt,
		Actor:      "human",
		OS:         "linux",
		PlanSHA256: ladoSHA('9'),
		State:      domain.LadoSetupAdoptionCompleted,
	}
}

// ladoFirstVersions gives each item version "<item>-v1".
func ladoFirstVersions(items ...domain.LadoSetupItem) []domain.LadoSetupItemVersion {
	versions := make([]domain.LadoSetupItemVersion, 0, len(items))
	for _, item := range items {
		versions = append(versions, ladoSetupVersion(item.ID, string(item.ID)+"-v1", 'a', item.CreatedAt))
	}
	return versions
}

func mustAdoptLadoSetup(t *testing.T, s *sqlite.Store, id, nonce string, items ...domain.LadoSetupItem) {
	t.Helper()
	adoption := ladoSetupAdoption(id)
	if err := s.RecordLadoSetupAdoption(context.Background(), adoption, items, ladoFirstVersions(items...),
		ladoSetupEvent("setup.adopted", id, ladoSetupAt), ladoSetupProof(nonce, domain.LadoPresenceSetupAdopt, adoption.PlanSHA256)); err != nil {
		t.Fatalf("RecordLadoSetupAdoption %s: %v", id, err)
	}
}

// mustCreateLadoSetupItem creates item with version "<item>-v1".
func mustCreateLadoSetupItem(t *testing.T, s *sqlite.Store, item domain.LadoSetupItem) {
	t.Helper()
	if err := s.CreateLadoSetupItem(context.Background(), item, ladoFirstVersions(item)[0],
		ladoSetupEvent("setup.item_created", string(item.ID), item.CreatedAt), nil); err != nil {
		t.Fatalf("CreateLadoSetupItem %s: %v", item.ID, err)
	}
}

func mustLadoSetupItem(t *testing.T, s *sqlite.Store, id domain.LadoSetupItemID) (domain.LadoSetupItem, []domain.LadoSetupItemVersion) {
	t.Helper()
	item, versions, ok, err := s.LadoSetupItem(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("LadoSetupItem %s = (%v, %v), want the item", id, ok, err)
	}
	return item, versions
}

// liveLadoSetup lists the live items as "<item>@<version>".
func liveLadoSetup(t *testing.T, s *sqlite.Store) []string {
	t.Helper()
	items, err := s.ListLiveLadoSetupItems(context.Background())
	if err != nil {
		t.Fatalf("ListLiveLadoSetupItems: %v", err)
	}
	return ladoSetupPairs(items)
}

func ladoSetupPairs(items []domain.LadoSetupItemWithVersion) []string {
	got := []string{}
	for _, iv := range items {
		if iv.Version.ItemID != iv.Item.ID {
			got = append(got, "mismatch:"+string(iv.Item.ID))
			continue
		}
		got = append(got, string(iv.Item.ID)+"@"+string(iv.Version.ID))
	}
	return got
}

func TestLadoSetupAdoptionRecordsItemsVersionsAndAudit(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	codex := ladoSetupItem("lado-setup-3", domain.LadoSetupKindMCPServer, "figma")
	codex.Tool, codex.OriginPath = domain.LadoSetupToolCodex, "/home/dev/.codex/config.toml#mcp_servers.figma"
	items := []domain.LadoSetupItem{
		codex,
		ladoSetupItem("lado-setup-2", domain.LadoSetupKindSkill, "review"),
		ladoSetupItem("lado-setup-1", domain.LadoSetupKindInstructions, "CLAUDE.md"),
	}
	versions := ladoFirstVersions(items...)
	versions[0].ContentKind = domain.LadoSetupContentJSON
	versions[1].ContentKind, versions[1].Size = domain.LadoSetupContentTree, 300
	versions[2].Executable = true
	versions[2].Version = 7 // the store numbers a first version 1
	adoption := ladoSetupAdoption("lado-adoption-1")
	adoption.ManifestSHA256 = ladoSHA('m')
	proof := ladoSetupProof("nonce-1", domain.LadoPresenceSetupAdopt, adoption.PlanSHA256)
	if err := s.RecordLadoSetupAdoption(ctx, adoption, items, versions, ladoSetupEvent("setup.adopted", "lado-adoption-1", ladoSetupAt), proof); err != nil {
		t.Fatalf("RecordLadoSetupAdoption: %v", err)
	}

	got, ok, err := s.LadoSetupAdoption(ctx, adoption.ID)
	if err != nil || !ok || !reflect.DeepEqual(got, adoption) {
		t.Fatalf("LadoSetupAdoption = (%+v, %v, %v), want %+v", got, ok, err, adoption)
	}
	// Ordered by tool, kind and name; each item has the adoption's id.
	if want := []string{"lado-setup-1@lado-setup-1-v1", "lado-setup-2@lado-setup-2-v1", "lado-setup-3@lado-setup-3-v1"}; !reflect.DeepEqual(liveLadoSetup(t, s), want) {
		t.Fatalf("live items = %v, want %v", liveLadoSetup(t, s), want)
	}
	item, stored := mustLadoSetupItem(t, s, "lado-setup-1")
	wantItem := items[2]
	wantItem.AdoptionID = adoption.ID
	wantVersion := versions[2]
	wantVersion.Version = 1
	if !reflect.DeepEqual(item, wantItem) || !reflect.DeepEqual(stored, []domain.LadoSetupItemVersion{wantVersion}) {
		t.Fatalf("LadoSetupItem = (%+v, %+v), want (%+v, [%+v])", item, stored, wantItem, wantVersion)
	}
	if v, ok, err := s.LadoSetupItemVersion(ctx, "lado-setup-2-v1"); err != nil || !ok || v.ContentKind != domain.LadoSetupContentTree || v.Size != 300 {
		t.Fatalf("LadoSetupItemVersion = (%+v, %v, %v), want the tree version", v, ok, err)
	}
	if action := latestLadoAuditAction(t, raw); action != "setup.adopted" {
		t.Fatalf("latest audit action = %q, want setup.adopted", action)
	}

	before := countLadoTables(t, raw, ladoSetupTables...)
	second := ladoSetupAdoption("lado-adoption-2")
	next := []domain.LadoSetupItem{ladoSetupItem("lado-setup-4", domain.LadoSetupKindAgent, "planner.md")}
	badSum := ladoFirstVersions(next...)
	badSum[0].SHA256 = "abc"
	taken := append(next, ladoSetupItem("lado-setup-5", domain.LadoSetupKindSkill, "review"))
	for name, c := range map[string]struct {
		items    []domain.LadoSetupItem
		versions []domain.LadoSetupItemVersion
		event    domain.LadoAuditEvent
		want     error
	}{
		"version the table refuses": {next, badSum, ladoSetupEvent("setup.adopted", "lado-adoption-2", ladoSetupAt), nil},
		"name a live item holds":    {taken, ladoFirstVersions(taken...), ladoSetupEvent("setup.adopted", "lado-adoption-2", ladoSetupAt), domain.ErrLadoSetupItemExists},
		"item without a version":    {taken, ladoFirstVersions(next...), ladoSetupEvent("setup.adopted", "lado-adoption-2", ladoSetupAt), nil},
		"incomplete audit event":    {next, ladoFirstVersions(next...), domain.LadoAuditEvent{Actor: "human", Action: "setup.adopted"}, nil},
	} {
		err := s.RecordLadoSetupAdoption(ctx, second, c.items, c.versions, c.event, ladoSetupProof("nonce-2", domain.LadoPresenceSetupAdopt, second.PlanSHA256))
		if err == nil || (c.want != nil && !errors.Is(err, c.want)) || errors.Is(err, domain.ErrLadoPresenceReplayed) {
			t.Fatalf("%s: RecordLadoSetupAdoption = %v, want a failure (%v)", name, err, c.want)
		}
		if after := countLadoTables(t, raw, ladoSetupTables...); !reflect.DeepEqual(after, before) {
			t.Fatalf("%s: rows = %v, want %v: nothing of a failed adoption stays", name, after, before)
		}
	}
	// A used nonce is refused, and nothing stays either.
	err = s.RecordLadoSetupAdoption(ctx, second, next, ladoFirstVersions(next...), ladoSetupEvent("setup.adopted", "lado-adoption-2", ladoSetupAt), proof)
	if !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("RecordLadoSetupAdoption with a used nonce = %v, want ErrLadoPresenceReplayed", err)
	}
	if after := countLadoTables(t, raw, ladoSetupTables...); !reflect.DeepEqual(after, before) {
		t.Fatalf("replay: rows = %v, want %v", after, before)
	}
	// The failures did not use up their nonce.
	second.State = domain.LadoSetupAdoptionPartial
	if err := s.RecordLadoSetupAdoption(ctx, second, next, ladoFirstVersions(next...), ladoSetupEvent("setup.adopted", "lado-adoption-2", ladoSetupAt), ladoSetupProof("nonce-2", domain.LadoPresenceSetupAdopt, second.PlanSHA256)); err != nil {
		t.Fatalf("RecordLadoSetupAdoption after the failures = %v, want success", err)
	}

	if err := s.MarkLadoSetupAdoptionPartial(ctx, adoption.ID, "remove skills/review: permission denied"); err != nil {
		t.Fatalf("MarkLadoSetupAdoptionPartial: %v", err)
	}
	if err := s.MarkLadoSetupAdoptionPartial(ctx, "lado-adoption-9", "x"); err == nil {
		t.Fatal("MarkLadoSetupAdoptionPartial of an unknown adoption succeeded")
	}
	adoptions, err := s.ListLadoSetupAdoptions(ctx)
	if err != nil || len(adoptions) != 2 {
		t.Fatalf("ListLadoSetupAdoptions = (%+v, %v), want both", adoptions, err)
	}
	if a := adoptions[0]; a.ID != adoption.ID || a.State != domain.LadoSetupAdoptionPartial || a.ErrorMessage != "remove skills/review: permission denied" {
		t.Fatalf("first adoption = %+v, want it partial with the error", a)
	}
	if adoptions[1].ID != second.ID || adoptions[1].State != domain.LadoSetupAdoptionPartial {
		t.Fatalf("second adoption = %+v, want it recorded partial", adoptions[1])
	}
	if _, ok, err := s.LadoSetupAdoption(ctx, "lado-adoption-9"); err != nil || ok {
		t.Fatalf("LadoSetupAdoption of an unknown id = (%v, %v), want none", ok, err)
	}
	if broken, err := s.VerifyLadoAuditChain(ctx); err != nil || broken != 0 {
		t.Fatalf("VerifyLadoAuditChain = (%d, %v)", broken, err)
	}
}

func TestLadoSetupVersionNeedsTheLatestBase(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	item := ladoSetupItem("lado-setup-1", domain.LadoSetupKindInstructions, "CLAUDE.md")
	item.OriginPath = ""
	mustCreateLadoSetupItem(t, s, item)
	at := ladoSetupAt.Add(time.Minute)
	add := func(base int, id string, proof *domain.LadoPresenceProof) error {
		return s.AddLadoSetupItemVersion(ctx, item.ID, base, ladoSetupVersion(item.ID, id, 'b', at), ladoSetupEvent("setup.item_updated", string(item.ID), at), proof)
	}
	if err := add(1, "lado-setup-1-v2", nil); err != nil {
		t.Fatalf("AddLadoSetupItemVersion at base 1: %v", err)
	}
	if got := liveLadoSetup(t, s); !reflect.DeepEqual(got, []string{"lado-setup-1@lado-setup-1-v2"}) {
		t.Fatalf("live items = %v, want the second version", got)
	}

	before := countLadoTables(t, raw, ladoSetupTables...)
	for _, base := range []int{1, 3, 0} {
		if err := add(base, "lado-setup-1-stale", ladoSetupProof("nonce-stale", domain.LadoPresenceSetupChange, "change")); !errors.Is(err, domain.ErrLadoSetupItemChanged) {
			t.Fatalf("AddLadoSetupItemVersion at base %d = %v, want ErrLadoSetupItemChanged", base, err)
		}
	}
	if after := countLadoTables(t, raw, ladoSetupTables...); !reflect.DeepEqual(after, before) {
		t.Fatalf("rows = %v, want %v: a stale edit stores nothing", after, before)
	}
	if err := add(2, "lado-setup-1-v3", ladoSetupProof("nonce-1", domain.LadoPresenceSetupChange, "change")); err != nil {
		t.Fatalf("AddLadoSetupItemVersion at base 2 with a proof: %v", err)
	}
	if err := add(3, "lado-setup-1-v4", ladoSetupProof("nonce-1", domain.LadoPresenceSetupChange, "change")); !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("AddLadoSetupItemVersion with a used nonce = %v, want ErrLadoPresenceReplayed", err)
	}
	if _, versions := mustLadoSetupItem(t, s, item.ID); len(versions) != 3 || versions[2].ID != "lado-setup-1-v3" || versions[2].Version != 3 {
		t.Fatalf("versions = %+v, want three, the last numbered 3", versions)
	}

	// A second live item cannot take the name; a deleted one gives it up.
	other := ladoSetupItem("lado-setup-2", domain.LadoSetupKindInstructions, "CLAUDE.md")
	if err := s.CreateLadoSetupItem(ctx, other, ladoFirstVersions(other)[0], ladoSetupEvent("setup.item_created", "lado-setup-2", at), nil); !errors.Is(err, domain.ErrLadoSetupItemExists) {
		t.Fatalf("CreateLadoSetupItem with a live name = %v, want ErrLadoSetupItemExists", err)
	}
	deletedAt := at.Add(time.Minute)
	if err := s.DeleteLadoSetupItem(ctx, item.ID, deletedAt, ladoSetupEvent("setup.item_deleted", string(item.ID), deletedAt)); err != nil {
		t.Fatalf("DeleteLadoSetupItem: %v", err)
	}
	if err := add(3, "lado-setup-1-v4", nil); !errors.Is(err, domain.ErrLadoSetupItemNotLive) {
		t.Fatalf("AddLadoSetupItemVersion of a deleted item = %v, want ErrLadoSetupItemNotLive", err)
	}
	if err := s.DeleteLadoSetupItem(ctx, item.ID, deletedAt, ladoSetupEvent("setup.item_deleted", string(item.ID), deletedAt)); !errors.Is(err, domain.ErrLadoSetupItemNotLive) {
		t.Fatalf("DeleteLadoSetupItem twice = %v, want ErrLadoSetupItemNotLive", err)
	}
	if err := s.AddLadoSetupItemVersion(ctx, "lado-setup-9", 1, ladoSetupVersion("lado-setup-9", "v", 'b', at), ladoSetupEvent("setup.item_updated", "lado-setup-9", at), nil); !errors.Is(err, domain.ErrLadoSetupItemNotLive) {
		t.Fatalf("AddLadoSetupItemVersion of an unknown item = %v, want ErrLadoSetupItemNotLive", err)
	}
	deleted, versions := mustLadoSetupItem(t, s, item.ID)
	if !deleted.DeletedAt.Equal(deletedAt) || deleted.Live() || len(versions) != 3 {
		t.Fatalf("deleted item = %+v with %d versions, want it ended at %v with its versions", deleted, len(versions), deletedAt)
	}
	if _, ok, err := s.LadoSetupItemVersion(ctx, "lado-setup-1-v2"); err != nil || !ok {
		t.Fatalf("LadoSetupItemVersion of a deleted item = (%v, %v), want it readable", ok, err)
	}
	mustCreateLadoSetupItem(t, s, other)
	if got := liveLadoSetup(t, s); !reflect.DeepEqual(got, []string{"lado-setup-2@lado-setup-2-v1"}) {
		t.Fatalf("live items = %v, want only the new item", got)
	}
}

func TestLadoSetupBindingsSetAndInherit(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	skill := ladoSetupItem("lado-setup-1", domain.LadoSetupKindSkill, "review")
	hook := ladoSetupItem("lado-setup-2", domain.LadoSetupKindHookScript, "guard.sh")
	mustCreateLadoSetupItem(t, s, skill)
	mustCreateLadoSetupItem(t, s, hook)
	set := func(item domain.LadoSetupItemID, scope domain.LadoRuleScope, scopeID string, state domain.LadoBindingState, inherit bool, proof *domain.LadoPresenceProof) error {
		b := domain.LadoSetupBinding{ItemID: item, Scope: scope, ScopeID: scopeID, State: state, UpdatedAt: ladoSetupAt}
		return s.SetLadoSetupBinding(ctx, b, inherit, ladoSetupEvent("setup.binding_set", string(item), ladoSetupAt), proof)
	}
	mustSet := func(item domain.LadoSetupItemID, scope domain.LadoRuleScope, scopeID string, state domain.LadoBindingState, inherit bool) {
		t.Helper()
		if err := set(item, scope, scopeID, state, inherit, nil); err != nil {
			t.Fatalf("SetLadoSetupBinding %s %s %q %s inherit=%v: %v", item, scope, scopeID, state, inherit, err)
		}
	}
	// bindings lists a scope's rows as "<item>=<state>".
	bindings := func(scope domain.LadoRuleScope, scopeID string) []string {
		t.Helper()
		var rows []domain.LadoSetupBinding
		var err error
		if scope == domain.LadoScopeGlobal {
			rows, err = s.ListLadoSetupGlobalBindings(ctx)
		} else {
			rows, err = s.ListLadoSetupBindings(ctx, scope, scopeID)
		}
		if err != nil {
			t.Fatalf("list %s bindings: %v", scope, err)
		}
		got := []string{}
		for _, b := range rows {
			if b.Scope != scope || b.ScopeID != scopeID || !b.UpdatedAt.Equal(ladoSetupAt) {
				t.Fatalf("binding %+v, want scope %s %q", b, scope, scopeID)
			}
			got = append(got, string(b.ItemID)+"="+string(b.State))
		}
		return got
	}
	expect := func(step string, scope domain.LadoRuleScope, scopeID string, want ...string) {
		t.Helper()
		if want == nil {
			want = []string{}
		}
		if got := bindings(scope, scopeID); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %s %q bindings = %v, want %v", step, scope, scopeID, got, want)
		}
	}

	// No Global row means on.
	expect("start", domain.LadoScopeGlobal, "")
	mustSet(skill.ID, domain.LadoScopeGlobal, "", domain.LadoBindingOff, false)
	expect("Global off", domain.LadoScopeGlobal, "", "lado-setup-1=off")
	mustSet(skill.ID, domain.LadoScopeGlobal, "", domain.LadoBindingOn, false)
	expect("Global on", domain.LadoScopeGlobal, "", "lado-setup-1=on")
	mustSet(skill.ID, domain.LadoScopeGlobal, "", "", true)
	expect("Global inherit", domain.LadoScopeGlobal, "")

	mustSet(hook.ID, domain.LadoScopeProject, "mer", domain.LadoBindingForceOff, false)
	mustSet(skill.ID, domain.LadoScopeProject, "mer", domain.LadoBindingForceOn, false)
	mustSet(skill.ID, domain.LadoScopeTask, "lado-task-1", domain.LadoBindingForceOff, false)
	expect("project force", domain.LadoScopeProject, "mer", "lado-setup-1=force_on", "lado-setup-2=force_off")
	expect("task force", domain.LadoScopeTask, "lado-task-1", "lado-setup-1=force_off")
	expect("other project", domain.LadoScopeProject, "abc")
	mustSet(skill.ID, domain.LadoScopeProject, "mer", "", true)
	expect("project inherit", domain.LadoScopeProject, "mer", "lado-setup-2=force_off")
	mustSet(skill.ID, domain.LadoScopeTask, "lado-task-1", "", true)
	expect("task inherit", domain.LadoScopeTask, "lado-task-1")

	before := countLadoTables(t, raw, ladoSetupTables...)
	for name, b := range map[string]domain.LadoSetupBinding{
		"Global force_on":       {ItemID: skill.ID, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingForceOn},
		"Global with a project": {ItemID: skill.ID, Scope: domain.LadoScopeGlobal, ScopeID: "mer", State: domain.LadoBindingOn},
		"project on":            {ItemID: skill.ID, Scope: domain.LadoScopeProject, ScopeID: "mer", State: domain.LadoBindingOn},
		"project without an id": {ItemID: skill.ID, Scope: domain.LadoScopeProject, State: domain.LadoBindingForceOn},
		"stored inherit":        {ItemID: skill.ID, Scope: domain.LadoScopeTask, ScopeID: "lado-task-1", State: domain.LadoBindingInherit},
	} {
		if err := set(b.ItemID, b.Scope, b.ScopeID, b.State, false, nil); !errors.Is(err, domain.ErrLadoSetupBindingInvalid) {
			t.Fatalf("%s: SetLadoSetupBinding = %v, want ErrLadoSetupBindingInvalid", name, err)
		}
	}
	if after := countLadoTables(t, raw, ladoSetupTables...); !reflect.DeepEqual(after, before) {
		t.Fatalf("rows = %v, want %v: an invalid binding stores nothing", after, before)
	}

	// Turning a hook script on carries its proof, which is used once.
	if err := set(hook.ID, domain.LadoScopeProject, "mer", domain.LadoBindingForceOn, false, ladoSetupProof("nonce-1", domain.LadoPresenceSetupChange, "change")); err != nil {
		t.Fatalf("SetLadoSetupBinding with a proof: %v", err)
	}
	if err := set(hook.ID, domain.LadoScopeTask, "lado-task-1", domain.LadoBindingForceOn, false, ladoSetupProof("nonce-1", domain.LadoPresenceSetupChange, "change")); !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("SetLadoSetupBinding with a used nonce = %v, want ErrLadoPresenceReplayed", err)
	}
	expect("proof", domain.LadoScopeProject, "mer", "lado-setup-2=force_on")
	expect("replayed proof", domain.LadoScopeTask, "lado-task-1")

	// A deleted item takes no binding, and keeps the ones it had.
	if err := s.DeleteLadoSetupItem(ctx, hook.ID, ladoSetupAt, ladoSetupEvent("setup.item_deleted", string(hook.ID), ladoSetupAt)); err != nil {
		t.Fatal(err)
	}
	for _, inherit := range []bool{false, true} {
		if err := set(hook.ID, domain.LadoScopeProject, "mer", domain.LadoBindingForceOff, inherit, nil); !errors.Is(err, domain.ErrLadoSetupItemNotLive) {
			t.Fatalf("SetLadoSetupBinding of a deleted item (inherit %v) = %v, want ErrLadoSetupItemNotLive", inherit, err)
		}
	}
	expect("deleted item", domain.LadoScopeProject, "mer", "lado-setup-2=force_on")
	if action := latestLadoAuditAction(t, raw); action != "setup.item_deleted" {
		t.Fatalf("latest audit action = %q, want the delete's: refused bindings append nothing", action)
	}
}

func TestLadoSetupRestoreMarksItems(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	kept := ladoSetupItem("lado-setup-1", domain.LadoSetupKindInstructions, "CLAUDE.md")
	deleted := ladoSetupItem("lado-setup-2", domain.LadoSetupKindSkill, "review")
	other := ladoSetupItem("lado-setup-3", domain.LadoSetupKindAgent, "planner.md")
	created := ladoSetupItem("lado-setup-4", domain.LadoSetupKindCommand, "ship.md")
	mustAdoptLadoSetup(t, s, "lado-adoption-1", "nonce-1", kept, deleted)
	mustAdoptLadoSetup(t, s, "lado-adoption-2", "nonce-2", other)
	mustCreateLadoSetupItem(t, s, created)
	deletedAt := ladoSetupAt.Add(time.Minute)
	if err := s.DeleteLadoSetupItem(ctx, deleted.ID, deletedAt, ladoSetupEvent("setup.item_deleted", string(deleted.ID), deletedAt)); err != nil {
		t.Fatal(err)
	}

	at := ladoSetupAt.Add(time.Hour)
	report := json.RawMessage(`{"entries":[{"name":"CLAUDE.md","result":"restored"}]}`)
	restore := func(id domain.LadoSetupAdoptionID, nonce string) error {
		return s.RestoreLadoSetupAdoption(ctx, id, report, at, ladoSetupEvent("setup.restored", string(id), at), ladoSetupProof(nonce, domain.LadoPresenceSetupRestore, string(id)))
	}
	if err := restore("lado-adoption-1", "nonce-3"); err != nil {
		t.Fatalf("RestoreLadoSetupAdoption: %v", err)
	}
	adoption, ok, err := s.LadoSetupAdoption(ctx, "lado-adoption-1")
	if err != nil || !ok || !adoption.RestoredAt.Equal(at) || string(adoption.RestoreReport) != string(report) {
		t.Fatalf("restored adoption = (%+v, %v, %v), want the restore time and report", adoption, ok, err)
	}
	// Both of its items end, the deleted one too; the others stay live.
	for _, id := range []domain.LadoSetupItemID{kept.ID, deleted.ID} {
		item, versions := mustLadoSetupItem(t, s, id)
		if !item.RestoredAt.Equal(at) || len(versions) != 1 {
			t.Fatalf("item %s = %+v with %d versions, want it restored at %v with its version", id, item, len(versions), at)
		}
	}
	if item, _ := mustLadoSetupItem(t, s, deleted.ID); !item.DeletedAt.Equal(deletedAt) {
		t.Fatalf("deleted item = %+v, want its delete time kept", item)
	}
	if got, want := liveLadoSetup(t, s), []string{"lado-setup-3@lado-setup-3-v1", "lado-setup-4@lado-setup-4-v1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("live items = %v, want %v", got, want)
	}
	if _, ok, err := s.LadoSetupItemVersion(ctx, "lado-setup-1-v1"); err != nil || !ok {
		t.Fatalf("LadoSetupItemVersion of a restored item = (%v, %v), want it readable", ok, err)
	}
	if other, ok, err := s.LadoSetupAdoption(ctx, "lado-adoption-2"); err != nil || !ok || !other.RestoredAt.IsZero() || other.RestoreReport != nil {
		t.Fatalf("other adoption = (%+v, %v, %v), want it not restored", other, ok, err)
	}

	before := countLadoTables(t, raw, ladoSetupTables...)
	if err := restore("lado-adoption-1", "nonce-4"); !errors.Is(err, domain.ErrLadoSetupAlreadyRestored) {
		t.Fatalf("second RestoreLadoSetupAdoption = %v, want ErrLadoSetupAlreadyRestored", err)
	}
	if err := restore("lado-adoption-9", "nonce-4"); err == nil || errors.Is(err, domain.ErrLadoSetupAlreadyRestored) {
		t.Fatalf("RestoreLadoSetupAdoption of an unknown adoption = %v, want an error of its own", err)
	}
	if err := restore("lado-adoption-2", "nonce-3"); !errors.Is(err, domain.ErrLadoPresenceReplayed) {
		t.Fatalf("RestoreLadoSetupAdoption with a used nonce = %v, want ErrLadoPresenceReplayed", err)
	}
	if after := countLadoTables(t, raw, ladoSetupTables...); !reflect.DeepEqual(after, before) {
		t.Fatalf("rows = %v, want %v: refused restores store nothing", after, before)
	}
	// The refusals did not use up their nonce.
	if err := restore("lado-adoption-2", "nonce-4"); err != nil {
		t.Fatalf("RestoreLadoSetupAdoption of the other adoption: %v", err)
	}
	if got := liveLadoSetup(t, s); !reflect.DeepEqual(got, []string{"lado-setup-4@lado-setup-4-v1"}) {
		t.Fatalf("live items = %v, want only the item made in LADO", got)
	}
}

func TestLadoSetupAcceptances(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	item := ladoSetupItem("lado-setup-1", domain.LadoSetupKindSetting, "permissions")
	gone := ladoSetupItem("lado-setup-2", domain.LadoSetupKindSetting, "hooks")
	mustCreateLadoSetupItem(t, s, item)
	mustCreateLadoSetupItem(t, s, gone)
	acceptance := func(id string, item domain.LadoSetupItemID, scope domain.LadoRuleScope, scopeID string, at time.Time) domain.LadoConflictAcceptance {
		return domain.LadoConflictAcceptance{
			ID: domain.LadoConflictAcceptanceID(id), Code: "LADO_SETUP_DENIES_GIT", ItemID: item, Detail: "Bash(git push:*)",
			ContentSHA256: ladoSHA('a'), Scope: scope, ScopeID: scopeID, Actor: "api", CreatedAt: at,
		}
	}
	global := acceptance("lado-acceptance-1", item.ID, domain.LadoScopeGlobal, "", ladoSetupAt)
	project := acceptance("lado-acceptance-2", item.ID, domain.LadoScopeProject, "mer", ladoSetupAt.Add(time.Minute))
	for _, a := range []domain.LadoConflictAcceptance{project, global} {
		if err := s.AcceptLadoConflict(ctx, a, ladoSetupEvent("setup.conflict_accepted", string(a.ID), a.CreatedAt)); err != nil {
			t.Fatalf("AcceptLadoConflict %s: %v", a.ID, err)
		}
	}
	live := func() []domain.LadoConflictAcceptance {
		t.Helper()
		got, err := s.ListLiveLadoConflictAcceptances(ctx)
		if err != nil {
			t.Fatalf("ListLiveLadoConflictAcceptances: %v", err)
		}
		return got
	}
	// Oldest first.
	if got, want := live(), []domain.LadoConflictAcceptance{global, project}; !reflect.DeepEqual(got, want) {
		t.Fatalf("live acceptances = %+v, want %+v", got, want)
	}

	at := ladoSetupAt.Add(time.Hour)
	revoke := func(id domain.LadoConflictAcceptanceID) error {
		return s.RevokeLadoConflictAcceptance(ctx, id, at, ladoSetupEvent("setup.conflict_revoked", string(id), at))
	}
	if err := revoke(global.ID); err != nil {
		t.Fatalf("RevokeLadoConflictAcceptance: %v", err)
	}
	if got, want := live(), []domain.LadoConflictAcceptance{project}; !reflect.DeepEqual(got, want) {
		t.Fatalf("live acceptances after the revoke = %+v, want %+v", got, want)
	}
	if action := latestLadoAuditAction(t, raw); action != "setup.conflict_revoked" {
		t.Fatalf("latest audit action = %q, want setup.conflict_revoked", action)
	}

	if err := s.DeleteLadoSetupItem(ctx, gone.ID, at, ladoSetupEvent("setup.item_deleted", string(gone.ID), at)); err != nil {
		t.Fatal(err)
	}
	before := countLadoTables(t, raw, ladoSetupTables...)
	for _, id := range []domain.LadoConflictAcceptanceID{global.ID, "lado-acceptance-9"} {
		if err := revoke(id); !errors.Is(err, domain.ErrLadoConflictAcceptanceNotLive) {
			t.Fatalf("RevokeLadoConflictAcceptance %s = %v, want ErrLadoConflictAcceptanceNotLive", id, err)
		}
	}
	for _, a := range []domain.LadoConflictAcceptance{
		acceptance("lado-acceptance-3", gone.ID, domain.LadoScopeGlobal, "", at),
		acceptance("lado-acceptance-4", "lado-setup-9", domain.LadoScopeGlobal, "", at),
	} {
		if err := s.AcceptLadoConflict(ctx, a, ladoSetupEvent("setup.conflict_accepted", string(a.ID), at)); !errors.Is(err, domain.ErrLadoSetupItemNotLive) {
			t.Fatalf("AcceptLadoConflict on %s = %v, want ErrLadoSetupItemNotLive", a.ItemID, err)
		}
	}
	if after := countLadoTables(t, raw, ladoSetupTables...); !reflect.DeepEqual(after, before) {
		t.Fatalf("rows = %v, want %v: refused acceptances store nothing", after, before)
	}
}

func TestApproveLadoPlanPinsSetupVersions(t *testing.T) {
	s, raw := openLadoAuditStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-2", "github:acme/demo#13"))
	mustLadoPlanRequest(t, s, "req-2", "lado-task-2", ladoPlanAt)
	claude := ladoSetupItem("lado-setup-1", domain.LadoSetupKindInstructions, "CLAUDE.md")
	codex := ladoSetupItem("lado-setup-2", domain.LadoSetupKindInstructions, "AGENTS.md")
	codex.Tool = domain.LadoSetupToolCodex
	mustCreateLadoSetupItem(t, s, codex)
	mustCreateLadoSetupItem(t, s, claude)
	if err := s.AddLadoSetupItemVersion(ctx, claude.ID, 1, ladoSetupVersion(claude.ID, "lado-setup-1-v2", 'b', ladoSetupAt), ladoSetupEvent("setup.item_updated", string(claude.ID), ladoSetupAt), nil); err != nil {
		t.Fatal(err)
	}
	first := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	snapshot := ladoPolicySnapshot("lado-snapshot-1", `{}`, false)
	snapshot.SetupVersionIDs = []domain.LadoSetupItemVersionID{"lado-setup-2-v1", "lado-setup-1-v2"}
	approval := mustApproveLadoPlan(t, s, "lado-approval-1", first, "nonce-1", snapshot)
	got, ok, err := s.LadoPolicySnapshot(ctx, approval.ID)
	// Ordered by tool, kind and name.
	if want := []domain.LadoSetupItemVersionID{"lado-setup-1-v2", "lado-setup-2-v1"}; err != nil || !ok || !reflect.DeepEqual(got.SetupVersionIDs, want) {
		t.Fatalf("LadoPolicySnapshot = (%+v, %v, %v), want setup pins %v", got, ok, err, want)
	}

	if err := s.DeleteLadoSetupItem(ctx, codex.ID, ladoSetupAt, ladoSetupEvent("setup.item_deleted", string(codex.ID), ladoSetupAt)); err != nil {
		t.Fatal(err)
	}
	rev := mustPin(t, s, ladoRevision("lado-plan-2", "lado-task-2", "req-2", 1, ladoSHA('b')))
	tables := []string{"lado_plan_approvals", "lado_presence_proofs", "lado_audit_events", "lado_policy_snapshots", "lado_policy_snapshot_rules", "lado_policy_snapshot_setup", "lado_queue_entries"}
	before := countLadoTables(t, raw, tables...)
	approve := func(pins ...domain.LadoSetupItemVersionID) error {
		snapshot := ladoPolicySnapshot("lado-snapshot-2", `{}`, false)
		snapshot.SetupVersionIDs = pins
		_, _, err := s.ApproveLadoPlan(ctx,
			domain.LadoPlanApproval{ID: "lado-approval-2", RevisionID: rev.ID, TaskID: rev.TaskID, SHA256: rev.SHA256, ApprovedAt: ladoPlanAt.Add(time.Hour)},
			ladoProof("nonce-2", string(rev.ID)), ladoApprovalEvent(string(rev.ID)), snapshot, &domain.LadoQueueEntry{ID: "lado-queue-2"})
		return err
	}
	for name, pins := range map[string][]domain.LadoSetupItemVersionID{
		"unknown version":       {"lado-setup-1-v2", "lado-setup-1-v9"},
		"version of a deletion": {"lado-setup-2-v1"},
	} {
		if err := approve(pins...); !errors.Is(err, domain.ErrLadoRulesChanged) {
			t.Fatalf("%s: ApproveLadoPlan = %v, want ErrLadoRulesChanged", name, err)
		}
		if after := countLadoTables(t, raw, tables...); !reflect.DeepEqual(after, before) {
			t.Fatalf("%s: rows = %v, want %v: no approval, nonce, snapshot, pin, queue entry or audit event", name, after, before)
		}
	}
	// The nonce was not used up: the approval goes through with the live
	// setup resolved again, and an older version pins as well as the latest.
	if err := approve("lado-setup-1-v1"); err != nil {
		t.Fatalf("ApproveLadoPlan with a live version = %v, want success", err)
	}
	if after := countLadoTables(t, raw, "lado_policy_snapshot_setup"); after["lado_policy_snapshot_setup"] != before["lado_policy_snapshot_setup"]+1 {
		t.Fatalf("pins = %v, want one more than %d", after, before["lado_policy_snapshot_setup"])
	}
	// A pin keeps the version of an item deleted since readable.
	if got, ok, err := s.LadoPolicySnapshot(ctx, approval.ID); err != nil || !ok || len(got.SetupVersionIDs) != 2 {
		t.Fatalf("first snapshot after the delete = (%+v, %v, %v), want both pins", got, ok, err)
	}
}

func TestLadoSetupVersionsForClientRequest(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedLadoTaskWithPlan(t, s)
	instructions := ladoSetupItem("lado-setup-1", domain.LadoSetupKindInstructions, "CLAUDE.md")
	skill := ladoSetupItem("lado-setup-2", domain.LadoSetupKindSkill, "review")
	mustCreateLadoSetupItem(t, s, instructions)
	mustCreateLadoSetupItem(t, s, skill)
	rev := mustPin(t, s, ladoRevision("lado-plan-1", "lado-task-1", "req-1", 1, ladoSHA('a')))
	snapshot := ladoPolicySnapshot("lado-snapshot-1", `{}`, false)
	snapshot.SetupVersionIDs = []domain.LadoSetupItemVersionID{"lado-setup-2-v1", "lado-setup-1-v1"}
	mustApproveLadoPlan(t, s, "lado-approval-1", rev, "nonce-1", snapshot)
	// A newer approval supersedes the first entry without pins.
	mustApproveLadoPlan(t, s, "lado-approval-2", rev, "nonce-2", ladoPolicySnapshot("lado-snapshot-2", `{}`, false))

	// Later edits and deletes never reach the execution.
	at := ladoSetupAt.Add(time.Hour)
	if err := s.AddLadoSetupItemVersion(ctx, instructions.ID, 1, ladoSetupVersion(instructions.ID, "lado-setup-1-v2", 'b', at), ladoSetupEvent("setup.item_updated", string(instructions.ID), at), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteLadoSetupItem(ctx, skill.ID, at, ladoSetupEvent("setup.item_deleted", string(skill.ID), at)); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		id   string
		want []string
	}{
		{domain.LadoClientRequestID("lado-queue-1", 1), []string{"lado-setup-1@lado-setup-1-v1", "lado-setup-2@lado-setup-2-v1"}},
		{domain.LadoClientRequestID("lado-queue-1", 2), []string{"lado-setup-1@lado-setup-1-v1", "lado-setup-2@lado-setup-2-v1"}},
		{domain.LadoClientRequestID("lado-queue-2", 1), []string{}},
	} {
		got, found, err := s.LadoSetupVersionsForClientRequest(ctx, c.id)
		if err != nil || !found || !reflect.DeepEqual(ladoSetupPairs(got), c.want) {
			t.Fatalf("setup of %q = (%v, %v, %v), want %v", c.id, ladoSetupPairs(got), found, err, c.want)
		}
		if len(got) == 2 && (got[0].Version.SHA256 != ladoSHA('a') || got[1].Item.DeletedAt.IsZero()) {
			t.Fatalf("setup of %q = %+v, want the pinned content and the item as it is now", c.id, got)
		}
	}
	for _, id := range []string{"", "lado-queue-1", "lado:lado-queue-1", "lado:lado-queue-1/0", "task-1", domain.LadoClientRequestID("lado-queue-missing", 1)} {
		got, found, err := s.LadoSetupVersionsForClientRequest(ctx, id)
		if err != nil || found || got != nil {
			t.Fatalf("setup of %q = (%v, %v, %v), want not an execution", id, got, found, err)
		}
	}
}

func TestLadoSetupCDCPublishesPerLadoProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for id, on := range map[domain.ProjectID]bool{"abc": true, "mer": true, "off": false} {
		seedProject(t, s, string(id))
		if _, err := s.SetLadoMode(ctx, id, on, ladoSetupAt); err != nil {
			t.Fatal(err)
		}
	}
	seedProject(t, s, "plain")
	// LADO mode for a project AO does not know publishes nothing.
	if _, err := s.SetLadoMode(ctx, "ghost", true, ladoSetupAt); err != nil {
		t.Fatal(err)
	}
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-1", "github:acme/demo#12"))
	seq, err := s.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// expect checks the projects the change events since the last call name.
	expect := func(step string, want ...string) {
		t.Helper()
		events, err := s.EventsAfter(ctx, seq, 1000)
		if err != nil {
			t.Fatalf("%s: EventsAfter: %v", step, err)
		}
		got := []string{}
		for _, ev := range events {
			seq = ev.Seq
			var payload struct {
				Kind      string `json:"kind"`
				ProjectID string `json:"projectId"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatalf("%s: payload %s: %v", step, ev.Payload, err)
			}
			if ev.Type != "session_updated" || ev.SessionID != "" || payload.Kind != "lado_setup" || payload.ProjectID != ev.ProjectID {
				t.Fatalf("%s: event %+v payload %+v, want a lado_setup session_updated event", step, ev, payload)
			}
			got = append(got, ev.ProjectID)
		}
		sort.Strings(got)
		if want == nil {
			want = []string{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: change events for %v, want %v", step, got, want)
		}
	}
	item := ladoSetupItem("lado-setup-1", domain.LadoSetupKindSkill, "review")
	bind := func(scope domain.LadoRuleScope, scopeID string, state domain.LadoBindingState, inherit bool) {
		t.Helper()
		b := domain.LadoSetupBinding{ItemID: item.ID, Scope: scope, ScopeID: scopeID, State: state, UpdatedAt: ladoSetupAt}
		if err := s.SetLadoSetupBinding(ctx, b, inherit, ladoSetupEvent("setup.binding_set", string(item.ID), ladoSetupAt), nil); err != nil {
			t.Fatalf("SetLadoSetupBinding %+v: %v", b, err)
		}
	}
	accept := func(id string, scope domain.LadoRuleScope, scopeID string) {
		t.Helper()
		a := domain.LadoConflictAcceptance{
			ID: domain.LadoConflictAcceptanceID(id), Code: "LADO_SETUP_HOOK_ADDS_PROMPT_TEXT", ItemID: item.ID,
			ContentSHA256: ladoSHA('a'), Scope: scope, ScopeID: scopeID, Actor: "api", CreatedAt: ladoSetupAt,
		}
		if err := s.AcceptLadoConflict(ctx, a, ladoSetupEvent("setup.conflict_accepted", id, ladoSetupAt)); err != nil {
			t.Fatalf("AcceptLadoConflict %s: %v", id, err)
		}
	}

	mustCreateLadoSetupItem(t, s, item)
	// The item and its first version.
	expect("create", "abc", "abc", "mer", "mer")
	at := ladoSetupAt.Add(time.Minute)
	if err := s.AddLadoSetupItemVersion(ctx, item.ID, 1, ladoSetupVersion(item.ID, "lado-setup-1-v2", 'b', at), ladoSetupEvent("setup.item_updated", string(item.ID), at), nil); err != nil {
		t.Fatal(err)
	}
	expect("new version", "abc", "mer")
	bind(domain.LadoScopeGlobal, "", domain.LadoBindingOff, false)
	expect("Global off", "abc", "mer")
	bind(domain.LadoScopeGlobal, "", domain.LadoBindingOn, false)
	expect("Global on", "abc", "mer")
	bind(domain.LadoScopeGlobal, "", "", true)
	expect("Global inherit", "abc", "mer")
	bind(domain.LadoScopeProject, "abc", domain.LadoBindingForceOff, false)
	expect("project force_off", "abc")
	bind(domain.LadoScopeProject, "abc", domain.LadoBindingForceOn, false)
	expect("project force_on", "abc")
	bind(domain.LadoScopeProject, "abc", "", true)
	expect("project inherit", "abc")
	bind(domain.LadoScopeTask, "lado-task-1", domain.LadoBindingForceOn, false)
	expect("task force_on", "mer")
	bind(domain.LadoScopeTask, "lado-task-1", "", true)
	expect("task inherit", "mer")
	accept("lado-acceptance-1", domain.LadoScopeGlobal, "")
	expect("Global acceptance", "abc", "mer")
	accept("lado-acceptance-2", domain.LadoScopeProject, "abc")
	expect("project acceptance", "abc")
	accept("lado-acceptance-3", domain.LadoScopeTask, "lado-task-1")
	expect("task acceptance", "mer")
	if err := s.RevokeLadoConflictAcceptance(ctx, "lado-acceptance-1", at, ladoSetupEvent("setup.conflict_revoked", "lado-acceptance-1", at)); err != nil {
		t.Fatal(err)
	}
	expect("revoke", "abc", "mer")
	if err := s.DeleteLadoSetupItem(ctx, item.ID, at, ladoSetupEvent("setup.item_deleted", string(item.ID), at)); err != nil {
		t.Fatal(err)
	}
	expect("delete", "abc", "mer")

	// An adoption publishes through its item and version; marking it partial
	// publishes nothing, and its restore publishes the item's end.
	mustAdoptLadoSetup(t, s, "lado-adoption-1", "nonce-1", ladoSetupItem("lado-setup-2", domain.LadoSetupKindAgent, "planner.md"))
	expect("adoption", "abc", "abc", "mer", "mer")
	if err := s.MarkLadoSetupAdoptionPartial(ctx, "lado-adoption-1", "stopped"); err != nil {
		t.Fatal(err)
	}
	expect("partial")
	if err := s.RestoreLadoSetupAdoption(ctx, "lado-adoption-1", nil, at, ladoSetupEvent("setup.restored", "lado-adoption-1", at), nil); err != nil {
		t.Fatal(err)
	}
	expect("restore", "abc", "mer")
}
