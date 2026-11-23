package controllers

// LADO fork: the model and effort each LADO stage's workers run with (issue
// #103).

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladostage"
)

// LadoStageAgentResponse is one stage's setting: the model, and optionally
// the effort, a worker of harness runs with.
type LadoStageAgentResponse struct {
	Harness string `json:"harness" enum:"claude-code,codex" description:"The harness the value is for; a stage that runs another harness skips it."`
	Model   string `json:"model" description:"The model; required."`
	Effort  string `json:"effort" description:"The effort; empty passes none, so AO picks one for the model when the worker starts."`
}

// LadoStageAgentsByStage are one scope's settings by stage. A missing stage
// follows the default.
type LadoStageAgentsByStage struct {
	Plan        *LadoStageAgentResponse `json:"plan,omitempty" description:"The plan worker (the designer)."`
	Critic      *LadoStageAgentResponse `json:"critic,omitempty" description:"The plan review worker."`
	Execution   *LadoStageAgentResponse `json:"execution,omitempty" description:"The execution worker: a single execution, a part or a part merge."`
	Integration *LadoStageAgentResponse `json:"integration,omitempty" description:"The integration worker of a run in parts."`
	Resolve     *LadoStageAgentResponse `json:"resolve,omitempty" description:"The worker that resolves a base overlap or conflict."`
}

// LadoStageAgentsResponse is the global stage defaults.
type LadoStageAgentsResponse struct {
	Stages LadoStageAgentsByStage `json:"stages" description:"The global value of each stage; a missing stage follows the default."`
}

// UpdateLadoStageAgentsRequest replaces one scope's stage settings as a
// whole: a stage it leaves out follows the default afterwards.
type UpdateLadoStageAgentsRequest struct {
	Stages *LadoStageAgentsByStage `json:"stages" nullable:"false" description:"Every stage the scope sets; send {} to clear the scope."`
}

// LadoStageResolvedResponse is a stage's final value: what its workers run
// with and where it comes from.
type LadoStageResolvedResponse struct {
	Harness string `json:"harness" description:"The harness the stage's workers run; empty when the project names none."`
	Model   string `json:"model" description:"The model LADO passes; empty passes none, so AO picks."`
	Effort  string `json:"effort" description:"The effort LADO passes; empty passes none, so AO picks one for the model."`
	Source  string `json:"source" enum:"project,global,plan_stage,execution_stage,task,orchestrator,worker" description:"project or global: that scope's setting; plan_stage or execution_stage: inherited from that stage's final value; task: the task's own model; orchestrator: the project orchestrator's model and effort; worker: the project worker's model."`
}

// LadoProjectStageAgentResponse is one stage as a project sees it.
type LadoProjectStageAgentResponse struct {
	Stage          string                    `json:"stage" enum:"plan,critic,execution,integration,resolve"`
	Harness        string                    `json:"harness" description:"The harness the project's stage runs: the orchestrator's, else the worker's, for plan and critic; the worker's for execution, integration and resolve. Empty when the project names none."`
	Project        *LadoStageAgentResponse   `json:"project,omitempty" description:"The project's value; omitted without one."`
	Global         *LadoStageAgentResponse   `json:"global,omitempty" description:"The global value; omitted without one."`
	ProjectApplies bool                      `json:"projectApplies" description:"The project's value is set and names the stage's harness."`
	ProjectReason  string                    `json:"projectReason,omitempty" enum:"harness_mismatch" description:"Why a set project value does not apply; omitted when it applies or is not set."`
	GlobalApplies  bool                      `json:"globalApplies" description:"The global value is what the stage uses: it is set, names the stage's harness and no project value applies."`
	GlobalWarning  string                    `json:"globalWarning,omitempty" enum:"harness_mismatch,model_unavailable" description:"harness_mismatch: the global value names another harness, so the stage skips it; model_unavailable: it applies but the project's cached catalog does not offer its model or effort, so a spawn may fail. Omitted otherwise."`
	Effective      LadoStageResolvedResponse `json:"effective" description:"What the stage's workers run with now."`
}

// LadoProjectStageAgentsResponse is every stage of a project.
type LadoProjectStageAgentsResponse struct {
	ProjectID string                          `json:"projectId"`
	Stages    []LadoProjectStageAgentResponse `json:"stages" description:"Every stage in order: plan, critic, execution, integration, resolve."`
}

// LadoEffectiveStagesResponse are the final values of the workers that
// follow an execution.
type LadoEffectiveStagesResponse struct {
	Integration LadoStageResolvedResponse `json:"integration"`
	Resolve     LadoStageResolvedResponse `json:"resolve"`
}

// LadoSnapshotStageAgentResponse is the model and effort one stage's workers
// spawn with under a snapshot; an empty effort passes none.
type LadoSnapshotStageAgentResponse struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// LadoExecutionSnapshotStagesResponse are the models an approval fixed for
// the workers that follow an execution.
type LadoExecutionSnapshotStagesResponse struct {
	Integration LadoSnapshotStageAgentResponse `json:"integration" description:"What an integration worker of the run spawns with."`
	Resolve     LadoSnapshotStageAgentResponse `json:"resolve" description:"What a resolve worker of the run spawns with."`
}

func ladoStageAgentResponse(agent domain.LadoStageAgent) *LadoStageAgentResponse {
	return &LadoStageAgentResponse{Harness: string(agent.Harness), Model: agent.Model, Effort: agent.Effort}
}

func ladoStageAgentsByStage(agents domain.LadoStageAgents) LadoStageAgentsByStage {
	var out LadoStageAgentsByStage
	for stage, agent := range agents {
		if slot := out.slot(stage); slot != nil {
			*slot = ladoStageAgentResponse(agent)
		}
	}
	return out
}

// slot is the field of stage, nil for an unknown stage.
func (b *LadoStageAgentsByStage) slot(stage domain.LadoStage) **LadoStageAgentResponse {
	switch stage {
	case domain.LadoStagePlan:
		return &b.Plan
	case domain.LadoStageCritic:
		return &b.Critic
	case domain.LadoStageExecution:
		return &b.Execution
	case domain.LadoStageIntegration:
		return &b.Integration
	case domain.LadoStageResolve:
		return &b.Resolve
	}
	return nil
}

// domainAgents turns the request's stages into the domain map; the service
// checks the values.
func (b LadoStageAgentsByStage) domainAgents() domain.LadoStageAgents {
	out := domain.LadoStageAgents{}
	for _, stage := range domain.LadoStages {
		if agent := *b.slot(stage); agent != nil {
			out[stage] = domain.LadoStageAgent{Harness: domain.AgentHarness(agent.Harness), Model: agent.Model, Effort: agent.Effort}
		}
	}
	return out
}

func ladoStageResolvedResponse(r domain.LadoStageResolved) LadoStageResolvedResponse {
	return LadoStageResolvedResponse{Harness: string(r.Harness), Model: r.Model, Effort: r.Effort, Source: string(r.Source)}
}

func ladoProjectStageAgentsResponse(view ladostage.ProjectView) LadoProjectStageAgentsResponse {
	resp := LadoProjectStageAgentsResponse{
		ProjectID: string(view.ProjectID),
		Stages:    make([]LadoProjectStageAgentResponse, 0, len(view.Stages)),
	}
	for _, sv := range view.Stages {
		stage := LadoProjectStageAgentResponse{
			Stage:          string(sv.Stage),
			Harness:        string(sv.Harness),
			ProjectApplies: sv.ProjectApplies,
			ProjectReason:  sv.ProjectReason,
			GlobalApplies:  sv.GlobalApplies,
			GlobalWarning:  sv.GlobalWarning,
			Effective:      ladoStageResolvedResponse(sv.Effective),
		}
		if sv.Project != nil {
			stage.Project = ladoStageAgentResponse(*sv.Project)
		}
		if sv.Global != nil {
			stage.Global = ladoStageAgentResponse(*sv.Global)
		}
		resp.Stages = append(resp.Stages, stage)
	}
	return resp
}

// ladoEffectiveAgentResponse is the agent that runs a task, with the final
// values of the stages after its execution.
func ladoEffectiveAgentResponse(agent ladopolicy.AgentChoice) LadoEffectiveAgentResponse {
	return LadoEffectiveAgentResponse{
		Harness: string(agent.Harness),
		Model:   agent.Model,
		Effort:  agent.Effort,
		Source:  agent.Source,
		Stages: LadoEffectiveStagesResponse{
			Integration: ladoStageResolvedResponse(agent.Stages.Integration),
			Resolve:     ladoStageResolvedResponse(agent.Stages.Resolve),
		},
	}
}

// ladoExecutionSnapshotStages are what the snapshot's integration and resolve
// workers spawn with; a lado-policy/3 snapshot gives the agent's model.
func ladoExecutionSnapshotStages(snapshot ladopolicy.SnapshotContent) LadoExecutionSnapshotStagesResponse {
	integration := snapshot.StageAgent(domain.LadoStageIntegration)
	resolve := snapshot.StageAgent(domain.LadoStageResolve)
	return LadoExecutionSnapshotStagesResponse{
		Integration: LadoSnapshotStageAgentResponse{Model: integration.Model, Effort: integration.Effort},
		Resolve:     LadoSnapshotStageAgentResponse{Model: resolve.Model, Effort: resolve.Effort},
	}
}
