package controllers_test

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladostage"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

const ladoStageAgentsPath = "/api/v1/lado/stage-agents"

// memoryStageStore keeps the stage settings in memory, with the audit events
// it was given.
type memoryStageStore struct {
	scopes map[domain.ProjectID]domain.LadoStageAgents
	events []domain.LadoAuditEvent
}

func (m *memoryStageStore) LadoStageAgents(_ context.Context, projectID domain.ProjectID) (domain.LadoStageAgents, error) {
	return maps.Clone(m.scopes[projectID]), nil
}

func (m *memoryStageStore) SetLadoStageAgents(_ context.Context, projectID domain.ProjectID, agents domain.LadoStageAgents, audit domain.LadoAuditEvent) error {
	m.scopes[projectID] = maps.Clone(agents)
	m.events = append(m.events, audit)
	return nil
}

type stageProjects map[string]domain.ProjectRecord

func (p stageProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	record, ok := p[id]
	return record, ok, nil
}

// stageCatalog answers by agent and project scope; a missing entry fails the
// read.
type stageCatalog map[[2]string]ports.AgentModelCatalog

func (c stageCatalog) Models(_ context.Context, agentID, projectID string, _ bool) (ports.AgentModelCatalog, error) {
	catalog, ok := c[[2]string{agentID, projectID}]
	if !ok {
		return ports.AgentModelCatalog{}, errors.New("discovery failed")
	}
	return catalog, nil
}

var stageClaudeModels = ports.AgentModelCatalog{AgentID: "claude-code", Models: []ports.AgentModelInfo{
	{ID: "claude-fable-5-2", Efforts: []string{"low", "medium", "high", "max"}},
	{ID: "claude-opus-5-5", Efforts: []string{"low", "medium", "high"}},
}}

func newLadoStageServer(t *testing.T) (*memoryStageStore, *httptest.Server) {
	t.Helper()
	store := &memoryStageStore{scopes: map[domain.ProjectID]domain.LadoStageAgents{}}
	catalog := stageCatalog{
		{"claude-code", ""}: stageClaudeModels,
		// The project's catalog lacks Fable, so a global Fable value warns.
		{"claude-code", "mer"}: {AgentID: "claude-code", Models: []ports.AgentModelInfo{{ID: "claude-opus-5-5", Efforts: []string{"low", "medium", "high"}}}},
	}
	projects := stageProjects{"mer": {ID: "mer", Config: domain.ProjectConfig{
		AgentConfig:  domain.AgentConfig{Model: "claude-opus-5-5"},
		Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Effort: "high"}},
		Worker:       domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}}}
	svc := ladostage.New(ladostage.Deps{Store: store, Projects: projects, Catalog: catalog, Clock: func() time.Time { return ladoRuleTime }})
	return store, newLadoTestServer(t, httpd.APIDeps{LadoStageAgents: svc})
}

func TestLadoAPI_StageAgentsGlobal(t *testing.T) {
	store, srv := newLadoStageServer(t)

	body, status, _ := doRequest(t, srv, "GET", ladoStageAgentsPath, "")
	if status != http.StatusOK || decodeLadoJSON[controllers.LadoStageAgentsResponse](t, body).Stages != (controllers.LadoStageAgentsByStage{}) {
		t.Fatalf("GET = %d %s, want no stage set", status, body)
	}

	body, status, _ = doRequest(t, srv, "PUT", ladoStageAgentsPath,
		`{"stages":{"plan":{"harness":"claude-code","model":"claude-fable-5-2","effort":"max"},"execution":{"harness":"codex","model":"my-codex-model","effort":""}}}`)
	if status != http.StatusOK {
		t.Fatalf("PUT = %d %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoStageAgentsResponse](t, body).Stages
	if got.Plan == nil || *got.Plan != (controllers.LadoStageAgentResponse{Harness: "claude-code", Model: "claude-fable-5-2", Effort: "max"}) ||
		got.Execution == nil || *got.Execution != (controllers.LadoStageAgentResponse{Harness: "codex", Model: "my-codex-model"}) ||
		got.Critic != nil || got.Integration != nil || got.Resolve != nil {
		t.Fatalf("kept = %+v", got)
	}
	if !strings.Contains(string(body), `"effort":""`) || strings.Contains(string(body), `"critic"`) {
		t.Fatalf("body = %s, want the set stages only, each with its effort", body)
	}
	if len(store.events) != 1 || store.events[0].Action != ladostage.AuditAction {
		t.Fatalf("audit = %+v, want one %s", store.events, ladostage.AuditAction)
	}
	body, _, _ = doRequest(t, srv, "GET", ladoStageAgentsPath, "")
	if again := decodeLadoJSON[controllers.LadoStageAgentsResponse](t, body).Stages; again.Plan == nil || again.Execution == nil {
		t.Fatalf("GET after PUT = %s", body)
	}

	// A refused value keeps nothing, under the code the app translates.
	for _, c := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"no stages", `{}`, http.StatusBadRequest, ladostage.CodeInvalid},
		{"unknown stage", `{"stages":{"deploy":{"harness":"claude-code","model":"claude-opus-5-5"}}}`, http.StatusBadRequest, "INVALID_JSON"},
		{"blank model", `{"stages":{"plan":{"harness":"claude-code","model":"","effort":""}}}`, http.StatusBadRequest, ladostage.CodeInvalid},
		{"effort without model", `{"stages":{"plan":{"harness":"claude-code","model":"","effort":"high"}}}`, http.StatusBadRequest, ladostage.CodeEffortWithoutModel},
		{"other harness", `{"stages":{"plan":{"harness":"aider","model":"sonnet"}}}`, http.StatusBadRequest, ladostage.CodeHarnessUnsupported},
		{"model off the catalog", `{"stages":{"plan":{"harness":"claude-code","model":"claude-nope"}}}`, http.StatusBadRequest, ladostage.CodeModelUnsupported},
		{"effort the model lacks", `{"stages":{"plan":{"harness":"claude-code","model":"claude-opus-5-5","effort":"max"}}}`, http.StatusBadRequest, ladostage.CodeEffortUnsupported},
		{"codex effort without a catalog", `{"stages":{"execution":{"harness":"codex","model":"my-codex-model","effort":"high"}}}`, http.StatusServiceUnavailable, ladostage.CodeModelUnverified},
	} {
		body, status, _ := doRequest(t, srv, "PUT", ladoStageAgentsPath, c.body)
		if status != c.status || ladoErrorCode(t, body) != c.code {
			t.Errorf("%s: PUT = %d %s, want %d %s", c.name, status, body, c.status, c.code)
		}
	}
	if len(store.events) != 1 || len(store.scopes[""]) != 2 {
		t.Fatalf("refused PUTs changed the store: %+v, %d events", store.scopes[""], len(store.events))
	}

	// {} clears the scope.
	body, status, _ = doRequest(t, srv, "PUT", ladoStageAgentsPath, `{"stages":{}}`)
	if status != http.StatusOK || decodeLadoJSON[controllers.LadoStageAgentsResponse](t, body).Stages != (controllers.LadoStageAgentsByStage{}) || len(store.scopes[""]) != 0 {
		t.Fatalf("clear = %d %s, scope %+v", status, body, store.scopes[""])
	}
}

func TestLadoAPI_StageAgentsProject(t *testing.T) {
	store, server := newLadoStageServer(t)
	path := "/api/v1/lado/projects/mer/stage-agents"

	body, status, _ := doRequest(t, server, "GET", "/api/v1/lado/projects/nope/stage-agents", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "PROJECT_NOT_FOUND")

	// The global plan value is Fable, which the project's catalog lacks.
	store.scopes[""] = domain.LadoStageAgents{
		domain.LadoStagePlan:      {Harness: domain.HarnessClaudeCode, Model: "claude-fable-5-2"},
		domain.LadoStageExecution: {Harness: domain.HarnessCodex, Model: "my-codex-model"},
	}

	body, status, _ = doRequest(t, server, "PUT", path, `{"stages":{"execution":{"harness":"codex","model":"my-codex-model"}}}`)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, ladostage.CodeHarnessMismatch)
	if len(store.scopes["mer"]) != 0 {
		t.Fatalf("a refused PUT kept %+v", store.scopes["mer"])
	}

	body, status, _ = doRequest(t, server, "PUT", path, `{"stages":{"critic":{"harness":"claude-code","model":"claude-opus-5-5","effort":"low"}}}`)
	if status != http.StatusOK {
		t.Fatalf("PUT = %d %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoProjectStageAgentsResponse](t, body)
	if got.ProjectID != "mer" || len(got.Stages) != 5 {
		t.Fatalf("view = %+v", got)
	}
	order := make([]string, 0, len(got.Stages))
	byStage := map[string]controllers.LadoProjectStageAgentResponse{}
	for _, s := range got.Stages {
		order = append(order, s.Stage)
		byStage[s.Stage] = s
	}
	if strings.Join(order, ",") != "plan,critic,execution,integration,resolve" {
		t.Fatalf("stage order = %v", order)
	}
	plan := byStage["plan"]
	if plan.Harness != "claude-code" || plan.Project != nil || plan.Global == nil || !plan.GlobalApplies || plan.GlobalWarning != "model_unavailable" ||
		plan.Effective != (controllers.LadoStageResolvedResponse{Harness: "claude-code", Model: "claude-fable-5-2", Source: "global"}) {
		t.Errorf("plan = %+v, want the global Fable value with no effort and a model_unavailable warning", plan)
	}
	critic := byStage["critic"]
	if critic.Project == nil || !critic.ProjectApplies || critic.GlobalApplies ||
		critic.Effective != (controllers.LadoStageResolvedResponse{Harness: "claude-code", Model: "claude-opus-5-5", Effort: "low", Source: "project"}) {
		t.Errorf("critic = %+v, want the project's value", critic)
	}
	execution := byStage["execution"]
	if execution.GlobalApplies || execution.GlobalWarning != "harness_mismatch" ||
		execution.Effective != (controllers.LadoStageResolvedResponse{Harness: "claude-code", Model: "claude-opus-5-5", Source: "worker"}) {
		t.Errorf("execution = %+v, want the worker's model past a Codex global value", execution)
	}
	if resolve := byStage["resolve"]; resolve.Effective.Source != "execution_stage" || resolve.Effective.Model != "claude-opus-5-5" {
		t.Errorf("resolve = %+v, want the execution's value", resolve)
	}
	if strings.Contains(string(body), `"projectReason"`) {
		t.Errorf("body = %s, want no projectReason while every project value applies", body)
	}

	body, status, _ = doRequest(t, server, "GET", path, "")
	if status != http.StatusOK || len(decodeLadoJSON[controllers.LadoProjectStageAgentsResponse](t, body).Stages) != 5 {
		t.Fatalf("GET = %d %s", status, body)
	}
}

func TestLadoAPI_StageAgentsWithoutAServiceAnswerNotImplemented(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, c := range []struct{ method, path, body string }{
		{"GET", ladoStageAgentsPath, ""},
		{"PUT", ladoStageAgentsPath, `{"stages":{}}`},
		{"GET", "/api/v1/lado/projects/mer/stage-agents", ""},
		{"PUT", "/api/v1/lado/projects/mer/stage-agents", `{"stages":{}}`},
	} {
		if _, status, _ := doRequest(t, srv, c.method, c.path, c.body); status != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501", c.method, c.path, status)
		}
	}
}

// TestLadoAPI_EffectiveShowsStageModels: the effective view names the
// execution's effort and source and the integration and resolve workers'
// values, and the approval comparison says the agent changed.
func TestLadoAPI_EffectiveShowsStageModels(t *testing.T) {
	view := ladopolicy.EffectiveView{
		Resolution: ladopolicy.Resolution{Ceiling: domain.PermissionModeAuto, Mode: domain.PermissionModeAuto,
			Content: ladopolicy.SnapshotContent{ProjectID: "mer", TaskID: "lado-task-1"}},
		Agent: ladopolicy.AgentChoice{Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5", Effort: "high", Source: ladopolicy.AgentFromProject,
			Stages: ladopolicy.AgentStages{
				Integration: domain.LadoStageResolved{Harness: domain.HarnessClaudeCode, Model: "claude-fable-5-2", Source: domain.LadoStageFromGlobal},
				Resolve:     domain.LadoStageResolved{Harness: domain.HarnessClaudeCode, Model: "claude-opus-5-5", Effort: "high", Source: domain.LadoStageFromExecutionStage},
			}},
		Approved: &ladopolicy.ApprovedView{ApprovedAt: ladoRuleTime, AgentChanged: true},
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: &fakeLadoRuleService{effective: view}})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/effective?taskId=lado-task-1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	resp := decodeLadoJSON[controllers.LadoEffectiveResponse](t, body)
	want := controllers.LadoEffectiveAgentResponse{Harness: "claude-code", Model: "claude-opus-5-5", Effort: "high", Source: "project",
		Stages: controllers.LadoEffectiveStagesResponse{
			Integration: controllers.LadoStageResolvedResponse{Harness: "claude-code", Model: "claude-fable-5-2", Source: "global"},
			Resolve:     controllers.LadoStageResolvedResponse{Harness: "claude-code", Model: "claude-opus-5-5", Effort: "high", Source: "execution_stage"},
		}}
	if resp.Agent != want {
		t.Fatalf("agent = %+v, want %+v", resp.Agent, want)
	}
	if resp.Approved == nil || !resp.Approved.AgentChanged || resp.Approved.Matches {
		t.Fatalf("approved = %+v, want agentChanged", resp.Approved)
	}
}

// TestLadoAPI_ExecutionShowsTheSessionModel: the execution read names the
// model and effort the worker session resolved to, and the approval's
// models by role; a snapshot without stage models gives the agent's.
func TestLadoAPI_ExecutionShowsTheSessionModel(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	seed := seedLadoQueued(t, store, "mer", 1, now.Add(-time.Hour))
	worker := runLado(t, store, "mer", seed, now, domain.LadoExecution{})
	rec, _, err := store.GetSession(ctx, worker)
	if err != nil {
		t.Fatalf("read the worker session: %v", err)
	}
	rec.Metadata.Model, rec.Metadata.Effort = "claude-sonnet-5", "medium"
	if err := store.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("update the worker session: %v", err)
	}

	srv := newLadoTestServer(t, httpd.APIDeps{LadoQueue: ladoqueue.New(ladoqueue.Deps{Store: store})})
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/executions/"+string(worker), "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoExecutionResponse](t, body)
	if got.SessionModel != "" || got.SessionEffort != "" {
		t.Fatalf("without a session reader the session model is %q/%q, want none", got.SessionModel, got.SessionEffort)
	}
	wantStages := controllers.LadoExecutionSnapshotStagesResponse{
		Integration: controllers.LadoSnapshotStageAgentResponse{Model: "claude-sonnet-5"},
		Resolve:     controllers.LadoSnapshotStageAgentResponse{Model: "claude-sonnet-5"},
	}
	if got.Snapshot.Effort != "" || got.Snapshot.Stages != wantStages {
		t.Fatalf("snapshot effort %q, stages %+v; want the agent's model for every role", got.Snapshot.Effort, got.Snapshot.Stages)
	}

	sessions := &fakeSessionService{sessions: map[domain.SessionID]domain.Session{worker: {SessionRecord: rec}}}
	lado := newLadoTestServer(t, httpd.APIDeps{LadoQueue: ladoqueue.New(ladoqueue.Deps{Store: store}), Sessions: sessions})
	body, status, _ = doRequest(t, lado, "GET", "/api/v1/lado/executions/"+string(worker), "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if got := decodeLadoJSON[controllers.LadoExecutionResponse](t, body); got.SessionModel != "claude-sonnet-5" || got.SessionEffort != "medium" {
		t.Fatalf("session model = %q/%q, want claude-sonnet-5/medium", got.SessionModel, got.SessionEffort)
	}

	// The task list, which the board's worker card reads, names them too.
	tasks := newLadoTestServer(t, httpd.APIDeps{LadoTasks: ladotask.New(ladotask.Deps{Store: store, Projects: store}), Sessions: sessions})
	body, status, _ = doRequest(t, tasks, "GET", "/api/v1/lado/tasks?projectId=mer", "")
	if status != http.StatusOK {
		t.Fatalf("tasks: status = %d, body %s", status, body)
	}
	list := decodeLadoJSON[controllers.LadoTaskListResponse](t, body).Tasks
	if len(list) != 1 || list[0].Execution == nil || list[0].Execution.SessionModel != "claude-sonnet-5" || list[0].Execution.SessionEffort != "medium" {
		t.Fatalf("tasks = %s, want the execution's session model", body)
	}
}
