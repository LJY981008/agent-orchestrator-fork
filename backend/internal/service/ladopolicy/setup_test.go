package ladopolicy_test

// Local setup in resolution, snapshots, previews and impact (step 7 plan
// §11), with the real store and the real setup service. The setup service
// reads a Claude folder under t.TempDir(), never the developer's.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladosetup"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/ladoblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

var setupEpoch = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

// setupFixture is the rule service with the setup service as its
// SetupSource, on one store, with two LADO projects: demo, whose worker runs
// Claude Code, and other, without a worker agent.
type setupFixture struct {
	t      *testing.T
	store  *sqlite.Store
	blobs  *ladoblob.Store
	setup  *ladosetup.Service
	policy *ladopolicy.Service
	now    time.Time
	ids    int
}

func newSetupFixture(t *testing.T) *setupFixture {
	t.Helper()
	ctx := context.Background()
	fx := &setupFixture{t: t, store: sqlitetest.MustOpen(t), blobs: ladoblob.New(t.TempDir()), now: setupEpoch}
	t.Cleanup(func() { _ = fx.blobs.Close() })
	for _, project := range []domain.ProjectRecord{
		{ID: "demo", DisplayName: "Demo", Path: t.TempDir(), RegisteredAt: setupEpoch,
			Config: domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode}}},
		{ID: "other", DisplayName: "Other", Path: t.TempDir(), RegisteredAt: setupEpoch},
	} {
		if err := fx.store.UpsertProject(ctx, project); err != nil {
			t.Fatalf("seed project %s: %v", project.ID, err)
		}
		if _, err := fx.store.SetLadoMode(ctx, domain.ProjectID(project.ID), true, setupEpoch); err != nil {
			t.Fatalf("turn on LADO mode for %s: %v", project.ID, err)
		}
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	home := t.TempDir()
	fx.setup = ladosetup.New(ladosetup.Deps{
		Store: fx.store, Blobs: fx.blobs, StateDir: filepath.Join(home, "state"), ClaudeDir: filepath.Join(home, ".claude"),
		GOOS: "linux", Clock: func() time.Time { return fx.now }, Log: quiet,
	})
	fx.policy = fx.service(fx.setup)
	if err := fx.policy.EnsureBuiltins(ctx); err != nil {
		t.Fatalf("EnsureBuiltins: %v", err)
	}
	return fx
}

// service is a rule service on the fixture's store with setup as its
// SetupSource.
func (fx *setupFixture) service(setup ladopolicy.SetupSource) *ladopolicy.Service {
	return ladopolicy.New(ladopolicy.Deps{
		Store: fx.store, Projects: fx.store, Blobs: fx.blobs, AOBuild: "1.0.0", Setup: setup,
		Clock: func() time.Time { return fx.now }, NewID: fx.newID, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func (fx *setupFixture) newID() string {
	fx.ids++
	return strconv.Itoa(fx.ids)
}

func (fx *setupFixture) event(action, subject string) domain.LadoAuditEvent {
	return domain.LadoAuditEvent{At: fx.now, Actor: "api", Action: action, SubjectKind: "setup_item", SubjectID: subject, Payload: json.RawMessage(`{}`)}
}

// item stores an item made in LADO whose version 1 holds content: JSON for a
// setting or an MCP server, a file otherwise.
func (fx *setupFixture) item(tool domain.LadoSetupTool, kind domain.LadoSetupKind, name, content string) domain.LadoSetupItemWithVersion {
	fx.t.Helper()
	id := domain.LadoSetupItemID("setup-" + fx.newID())
	item := domain.LadoSetupItem{ID: id, Tool: tool, Kind: kind, Name: name, CreatedAt: fx.now}
	v := fx.versionOf(id, kind, 1, content)
	if err := fx.store.CreateLadoSetupItem(context.Background(), item, v, fx.event("setup.item_created", string(id)), nil); err != nil {
		fx.t.Fatalf("CreateLadoSetupItem %s: %v", name, err)
	}
	return domain.LadoSetupItemWithVersion{Item: item, Version: v}
}

// edit stores the item's next version, which holds content.
func (fx *setupFixture) edit(iv domain.LadoSetupItemWithVersion, content string) domain.LadoSetupItemWithVersion {
	fx.t.Helper()
	v := fx.versionOf(iv.Item.ID, iv.Item.Kind, iv.Version.Version+1, content)
	if err := fx.store.AddLadoSetupItemVersion(context.Background(), iv.Item.ID, iv.Version.Version, v, fx.event("setup.item_updated", string(iv.Item.ID)), nil); err != nil {
		fx.t.Fatalf("AddLadoSetupItemVersion %s: %v", iv.Item.Name, err)
	}
	return domain.LadoSetupItemWithVersion{Item: iv.Item, Version: v}
}

// versionOf is a version of content with its blob stored.
func (fx *setupFixture) versionOf(id domain.LadoSetupItemID, kind domain.LadoSetupKind, number int, content string) domain.LadoSetupItemVersion {
	fx.t.Helper()
	sum, err := fx.blobs.Put([]byte(content))
	if err != nil {
		fx.t.Fatalf("put blob: %v", err)
	}
	contentKind := domain.LadoSetupContentFile
	if kind == domain.LadoSetupKindSetting || kind == domain.LadoSetupKindMCPServer {
		contentKind = domain.LadoSetupContentJSON
	}
	return domain.LadoSetupItemVersion{
		ID: domain.LadoSetupItemVersionID(fmt.Sprintf("%s-v%d", id, number)), ItemID: id, Version: number,
		ContentKind: contentKind, SHA256: sum, Size: int64(len(content)), Actor: "api", CreatedAt: fx.now,
	}
}

// bind sets an item's binding at a scope; inherit removes it.
func (fx *setupFixture) bind(iv domain.LadoSetupItemWithVersion, scope domain.LadoRuleScope, scopeID string, state domain.LadoBindingState) {
	fx.t.Helper()
	b := domain.LadoSetupBinding{ItemID: iv.Item.ID, Scope: scope, ScopeID: scopeID, State: state, UpdatedAt: fx.now}
	if err := fx.store.SetLadoSetupBinding(context.Background(), b, state == domain.LadoBindingInherit, fx.event("setup.binding_set", string(iv.Item.ID)), nil); err != nil {
		fx.t.Fatalf("SetLadoSetupBinding %s %s %s: %v", iv.Item.Name, scope, state, err)
	}
}

// accept accepts a warning about the item's current version at a scope.
func (fx *setupFixture) accept(iv domain.LadoSetupItemWithVersion, code, detail string, scope domain.LadoRuleScope, scopeID string) domain.LadoConflictAcceptanceID {
	fx.t.Helper()
	id := domain.LadoConflictAcceptanceID("lado-acceptance-" + fx.newID())
	a := domain.LadoConflictAcceptance{
		ID: id, Code: code, ItemID: iv.Item.ID, Detail: detail, ContentSHA256: iv.Version.SHA256,
		Scope: scope, ScopeID: scopeID, Actor: "api", CreatedAt: fx.now,
	}
	if err := fx.store.AcceptLadoConflict(context.Background(), a, fx.event("setup.conflict_accepted", string(iv.Item.ID))); err != nil {
		fx.t.Fatalf("AcceptLadoConflict: %v", err)
	}
	return id
}

// task adds a task of the project with an open issue, run by agent.
func (fx *setupFixture) task(project domain.ProjectID, agent string) domain.LadoTask {
	fx.t.Helper()
	n := fx.newID()
	task, err := fx.store.CreateLadoTask(context.Background(), domain.LadoTask{
		ID:        domain.LadoTaskID("lado-task-" + n),
		ProjectID: project,
		Repo:      domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
		IssueRef:  domain.IssueID("github:acme/demo#" + n),
		Issue: domain.LadoObservedIssue{
			Title: "Task " + n, State: domain.IssueOpen, URL: "https://github.com/acme/demo/issues/" + n,
			SnapshotSHA256: strings.Repeat("1", 64), ObservedAt: fx.now,
		},
		Agent:     agent,
		CreatedAt: fx.now,
		UpdatedAt: fx.now,
	})
	if err != nil {
		fx.t.Fatalf("CreateLadoTask: %v", err)
	}
	return task
}

const setupPlanTemplate = `---
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
permission_ceiling: auto
review:
  required: false
plan_source: issue
---

Plan.
`

// approved adds a task of the project run by agent and approves its plan
// under the rules and setup as they are now, as the task service does. It
// returns the task as the store reads it then and the stored snapshot.
func (fx *setupFixture) approved(project domain.ProjectID, agent string) (domain.LadoTask, domain.LadoPolicySnapshot) {
	fx.t.Helper()
	ctx := context.Background()
	task := fx.task(project, agent)
	n := fx.newID()
	sum, err := fx.blobs.Put([]byte(fmt.Sprintf(setupPlanTemplate, task.ID, task.IssueRef)))
	if err != nil {
		fx.t.Fatal(err)
	}
	req, err := fx.store.CreateLadoPlanRequest(ctx, domain.LadoPlanRequest{
		ID: domain.LadoPlanRequestID("lado-plan-request-" + n), TaskID: task.ID, SessionID: "demo-orchestrator-1",
		IssueSnapshotSHA256: strings.Repeat("1", 64), RequestedAt: fx.now,
	})
	if err != nil {
		fx.t.Fatalf("CreateLadoPlanRequest: %v", err)
	}
	rev, created, err := fx.store.PinLadoPlanRevision(ctx, domain.LadoPlanRevision{
		ID: domain.LadoPlanRevisionID("lado-plan-" + n), TaskID: task.ID, RequestID: req.ID, SessionID: "demo-orchestrator-1",
		Path: domain.LadoPlanPath(task.ID), ArtifactVersion: 1, Source: "agent", SHA256: sum, BaseRef: "main",
		BaseSHA: strings.Repeat("c", 40), IssueSnapshotSHA256: strings.Repeat("1", 64), CreatedAt: fx.now,
	})
	if err != nil || !created {
		fx.t.Fatalf("PinLadoPlanRevision = (%v, %v), want a new revision", created, err)
	}
	task = fx.reload(task.ID)
	snapshot, err := fx.policy.Snapshot(ctx, task, rev)
	if err != nil {
		fx.t.Fatalf("Snapshot: %v", err)
	}
	approval := domain.LadoPlanApprovalID("lado-approval-" + n)
	proof := domain.LadoPresenceProof{
		Nonce: "approve-" + n + "-AAAAAAAAAAAAAAAA", Action: domain.LadoPresencePlanApprove, SubjectID: string(rev.ID),
		ExpiresAt: fx.now.Add(time.Minute), UsedAt: fx.now,
	}
	if _, _, err := fx.store.ApproveLadoPlan(ctx,
		domain.LadoPlanApproval{ID: approval, RevisionID: rev.ID, TaskID: task.ID, SHA256: rev.SHA256, ProofNonce: proof.Nonce, ApprovedAt: fx.now},
		proof,
		domain.LadoAuditEvent{At: fx.now, Actor: "human", Action: "plan.approved", SubjectKind: "plan_revision", SubjectID: string(rev.ID), Payload: json.RawMessage(`{}`)},
		&snapshot, &domain.LadoQueueEntry{ID: domain.LadoQueueEntryID("lado-queue-" + n)},
	); err != nil {
		fx.t.Fatalf("ApproveLadoPlan: %v", err)
	}
	stored, ok, err := fx.store.LadoPolicySnapshot(ctx, approval)
	if err != nil || !ok {
		fx.t.Fatalf("LadoPolicySnapshot = (%v, %v)", ok, err)
	}
	return fx.reload(task.ID), stored
}

func (fx *setupFixture) reload(id domain.LadoTaskID) domain.LadoTask {
	fx.t.Helper()
	task, ok, err := fx.store.LadoTask(context.Background(), id)
	if err != nil || !ok {
		fx.t.Fatalf("LadoTask %s = (%v, %v)", id, ok, err)
	}
	return task
}

func (fx *setupFixture) effective(in ladopolicy.EffectiveInput) ladopolicy.EffectiveView {
	fx.t.Helper()
	view, err := fx.policy.Effective(context.Background(), in)
	if err != nil {
		fx.t.Fatalf("Effective(%+v): %v", in, err)
	}
	return view
}

// setupNames lists the resolved items as tool/name, and the on ones apart.
func setupNames(setup ladopolicy.SetupResolution) (all, on []string) {
	all, on = []string{}, []string{}
	for _, item := range setup.Items {
		all = append(all, string(item.Tool)+"/"+item.Name)
		if item.On {
			on = append(on, string(item.Tool)+"/"+item.Name)
		}
	}
	return all, on
}

func snapshotNames(items []ladopolicy.SnapshotSetupItem) []string {
	names := []string{}
	for _, item := range items {
		names = append(names, string(item.Tool)+"/"+item.Name)
	}
	return names
}

func problemCodes(problems []domain.LadoPolicyProblem) []string {
	codes := []string{}
	for _, p := range problems {
		codes = append(codes, p.Code)
	}
	return codes
}

func TestResolveIncludesSetupOfTheAgentTool(t *testing.T) {
	fx := newSetupFixture(t)
	claude, codex := domain.LadoSetupToolClaudeCode, domain.LadoSetupToolCodex
	fx.item(claude, domain.LadoSetupKindInstructions, "CLAUDE.md", "Be brief.\n")
	fx.item(claude, domain.LadoSetupKindSetting, "model", `"opus"`)
	fx.item(codex, domain.LadoSetupKindInstructions, "AGENTS.md", "Use pnpm.\n")
	fx.item(codex, domain.LadoSetupKindMCPServer, "figma", `{"url":"https://figma.example"}`)
	_, aoSystem, err := ladosetup.CurrentAOSystem()
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		agent string
		want  []string
	}{
		{"claude-code", []string{"claude-code/CLAUDE.md", "claude-code/model"}},
		{"codex", []string{"codex/AGENTS.md", "codex/figma"}},
		// A task without an agent is blocked until one is chosen, and gets no
		// setup; neither does an agent LADO manages no setup for.
		{"", []string{}},
		{"gemini", []string{}},
	} {
		task := fx.task("other", c.agent)
		view := fx.effective(ladopolicy.EffectiveInput{TaskID: task.ID})
		all, on := setupNames(view.Setup)
		if !slices.Equal(all, c.want) || !slices.Equal(on, c.want) || !slices.Equal(snapshotNames(view.Content.Setup), c.want) {
			t.Errorf("agent %q: items %v, on %v, snapshot %v; want %v", c.agent, all, on, snapshotNames(view.Content.Setup), c.want)
		}
		if view.Setup.AOSystemSHA256 != aoSystem || view.Content.AOSystem.SHA256 != aoSystem {
			t.Errorf("agent %q: aoSystem %q in the setup, %q in the snapshot; want this build's %s", c.agent, view.Setup.AOSystemSHA256, view.Content.AOSystem.SHA256, aoSystem)
		}
		for _, item := range view.Setup.Items {
			if item.VersionID == "" || item.Version != 1 || len(item.SHA256) != 64 {
				t.Errorf("agent %q: item %+v, want its latest version", c.agent, item)
			}
		}
	}

	// A project resolves with its worker agent: demo's is Claude Code, other
	// has none.
	if all, _ := setupNames(fx.effective(ladopolicy.EffectiveInput{ProjectID: "demo"}).Setup); !slices.Equal(all, []string{"claude-code/CLAUDE.md", "claude-code/model"}) {
		t.Errorf("demo's items %v, want its Claude Code worker's", all)
	}
	if all, _ := setupNames(fx.effective(ladopolicy.EffectiveInput{ProjectID: "other"}).Setup); len(all) != 0 {
		t.Errorf("other's items %v, want none without a worker agent", all)
	}

	// Without a SetupSource there is no setup at all, not even the AO layer.
	task := fx.task("demo", "claude-code")
	view, err := fx.service(nil).Effective(context.Background(), ladopolicy.EffectiveInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Setup.Items) != 0 || len(view.Content.Setup) != 0 || view.Content.AOSystem.SHA256 != "" || len(view.Content.AcceptedConflicts) != 0 {
		t.Errorf("without a setup source: setup %+v, snapshot setup %+v, aoSystem %q", view.Setup, view.Content.Setup, view.Content.AOSystem.SHA256)
	}
}

func TestSetupBindingsResolveLikeRules(t *testing.T) {
	fx := newSetupFixture(t)
	claude := domain.LadoSetupToolClaudeCode
	md := func(name string) domain.LadoSetupItemWithVersion {
		return fx.item(claude, domain.LadoSetupKindInstructions, name, name+"\n")
	}
	byDefault, globalOff, projectOff, taskOn, projectOn := md("A.md"), md("B.md"), md("C.md"), md("D.md"), md("E.md")
	task := fx.task("demo", "claude-code")
	otherTask := fx.task("demo", "claude-code")
	fx.bind(globalOff, domain.LadoScopeGlobal, "", domain.LadoBindingOff)
	// Bindings of another project and another task do not count.
	fx.bind(globalOff, domain.LadoScopeProject, "other", domain.LadoBindingForceOn)
	fx.bind(globalOff, domain.LadoScopeTask, string(otherTask.ID), domain.LadoBindingForceOn)
	fx.bind(projectOff, domain.LadoScopeGlobal, "", domain.LadoBindingOn)
	fx.bind(projectOff, domain.LadoScopeProject, "demo", domain.LadoBindingForceOff)
	// The nearest binding wins: the task's over the project's over Global.
	fx.bind(taskOn, domain.LadoScopeGlobal, "", domain.LadoBindingOff)
	fx.bind(taskOn, domain.LadoScopeProject, "demo", domain.LadoBindingForceOff)
	fx.bind(taskOn, domain.LadoScopeTask, string(task.ID), domain.LadoBindingForceOn)
	fx.bind(projectOn, domain.LadoScopeGlobal, "", domain.LadoBindingOff)
	fx.bind(projectOn, domain.LadoScopeProject, "demo", domain.LadoBindingForceOn)

	type want struct {
		on                    bool
		source                ladopolicy.Source
		global, project, task domain.LadoBindingState
	}
	on, off, inherit := domain.LadoBindingOn, domain.LadoBindingOff, domain.LadoBindingInherit
	forceOn, forceOff := domain.LadoBindingForceOn, domain.LadoBindingForceOff
	global := func(state domain.LadoBindingState) ladopolicy.Source {
		return ladopolicy.Source{Scope: domain.LadoScopeGlobal, State: state}
	}
	project := func(state domain.LadoBindingState) ladopolicy.Source {
		return ladopolicy.Source{Scope: domain.LadoScopeProject, ScopeID: "demo", State: state}
	}
	check := func(t *testing.T, view ladopolicy.EffectiveView, wants map[domain.LadoSetupItemID]want) {
		t.Helper()
		if len(view.Setup.Items) != len(wants) {
			t.Fatalf("items %+v, want %d", view.Setup.Items, len(wants))
		}
		var snapshot []domain.LadoSetupItemID
		for _, item := range view.Setup.Items {
			w := wants[item.ItemID]
			got := want{item.On, item.Source, item.Global, item.Project, item.Task}
			if got != w {
				t.Errorf("%s = %+v, want %+v", item.Name, got, w)
			}
			if w.on {
				snapshot = append(snapshot, item.ItemID)
			}
		}
		var pinned []domain.LadoSetupItemID
		for _, item := range view.Content.Setup {
			pinned = append(pinned, item.ItemID)
			if w := wants[item.ItemID]; item.Source != (ladopolicy.SnapshotSource{Scope: w.source.Scope, ScopeID: w.source.ScopeID, State: w.source.State}) {
				t.Errorf("snapshot %s comes from %+v, want %+v", item.Name, item.Source, w.source)
			}
		}
		if !slices.Equal(pinned, snapshot) {
			t.Errorf("snapshot items %v, want the on ones %v", pinned, snapshot)
		}
	}

	t.Run("task", func(t *testing.T) {
		check(t, fx.effective(ladopolicy.EffectiveInput{TaskID: task.ID}), map[domain.LadoSetupItemID]want{
			byDefault.Item.ID:  {true, global(on), on, inherit, inherit},
			globalOff.Item.ID:  {false, global(off), off, inherit, inherit},
			projectOff.Item.ID: {false, project(forceOff), on, forceOff, inherit},
			taskOn.Item.ID:     {true, ladopolicy.Source{Scope: domain.LadoScopeTask, ScopeID: string(task.ID), State: forceOn}, off, forceOff, forceOn},
			projectOn.Item.ID:  {true, project(forceOn), off, forceOn, inherit},
		})
	})
	t.Run("project", func(t *testing.T) {
		check(t, fx.effective(ladopolicy.EffectiveInput{ProjectID: "demo"}), map[domain.LadoSetupItemID]want{
			byDefault.Item.ID:  {true, global(on), on, inherit, inherit},
			globalOff.Item.ID:  {false, global(off), off, inherit, inherit},
			projectOff.Item.ID: {false, project(forceOff), on, forceOff, inherit},
			taskOn.Item.ID:     {false, project(forceOff), off, forceOff, inherit},
			projectOn.Item.ID:  {true, project(forceOn), off, forceOn, inherit},
		})
	})
	t.Run("inherit removes a binding", func(t *testing.T) {
		fx.bind(taskOn, domain.LadoScopeTask, string(task.ID), inherit)
		fx.bind(globalOff, domain.LadoScopeGlobal, "", inherit)
		view := fx.effective(ladopolicy.EffectiveInput{TaskID: task.ID})
		for _, item := range view.Setup.Items {
			switch item.ItemID {
			case taskOn.Item.ID:
				if item.On || item.Source != project(forceOff) {
					t.Errorf("%s = %+v, want off from the project again", item.Name, item)
				}
			case globalOff.Item.ID:
				if !item.On || item.Source != global(on) || item.Global != on {
					t.Errorf("%s = %+v, want on without a Global binding", item.Name, item)
				}
			}
		}
	})
}

func TestSetupConflictsBecomeProblems(t *testing.T) {
	fx := newSetupFixture(t)
	claude := domain.LadoSetupToolClaudeCode
	ao := fx.item(claude, domain.LadoSetupKindMCPServer, "ao", `{"command":"ao-copy","type":"stdio"}`)
	permissions := fx.item(claude, domain.LadoSetupKindSetting, "permissions", `{"deny":["Bash(git push:*)","Bash(gh pr merge:*)"]}`)
	fx.accept(permissions, "LADO_SETUP_DENIES_GIT", "Bash(git push:*)", domain.LadoScopeProject, "demo")
	// An acceptance for another project does not hold here.
	fx.accept(permissions, "LADO_SETUP_DENIES_GIT", "Bash(gh pr merge:*)", domain.LadoScopeProject, "other")
	task := fx.task("demo", "claude-code")

	view := fx.effective(ladopolicy.EffectiveInput{TaskID: task.ID})
	if got := problemCodes(view.Setup.Problems); !slices.Equal(got, []string{"LADO_SETUP_MCP_NAME_TAKEN", "LADO_SETUP_DENIES_GIT"}) {
		t.Fatalf("setup problems %v (%+v), want the ao server and the warning no acceptance covers", got, view.Setup.Problems)
	}
	for _, p := range view.Setup.Problems {
		want := map[string]domain.LadoSetupItemID{"LADO_SETUP_MCP_NAME_TAKEN": ao.Item.ID, "LADO_SETUP_DENIES_GIT": permissions.Item.ID}[p.Code]
		if p.Details["item"] != string(want) || !slices.ContainsFunc(view.Problems, func(q domain.LadoPolicyProblem) bool {
			return q.Code == p.Code && q.Details["item"] == p.Details["item"] && q.Details["rule"] == p.Details["rule"]
		}) {
			t.Errorf("problem %+v: want it about %s and among the resolution's problems %+v", p, want, view.Problems)
		}
	}
	if !view.Blocking() || !slices.ContainsFunc(view.Content.Problems, func(p domain.LadoPolicyProblem) bool { return p.Code == "LADO_SETUP_MCP_NAME_TAKEN" }) {
		t.Fatalf("blocking %v, snapshot problems %+v; want the blocking conflict to block", view.Blocking(), view.Content.Problems)
	}
	if len(view.Setup.Accepted) != 1 || len(view.Content.AcceptedConflicts) != 1 {
		t.Fatalf("accepted %+v, snapshot %+v; want the one demo acceptance", view.Setup.Accepted, view.Content.AcceptedConflicts)
	}
	accepted := view.Content.AcceptedConflicts[0]
	if accepted.Code != "LADO_SETUP_DENIES_GIT" || accepted.ItemID != permissions.Item.ID || accepted.Detail != "Bash(git push:*)" || accepted.AcceptanceID == "" {
		t.Fatalf("accepted conflict %+v, want the git push warning with its acceptance", accepted)
	}
	if slices.ContainsFunc(view.Problems, func(p domain.LadoPolicyProblem) bool { return p.Details["rule"] == "Bash(git push:*)" }) {
		t.Fatalf("problems %+v hold the accepted warning", view.Problems)
	}

	// The approval's snapshot is blocking, so the task is Blocked.
	_, snapshot := fx.approved("demo", "claude-code")
	if !snapshot.Blocking {
		t.Fatalf("snapshot %+v, want it blocking", snapshot)
	}

	// A new version of the item needs an acceptance of its own.
	fx.edit(permissions, `{"deny":["Bash(git push:*)"]}`)
	view = fx.effective(ladopolicy.EffectiveInput{TaskID: task.ID})
	if len(view.Setup.Accepted) != 0 || !slices.Contains(problemCodes(view.Setup.Problems), "LADO_SETUP_DENIES_GIT") {
		t.Fatalf("after an edit: accepted %+v, problems %+v; want the warning back", view.Setup.Accepted, view.Setup.Problems)
	}
}

func TestEffectiveShowsSetupDiffSinceApproval(t *testing.T) {
	fx := newSetupFixture(t)
	ctx := context.Background()
	claude := domain.LadoSetupToolClaudeCode
	md := fx.item(claude, domain.LadoSetupKindInstructions, "CLAUDE.md", "Be brief.\n")
	model := fx.item(claude, domain.LadoSetupKindSetting, "model", `"opus"`)
	style := fx.item(claude, domain.LadoSetupKindOutputStyle, "terse.md", "Terse.\n")
	fx.bind(style, domain.LadoScopeGlobal, "", domain.LadoBindingOff)
	task, snapshot := fx.approved("demo", "claude-code")

	// The approval pins the on items' versions.
	if want := []domain.LadoSetupItemVersionID{md.Version.ID, model.Version.ID}; !slices.Equal(snapshot.SetupVersionIDs, want) {
		t.Fatalf("pinned %v, want %v", snapshot.SetupVersionIDs, want)
	}
	view := fx.effective(ladopolicy.EffectiveInput{TaskID: task.ID})
	if view.Approved == nil || !view.Approved.Matches || len(view.Approved.SetupAdded)+len(view.Approved.SetupRemoved)+len(view.Approved.SetupChanged) != 0 {
		t.Fatalf("approved %+v, want a match without setup changes", view.Approved)
	}

	md = fx.edit(md, "Be briefer.\n")
	if err := fx.store.DeleteLadoSetupItem(ctx, model.Item.ID, fx.now, fx.event("setup.item_deleted", string(model.Item.ID))); err != nil {
		t.Fatal(err)
	}
	agent := fx.item(claude, domain.LadoSetupKindAgent, "reviewer.md", "Review.\n")
	fx.bind(style, domain.LadoScopeTask, string(task.ID), domain.LadoBindingForceOn)

	view = fx.effective(ladopolicy.EffectiveInput{TaskID: task.ID})
	approved := view.Approved
	if approved == nil || approved.Matches {
		t.Fatalf("approved %+v, want it not to match now", approved)
	}
	wantAdded := []domain.LadoSetupItemID{agent.Item.ID, style.Item.ID}
	slices.Sort(wantAdded)
	if !slices.Equal(approved.SetupAdded, wantAdded) || !slices.Equal(approved.SetupRemoved, []domain.LadoSetupItemID{model.Item.ID}) ||
		!slices.Equal(approved.SetupChanged, []domain.LadoSetupItemID{md.Item.ID}) {
		t.Fatalf("added %v, removed %v, changed %v; want %v, [%s], [%s]", approved.SetupAdded, approved.SetupRemoved, approved.SetupChanged, wantAdded, model.Item.ID, md.Item.ID)
	}
	if len(approved.Added)+len(approved.Removed)+len(approved.Changed) != 0 {
		t.Fatalf("rule changes %v %v %v, want none", approved.Added, approved.Removed, approved.Changed)
	}
	if !slices.Equal(snapshotNames(approved.Content.Setup), []string{"claude-code/CLAUDE.md", "claude-code/model"}) {
		t.Fatalf("approved setup %v, want what the approval fixed", snapshotNames(approved.Content.Setup))
	}
	// The task needs review: the rules give another snapshot now.
	current, err := fx.policy.CurrentSHA256(ctx, task)
	if err != nil || current == snapshot.SHA256 {
		t.Fatalf("CurrentSHA256 = %s, %v; want another hash than the approval's %s", current, err, snapshot.SHA256)
	}
}

func TestSetupImpactListsApprovedTasks(t *testing.T) {
	fx := newSetupFixture(t)
	ctx := context.Background()
	md := fx.item(domain.LadoSetupToolClaudeCode, domain.LadoSetupKindInstructions, "CLAUDE.md", "Be brief.\n")
	agents := fx.item(domain.LadoSetupToolCodex, domain.LadoSetupKindInstructions, "AGENTS.md", "Use pnpm.\n")
	permissions := fx.item(domain.LadoSetupToolClaudeCode, domain.LadoSetupKindSetting, "permissions", `{"deny":["Bash(git push:*)"]}`)
	claudeDemo, _ := fx.approved("demo", "claude-code")
	codexDemo, _ := fx.approved("demo", "codex")
	claudeOther, _ := fx.approved("other", "claude-code")
	before, err := fx.store.ListLiveLadoSetupItems(ctx)
	if err != nil {
		t.Fatal(err)
	}

	taskIDs := func(impact ladopolicy.Impact) []domain.LadoTaskID {
		ids := []domain.LadoTaskID{}
		for _, task := range impact.Tasks {
			ids = append(ids, task.ID)
		}
		slices.Sort(ids)
		return ids
	}
	projectIDs := func(impact ladopolicy.Impact) []domain.ProjectID {
		ids := []domain.ProjectID{}
		for _, p := range impact.Projects {
			ids = append(ids, p.ID)
		}
		return ids
	}
	sorted := func(ids ...domain.LadoTaskID) []domain.LadoTaskID {
		slices.Sort(ids)
		return ids
	}
	next := func(iv domain.LadoSetupItemWithVersion, content string) *domain.LadoSetupItemWithVersion {
		v := iv.Version
		v.ID, v.Version, v.SHA256 = iv.Version.ID+"-next", iv.Version.Version+1, hexSHA256(content)
		return &domain.LadoSetupItemWithVersion{Item: iv.Item, Version: v}
	}
	for _, c := range []struct {
		name     string
		change   ladopolicy.SetupChange
		tasks    []domain.LadoTaskID
		projects []domain.ProjectID
	}{
		{
			// Demo's worker runs Claude Code; other has no worker agent.
			name:     "a new version of a Claude Code item reaches every approved Claude Code task",
			change:   ladopolicy.SetupChange{Item: next(md, "Be briefer.\n")},
			tasks:    sorted(claudeDemo.ID, claudeOther.ID),
			projects: []domain.ProjectID{"demo"},
		},
		{
			name:     "a Codex item reaches the Codex task",
			change:   ladopolicy.SetupChange{Remove: agents.Item.ID},
			tasks:    []domain.LadoTaskID{codexDemo.ID},
			projects: []domain.ProjectID{},
		},
		{
			name: "a project binding reaches that project's tasks of the tool",
			change: ladopolicy.SetupChange{Binding: &domain.LadoSetupBinding{
				ItemID: md.Item.ID, Scope: domain.LadoScopeProject, ScopeID: "demo", State: domain.LadoBindingForceOff,
			}},
			tasks:    []domain.LadoTaskID{claudeDemo.ID},
			projects: []domain.ProjectID{"demo"},
		},
		{
			name: "a new item reaches the tool's tasks",
			change: ladopolicy.SetupChange{
				Item: &domain.LadoSetupItemWithVersion{
					Item:    domain.LadoSetupItem{ID: "setup-new", Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindMCPServer, Name: "ao"},
					Version: domain.LadoSetupItemVersion{ID: "setup-new-v1", ItemID: "setup-new", Version: 1, ContentKind: domain.LadoSetupContentJSON, SHA256: hexSHA256(`{"command":"x"}`)},
				},
				Content: []byte(`{"command":"x"}`),
			},
			tasks:    sorted(claudeDemo.ID, claudeOther.ID),
			projects: []domain.ProjectID{"demo"},
		},
		{
			name: "accepting a warning reaches the tasks it covers",
			change: ladopolicy.SetupChange{Accept: &domain.LadoConflictAcceptance{
				ID: "lado-acceptance-new", Code: "LADO_SETUP_DENIES_GIT", ItemID: permissions.Item.ID, Detail: "Bash(git push:*)",
				ContentSHA256: permissions.Version.SHA256, Scope: domain.LadoScopeProject, ScopeID: "other",
			}},
			tasks:    []domain.LadoTaskID{claudeOther.ID},
			projects: []domain.ProjectID{},
		},
		{
			name: "turning an item on that is on changes nothing",
			change: ladopolicy.SetupChange{Binding: &domain.LadoSetupBinding{
				ItemID: md.Item.ID, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn,
			}},
			tasks:    []domain.LadoTaskID{},
			projects: []domain.ProjectID{},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			impact, err := fx.policy.ImpactOfSetupChange(ctx, c.change)
			if err != nil {
				t.Fatal(err)
			}
			if got := taskIDs(impact); !slices.Equal(got, c.tasks) {
				t.Errorf("tasks %v, want %v", got, c.tasks)
			}
			if got := projectIDs(impact); !slices.Equal(got, c.projects) {
				t.Errorf("projects %v, want %v", got, c.projects)
			}
		})
	}
	// A dry run changes nothing.
	after, err := fx.store.ListLiveLadoSetupItems(ctx)
	if err != nil || !slices.EqualFunc(after, before, func(a, b domain.LadoSetupItemWithVersion) bool {
		return a.Item.ID == b.Item.ID && a.Version.ID == b.Version.ID
	}) {
		t.Fatalf("items after the impacts %+v, %v; want %+v", after, err, before)
	}
	for _, task := range []domain.LadoTask{claudeDemo, codexDemo, claudeOther} {
		current, err := fx.policy.CurrentSHA256(ctx, task)
		if err != nil || current != task.Plan.Snapshot.SHA256 {
			t.Fatalf("task %s: CurrentSHA256 = %s, %v; want its approval's %s", task.ID, current, err, task.Plan.Snapshot.SHA256)
		}
	}
}
