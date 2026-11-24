package ladotask

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// setStageAgents replaces one scope's stage settings (issue #103).
func (pf *planFixture) setStageAgents(t *testing.T, project domain.ProjectID, agents domain.LadoStageAgents) {
	t.Helper()
	event := domain.LadoAuditEvent{At: pf.now, Actor: "test", Action: "stage_agents.changed", SubjectKind: "stage_agents", SubjectID: string(project), Payload: []byte(`{}`)}
	if err := pf.store.SetLadoStageAgents(context.Background(), project, agents, event); err != nil {
		t.Fatalf("SetLadoStageAgents(%q): %v", project, err)
	}
}

// auditPayloads returns the payloads of action's audit events, oldest first.
func (pf *planFixture) auditPayloads(t *testing.T, action string) []map[string]any {
	t.Helper()
	rows, err := pf.raw.Query(`SELECT payload_json FROM lado_audit_events WHERE action = ? ORDER BY id`, action)
	if err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var payloads []map[string]any
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("payload %q: %v", raw, err)
		}
		payloads = append(payloads, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return payloads
}

// A plan worker of each stage takes the stage's project setting, else its
// global one, else (the critic) the plan stage's final value, else the
// orchestrator's model and effort. A setting of another harness is skipped,
// and a setting with a model alone passes no effort: neither the
// orchestrator's nor the parent's effort joins it.
func TestPlanAgentFollowsTheStageSettings(t *testing.T) {
	claude := func(model, effort string) domain.LadoStageAgent {
		return domain.LadoStageAgent{Harness: domain.HarnessClaudeCode, Model: model, Effort: effort}
	}
	codex := domain.LadoStageAgent{Harness: domain.HarnessCodex, Model: "gpt-5.1-codex", Effort: "high"}
	type want struct {
		model, effort string
		source        domain.LadoStageSource
	}
	cases := []struct {
		name            string
		project, global domain.LadoStageAgents
		plan, critic    want
	}{
		{
			name:   "no settings run as before",
			plan:   want{"opus", "high", domain.LadoStageFromOrchestrator},
			critic: want{"opus", "high", domain.LadoStageFromPlanStage},
		},
		{
			name:   "the global plan setting reaches the critic as one pair",
			global: domain.LadoStageAgents{domain.LadoStagePlan: claude("fable", "max")},
			plan:   want{"fable", "max", domain.LadoStageFromGlobal},
			critic: want{"fable", "max", domain.LadoStageFromPlanStage},
		},
		{
			name:    "the project comes before the global setting",
			project: domain.LadoStageAgents{domain.LadoStagePlan: claude("sonnet", "low")},
			global:  domain.LadoStageAgents{domain.LadoStagePlan: claude("fable", "max")},
			plan:    want{"sonnet", "low", domain.LadoStageFromProject},
			critic:  want{"sonnet", "low", domain.LadoStageFromPlanStage},
		},
		{
			name:    "a critic setting of its own",
			project: domain.LadoStageAgents{domain.LadoStageCritic: claude("haiku", "")},
			global:  domain.LadoStageAgents{domain.LadoStagePlan: claude("fable", "max")},
			plan:    want{"fable", "max", domain.LadoStageFromGlobal},
			critic:  want{"haiku", "", domain.LadoStageFromProject},
		},
		{
			name:   "a model alone passes no effort",
			global: domain.LadoStageAgents{domain.LadoStagePlan: claude("fable", "")},
			plan:   want{"fable", "", domain.LadoStageFromGlobal},
			critic: want{"fable", "", domain.LadoStageFromPlanStage},
		},
		{
			name:    "settings of another harness are skipped",
			project: domain.LadoStageAgents{domain.LadoStagePlan: codex},
			global:  domain.LadoStageAgents{domain.LadoStageCritic: codex},
			plan:    want{"opus", "high", domain.LadoStageFromOrchestrator},
			critic:  want{"opus", "high", domain.LadoStageFromPlanStage},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pf := newPlanFixture(t)
			pf.setStageAgents(t, pf.task.ProjectID, tc.project)
			pf.setStageAgents(t, domain.LadoStageGlobalScope, tc.global)
			for stage, w := range map[domain.LadoStage]want{domain.LadoStagePlan: tc.plan, domain.LadoStageCritic: tc.critic} {
				got, err := pf.svc.planAgent(context.Background(), pf.task, stage)
				wantCfg := ports.AgentConfig{Model: w.model, Effort: w.effort, Permissions: domain.PermissionModeAuto}
				if err != nil || got.Harness != domain.HarnessClaudeCode || got.Config != wantCfg || got.Source != w.source {
					t.Errorf("planAgent(%s) = %+v, %v; want %+v from %s", stage, got, err, wantCfg, w.source)
				}
			}
		})
	}
}

// The designer and the critic spawn with their stages' values, read at each
// spawn, and the audit keeps the model, the effort and where they came from.
func TestPlanWorkersSpawnWithTheirStageAndAuditIt(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	pf.setStageAgents(t, domain.LadoStageGlobalScope, domain.LadoStageAgents{
		domain.LadoStagePlan: {Harness: domain.HarnessClaudeCode, Model: "fable"},
	})
	req := pf.requestAndStart(t, PlanRequestInput{TaskID: pf.task.ID})
	designer := pf.designer(t, req.ID)
	// A change after the designer started reaches the next spawn only.
	pf.setStageAgents(t, pf.task.ProjectID, domain.LadoStageAgents{
		domain.LadoStageCritic: {Harness: domain.HarnessClaudeCode, Model: "haiku", Effort: "low"},
	})
	pf.now = pf.now.Add(10 * time.Minute)
	pf.submit(t, req.ID, designer, pf.workerPlan("issue", "", "backend/a/**", "backend/b/**", "frontend/**", "docs/**"), true)
	pf.svc.DrivePlanWorkers(ctx)

	spawns := pf.sessions.spawns()
	if len(spawns) != 2 {
		t.Fatalf("%d spawns, want the designer and a critic", len(spawns))
	}
	if got, want := spawns[0].AgentConfig, (ports.AgentConfig{Model: "fable", Permissions: domain.PermissionModeAuto}); got != want {
		t.Errorf("designer agent = %+v, want %+v", got, want)
	}
	if got, want := spawns[1].AgentConfig, (ports.AgentConfig{Model: "haiku", Effort: "low", Permissions: domain.PermissionModeAuto}); got != want {
		t.Errorf("critic agent = %+v, want %+v", got, want)
	}
	critique, _, _ := pf.store.LadoPlanCritique(ctx, req.ID, 1)

	started := pf.auditPayloads(t, "plan_worker.started")
	if len(started) != 1 || started[0]["session"] != string(designer) || started[0]["agent"] != string(domain.HarnessClaudeCode) ||
		started[0]["model"] != "fable" || started[0]["effort"] != "" || started[0]["modelSource"] != string(domain.LadoStageFromGlobal) {
		t.Errorf("plan_worker.started = %v", started)
	}
	spawned := pf.auditPayloads(t, "plan_worker.critic_spawned")
	if len(spawned) != 1 || spawned[0]["session"] != string(critique.SessionID) || spawned[0]["attempt"] != float64(1) ||
		spawned[0]["agent"] != string(domain.HarnessClaudeCode) || spawned[0]["model"] != "haiku" || spawned[0]["effort"] != "low" ||
		spawned[0]["modelSource"] != string(domain.LadoStageFromProject) || spawned[0]["task"] != string(pf.task.ID) {
		t.Errorf("plan_worker.critic_spawned = %v (critic %s)", spawned, critique.SessionID)
	}
}

// With no settings the audit names the orchestrator's model and effort.
func TestPlanWorkerStartedAuditsTheOrchestratorValue(t *testing.T) {
	pf := newPlanFixture(t)
	pf.requestAndStart(t, PlanRequestInput{TaskID: pf.task.ID})
	started := pf.auditPayloads(t, "plan_worker.started")
	if len(started) != 1 || started[0]["model"] != "opus" || started[0]["effort"] != "high" ||
		started[0]["modelSource"] != string(domain.LadoStageFromOrchestrator) {
		t.Fatalf("plan_worker.started = %v", started)
	}
}
