package ladostage_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladostage"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var now = time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

// recordingStore is the real sqlite store, with the audit events it was
// given kept for the tests.
type recordingStore struct {
	*sqlite.Store
	events []domain.LadoAuditEvent
}

func (r *recordingStore) SetLadoStageAgents(ctx context.Context, projectID domain.ProjectID, agents domain.LadoStageAgents, audit domain.LadoAuditEvent) error {
	if err := r.Store.SetLadoStageAgents(ctx, projectID, agents, audit); err != nil {
		return err
	}
	r.events = append(r.events, audit)
	return nil
}

type fakeProjects map[string]domain.ProjectRecord

func (f fakeProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, ok := f[id]
	return p, ok, nil
}

type catalogKey struct {
	agent, project string
	refresh        bool
}

// fakeCatalog answers by agent and scope; a refreshed read and a cached one
// may differ. A missing entry fails the read.
type fakeCatalog struct {
	catalogs map[catalogKey]ports.AgentModelCatalog
	calls    []catalogKey
}

func (f *fakeCatalog) Models(_ context.Context, agentID, projectID string, refresh bool) (ports.AgentModelCatalog, error) {
	key := catalogKey{agentID, projectID, refresh}
	f.calls = append(f.calls, key)
	c, ok := f.catalogs[key]
	if !ok {
		return ports.AgentModelCatalog{}, errors.New("discovery failed")
	}
	return c, nil
}

// both sets the catalog for refreshed and cached reads.
func (f *fakeCatalog) both(agent, project string, c ports.AgentModelCatalog) {
	f.catalogs[catalogKey{agent, project, true}] = c
	f.catalogs[catalogKey{agent, project, false}] = c
}

var claudeCatalog = ports.AgentModelCatalog{AgentID: "claude-code", Models: []ports.AgentModelInfo{
	{ID: "fable", Label: "Fable"},
	{ID: "claude-fable-5-2", Label: "Fable 5.2", Efforts: []string{"low", "medium", "high", "max"}},
	{ID: "claude-opus-5-5", Label: "Opus 5.5", Efforts: []string{"low", "medium", "high"}},
	{ID: "claude-haiku-4-5", Label: "Haiku 4.5"},
}}

var codexCatalog = ports.AgentModelCatalog{AgentID: "codex", Models: []ports.AgentModelInfo{
	{ID: "gpt-5.1-codex", Label: "GPT-5.1 Codex", Efforts: []string{"low", "medium", "high"}},
}}

var (
	dbOnce sync.Once
	db     *sqlite.Store
	dbErr  error
	dbDir  string
)

// sharedDB is one migrated database for the package: migrating takes long
// under -race. Each fixture clears the scopes the tests use.
func sharedDB(t *testing.T) *sqlite.Store {
	t.Helper()
	dbOnce.Do(func() {
		dbDir, dbErr = os.MkdirTemp("", "ladostage-test-")
		if dbErr != nil {
			return
		}
		db, dbErr = sqlite.Open(dbDir)
	})
	if dbErr != nil {
		t.Fatal(dbErr)
	}
	return db
}

type fixture struct {
	svc     *ladostage.Service
	store   *recordingStore
	catalog *fakeCatalog
}

// newFixture has project "claude" (orchestrator and worker on Claude Code)
// and project "mixed" (orchestrator on Claude Code, worker on Codex).
func newFixture(t *testing.T) fixture {
	t.Helper()
	db := sharedDB(t)
	for _, scope := range []domain.ProjectID{"", "claude", "mixed"} {
		if err := db.SetLadoStageAgents(context.Background(), scope, nil, domain.LadoAuditEvent{
			At: now, Actor: "test", Action: "test.reset", SubjectKind: "stage_agents", SubjectID: string(scope), Payload: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	store := &recordingStore{Store: db}
	catalog := &fakeCatalog{catalogs: map[catalogKey]ports.AgentModelCatalog{}}
	for _, scope := range []string{"", "claude", "mixed"} {
		catalog.both("claude-code", scope, claudeCatalog)
		catalog.both("codex", scope, codexCatalog)
	}
	projects := fakeProjects{
		"claude": {ID: "claude", Config: domain.ProjectConfig{
			AgentConfig:  domain.AgentConfig{Model: "claude-opus-5-5", Effort: "medium"},
			Worker:       domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Effort: "high"}},
			Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Effort: "low"}},
		}},
		"mixed": {ID: "mixed", Config: domain.ProjectConfig{
			Worker:       domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "gpt-5.1-codex"}},
			Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Model: "claude-opus-5-5", Effort: "high"}},
		}},
		"gone": {ID: "gone", ArchivedAt: now},
	}
	svc := ladostage.New(ladostage.Deps{Store: store, Projects: projects, Catalog: catalog, Clock: func() time.Time { return now }})
	return fixture{svc: svc, store: store, catalog: catalog}
}

func code(t *testing.T, err error) string {
	t.Helper()
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("error %v is not an apierr.Error", err)
	}
	return ae.Code
}

func (f fixture) stored(t *testing.T, scope domain.ProjectID) domain.LadoStageAgents {
	t.Helper()
	got, err := f.store.LadoStageAgents(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Every refused value keeps nothing, in either scope, and reports its code.
func TestSetRefusals(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	kept := domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "claude-fable-5-2", Effort: "high"}}
	if _, err := f.svc.SetGlobal(ctx, kept); err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}
	keptProject := domain.LadoStageAgents{domain.LadoStageExecution: {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex"}}
	if _, err := f.svc.SetProject(ctx, "mixed", keptProject); err != nil {
		t.Fatalf("SetProject: %v", err)
	}
	events := len(f.store.events)

	stale := claudeCatalog
	stale.Stale = true
	cases := []struct {
		name    string
		project domain.ProjectID // "" sets the global defaults
		agents  domain.LadoStageAgents
		prepare func(*fakeCatalog)
		want    string
	}{
		{name: "effort without model", agents: domain.LadoStageAgents{domain.LadoStageCritic: {Harness: domain.HarnessClaudeCode, Effort: "high"}},
			want: ladostage.CodeEffortWithoutModel},
		{name: "unknown stage", agents: domain.LadoStageAgents{"review": {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5"}},
			want: ladostage.CodeInvalid},
		{name: "unsupported harness", agents: domain.LadoStageAgents{domain.LadoStagePlan: {Harness: "aider", Model: "x"}},
			want: ladostage.CodeHarnessUnsupported},
		{name: "claude model not in catalog", agents: domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "claude-opus-9"}},
			want: ladostage.CodeModelUnsupported},
		{name: "claude with a stale catalog", agents: domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5"}},
			prepare: func(c *fakeCatalog) { c.catalogs[catalogKey{"claude-code", "", true}] = stale },
			want:    ladostage.CodeModelUnverified},
		{name: "claude with an unreadable catalog", agents: domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5"}},
			prepare: func(c *fakeCatalog) { delete(c.catalogs, catalogKey{"claude-code", "", true}) },
			want:    ladostage.CodeModelUnverified},
		{name: "effort the model does not offer", agents: domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5", Effort: "max"}},
			want: ladostage.CodeEffortUnsupported},
		{name: "effort for a model with none", agents: domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "claude-haiku-4-5", Effort: "low"}},
			want: ladostage.CodeEffortUnsupported},
		{name: "codex direct model with an effort", agents: domain.LadoStageAgents{domain.LadoStageExecution: {Harness: domain.HarnessCodex, Model: "my-codex-fine-tune", Effort: "high"}},
			want: ladostage.CodeModelUnsupported},
		{name: "codex effort with an unreadable catalog", agents: domain.LadoStageAgents{domain.LadoStageExecution: {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex", Effort: "high"}},
			prepare: func(c *fakeCatalog) { delete(c.catalogs, catalogKey{"codex", "", true}) },
			want:    ladostage.CodeModelUnverified},
		{name: "project value of another harness", project: "mixed",
			agents: domain.LadoStageAgents{domain.LadoStageIntegration: {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5"}},
			want:   ladostage.CodeHarnessMismatch},
		{name: "project plan value of the worker's harness", project: "mixed",
			agents: domain.LadoStageAgents{domain.LadoStageCritic: {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex"}},
			want:   ladostage.CodeHarnessMismatch},
		{name: "project model checked in the project scope", project: "claude",
			agents:  domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "claude-fable-5-2"}},
			prepare: func(c *fakeCatalog) { c.catalogs[catalogKey{"claude-code", "claude", true}] = codexCatalog },
			want:    ladostage.CodeModelUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.catalog.catalogs = map[catalogKey]ports.AgentModelCatalog{}
			for _, scope := range []string{"", "claude", "mixed"} {
				f.catalog.both("claude-code", scope, claudeCatalog)
				f.catalog.both("codex", scope, codexCatalog)
			}
			if tc.prepare != nil {
				tc.prepare(f.catalog)
			}
			var err error
			if tc.project == "" {
				_, err = f.svc.SetGlobal(ctx, tc.agents)
			} else {
				_, err = f.svc.SetProject(ctx, tc.project, tc.agents)
			}
			if err == nil {
				t.Fatalf("set %v: no error, want %s", tc.agents, tc.want)
			}
			if got := code(t, err); got != tc.want {
				t.Fatalf("set %v: code %s (%v), want %s", tc.agents, got, err, tc.want)
			}
			if got := f.stored(t, ""); !maps.Equal(got, kept) {
				t.Fatalf("global after a refusal = %v, want %v", got, kept)
			}
			if got := f.stored(t, "mixed"); !maps.Equal(got, keptProject) {
				t.Fatalf("project after a refusal = %v, want %v", got, keptProject)
			}
			if got := f.stored(t, "claude"); len(got) != 0 {
				t.Fatalf("project claude after a refusal = %v, want none", got)
			}
			if len(f.store.events) != events {
				t.Fatalf("a refusal audited %d events", len(f.store.events)-events)
			}
		})
	}

	if _, err := f.svc.SetProject(ctx, "gone", nil); code(t, err) != "PROJECT_NOT_FOUND" {
		t.Fatalf("archived project: %v, want PROJECT_NOT_FOUND", err)
	}
	if _, err := f.svc.Project(ctx, "nobody"); code(t, err) != "PROJECT_NOT_FOUND" {
		t.Fatalf("unknown project: %v, want PROJECT_NOT_FOUND", err)
	}
}

// Accepted values are kept as a whole scope, audited with from and to, read
// with the spawn's refresh, and a resend of the same values writes nothing.
func TestSetKeepsAndAudits(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	// A Codex model without an effort needs no catalog, even one the
	// catalog does not list; a Claude alias takes its family's efforts.
	global := domain.LadoStageAgents{
		domain.LadoStagePlan:      {Harness: domain.HarnessClaudeCode, Model: "fable", Effort: "max"},
		domain.LadoStageExecution: {Harness: domain.HarnessCodex, Model: "my-codex-fine-tune"},
	}
	f.catalog.calls = nil
	got, err := f.svc.SetGlobal(ctx, global)
	if err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}
	if !maps.Equal(got, global) || !maps.Equal(f.stored(t, ""), global) {
		t.Fatalf("SetGlobal kept %v (returned %v), want %v", f.stored(t, ""), got, global)
	}
	if want := []catalogKey{{"claude-code", "", true}}; len(f.catalog.calls) != 1 || f.catalog.calls[0] != want[0] {
		t.Fatalf("catalog reads = %v, want %v", f.catalog.calls, want)
	}
	if len(f.store.events) != 1 {
		t.Fatalf("events = %d, want 1", len(f.store.events))
	}
	ev := f.store.events[0]
	if ev.Action != ladostage.AuditAction || ev.SubjectKind != "stage_agents" || ev.SubjectID != "global" || !ev.At.Equal(now) {
		t.Fatalf("event = %+v", ev)
	}
	var payload struct {
		Scope string                 `json:"scope"`
		From  domain.LadoStageAgents `json:"from"`
		To    domain.LadoStageAgents `json:"to"`
	}
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Scope != "global" || len(payload.From) != 0 || !maps.Equal(payload.To, global) {
		t.Fatalf("payload = %s", ev.Payload)
	}

	if _, err := f.svc.SetGlobal(ctx, global); err != nil {
		t.Fatalf("resend: %v", err)
	}
	if len(f.store.events) != 1 {
		t.Fatalf("a resend audited an event")
	}

	// A project scope replaces its rows as a whole; a missing stage
	// follows the default again.
	first := domain.LadoStageAgents{
		domain.LadoStageExecution: {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex", Effort: "high"},
		domain.LadoStageResolve:   {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex"},
	}
	if _, err := f.svc.SetProject(ctx, "mixed", first); err != nil {
		t.Fatalf("SetProject: %v", err)
	}
	second := domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5"}}
	if _, err := f.svc.SetProject(ctx, "mixed", second); err != nil {
		t.Fatalf("SetProject again: %v", err)
	}
	if got := f.stored(t, "mixed"); !maps.Equal(got, second) {
		t.Fatalf("project = %v, want %v", got, second)
	}
	last := f.store.events[len(f.store.events)-1]
	payload.From, payload.To = nil, nil
	if err := json.Unmarshal(last.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if last.SubjectID != "mixed" || payload.Scope != "mixed" || !maps.Equal(payload.From, first) || !maps.Equal(payload.To, second) {
		t.Fatalf("project event = %+v %s", last, last.Payload)
	}
	if !maps.Equal(f.stored(t, ""), global) {
		t.Fatalf("a project save changed the global scope")
	}

	if _, err := f.svc.SetProject(ctx, "mixed", nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := f.stored(t, "mixed"); len(got) != 0 {
		t.Fatalf("cleared project = %v", got)
	}
}

// A save checks only the values it adds or changes. A kept value that no
// longer names its stage's harness (the project moved its orchestrator or
// worker to another harness), or whose model the catalog dropped, does not
// lock the scope: the developer can still change the other stages or clear
// it, and only a new or changed value is refused.
func TestSetChecksOnlyChangedValues(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	keep := func(scope domain.ProjectID, agents domain.LadoStageAgents) {
		t.Helper()
		if err := f.store.Store.SetLadoStageAgents(ctx, scope, agents, domain.LadoAuditEvent{
			At: now, Actor: "test", Action: "test.seed", SubjectKind: "stage_agents", SubjectID: string(scope), Payload: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Project "claude" runs every stage on Claude Code; two kept values name
	// Codex, as after its agents moved from Codex.
	stale := domain.LadoStageAgents{
		domain.LadoStagePlan:      {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex"},
		domain.LadoStageExecution: {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex"},
	}
	keep("claude", stale)
	withCritic := maps.Clone(stale)
	withCritic[domain.LadoStageCritic] = domain.LadoStageAgent{Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5"}
	if _, err := f.svc.SetProject(ctx, "claude", withCritic); err != nil {
		t.Fatalf("adding a stage beside kept mismatched values: %v", err)
	}
	if got := f.stored(t, "claude"); !maps.Equal(got, withCritic) {
		t.Fatalf("project = %v, want %v", got, withCritic)
	}
	onlyPlan := domain.LadoStageAgents{domain.LadoStagePlan: stale[domain.LadoStagePlan]}
	if _, err := f.svc.SetProject(ctx, "claude", onlyPlan); err != nil {
		t.Fatalf("clearing stages beside a kept mismatched value: %v", err)
	}
	changedPlan := domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex-mini"}}
	if _, err := f.svc.SetProject(ctx, "claude", changedPlan); code(t, err) != ladostage.CodeHarnessMismatch {
		t.Fatalf("changing a mismatched value: err = %v, want %s", err, ladostage.CodeHarnessMismatch)
	}
	if got := f.stored(t, "claude"); !maps.Equal(got, onlyPlan) {
		t.Fatalf("a refused save changed the project: %v, want %v", got, onlyPlan)
	}
	if _, err := f.svc.SetProject(ctx, "claude", nil); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	if got := f.stored(t, "claude"); len(got) != 0 {
		t.Fatalf("cleared project = %v", got)
	}

	// A kept global value whose model the catalog no longer lists is not
	// read again; a new unlisted one is refused.
	gone := domain.LadoStageAgents{domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "claude-gone", Effort: "high"}}
	keep(domain.LadoStageGlobalScope, gone)
	withExecution := maps.Clone(gone)
	withExecution[domain.LadoStageExecution] = domain.LadoStageAgent{Harness: domain.HarnessCodex, Model: "gpt-5.1-codex"}
	f.catalog.calls = nil
	if _, err := f.svc.SetGlobal(ctx, withExecution); err != nil {
		t.Fatalf("adding a stage beside a kept value the catalog dropped: %v", err)
	}
	if len(f.catalog.calls) != 0 {
		t.Fatalf("catalog reads = %v, want none", f.catalog.calls)
	}
	newGone := domain.LadoStageAgents{domain.LadoStageCritic: {Harness: domain.HarnessClaudeCode, Model: "claude-gone"}}
	if _, err := f.svc.SetGlobal(ctx, newGone); code(t, err) != ladostage.CodeModelUnsupported {
		t.Fatalf("a new unlisted model: err = %v, want %s", err, ladostage.CodeModelUnsupported)
	}
	if got := f.stored(t, domain.LadoStageGlobalScope); !maps.Equal(got, withExecution) {
		t.Fatalf("a refused save changed the global scope: %v", got)
	}
}

// The project view shows both scopes, which applies, the warnings, and the
// final value with its source.
func TestProjectView(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	view, err := f.svc.Project(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	// Without settings every stage runs what it ran before: plan workers the
	// orchestrator's merged value, executions the worker's model with the
	// effort AO gives the worker.
	wantEmpty := map[domain.LadoStage]domain.LadoStageResolved{
		domain.LadoStagePlan:        {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5", Effort: "low", Source: domain.LadoStageFromOrchestrator},
		domain.LadoStageCritic:      {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5", Effort: "low", Source: domain.LadoStageFromPlanStage},
		domain.LadoStageExecution:   {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5", Effort: "high", Source: domain.LadoStageFromWorker},
		domain.LadoStageIntegration: {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5", Effort: "high", Source: domain.LadoStageFromExecutionStage},
		domain.LadoStageResolve:     {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5", Effort: "high", Source: domain.LadoStageFromExecutionStage},
	}
	if len(view.Stages) != len(domain.LadoStages) {
		t.Fatalf("stages = %d", len(view.Stages))
	}
	for i, sv := range view.Stages {
		if sv.Stage != domain.LadoStages[i] || sv.Effective != wantEmpty[sv.Stage] || sv.Project != nil || sv.Global != nil {
			t.Fatalf("empty view stage %d = %+v, want %+v", i, sv, wantEmpty[domain.LadoStages[i]])
		}
	}

	// The global plan value is a model the project's cached catalog lacks;
	// the global execution value is Codex, not this project's harness.
	global := domain.LadoStageAgents{
		domain.LadoStagePlan:      {Harness: domain.HarnessClaudeCode, Model: "claude-fable-5-2"},
		domain.LadoStageCritic:    {Harness: domain.HarnessClaudeCode, Model: "claude-haiku-4-5"},
		domain.LadoStageExecution: {Harness: domain.HarnessCodex, Model: "gpt-5.1-codex", Effort: "high"},
	}
	if _, err := f.svc.SetGlobal(ctx, global); err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}
	if _, err := f.svc.SetProject(ctx, "claude", domain.LadoStageAgents{
		domain.LadoStageCritic: {Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5", Effort: "medium"},
	}); err != nil {
		t.Fatalf("SetProject: %v", err)
	}
	projectCatalog := ports.AgentModelCatalog{AgentID: "claude-code", Models: []ports.AgentModelInfo{
		{ID: "claude-opus-5-5", Label: "Opus 5.5", Efforts: []string{"low", "medium", "high"}},
		{ID: "claude-haiku-4-5", Label: "Haiku 4.5"},
	}}
	f.catalog.catalogs[catalogKey{"claude-code", "claude", false}] = projectCatalog
	delete(f.catalog.catalogs, catalogKey{"claude-code", "claude", true})
	f.catalog.calls = nil

	view, err = f.svc.Project(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range f.catalog.calls {
		if call.refresh || call.project != "claude" {
			t.Fatalf("the view read catalog %+v, want the project's cached catalog", call)
		}
	}
	plan, critic, execution, integration := view.Stages[0], view.Stages[1], view.Stages[2], view.Stages[3]
	if !plan.GlobalApplies || plan.GlobalWarning != ladostage.ReasonModelUnavailable || plan.ProjectApplies {
		t.Fatalf("plan = %+v, want an applying global value warned model_unavailable", plan)
	}
	// The warning only warns: the value still resolves, with no effort.
	if want := (domain.LadoStageResolved{Harness: domain.HarnessClaudeCode, Model: "claude-fable-5-2", Source: domain.LadoStageFromGlobal}); plan.Effective != want {
		t.Fatalf("plan effective = %+v, want %+v", plan.Effective, want)
	}
	if !critic.ProjectApplies || critic.GlobalApplies || critic.GlobalWarning != "" || critic.Global == nil {
		t.Fatalf("critic = %+v, want the project value over the global one", critic)
	}
	if want := (domain.LadoStageResolved{Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5", Effort: "medium", Source: domain.LadoStageFromProject}); critic.Effective != want {
		t.Fatalf("critic effective = %+v, want %+v", critic.Effective, want)
	}
	if execution.GlobalApplies || execution.GlobalWarning != ladostage.ReasonHarnessMismatch || execution.Effective != wantEmpty[domain.LadoStageExecution] {
		t.Fatalf("execution = %+v, want the Codex global value skipped", execution)
	}
	if integration.Effective != wantEmpty[domain.LadoStageIntegration] {
		t.Fatalf("integration = %+v", integration)
	}

	// On the mixed project the execution stages run Codex, so the global
	// Codex value applies there; the model is in its catalog.
	view, err = f.svc.Project(ctx, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	execution, integration = view.Stages[2], view.Stages[3]
	if execution.Harness != domain.HarnessCodex || !execution.GlobalApplies || execution.GlobalWarning != "" {
		t.Fatalf("mixed execution = %+v", execution)
	}
	wantExec := domain.LadoStageResolved{Harness: domain.HarnessCodex, Model: "gpt-5.1-codex", Effort: "high", Source: domain.LadoStageFromGlobal}
	if execution.Effective != wantExec {
		t.Fatalf("mixed execution effective = %+v, want %+v", execution.Effective, wantExec)
	}
	wantExec.Source = domain.LadoStageFromExecutionStage
	if integration.Effective != wantExec {
		t.Fatalf("mixed integration effective = %+v, want %+v", integration.Effective, wantExec)
	}
	// The mixed project's plan stages run Claude Code from the
	// orchestrator: the plan's global value applies there too.
	if plan := view.Stages[0]; plan.Harness != domain.HarnessClaudeCode || !plan.GlobalApplies {
		t.Fatalf("mixed plan = %+v", plan)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	if db != nil {
		_ = db.Close()
	}
	if dbDir != "" {
		_ = os.RemoveAll(dbDir)
	}
	os.Exit(code)
}
