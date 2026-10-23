package ladopolicy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

var serviceEpoch = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

// fakeBlobs is the blob store of pinned plans.
type fakeBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (b *fakeBlobs) put(content string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	sum := sha256.Sum256([]byte(content))
	key := hex.EncodeToString(sum[:])
	b.data[key] = []byte(content)
	return key
}

func (b *fakeBlobs) Get(sum string, limit int64) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.data[sum]
	switch {
	case !ok:
		return nil, fmt.Errorf("blob %s is missing", sum)
	case int64(len(data)) > limit:
		return nil, fmt.Errorf("blob %s is over %d bytes", sum, limit)
	}
	return data, nil
}

// serviceFixture is a policy service on a real store, with the built-in
// rules seeded and two LADO projects, demo and other.
type serviceFixture struct {
	svc   *Service
	store *sqlite.Store
	raw   *sql.DB
	blobs *fakeBlobs
	now   time.Time
	tasks int
	plans int
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	store := sqlitetest.MustOpenAt(t, dir)
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	fx := &serviceFixture{store: store, raw: raw, blobs: &fakeBlobs{data: map[string][]byte{}}, now: serviceEpoch}
	for _, project := range []domain.ProjectRecord{
		{ID: "demo", DisplayName: "Demo", Path: t.TempDir(), RegisteredAt: serviceEpoch},
		{ID: "other", DisplayName: "Other", Path: t.TempDir(), RegisteredAt: serviceEpoch},
	} {
		if err := store.UpsertProject(ctx, project); err != nil {
			t.Fatalf("seed project %s: %v", project.ID, err)
		}
		if _, err := store.SetLadoMode(ctx, domain.ProjectID(project.ID), true, serviceEpoch); err != nil {
			t.Fatalf("turn on LADO mode for %s: %v", project.ID, err)
		}
	}
	ids := 0
	fx.svc = New(Deps{
		Store:    store,
		Projects: store,
		Blobs:    fx.blobs,
		AOBuild:  "0.42.0",
		Clock:    func() time.Time { return fx.now },
		NewID: func() string {
			ids++
			return strconv.Itoa(ids)
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := fx.svc.EnsureBuiltins(ctx); err != nil {
		t.Fatalf("EnsureBuiltins: %v", err)
	}
	return fx
}

// task adds a task of demo with an open issue, run by agent.
func (fx *serviceFixture) task(t *testing.T, agent string) domain.LadoTask {
	t.Helper()
	return fx.taskOf(t, "demo", agent)
}

// taskOf adds a task of the project with an open issue, run by agent.
func (fx *serviceFixture) taskOf(t *testing.T, project domain.ProjectID, agent string) domain.LadoTask {
	t.Helper()
	fx.tasks++
	n := fx.tasks
	task, err := fx.store.CreateLadoTask(context.Background(), domain.LadoTask{
		ID:        domain.LadoTaskID(fmt.Sprintf("lado-task-%d", n)),
		ProjectID: project,
		Repo:      domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
		IssueRef:  domain.IssueID(fmt.Sprintf("github:acme/demo#%d", n)),
		Issue: domain.LadoObservedIssue{
			Title:          fmt.Sprintf("Task %d", n),
			State:          domain.IssueOpen,
			URL:            fmt.Sprintf("https://github.com/acme/demo/issues/%d", n),
			SnapshotSHA256: strings.Repeat("1", 64),
			ObservedAt:     fx.now,
		},
		Agent:     agent,
		CreatedAt: fx.now,
		UpdatedAt: fx.now,
	})
	if err != nil {
		t.Fatalf("CreateLadoTask: %v", err)
	}
	return task
}

const planTemplate = `---
task: %s
issue: %s
base_ref: main
steps:
  - Add the login route
acceptance_criteria:
  - A user can sign in
affected_paths:
  - backend/**
test_commands:
  - go test ./...
risk: low
permission_ceiling: %s
review:
  required: false
plan_source: issue
---

Plan.
`

// plan pins a revision of the task's plan that asks for ceiling, as the task
// service does, and returns the task as the store reads it then.
func (fx *serviceFixture) plan(t *testing.T, task domain.LadoTask, ceiling domain.PermissionMode) (domain.LadoTask, domain.LadoPlanRevision) {
	t.Helper()
	ctx := context.Background()
	fx.plans++
	sum := fx.blobs.put(fmt.Sprintf(planTemplate, task.ID, task.IssueRef, ceiling))
	req, err := fx.store.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{
		ID:                  domain.LadoPlanRequestID(fmt.Sprintf("lado-plan-request-%d", fx.plans)),
		TaskID:              task.ID,
		SessionID:           "demo-orchestrator-1",
		IssueSnapshotSHA256: strings.Repeat("1", 64),
		RequestedAt:         fx.now,
	})
	if err != nil {
		t.Fatalf("CreateLadoPlanRequest: %v", err)
	}
	rev, created, err := fx.store.PinLadoPlanRevision(ctx, domain.LadoPlanRevision{
		ID:                  domain.LadoPlanRevisionID(fmt.Sprintf("lado-plan-%d", fx.plans)),
		TaskID:              task.ID,
		RequestID:           req.ID,
		SessionID:           "demo-orchestrator-1",
		Path:                domain.LadoPlanPath(task.ID),
		ArtifactVersion:     fx.plans,
		Source:              "agent",
		SHA256:              sum,
		BaseRef:             "main",
		BaseSHA:             strings.Repeat("c", 40),
		IssueSnapshotSHA256: strings.Repeat("1", 64),
		CreatedAt:           fx.now,
	})
	if err != nil || !created {
		t.Fatalf("PinLadoPlanRevision = (%v, %v), want a new revision", created, err)
	}
	return fx.reload(t, task.ID), rev
}

// approve approves the revision with the rules' snapshot of it, as the task
// service does, and returns the task and the stored snapshot.
func (fx *serviceFixture) approve(t *testing.T, task domain.LadoTask, rev domain.LadoPlanRevision) (domain.LadoTask, domain.LadoPolicySnapshot) {
	t.Helper()
	ctx := context.Background()
	snapshot, err := fx.svc.Snapshot(ctx, task, rev)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	id := domain.LadoPlanApprovalID("lado-approval-" + string(rev.ID))
	proof := domain.LadoPresenceProof{
		Nonce: "approve-" + string(rev.ID) + "-AAAAAAAAAAAAAAAA", Action: domain.LadoPresencePlanApprove, SubjectID: string(rev.ID),
		ExpiresAt: fx.now.Add(time.Minute), UsedAt: fx.now,
	}
	if _, _, err := fx.store.ApproveLadoPlan(ctx,
		domain.LadoPlanApproval{ID: id, RevisionID: rev.ID, TaskID: task.ID, SHA256: rev.SHA256, ProofNonce: proof.Nonce, ApprovedAt: fx.now},
		proof,
		domain.LadoAuditEvent{At: fx.now, Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: string(rev.ID), Payload: json.RawMessage(`{}`)},
		&snapshot, nil,
	); err != nil {
		t.Fatalf("ApproveLadoPlan: %v", err)
	}
	stored, ok, err := fx.store.LadoPolicySnapshot(ctx, id)
	if err != nil || !ok {
		t.Fatalf("LadoPolicySnapshot = (%v, %v)", ok, err)
	}
	return fx.reload(t, task.ID), stored
}

// approvedTask adds a task of demo run by agent whose plan the developer
// approved under the rules as they are now.
func (fx *serviceFixture) approvedTask(t *testing.T, agent string) domain.LadoTask {
	t.Helper()
	task, rev := fx.plan(t, fx.task(t, agent), domain.PermissionModeAuto)
	task, _ = fx.approve(t, task, rev)
	return task
}

func (fx *serviceFixture) reload(t *testing.T, id domain.LadoTaskID) domain.LadoTask {
	t.Helper()
	task, ok, err := fx.store.LadoTask(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("LadoTask %s = (%v, %v)", id, ok, err)
	}
	return task
}

// userRule creates a user rule at Global, on or off.
func (fx *serviceFixture) userRule(t *testing.T, on bool) domain.LadoRule {
	t.Helper()
	got, err := fx.svc.Create(context.Background(), CreateInput{
		Name: "Test before commit", Category: "git", Instruction: "Run the tests before every commit.",
		Scope: domain.LadoScopeGlobal, On: on,
	})
	if err != nil || !got.Applied {
		t.Fatalf("Create = (%+v, %v), want an applied rule", got, err)
	}
	return got.Rule.Rule
}

// presence returns a presence check that hands out a proof with the nonce
// and records what it was asked for.
func (fx *serviceFixture) presence(nonce string, dev bool, asked *[]string) func(action, subject string) (domain.LadoPresenceProof, error) {
	return func(action, subject string) (domain.LadoPresenceProof, error) {
		if asked != nil {
			*asked = append(*asked, action+" "+subject)
		}
		return domain.LadoPresenceProof{
			Nonce: nonce, Action: action, SubjectID: subject, Dev: dev, ExpiresAt: fx.now.Add(time.Minute), UsedAt: fx.now,
		}, nil
	}
}

type ruleRows struct{ rules, versions, bindings, events, proofs int }

func (fx *serviceFixture) rows(t *testing.T) ruleRows {
	t.Helper()
	var rows ruleRows
	for table, n := range map[string]*int{
		"lado_rules":           &rows.rules,
		"lado_rule_versions":   &rows.versions,
		"lado_rule_bindings":   &rows.bindings,
		"lado_audit_events":    &rows.events,
		"lado_presence_proofs": &rows.proofs,
	} {
		if err := fx.raw.QueryRow("SELECT COUNT(*) FROM " + table).Scan(n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	return rows
}

type auditRow struct {
	Actor, Action, Subject string
	Payload                map[string]any
}

// events returns the audit log, oldest first.
func (fx *serviceFixture) events(t *testing.T) []auditRow {
	t.Helper()
	rows, err := fx.raw.Query(`SELECT actor, action, subject_id, payload_json FROM lado_audit_events ORDER BY id`)
	if err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var events []auditRow
	for rows.Next() {
		var event auditRow
		var payload string
		if err := rows.Scan(&event.Actor, &event.Action, &event.Subject, &payload); err != nil {
			t.Fatalf("scan audit event: %v", err)
		}
		if err := json.Unmarshal([]byte(payload), &event.Payload); err != nil {
			t.Fatalf("decode audit payload %s: %v", payload, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	return events
}

func (fx *serviceFixture) lastEvent(t *testing.T) auditRow {
	t.Helper()
	events := fx.events(t)
	if len(events) == 0 {
		t.Fatal("the audit log is empty")
	}
	return events[len(events)-1]
}

func (fx *serviceFixture) rule(t *testing.T, id domain.LadoRuleID) domain.LadoRule {
	t.Helper()
	rule, ok, err := fx.store.LadoRule(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("LadoRule %s = (%v, %v)", id, ok, err)
	}
	return rule
}

// entry returns the rule's entry in a resolution.
func entry(t *testing.T, r Resolution, id domain.LadoRuleID) Entry {
	t.Helper()
	for _, e := range r.Entries {
		if e.Rule.ID == id {
			return e
		}
	}
	t.Fatalf("rule %s is not in the resolution", id)
	return Entry{}
}

// wantAPIError fails unless err is an API error of the kind with one of the
// codes.
func wantAPIError(t *testing.T, err error, kind apierr.Kind, codes ...string) {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != kind || !slices.Contains(codes, apiErr.Code) {
		t.Fatalf("error = %#v, want kind %d with code %v", err, kind, codes)
	}
}

func ptr[T any](v T) *T { return &v }

func TestCreateRuleAtEachScope(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	task := fx.task(t, "claude-code")
	create := func(t *testing.T, in CreateInput) RuleView {
		t.Helper()
		in.Name, in.Category, in.Instruction = "Test before commit", "git", "Run the tests before every commit."
		got, err := fx.svc.Create(ctx, in)
		if err != nil || !got.Applied {
			t.Fatalf("Create(%+v) = (%+v, %v), want an applied rule", in, got, err)
		}
		rule := got.Rule.Rule
		if v := rule.Version; v.Number != 1 || v.Actor != "api" || v.ID == "" || rule.Builtin || rule.Protected ||
			!slices.Equal(v.Content.Enforcement, []domain.LadoEnforcement{domain.LadoEnforcementPrompt}) {
			t.Fatalf("created rule = %+v, want a user prompt rule at version 1 by api", rule)
		}
		if event := fx.lastEvent(t); event.Action != "rule.created" || event.Actor != "api" || event.Subject != string(rule.ID) {
			t.Fatalf("audit event = %+v, want rule.created by api", event)
		}
		if stored := fx.rule(t, rule.ID); !reflect.DeepEqual(stored, rule) {
			t.Fatalf("stored rule = %+v, want the returned %+v", stored, rule)
		}
		return got.Rule
	}
	effective := func(t *testing.T, in EffectiveInput, id domain.LadoRuleID) Entry {
		t.Helper()
		view, err := fx.svc.Effective(ctx, in)
		if err != nil {
			t.Fatalf("Effective(%+v): %v", in, err)
		}
		return entry(t, view.Resolution, id)
	}

	t.Run("global on", func(t *testing.T) {
		got := create(t, CreateInput{Scope: domain.LadoScopeGlobal, On: true})
		if got.Rule.Global != domain.LadoBindingOn || len(got.Rule.Overrides) != 0 {
			t.Fatalf("rule = %+v, want on at Global without overrides", got.Rule)
		}
		for _, project := range []domain.ProjectID{"demo", "other"} {
			if e := effective(t, EffectiveInput{ProjectID: project}, got.Rule.ID); !e.On || e.Source.Scope != domain.LadoScopeGlobal {
				t.Fatalf("%s: entry = %+v, want on from Global", project, e)
			}
		}
	})
	t.Run("global off", func(t *testing.T) {
		got := create(t, CreateInput{Scope: domain.LadoScopeGlobal})
		if got.Rule.Global != domain.LadoBindingOff || len(got.Rule.Overrides) != 0 {
			t.Fatalf("rule = %+v, want off at Global without overrides", got.Rule)
		}
		if e := effective(t, EffectiveInput{ProjectID: "demo"}, got.Rule.ID); e.On {
			t.Fatalf("entry = %+v, want off", e)
		}
	})
	t.Run("project", func(t *testing.T) {
		got := create(t, CreateInput{Scope: domain.LadoScopeProject, ScopeID: "demo", On: true})
		want := []domain.LadoRuleBinding{{RuleID: got.Rule.ID, Scope: domain.LadoScopeProject, ScopeID: "demo", State: domain.LadoBindingForceOn, UpdatedAt: fx.now}}
		if got.Rule.Global != domain.LadoBindingOff || !reflect.DeepEqual(got.Rule.Overrides, want) {
			t.Fatalf("rule = %+v, want off at Global and forced on for demo", got.Rule)
		}
		if label := got.Labels[LabelKey(domain.LadoScopeProject, "demo")]; label != "Demo" {
			t.Fatalf("labels = %v, want demo named Demo", got.Labels)
		}
		if e := effective(t, EffectiveInput{ProjectID: "demo"}, got.Rule.ID); !e.On || e.Source.Scope != domain.LadoScopeProject {
			t.Fatalf("demo: entry = %+v, want on from the project", e)
		}
		if e := effective(t, EffectiveInput{ProjectID: "other"}, got.Rule.ID); e.On {
			t.Fatalf("other: entry = %+v, want off", e)
		}
	})
	t.Run("task", func(t *testing.T) {
		got := create(t, CreateInput{Scope: domain.LadoScopeTask, ScopeID: string(task.ID)})
		want := []domain.LadoRuleBinding{{RuleID: got.Rule.ID, Scope: domain.LadoScopeTask, ScopeID: string(task.ID), State: domain.LadoBindingForceOn, UpdatedAt: fx.now}}
		if got.Rule.Global != domain.LadoBindingOff || !reflect.DeepEqual(got.Rule.Overrides, want) {
			t.Fatalf("rule = %+v, want off at Global and forced on for the task", got.Rule)
		}
		if label := got.Labels[LabelKey(domain.LadoScopeTask, string(task.ID))]; label != "Task 1" {
			t.Fatalf("labels = %v, want the task named by its issue title", got.Labels)
		}
		if e := effective(t, EffectiveInput{TaskID: task.ID}, got.Rule.ID); !e.On || e.Source.Scope != domain.LadoScopeTask {
			t.Fatalf("task: entry = %+v, want on from the task", e)
		}
		if e := effective(t, EffectiveInput{ProjectID: "demo"}, got.Rule.ID); e.On {
			t.Fatalf("demo: entry = %+v, want off", e)
		}
	})

	cancelled := fx.task(t, "claude-code")
	if _, _, err := fx.store.CancelLadoTask(ctx, cancelled.ID, fx.now); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("규", maxNameRunes+1)
	refused := []struct {
		name string
		in   CreateInput
		kind apierr.Kind
		code string
	}{
		{"cancelled task", CreateInput{Name: "N", Instruction: "I", Scope: domain.LadoScopeTask, ScopeID: string(cancelled.ID)}, apierr.KindConflict, "LADO_TASK_CANCELLED"},
		{"unknown project", CreateInput{Name: "N", Instruction: "I", Scope: domain.LadoScopeProject, ScopeID: "missing"}, apierr.KindNotFound, "LADO_SCOPE_NOT_FOUND"},
		{"unknown scope", CreateInput{Name: "N", Instruction: "I", Scope: "team", ScopeID: "demo"}, apierr.KindInvalid, "LADO_BINDING_SCOPE_INVALID"},
		{"no name", CreateInput{Name: "  ", Instruction: "I", Scope: domain.LadoScopeGlobal}, apierr.KindInvalid, "LADO_RULE_NAME_REQUIRED"},
		{"long name", CreateInput{Name: long, Instruction: "I", Scope: domain.LadoScopeGlobal}, apierr.KindInvalid, "LADO_RULE_NAME_TOO_LONG"},
		{"no instruction", CreateInput{Name: "N", Scope: domain.LadoScopeGlobal}, apierr.KindInvalid, "LADO_RULE_INSTRUCTION_REQUIRED"},
		{"unknown conflict", CreateInput{Name: "N", Instruction: "I", Conflicts: []domain.LadoRuleID{"lado-rule-missing"}, Scope: domain.LadoScopeGlobal}, apierr.KindInvalid, "LADO_RULE_CONFLICT_UNKNOWN"},
		{"protected conflict", CreateInput{Name: "N", Instruction: "I", Conflicts: []domain.LadoRuleID{domain.LadoRulePlanApproval}, Scope: domain.LadoScopeGlobal}, apierr.KindInvalid, "LADO_RULE_CONFLICT_PROTECTED"},
	}
	before := fx.rows(t)
	for _, tc := range refused {
		_, err := fx.svc.Create(ctx, tc.in)
		wantAPIError(t, err, tc.kind, tc.code)
	}
	// A rule cannot name itself: it has no id before it exists, so an edit
	// tries.
	rule := fx.rule(t, domain.LadoRuleKoreanCommits)
	_, err := fx.svc.Update(ctx, rule.ID, UpdateInput{BaseVersion: 1, Conflicts: &[]domain.LadoRuleID{rule.ID}})
	wantAPIError(t, err, apierr.KindInvalid, "LADO_RULE_CONFLICT_SELF")
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after refused changes = %+v, want %+v", after, before)
	}
	// The limit counts characters, not bytes.
	if _, err := fx.svc.Create(ctx, CreateInput{Name: long[:len(long)-len("규")], Instruction: "I", Scope: domain.LadoScopeGlobal, DryRun: true}); err != nil {
		t.Fatalf("a %d-character name = %v, want it accepted", maxNameRunes, err)
	}
}

func TestUpdateRuleAddsAVersion(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	rule := fx.userRule(t, true)

	got, err := fx.svc.Update(ctx, rule.ID, UpdateInput{BaseVersion: 1, Instruction: ptr("Write the test first."), Reason: "clearer"})
	if err != nil || !got.Applied {
		t.Fatalf("Update = (%+v, %v), want applied", got, err)
	}
	if v := got.Rule.Rule.Version; v.Number != 2 || v.Content.Instruction != "Write the test first." || v.Actor != "api" || v.SHA256 != v.Content.SHA256() {
		t.Fatalf("version = %+v, want version 2 by api with the new instruction", v)
	}
	event := fx.lastEvent(t)
	if event.Action != "rule.updated" || event.Actor != "api" || event.Subject != string(rule.ID) ||
		event.Payload["version"] != float64(2) || event.Payload["reason"] != "clearer" {
		t.Fatalf("audit event = %+v, want rule.updated of version 2", event)
	}

	before := fx.rows(t)
	same, err := fx.svc.Update(ctx, rule.ID, UpdateInput{BaseVersion: 2, Name: ptr(" Test before commit "), Instruction: ptr("Write the test first.\n")})
	if err != nil || same.Rule.Rule.Version.Number != 2 {
		t.Fatalf("an edit that changes nothing = (%+v, %v), want version 2 kept", same, err)
	}
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after an edit that changes nothing = %+v, want %+v", after, before)
	}
	_, versions, err := fx.svc.Rule(ctx, rule.ID)
	if err != nil || len(versions) != 2 || versions[0].Content.Instruction != "Run the tests before every commit." || versions[1].Content.Instruction != "Write the test first." {
		t.Fatalf("versions = %+v, %v, want the first and the edited one", versions, err)
	}
}

func TestUpdateRuleRefusesAStaleBase(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	rule := fx.userRule(t, false)
	if _, err := fx.svc.Update(ctx, rule.ID, UpdateInput{BaseVersion: 1, Instruction: ptr("Second.")}); err != nil {
		t.Fatal(err)
	}

	before := fx.rows(t)
	for _, dryRun := range []bool{false, true} {
		_, err := fx.svc.Update(ctx, rule.ID, UpdateInput{BaseVersion: 1, Instruction: ptr("Third."), DryRun: dryRun})
		wantAPIError(t, err, apierr.KindConflict, "LADO_RULE_CHANGED")
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Details["baseVersion"] != 1 {
			t.Fatalf("details = %v, want the stale base", apiErr.Details)
		}
	}
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after a stale edit = %+v, want %+v", after, before)
	}
	if got := fx.rule(t, rule.ID).Version; got.Number != 2 || got.Content.Instruction != "Second." {
		t.Fatalf("active version = %+v, want version 2 kept", got)
	}
}

// A deleted rule stays in the conflicts of a rule that named it, so the
// editor sends it back with every change of the list. It must not block the
// change; a rule the change adds is still checked.
func TestUpdateKeepsConflictsWithADeletedRule(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	create := func(name string, conflicts ...domain.LadoRuleID) domain.LadoRule {
		t.Helper()
		got, err := fx.svc.Create(ctx, CreateInput{Name: name, Instruction: name + ".", Conflicts: conflicts, Scope: domain.LadoScopeGlobal})
		if err != nil || !got.Applied {
			t.Fatalf("Create %s = (%+v, %v), want an applied rule", name, got, err)
		}
		return got.Rule.Rule
	}
	b := create("Squash merges")
	a := create("Merge commits", b.ID)
	// No snapshot used B, so it can be deleted.
	if err := fx.svc.Delete(ctx, b.ID); err != nil {
		t.Fatalf("Delete B: %v", err)
	}
	c := create("Linear history")

	got, err := fx.svc.Update(ctx, a.ID, UpdateInput{BaseVersion: 1, Conflicts: &[]domain.LadoRuleID{b.ID, c.ID}})
	if err != nil || !got.Applied {
		t.Fatalf("adding C next to the deleted B = (%+v, %v), want applied", got, err)
	}
	want := []domain.LadoRuleID{b.ID, c.ID}
	slices.Sort(want)
	if v := fx.rule(t, a.ID).Version; v.Number != 2 || !slices.Equal(v.Content.Conflicts, want) {
		t.Fatalf("A's active version = %+v, want version 2 conflicting with %v", v, want)
	}

	before := fx.rows(t)
	for code, added := range map[string]domain.LadoRuleID{
		"LADO_RULE_CONFLICT_UNKNOWN":   "lado-rule-missing",
		"LADO_RULE_CONFLICT_SELF":      a.ID,
		"LADO_RULE_CONFLICT_PROTECTED": domain.LadoRulePlanApproval,
	} {
		_, err := fx.svc.Update(ctx, a.ID, UpdateInput{BaseVersion: 2, Conflicts: &[]domain.LadoRuleID{b.ID, c.ID, added}})
		wantAPIError(t, err, apierr.KindInvalid, code)
	}
	// A new rule has no list to keep: every rule it names must exist.
	_, err = fx.svc.Create(ctx, CreateInput{Name: "Rebase", Instruction: "Rebase.", Conflicts: []domain.LadoRuleID{b.ID}, Scope: domain.LadoScopeGlobal})
	wantAPIError(t, err, apierr.KindInvalid, "LADO_RULE_CONFLICT_UNKNOWN")
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after the refused changes = %+v, want %+v", after, before)
	}
}

// HARNESS 15.3: the protected gates stay on whatever an API caller asks.
func TestProtectedRulesRefuseBindingsArchiveAndDelete(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	task := fx.task(t, "claude-code")
	before := fx.rows(t)
	for _, id := range []domain.LadoRuleID{domain.LadoRulePlanApproval, domain.LadoRulePermissionCeiling} {
		attempts := []struct {
			name string
			run  func() error
		}{
			{"task force_off", func() error {
				_, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: id, Scope: domain.LadoScopeTask, ScopeID: string(task.ID), State: domain.LadoBindingForceOff})
				return err
			}},
			{"task force_off as a dry run", func() error {
				_, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: id, Scope: domain.LadoScopeTask, ScopeID: string(task.ID), State: domain.LadoBindingForceOff, DryRun: true})
				return err
			}},
			{"global off", func() error {
				_, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: id, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff})
				return err
			}},
			{"archive", func() error {
				_, err := fx.svc.Update(ctx, id, UpdateInput{BaseVersion: 1, Archived: ptr(true)})
				return err
			}},
			{"rename", func() error {
				_, err := fx.svc.Update(ctx, id, UpdateInput{BaseVersion: 1, Name: ptr("Optional")})
				return err
			}},
			{"delete", func() error { return fx.svc.Delete(ctx, id) }},
		}
		for _, attempt := range attempts {
			err := attempt.run()
			if attempt.name == "delete" {
				// Every protected rule is built in; either refusal holds.
				wantAPIError(t, err, apierr.KindForbidden, "LADO_RULE_PROTECTED", "LADO_RULE_BUILTIN")
				continue
			}
			wantAPIError(t, err, apierr.KindForbidden, "LADO_RULE_PROTECTED")
		}
	}
	bypass := domain.PermissionModeBypassPermissions
	_, err := fx.svc.Update(ctx, domain.LadoRulePlanApproval, UpdateInput{BaseVersion: 1, CeilingMode: &bypass, Presence: fx.presence("approval-mode-AAAAAAAAAAAAAAAA", false, nil)})
	wantAPIError(t, err, apierr.KindForbidden, "LADO_RULE_PROTECTED")
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after refused changes = %+v, want %+v", after, before)
	}
	view, err := fx.svc.Effective(ctx, EffectiveInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.LadoRuleID{domain.LadoRulePlanApproval, domain.LadoRulePermissionCeiling} {
		if e := entry(t, view.Resolution, id); !e.On {
			t.Fatalf("%s = %+v, want on", id, e)
		}
	}
}

// HARNESS 15.3: an agent calling the API cannot raise its own ceiling; only
// the desktop app's confirmation carries presence.
func TestCeilingChangeNeedsPresence(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	bypass := domain.PermissionModeBypassPermissions
	before := fx.rows(t)

	_, err := fx.svc.Update(ctx, domain.LadoRulePermissionCeiling, UpdateInput{BaseVersion: 1, CeilingMode: &bypass})
	wantAPIError(t, err, apierr.KindForbidden, "LADO_PRESENCE_REQUIRED")
	invalid := apierr.Forbidden("LADO_PRESENCE_INVALID", "The confirmation could not be verified.")
	_, err = fx.svc.Update(ctx, domain.LadoRulePermissionCeiling, UpdateInput{
		BaseVersion: 1, CeilingMode: &bypass,
		Presence: func(string, string) (domain.LadoPresenceProof, error) { return domain.LadoPresenceProof{}, invalid },
	})
	if !errors.Is(err, invalid) {
		t.Fatalf("a refused proof = %v, want the presence check's error", err)
	}
	for _, mode := range []domain.PermissionMode{domain.PermissionModeDefault, "yolo"} {
		_, err := fx.svc.Update(ctx, domain.LadoRulePermissionCeiling, UpdateInput{BaseVersion: 1, CeilingMode: &mode, Presence: fx.presence("never-used-AAAAAAAAAAAAAAAA", false, nil)})
		wantAPIError(t, err, apierr.KindInvalid, "LADO_RULE_CEILING_INVALID")
	}
	_, err = fx.svc.Update(ctx, domain.LadoRuleKoreanCommits, UpdateInput{BaseVersion: 1, CeilingMode: &bypass})
	wantAPIError(t, err, apierr.KindInvalid, "LADO_RULE_CEILING_INVALID")
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after refused ceiling changes = %+v, want %+v", after, before)
	}

	// Showing the change needs no presence and keeps nothing.
	preview, err := fx.svc.Update(ctx, domain.LadoRulePermissionCeiling, UpdateInput{BaseVersion: 1, CeilingMode: &bypass, DryRun: true})
	if err != nil || preview.Applied || string(preview.Rule.Rule.Version.Content.Config) != `{"mode":"bypass-permissions"}` {
		t.Fatalf("dry run = (%+v, %v), want the change shown and not applied", preview, err)
	}
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after a dry run = %+v, want %+v", after, before)
	}
	if got := fx.rule(t, domain.LadoRulePermissionCeiling).Version; got.Number != 1 || string(got.Content.Config) != `{"mode":"auto"}` {
		t.Fatalf("ceiling = %+v, want version 1 at auto", got)
	}
}

func TestCeilingChangeWithProofRecordsAudit(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	bypass := domain.PermissionModeBypassPermissions
	nonce := "ceiling-nonce-AAAAAAAAAAAAAAAA"
	var asked []string

	got, err := fx.svc.Update(ctx, domain.LadoRulePermissionCeiling, UpdateInput{
		BaseVersion: 1, CeilingMode: &bypass, Reason: "trusted repository", Presence: fx.presence(nonce, false, &asked),
	})
	if err != nil || !got.Applied {
		t.Fatalf("Update = (%+v, %v), want applied", got, err)
	}
	if want := []string{"rule.protected_change agent.permission.ceiling"}; !slices.Equal(asked, want) {
		t.Fatalf("presence asked for %v, want %v", asked, want)
	}
	if v := got.Rule.Rule.Version; v.Number != 2 || string(v.Content.Config) != `{"mode":"bypass-permissions"}` || v.Actor != "human" {
		t.Fatalf("version = %+v, want version 2 at bypass-permissions by human", v)
	}
	event := fx.lastEvent(t)
	want := map[string]any{"from": "auto", "to": "bypass-permissions", "version": float64(2), "proof": nonce, "dev": false, "reason": "trusted repository"}
	if event.Action != "rule.protected_changed" || event.Actor != "human" || event.Subject != string(domain.LadoRulePermissionCeiling) || !reflect.DeepEqual(event.Payload, want) {
		t.Fatalf("audit event = %+v, want rule.protected_changed by human with %v", event, want)
	}
	var action, subject string
	if err := fx.raw.QueryRow(`SELECT action, subject_id FROM lado_presence_proofs WHERE nonce = ?`, nonce).Scan(&action, &subject); err != nil ||
		action != domain.LadoPresenceRuleChange || subject != string(domain.LadoRulePermissionCeiling) {
		t.Fatalf("stored proof = (%q, %q, %v), want the rule change's", action, subject, err)
	}
	view, err := fx.svc.Effective(ctx, EffectiveInput{ProjectID: "demo"})
	if err != nil || view.Ceiling != bypass {
		t.Fatalf("effective ceiling = %q, %v, want bypass-permissions", view.Ceiling, err)
	}

	// Development presence is recorded as such.
	accept := domain.PermissionModeAcceptEdits
	if _, err := fx.svc.Update(ctx, domain.LadoRulePermissionCeiling, UpdateInput{BaseVersion: 2, CeilingMode: &accept, Presence: fx.presence("dev-nonce-AAAAAAAAAAAAAAAA", true, nil)}); err != nil {
		t.Fatal(err)
	}
	if v := fx.rule(t, domain.LadoRulePermissionCeiling).Version; v.Number != 3 || v.Actor != "human:dev-presence" {
		t.Fatalf("version = %+v, want version 3 by human:dev-presence", v)
	}
	if event := fx.lastEvent(t); event.Actor != "human:dev-presence" || event.Payload["dev"] != true || event.Payload["from"] != "bypass-permissions" {
		t.Fatalf("audit event = %+v, want a development change from bypass-permissions", event)
	}
}

func TestCeilingChangeRefusesReplayedProof(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	nonce := "ceiling-nonce-AAAAAAAAAAAAAAAA"
	bypass, accept := domain.PermissionModeBypassPermissions, domain.PermissionModeAcceptEdits
	if _, err := fx.svc.Update(ctx, domain.LadoRulePermissionCeiling, UpdateInput{BaseVersion: 1, CeilingMode: &bypass, Presence: fx.presence(nonce, false, nil)}); err != nil {
		t.Fatal(err)
	}

	before := fx.rows(t)
	_, err := fx.svc.Update(ctx, domain.LadoRulePermissionCeiling, UpdateInput{BaseVersion: 2, CeilingMode: &accept, Presence: fx.presence(nonce, false, nil)})
	wantAPIError(t, err, apierr.KindForbidden, "LADO_PRESENCE_REPLAYED")
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after a replayed proof = %+v, want %+v", after, before)
	}
	if v := fx.rule(t, domain.LadoRulePermissionCeiling).Version; v.Number != 2 || string(v.Content.Config) != `{"mode":"bypass-permissions"}` {
		t.Fatalf("ceiling = %+v, want version 2 kept", v)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	task := fx.approvedTask(t, "claude-code")
	user := fx.userRule(t, false)
	bypass := domain.PermissionModeBypassPermissions
	before := fx.rows(t)

	created, err := fx.svc.Create(ctx, CreateInput{Name: "N", Instruction: "I", Scope: domain.LadoScopeTask, ScopeID: string(task.ID), DryRun: true})
	if err != nil || created.Applied || created.Rule.Rule.Version.Number != 1 || len(created.Rule.Rule.Overrides) != 1 {
		t.Fatalf("Create dry run = (%+v, %v), want the rule it would make", created, err)
	}
	updated, err := fx.svc.Update(ctx, user.ID, UpdateInput{BaseVersion: 1, Instruction: ptr("Changed."), DryRun: true})
	if err != nil || updated.Applied || updated.Rule.Rule.Version.Number != 2 {
		t.Fatalf("Update dry run = (%+v, %v), want the version it would add", updated, err)
	}
	archived, err := fx.svc.Update(ctx, user.ID, UpdateInput{BaseVersion: 1, Archived: ptr(true), DryRun: true})
	if err != nil || archived.Applied || archived.Rule.Rule.ArchivedAt.IsZero() {
		t.Fatalf("archive dry run = (%+v, %v), want the rule archived", archived, err)
	}
	bound, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: domain.LadoRuleKoreanCommits, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn, DryRun: true})
	if err != nil || bound.Applied || bound.Rule.Rule.Global != domain.LadoBindingOn {
		t.Fatalf("SetBinding dry run = (%+v, %v), want the binding it would set", bound, err)
	}
	ceiling, err := fx.svc.Update(ctx, domain.LadoRulePermissionCeiling, UpdateInput{BaseVersion: 1, CeilingMode: &bypass, DryRun: true})
	if err != nil || ceiling.Applied {
		t.Fatalf("ceiling dry run = (%+v, %v), want it shown without presence", ceiling, err)
	}
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after dry runs = %+v, want %+v", after, before)
	}
	if got := fx.rule(t, user.ID); got.Version.Number != 1 || !got.ArchivedAt.IsZero() {
		t.Fatalf("user rule = %+v, want version 1, not archived", got)
	}
	if got := fx.rule(t, domain.LadoRuleKoreanCommits); got.Global != domain.LadoBindingOff {
		t.Fatalf("Korean rule = %+v, want it still off", got)
	}
	if rules, err := fx.svc.List(ctx, true); err != nil || len(rules) != 4 {
		t.Fatalf("List = %d rules, %v, want the 3 built-in and 1 user rule", len(rules), err)
	}
	// Asking for what is already so writes nothing either.
	same, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: user.ID, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff})
	if err != nil || !same.Applied || len(same.Impact.Tasks) != 0 {
		t.Fatalf("binding to the current state = (%+v, %v)", same, err)
	}
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after a binding to the current state = %+v, want %+v", after, before)
	}
}

func TestSetBindingChecksScopeAndState(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	task := fx.task(t, "claude-code")
	korean := domain.LadoRuleKoreanCommits
	refused := []struct {
		name string
		in   BindingInput
		kind apierr.Kind
		code string
	}{
		{"force_on at Global", BindingInput{RuleID: korean, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingForceOn}, apierr.KindInvalid, "LADO_BINDING_STATE_INVALID"},
		{"inherit at Global", BindingInput{RuleID: korean, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingInherit}, apierr.KindInvalid, "LADO_BINDING_STATE_INVALID"},
		{"on for a project", BindingInput{RuleID: korean, Scope: domain.LadoScopeProject, ScopeID: "demo", State: domain.LadoBindingOn}, apierr.KindInvalid, "LADO_BINDING_STATE_INVALID"},
		{"Global with an id", BindingInput{RuleID: korean, Scope: domain.LadoScopeGlobal, ScopeID: "demo", State: domain.LadoBindingOn}, apierr.KindInvalid, "LADO_BINDING_SCOPE_INVALID"},
		{"a task without an id", BindingInput{RuleID: korean, Scope: domain.LadoScopeTask, State: domain.LadoBindingForceOn}, apierr.KindInvalid, "LADO_BINDING_SCOPE_INVALID"},
		{"an unknown task", BindingInput{RuleID: korean, Scope: domain.LadoScopeTask, ScopeID: "lado-task-missing", State: domain.LadoBindingForceOn}, apierr.KindNotFound, "LADO_SCOPE_NOT_FOUND"},
		{"an unknown rule", BindingInput{RuleID: "lado-rule-missing", Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn}, apierr.KindNotFound, "LADO_RULE_NOT_FOUND"},
	}
	before := fx.rows(t)
	for _, tc := range refused {
		_, err := fx.svc.SetBinding(ctx, tc.in)
		wantAPIError(t, err, tc.kind, tc.code)
	}
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after refused bindings = %+v, want %+v", after, before)
	}

	// A cancelled task takes no new binding, but its old one can go.
	forced := BindingInput{RuleID: korean, Scope: domain.LadoScopeTask, ScopeID: string(task.ID), State: domain.LadoBindingForceOn}
	if _, err := fx.svc.SetBinding(ctx, forced); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fx.store.CancelLadoTask(ctx, task.ID, fx.now); err != nil {
		t.Fatal(err)
	}
	forced.State = domain.LadoBindingForceOff
	_, err := fx.svc.SetBinding(ctx, forced)
	wantAPIError(t, err, apierr.KindConflict, "LADO_TASK_CANCELLED")
	forced.State = domain.LadoBindingInherit
	got, err := fx.svc.SetBinding(ctx, forced)
	if err != nil || !got.Applied || len(got.Rule.Rule.Overrides) != 0 || len(fx.rule(t, korean).Overrides) != 0 {
		t.Fatalf("inherit on a cancelled task = (%+v, %v), want the binding removed", got, err)
	}
	if event := fx.lastEvent(t); event.Payload["from"] != "force_on" || event.Payload["to"] != "inherit" {
		t.Fatalf("audit event = %+v, want force_on to inherit", event)
	}
}

// HARNESS 15.4: turning a rule off and archiving it keeps its versions and
// every approval's snapshot as they were.
func TestDisablingKeepsHistoryAndSnapshots(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	rule := fx.userRule(t, true)
	if _, err := fx.svc.Update(ctx, rule.ID, UpdateInput{BaseVersion: 1, Instruction: ptr("Second wording.")}); err != nil {
		t.Fatal(err)
	}
	task, rev := fx.plan(t, fx.task(t, "claude-code"), domain.PermissionModeAuto)
	task, approved := fx.approve(t, task, rev)
	var content SnapshotContent
	if err := json.Unmarshal(approved.Content, &content); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(content.Rules, func(r SnapshotRule) bool { return r.RuleID == rule.ID && r.Version == 2 }) {
		t.Fatalf("snapshot rules = %+v, want version 2 of %s", content.Rules, rule.ID)
	}

	project := BindingInput{RuleID: rule.ID, Scope: domain.LadoScopeProject, ScopeID: "demo", State: domain.LadoBindingForceOn}
	if _, err := fx.svc.SetBinding(ctx, project); err != nil {
		t.Fatal(err)
	}
	if effective, err := fx.svc.Effective(ctx, EffectiveInput{TaskID: task.ID}); err != nil || effective.Approved == nil || effective.Approved.Matches ||
		!slices.Equal(effective.Approved.Changed, []domain.LadoRuleID{rule.ID}) {
		t.Fatalf("on from the project now: approved = %+v, %v, want the rule changed", effective.Approved, err)
	}
	project.State = domain.LadoBindingInherit
	if _, err := fx.svc.SetBinding(ctx, project); err != nil {
		t.Fatal(err)
	}
	if effective, err := fx.svc.Effective(ctx, EffectiveInput{TaskID: task.ID}); err != nil || effective.Approved == nil || !effective.Approved.Matches ||
		effective.Task.State() != domain.LadoTaskApproved {
		t.Fatalf("back to the approved rules: approved = %+v, %v, want a match", effective.Approved, err)
	}
	if _, err := fx.svc.Update(ctx, rule.ID, UpdateInput{BaseVersion: 2, Instruction: ptr("Third wording.")}); err != nil {
		t.Fatal(err)
	}
	if effective, err := fx.svc.Effective(ctx, EffectiveInput{TaskID: task.ID}); err != nil || effective.Approved == nil ||
		!slices.Equal(effective.Approved.Changed, []domain.LadoRuleID{rule.ID}) || len(effective.Approved.Removed) != 0 {
		t.Fatalf("after a new version: approved = %+v, %v, want the rule changed", effective.Approved, err)
	}
	if _, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: rule.ID, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff}); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.Update(ctx, rule.ID, UpdateInput{BaseVersion: 3, Archived: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if event := fx.lastEvent(t); event.Action != "rule.archived" || event.Actor != "api" {
		t.Fatalf("audit event = %+v, want rule.archived by api", event)
	}

	view, versions, err := fx.svc.Rule(ctx, rule.ID)
	if err != nil || view.Rule.ArchivedAt.IsZero() || view.Rule.Global != domain.LadoBindingOff {
		t.Fatalf("rule = %+v, %v, want it off and archived", view.Rule, err)
	}
	if len(versions) != 3 || versions[0].Content.Instruction != "Run the tests before every commit." || versions[1].Content.Instruction != "Second wording." {
		t.Fatalf("versions = %+v, want every version kept", versions)
	}
	_, err = fx.svc.SetBinding(ctx, BindingInput{RuleID: rule.ID, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn})
	wantAPIError(t, err, apierr.KindConflict, "LADO_RULE_ARCHIVED")
	if listed, err := fx.svc.List(ctx, false); err != nil || slices.ContainsFunc(listed, func(v RuleView) bool { return v.Rule.ID == rule.ID }) {
		t.Fatalf("List without archived = %+v, %v, want the rule left out", listed, err)
	}
	stored, ok, err := fx.store.LadoPolicySnapshot(ctx, task.Plan.Approval.ID)
	sum := sha256.Sum256(stored.Content)
	if err != nil || !ok || !bytes.Equal(stored.Content, approved.Content) || stored.SHA256 != approved.SHA256 || hex.EncodeToString(sum[:]) != approved.SHA256 {
		t.Fatalf("stored snapshot = %+v, %v, %v, want the approved bytes and hash", stored, ok, err)
	}
	effective, err := fx.svc.Effective(ctx, EffectiveInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	got := effective.Approved
	if got == nil || got.Matches || got.Snapshot.SHA256 != approved.SHA256 || got.RevisionNumber != rev.Number ||
		!slices.Equal(got.Removed, []domain.LadoRuleID{rule.ID}) || len(got.Added) != 0 || len(got.Changed) != 0 {
		t.Fatalf("approved = %+v, want the old snapshot with the rule removed since", got)
	}
	if state := effective.Task.State(); state != domain.LadoTaskReviewRequired {
		t.Fatalf("task state = %s, want review_required", state)
	}

	unarchived, err := fx.svc.Update(ctx, rule.ID, UpdateInput{BaseVersion: 3, Archived: ptr(false)})
	if err != nil || !unarchived.Rule.Rule.ArchivedAt.IsZero() || unarchived.Rule.Rule.Version.Number != 3 {
		t.Fatalf("unarchive = (%+v, %v), want the rule back at version 3", unarchived, err)
	}
	if event := fx.lastEvent(t); event.Action != "rule.unarchived" || event.Actor != "api" {
		t.Fatalf("audit event = %+v, want rule.unarchived by api", event)
	}
}

func TestDeleteRule(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	unused := fx.userRule(t, false)
	if err := fx.svc.Delete(ctx, unused.ID); err != nil {
		t.Fatalf("deleting an unused rule = %v", err)
	}
	_, _, err := fx.svc.Rule(ctx, unused.ID)
	wantAPIError(t, err, apierr.KindNotFound, "LADO_RULE_NOT_FOUND")
	if event := fx.lastEvent(t); event.Action != "rule.deleted" || event.Actor != "api" || event.Subject != string(unused.ID) {
		t.Fatalf("audit event = %+v, want rule.deleted by api", event)
	}

	used := fx.userRule(t, true)
	fx.approvedTask(t, "claude-code")
	before := fx.rows(t)
	err = fx.svc.Delete(ctx, used.ID)
	wantAPIError(t, err, apierr.KindConflict, "LADO_RULE_REFERENCED")
	err = fx.svc.Delete(ctx, domain.LadoRuleKoreanCommits)
	wantAPIError(t, err, apierr.KindForbidden, "LADO_RULE_BUILTIN")
	err = fx.svc.Delete(ctx, "lado-rule-missing")
	wantAPIError(t, err, apierr.KindNotFound, "LADO_RULE_NOT_FOUND")
	if after := fx.rows(t); after != before {
		t.Fatalf("rows after refused deletes = %+v, want %+v", after, before)
	}
	if got := fx.rule(t, used.ID); got.Version.Number != 1 {
		t.Fatalf("used rule = %+v, want it kept", got)
	}
}

func TestEnsureBuiltinsSeedsOnce(t *testing.T) {
	fx := newServiceFixture(t) // the first call
	ctx := context.Background()
	if _, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: domain.LadoRuleKoreanCommits, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn}); err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.EnsureBuiltins(ctx); err != nil {
		t.Fatalf("second EnsureBuiltins: %v", err)
	}

	if rows := fx.rows(t); rows.rules != 3 || rows.versions != 3 || rows.bindings != 3 {
		t.Fatalf("rows = %+v, want 3 rules with one version and one binding each", rows)
	}
	var seeded []auditRow
	for _, event := range fx.events(t) {
		if event.Action == "rule.created" {
			seeded = append(seeded, event)
		}
	}
	if len(seeded) != 3 {
		t.Fatalf("rule.created events = %+v, want 3", seeded)
	}
	for _, event := range seeded {
		if event.Actor != "lado" || !reflect.DeepEqual(event.Payload, map[string]any{"version": float64(1), "builtin": true}) {
			t.Fatalf("seed event = %+v, want lado's with version 1, builtin", event)
		}
	}
	rules, err := fx.svc.List(ctx, true)
	if err != nil || len(rules) != 3 {
		t.Fatalf("List = %+v, %v, want 3 rules", rules, err)
	}
	global := map[domain.LadoRuleID]domain.LadoBindingState{
		domain.LadoRulePlanApproval:      domain.LadoBindingOn,
		domain.LadoRulePermissionCeiling: domain.LadoBindingOn,
		domain.LadoRuleKoreanCommits:     domain.LadoBindingOn, // the developer's change stays
	}
	for _, view := range rules {
		rule := view.Rule
		if want, ok := global[rule.ID]; !ok || rule.Global != want || !rule.Builtin || rule.Version.Number != 1 || rule.Version.Actor != "lado" ||
			!strings.HasPrefix(string(rule.Version.ID), "lado-rule-version-") || !rule.CreatedAt.Equal(serviceEpoch) {
			t.Fatalf("rule = %+v, want a built-in seeded once", rule)
		}
	}
}

// HARNESS 15.7: an agent that cannot take the rules' text blocks the task.
func TestEffectiveBlocksAnUnsupportedAgent(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	task := fx.task(t, "cursor")
	if _, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: domain.LadoRuleKoreanCommits, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn}); err != nil {
		t.Fatal(err)
	}

	view, err := fx.svc.Effective(ctx, EffectiveInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	unsupported := domain.LadoPolicyProblem{
		Code: ProblemCapabilityUnsupported, Severity: domain.LadoSeverityBlocking, Rules: []domain.LadoRuleID{domain.LadoRuleKoreanCommits},
		Details: map[string]string{"agent": "cursor", "capability": string(CapabilityPrompt)},
	}
	if !view.Blocking() || !slices.ContainsFunc(view.Problems, func(p domain.LadoPolicyProblem) bool { return reflect.DeepEqual(p, unsupported) }) {
		t.Fatalf("problems = %+v, want the Korean rule blocking cursor", view.Problems)
	}
	if want := (AgentChoice{Harness: "cursor", Source: AgentFromTask}); view.Agent != want || view.Task == nil || view.Task.ID != task.ID || view.Approved != nil {
		t.Fatalf("view = %+v, want cursor from the task and no approval", view)
	}

	if _, _, err := fx.store.SetLadoTaskPreferences(ctx, task.ID, "claude-code", "", fx.now); err != nil {
		t.Fatal(err)
	}
	view, err = fx.svc.Effective(ctx, EffectiveInput{TaskID: task.ID})
	if err != nil || view.Blocking() {
		t.Fatalf("claude-code: problems = %+v, %v, want nothing blocking", view.Problems, err)
	}
	if !strings.Contains(view.Prompt, "### Commit messages in Korean") {
		t.Fatalf("prompt = %q, want the Korean rule", view.Prompt)
	}
}

func TestSnapshotMatchesCurrentSHA(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	task, rev := fx.plan(t, fx.task(t, "claude-code"), domain.PermissionModeAcceptEdits)

	snapshot, err := fx.svc.Snapshot(ctx, task, rev)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(snapshot.Content)
	if !strings.HasPrefix(string(snapshot.ID), "lado-snapshot-") || hex.EncodeToString(sum[:]) != snapshot.SHA256 || snapshot.Blocking {
		t.Fatalf("snapshot = %+v, want a new id and the hash of its content, not blocking", snapshot)
	}
	wantRules := []domain.LadoPolicySnapshotRule{
		{RuleID: domain.LadoRulePermissionCeiling, VersionID: fx.rule(t, domain.LadoRulePermissionCeiling).Version.ID},
		{RuleID: domain.LadoRulePlanApproval, VersionID: fx.rule(t, domain.LadoRulePlanApproval).Version.ID},
	}
	if !reflect.DeepEqual(snapshot.Rules, wantRules) {
		t.Fatalf("snapshot rules = %+v, want the protected rules' versions %+v", snapshot.Rules, wantRules)
	}
	current, err := fx.svc.CurrentSHA256(ctx, task)
	if err != nil || current != snapshot.SHA256 {
		t.Fatalf("CurrentSHA256 = %s, %v, want the snapshot's %s", current, err, snapshot.SHA256)
	}
	again, err := fx.svc.Snapshot(ctx, task, rev)
	if err != nil || again.SHA256 != snapshot.SHA256 || !bytes.Equal(again.Content, snapshot.Content) || again.ID == snapshot.ID {
		t.Fatalf("second snapshot = %+v, %v, want the same content under a new id", again, err)
	}
	view, err := fx.svc.Effective(ctx, EffectiveInput{TaskID: task.ID})
	if err != nil || view.SHA256 != snapshot.SHA256 || view.Mode != domain.PermissionModeAcceptEdits {
		t.Fatalf("effective = %s at %s, %v, want the snapshot at the plan's accept-edits", view.SHA256, view.Mode, err)
	}

	// A binding of another task leaves it; one of this task changes it.
	other := fx.task(t, "claude-code")
	if _, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: domain.LadoRuleKoreanCommits, Scope: domain.LadoScopeTask, ScopeID: string(other.ID), State: domain.LadoBindingForceOn}); err != nil {
		t.Fatal(err)
	}
	if current, err := fx.svc.CurrentSHA256(ctx, task); err != nil || current != snapshot.SHA256 {
		t.Fatalf("after another task's binding = %s, %v, want %s", current, err, snapshot.SHA256)
	}
	if _, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: domain.LadoRuleKoreanCommits, Scope: domain.LadoScopeTask, ScopeID: string(task.ID), State: domain.LadoBindingForceOn}); err != nil {
		t.Fatal(err)
	}
	if event := fx.lastEvent(t); event.Action != "rule.binding_set" || event.Actor != "api" ||
		!reflect.DeepEqual(event.Payload, map[string]any{"scope": "task", "scopeId": string(task.ID), "from": "inherit", "to": "force_on"}) {
		t.Fatalf("audit event = %+v, want rule.binding_set from inherit to force_on", event)
	}
	if current, err := fx.svc.CurrentSHA256(ctx, task); err != nil || current == snapshot.SHA256 {
		t.Fatalf("after the task's binding = %s, %v, want another hash", current, err)
	}
}

// countingStore counts the rule reads of the service.
type countingStore struct {
	Store
	mu    sync.Mutex
	lists int
}

func (c *countingStore) ListLadoRules(ctx context.Context, includeArchived bool) ([]domain.LadoRule, error) {
	c.mu.Lock()
	c.lists++
	c.mu.Unlock()
	return c.Store.ListLadoRules(ctx, includeArchived)
}

// countingProjects counts the reads of each project.
type countingProjects struct {
	Projects
	mu    sync.Mutex
	reads map[string]int
}

func (c *countingProjects) GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error) {
	c.mu.Lock()
	c.reads[id]++
	c.mu.Unlock()
	return c.Projects.GetProject(ctx, id)
}

func TestCurrentSHA256sMatchesOneByOne(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	bound := fx.approvedTask(t, "claude-code")
	if _, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: domain.LadoRuleKoreanCommits, Scope: domain.LadoScopeTask, ScopeID: string(bound.ID), State: domain.LadoBindingForceOn}); err != nil {
		t.Fatal(err)
	}
	lower, _ := fx.plan(t, fx.task(t, "claude-code"), domain.PermissionModeAcceptEdits)
	unreadable, rev := fx.plan(t, fx.task(t, "claude-code"), domain.PermissionModeAuto)
	fx.blobs.mu.Lock()
	delete(fx.blobs.data, rev.SHA256)
	fx.blobs.mu.Unlock()
	tasks := []domain.LadoTask{
		fx.approvedTask(t, "claude-code"),
		fx.approvedTask(t, "codex"),
		bound,
		lower,
		fx.taskOf(t, "other", "claude-code"),
		fx.task(t, ""),
		unreadable,
		fx.taskOf(t, "ghost", "claude-code"),
	}

	got, err := fx.svc.CurrentSHA256s(ctx, tasks)
	if err != nil {
		t.Fatalf("CurrentSHA256s: %v", err)
	}
	// A task CurrentSHA256 cannot resolve, for its pinned plan or its
	// project, is left out.
	want := map[domain.LadoTaskID]string{}
	for _, task := range tasks {
		if sum, err := fx.svc.CurrentSHA256(ctx, task); err == nil {
			want[task.ID] = sum
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CurrentSHA256s = %v, want the hashes one by one %v", got, want)
	}
	distinct := map[string]bool{}
	for _, sum := range want {
		distinct[sum] = true
	}
	if len(want) != len(tasks)-2 || len(distinct) != len(want) {
		t.Fatalf("one by one: %d hashes, %d distinct, want %d different ones", len(want), len(distinct), len(tasks)-2)
	}
}

func TestCurrentSHA256sReadsRulesOnce(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	tasks := []domain.LadoTask{fx.approvedTask(t, "claude-code"), fx.approvedTask(t, "codex"), fx.approvedTask(t, "claude-code")}
	store := &countingStore{Store: fx.store}
	projects := &countingProjects{Projects: fx.store, reads: map[string]int{}}
	fx.svc.deps.Store, fx.svc.deps.Projects = store, projects

	sums, err := fx.svc.CurrentSHA256s(ctx, tasks)
	if err != nil || len(sums) != len(tasks) {
		t.Fatalf("CurrentSHA256s = (%v, %v), want a hash for each task", sums, err)
	}
	if store.lists != 1 || !reflect.DeepEqual(projects.reads, map[string]int{"demo": 1}) {
		t.Fatalf("three tasks of demo read the rules %d times and the projects %v, want once each", store.lists, projects.reads)
	}
}

// A cancelled request reads no pinned plan, and the ceiling it did not read
// is not kept for the requests after it.
func TestPlanCeilingReadsNoBlobOnceCancelled(t *testing.T) {
	fx := newServiceFixture(t)
	_, rev := fx.plan(t, fx.task(t, "claude-code"), domain.PermissionModeAcceptEdits)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if got, err := fx.svc.planCeiling(cancelled, rev); !errors.Is(err, context.Canceled) {
		t.Fatalf("planCeiling with a cancelled context = (%q, %v), want context.Canceled", got, err)
	}
	if got, err := fx.svc.planCeiling(context.Background(), rev); err != nil || got != domain.PermissionModeAcceptEdits {
		t.Fatalf("planCeiling after the cancelled read = (%q, %v), want accept-edits", got, err)
	}
}

func TestImpactListsApprovedTasks(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	approved := fx.approvedTask(t, "claude-code")
	// A task whose approval already needs review: its model changed.
	stale := fx.approvedTask(t, "claude-code")
	if _, _, err := fx.store.SetLadoTaskPreferences(ctx, stale.ID, "claude-code", "opus", fx.now); err != nil {
		t.Fatal(err)
	}
	planned, _ := fx.plan(t, fx.task(t, "claude-code"), domain.PermissionModeAuto)
	want := []ImpactTask{{ID: approved.ID, ProjectID: "demo", Title: "Task 1", IssueRef: approved.IssueRef}}
	bind := func(t *testing.T, scope domain.LadoRuleScope, scopeID string, state domain.LadoBindingState, dryRun bool) Impact {
		t.Helper()
		got, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: domain.LadoRuleKoreanCommits, Scope: scope, ScopeID: scopeID, State: state, DryRun: dryRun})
		if err != nil || got.Applied == dryRun {
			t.Fatalf("SetBinding(%s %s %s, dry run %v) = (%+v, %v)", scope, scopeID, state, dryRun, got, err)
		}
		return got.Impact
	}

	if got := bind(t, domain.LadoScopeTask, string(approved.ID), domain.LadoBindingForceOn, true); !reflect.DeepEqual(got.Tasks, want) || len(got.Projects) != 0 {
		t.Fatalf("binding the approved task: impact = %+v, want only the task", got)
	}
	untouched := []struct {
		name         string
		scope        domain.LadoRuleScope
		scopeID      string
		wantProjects []ImpactProject
	}{
		{"another project", domain.LadoScopeProject, "other", []ImpactProject{{ID: "other", Name: "Other"}}},
		{"a task without approval", domain.LadoScopeTask, string(planned.ID), []ImpactProject{}},
		{"a task that needs review already", domain.LadoScopeTask, string(stale.ID), []ImpactProject{}},
	}
	for _, tc := range untouched {
		if got := bind(t, tc.scope, tc.scopeID, domain.LadoBindingForceOn, true); len(got.Tasks) != 0 || !reflect.DeepEqual(got.Projects, tc.wantProjects) {
			t.Fatalf("%s: impact = %+v, want no task and projects %+v", tc.name, got, tc.wantProjects)
		}
	}
	everywhere := []ImpactProject{{ID: "demo", Name: "Demo"}, {ID: "other", Name: "Other"}}
	if got := bind(t, domain.LadoScopeGlobal, "", domain.LadoBindingOn, true); !reflect.DeepEqual(got.Tasks, want) || !reflect.DeepEqual(got.Projects, everywhere) {
		t.Fatalf("Global on: impact = %+v, want the approved task and both projects", got)
	}

	// Applying the change reports the same impact, computed before writing.
	if got := bind(t, domain.LadoScopeTask, string(approved.ID), domain.LadoBindingForceOn, false); !reflect.DeepEqual(got.Tasks, want) {
		t.Fatalf("applied binding: impact = %+v, want the approved task", got)
	}
	view, err := fx.svc.Effective(ctx, EffectiveInput{TaskID: approved.ID})
	if err != nil || view.Approved == nil || view.Approved.Matches || !slices.Equal(view.Approved.Added, []domain.LadoRuleID{domain.LadoRuleKoreanCommits}) {
		t.Fatalf("approved = %+v, %v, want the Korean rule added since", view.Approved, err)
	}
	// Once it needs review, the next change no longer lists it.
	if got := bind(t, domain.LadoScopeGlobal, "", domain.LadoBindingOn, true); len(got.Tasks) != 0 {
		t.Fatalf("after the change: impact = %+v, want no task", got)
	}
	// An archived project is out of reach.
	if _, err := fx.store.ArchiveProject(ctx, "other", fx.now); err != nil {
		t.Fatal(err)
	}
	if got := bind(t, domain.LadoScopeGlobal, "", domain.LadoBindingOn, true); !reflect.DeepEqual(got.Projects, []ImpactProject{{ID: "demo", Name: "Demo"}}) {
		t.Fatalf("with other archived: impact = %+v, want demo only", got)
	}
	_, err = fx.svc.SetBinding(ctx, BindingInput{RuleID: domain.LadoRuleKoreanCommits, Scope: domain.LadoScopeProject, ScopeID: "other", State: domain.LadoBindingForceOn})
	wantAPIError(t, err, apierr.KindNotFound, "LADO_SCOPE_NOT_FOUND")
	_, err = fx.svc.Effective(ctx, EffectiveInput{ProjectID: "other"})
	wantAPIError(t, err, apierr.KindNotFound, "LADO_SCOPE_NOT_FOUND")
}

func TestEffectiveNamesOneScope(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	task := fx.task(t, "claude-code")
	refused := []struct {
		in   EffectiveInput
		kind apierr.Kind
		code string
	}{
		{EffectiveInput{}, apierr.KindInvalid, "LADO_EFFECTIVE_SCOPE_INVALID"},
		{EffectiveInput{ProjectID: "other", TaskID: task.ID}, apierr.KindInvalid, "LADO_EFFECTIVE_SCOPE_INVALID"},
		{EffectiveInput{ProjectID: "missing"}, apierr.KindNotFound, "LADO_SCOPE_NOT_FOUND"},
		{EffectiveInput{TaskID: "lado-task-missing"}, apierr.KindNotFound, "LADO_SCOPE_NOT_FOUND"},
	}
	for _, tc := range refused {
		_, err := fx.svc.Effective(ctx, tc.in)
		wantAPIError(t, err, tc.kind, tc.code)
	}
	view, err := fx.svc.Effective(ctx, EffectiveInput{ProjectID: "demo", TaskID: task.ID})
	if err != nil || view.Content.TaskID != task.ID || view.Content.ProjectID != "demo" {
		t.Fatalf("Effective with the task's own project = %+v, %v", view.Content, err)
	}
}
