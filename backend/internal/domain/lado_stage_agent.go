package domain

// LADO fork: the model and effort each LADO stage's workers run with (issue
// #103). A global default (project id "") and a per-project override hold one
// optional {harness, model, effort} per stage; ResolveLadoStageAgents turns
// them, with the values LADO used before the setting existed, into what each
// stage spawns with. The plan worker, the policy snapshot and the settings
// service all read that one function, so they agree on the order.

import (
	"errors"
	"fmt"
	"slices"
	"unicode"
)

// LadoStage is one stage of a LADO task whose workers can get their own
// model.
type LadoStage string

// The stages, in the order a task meets them.
const (
	// LadoStagePlan is the plan worker (the designer).
	LadoStagePlan LadoStage = "plan"
	// LadoStageCritic is the plan review worker.
	LadoStageCritic LadoStage = "critic"
	// LadoStageExecution is the execution worker: a single execution, a part
	// or a part merge.
	LadoStageExecution LadoStage = "execution"
	// LadoStageIntegration is the integration worker of a run in parts.
	LadoStageIntegration LadoStage = "integration"
	// LadoStageResolve is the worker that resolves a base overlap.
	LadoStageResolve LadoStage = "resolve"
)

// LadoStages lists every stage in order.
var LadoStages = []LadoStage{LadoStagePlan, LadoStageCritic, LadoStageExecution, LadoStageIntegration, LadoStageResolve}

// Valid reports whether s is one of LadoStages.
func (s LadoStage) Valid() bool { return slices.Contains(LadoStages, s) }

// Parent is the stage whose final value s inherits when no setting names s:
// the plan stage for the critic, the execution stage for integration and
// resolve. The plan and execution stages have none and return "".
func (s LadoStage) Parent() LadoStage {
	switch s {
	case LadoStageCritic:
		return LadoStagePlan
	case LadoStageIntegration, LadoStageResolve:
		return LadoStageExecution
	default:
		return ""
	}
}

// LadoStageGlobalScope is the project id of the global defaults.
const LadoStageGlobalScope ProjectID = ""

// LadoStageValueMaxBytes is the longest model or effort a setting may hold.
const LadoStageValueMaxBytes = 200

// LadoStageAgent is one stage's setting: the model, and optionally the
// effort, a worker of Harness runs with. Model is required; an empty Effort
// passes no effort, so AO picks it at spawn. It is comparable.
type LadoStageAgent struct {
	Harness AgentHarness `json:"harness"`
	Model   string       `json:"model"`
	Effort  string       `json:"effort"`
}

// LadoStageAgents are one scope's settings by stage, as stored and as the
// API carries them. A missing stage follows the default.
type LadoStageAgents map[LadoStage]LadoStageAgent

// Errors of a stage setting. The services report them under the codes named
// on each, which the app shows in the developer's language.
var (
	// ErrLadoStageInvalid means an unknown stage, an empty model, or a model
	// or effort with white space, a control character or more than
	// LadoStageValueMaxBytes bytes. LADO_STAGE_INVALID.
	ErrLadoStageInvalid = errors.New("invalid LADO stage setting")
	// ErrLadoStageEffortWithoutModel means an effort without a model, which
	// would break every Claude spawn. LADO_STAGE_EFFORT_WITHOUT_MODEL.
	ErrLadoStageEffortWithoutModel = errors.New("a LADO stage effort needs a model")
	// ErrLadoStageHarnessUnsupported means a harness other than claude-code
	// or codex. LADO_STAGE_HARNESS_UNSUPPORTED.
	ErrLadoStageHarnessUnsupported = errors.New("the LADO stage harness is not supported")
	// ErrLadoStageHarnessMismatch means a project setting whose harness is
	// not the harness the project's stage runs. LADO_STAGE_HARNESS_MISMATCH.
	ErrLadoStageHarnessMismatch = errors.New("the LADO stage harness is not the stage's harness")
	// ErrLadoStageModelUnsupported means a model the harness's catalog does
	// not list. LADO_STAGE_MODEL_UNSUPPORTED.
	ErrLadoStageModelUnsupported = errors.New("the harness does not offer the LADO stage model")
	// ErrLadoStageModelUnverified means the harness's catalog could not be
	// read, or is stale, when the setting needs it. LADO_STAGE_MODEL_UNVERIFIED.
	ErrLadoStageModelUnverified = errors.New("the LADO stage model could not be checked")
	// ErrLadoStageEffortUnsupported means an effort the model does not offer.
	// LADO_STAGE_EFFORT_UNSUPPORTED.
	ErrLadoStageEffortUnsupported = errors.New("the model does not offer the LADO stage effort")
)

// LadoStageHarnessSupported reports whether a stage setting may name h: the
// harnesses LADO keeps plan workers read-only in and runs executions of.
func LadoStageHarnessSupported(h AgentHarness) bool {
	return h == HarnessClaudeCode || h == HarnessCodex
}

// ValidateLadoStageAgents checks the shape of one scope's settings: known
// stages, a supported harness, a model, an effort only with a model, and no
// white space, control character or overlong value. It does not check the
// model against a catalog. A nil or empty map is valid.
func ValidateLadoStageAgents(agents LadoStageAgents) error {
	for _, stage := range sortedLadoStages(agents) {
		agent := agents[stage]
		if !stage.Valid() {
			return fmt.Errorf("%w: unknown stage %q", ErrLadoStageInvalid, stage)
		}
		if !LadoStageHarnessSupported(agent.Harness) {
			return fmt.Errorf("%w: stage %s names harness %q", ErrLadoStageHarnessUnsupported, stage, agent.Harness)
		}
		if agent.Model == "" {
			if agent.Effort != "" {
				return fmt.Errorf("%w: stage %s", ErrLadoStageEffortWithoutModel, stage)
			}
			return fmt.Errorf("%w: stage %s has no model", ErrLadoStageInvalid, stage)
		}
		if err := checkLadoStageValue(agent.Model); err != nil {
			return fmt.Errorf("%w: stage %s model %w", ErrLadoStageInvalid, stage, err)
		}
		if err := checkLadoStageValue(agent.Effort); err != nil {
			return fmt.Errorf("%w: stage %s effort %w", ErrLadoStageInvalid, stage, err)
		}
	}
	return nil
}

// sortedLadoStages returns the map's stages in LadoStages order, unknown ones
// last by name, so a check reports the same stage every time.
func sortedLadoStages(agents LadoStageAgents) []LadoStage {
	stages := make([]LadoStage, 0, len(agents))
	for stage := range agents {
		stages = append(stages, stage)
	}
	slices.SortFunc(stages, func(a, b LadoStage) int {
		ia, ib := slices.Index(LadoStages, a), slices.Index(LadoStages, b)
		switch {
		case ia >= 0 && ib >= 0:
			return ia - ib
		case ia >= 0:
			return -1
		case ib >= 0:
			return 1
		case a < b:
			return -1
		case a > b:
			return 1
		}
		return 0
	})
	return stages
}

func checkLadoStageValue(v string) error {
	if len(v) > LadoStageValueMaxBytes {
		return fmt.Errorf("is longer than %d bytes", LadoStageValueMaxBytes)
	}
	for _, r := range v {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("has white space or a control character")
		}
	}
	return nil
}

// LadoStageSource says where a stage's final value comes from.
type LadoStageSource string

// The sources of a stage's final value.
const (
	// LadoStageFromProject is the project's setting for the stage.
	LadoStageFromProject LadoStageSource = "project"
	// LadoStageFromGlobal is the global setting for the stage.
	LadoStageFromGlobal LadoStageSource = "global"
	// LadoStageFromPlanStage is the plan stage's final value (the critic).
	LadoStageFromPlanStage LadoStageSource = "plan_stage"
	// LadoStageFromExecutionStage is the execution stage's final value
	// (integration and resolve).
	LadoStageFromExecutionStage LadoStageSource = "execution_stage"
	// LadoStageFromTask is the task's own model (execution).
	LadoStageFromTask LadoStageSource = "task"
	// LadoStageFromOrchestrator is the project orchestrator's model and
	// effort, as plan workers ran before the setting existed (plan).
	LadoStageFromOrchestrator LadoStageSource = "orchestrator"
	// LadoStageFromWorker is the project worker's model with no effort, as
	// executions ran before the setting existed (execution).
	LadoStageFromWorker LadoStageSource = "worker"
)

// LadoStageSources lists every source.
var LadoStageSources = []LadoStageSource{LadoStageFromProject, LadoStageFromGlobal, LadoStageFromPlanStage,
	LadoStageFromExecutionStage, LadoStageFromTask, LadoStageFromOrchestrator, LadoStageFromWorker}

// LadoStageSetting picks a stage's setting for a worker of harness: the
// project's, else the global one. A setting of another harness is skipped,
// as AO skips a role's model for another harness. ok is false when neither
// scope has a setting that applies; source is LadoStageFromProject or
// LadoStageFromGlobal otherwise.
func LadoStageSetting(stage LadoStage, harness AgentHarness, project, global LadoStageAgents) (agent LadoStageAgent, source LadoStageSource, ok bool) {
	if a, found := project[stage]; found && a.Harness == harness && a.Model != "" {
		return a, LadoStageFromProject, true
	}
	if a, found := global[stage]; found && a.Harness == harness && a.Model != "" {
		return a, LadoStageFromGlobal, true
	}
	return LadoStageAgent{}, "", false
}

// LadoStageResolved is one stage's final value: the harness its worker runs,
// the model and effort LADO passes (empty passes none, so AO picks), and
// where they come from. It is comparable.
type LadoStageResolved struct {
	Harness AgentHarness    `json:"harness"`
	Model   string          `json:"model"`
	Effort  string          `json:"effort"`
	Source  LadoStageSource `json:"source"`
}

// LadoStageResolution is every stage's final value. It is comparable.
type LadoStageResolution struct {
	Plan, Critic, Execution, Integration, Resolve LadoStageResolved
}

// Stage returns the stage's final value, the zero value for an unknown stage.
func (r LadoStageResolution) Stage(stage LadoStage) LadoStageResolved {
	switch stage {
	case LadoStagePlan:
		return r.Plan
	case LadoStageCritic:
		return r.Critic
	case LadoStageExecution:
		return r.Execution
	case LadoStageIntegration:
		return r.Integration
	case LadoStageResolve:
		return r.Resolve
	default:
		return LadoStageResolved{}
	}
}

// LadoStageInput is what ResolveLadoStageAgents reads.
type LadoStageInput struct {
	// Project and Global are the two scopes' settings; either may be nil.
	Project, Global LadoStageAgents
	// PlanHarness is the harness plan and critic workers run (the
	// orchestrator's, else the worker's). PlanModel and PlanEffort are the
	// value they ran with before the setting existed: the orchestrator's
	// merged model and effort.
	PlanHarness           AgentHarness
	PlanModel, PlanEffort string
	// ExecutionHarness is the harness execution, integration and resolve
	// workers run. ExecutionModel and ExecutionEffort are the value they ran
	// with before the setting existed: the task's own model when TaskModel,
	// else the project worker's model with an empty effort (AO gives the
	// worker's effort at spawn).
	ExecutionHarness                AgentHarness
	ExecutionModel, ExecutionEffort string
	// TaskModel says ExecutionModel is the task's own choice, which comes
	// before the settings.
	TaskModel bool
}

// ResolveLadoStageAgents returns every stage's final value. A stage takes the
// project's setting, else the global one (LadoStageSetting), else its
// parent stage's final value; the plan stage ends at the orchestrator's
// value, the execution stage at the worker's. The execution stage takes the
// task's own model before any setting. With both scopes empty every stage
// gets the value LADO used before the setting existed.
//
// A setting brings only its own effort: an empty effort stays empty, so LADO
// passes none and AO picks one for the model, and a parent's or the
// orchestrator's effort never joins another model. A stage that inherits
// takes its parent's model and effort as one pair.
func ResolveLadoStageAgents(in LadoStageInput) LadoStageResolution {
	var r LadoStageResolution
	r.Plan = ladoStageValue(LadoStagePlan, in.PlanHarness, in, LadoStageResolved{
		Harness: in.PlanHarness, Model: in.PlanModel, Effort: in.PlanEffort, Source: LadoStageFromOrchestrator})
	r.Critic = ladoStageValue(LadoStageCritic, in.PlanHarness, in, inheritLadoStage(r.Plan, LadoStageFromPlanStage))
	execution := LadoStageResolved{Harness: in.ExecutionHarness, Model: in.ExecutionModel, Effort: in.ExecutionEffort,
		Source: LadoStageFromWorker}
	if in.TaskModel {
		execution.Source = LadoStageFromTask
		r.Execution = execution
	} else {
		r.Execution = ladoStageValue(LadoStageExecution, in.ExecutionHarness, in, execution)
	}
	parent := inheritLadoStage(r.Execution, LadoStageFromExecutionStage)
	r.Integration = ladoStageValue(LadoStageIntegration, in.ExecutionHarness, in, parent)
	r.Resolve = ladoStageValue(LadoStageResolve, in.ExecutionHarness, in, parent)
	return r
}

// ladoStageValue is the stage's setting for harness, else fallback.
func ladoStageValue(stage LadoStage, harness AgentHarness, in LadoStageInput, fallback LadoStageResolved) LadoStageResolved {
	agent, source, ok := LadoStageSetting(stage, harness, in.Project, in.Global)
	if !ok {
		return fallback
	}
	return LadoStageResolved{Harness: harness, Model: agent.Model, Effort: agent.Effort, Source: source}
}

// inheritLadoStage is a parent's final value as a child inherits it: the same
// harness, model and effort under source.
func inheritLadoStage(parent LadoStageResolved, source LadoStageSource) LadoStageResolved {
	parent.Source = source
	return parent
}
