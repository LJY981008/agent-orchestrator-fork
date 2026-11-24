package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestLadoStageParent(t *testing.T) {
	for stage, want := range map[LadoStage]LadoStage{
		LadoStagePlan: "", LadoStageCritic: LadoStagePlan, LadoStageExecution: "",
		LadoStageIntegration: LadoStageExecution, LadoStageResolve: LadoStageExecution, "review": "",
	} {
		if got := stage.Parent(); got != want {
			t.Errorf("%q.Parent() = %q, want %q", stage, got, want)
		}
	}
	for _, stage := range LadoStages {
		if !stage.Valid() {
			t.Errorf("%q is not valid", stage)
		}
	}
	if LadoStage("review").Valid() || LadoStage("").Valid() {
		t.Error("an unknown stage is valid")
	}
}

func TestValidateLadoStageAgents(t *testing.T) {
	claude := func(model, effort string) LadoStageAgent {
		return LadoStageAgent{Harness: HarnessClaudeCode, Model: model, Effort: effort}
	}
	for name, tc := range map[string]struct {
		agents LadoStageAgents
		want   error
	}{
		"nil":                 {nil, nil},
		"empty":               {LadoStageAgents{}, nil},
		"every stage":         {LadoStageAgents{LadoStagePlan: claude("fable", "high"), LadoStageCritic: claude("sonnet", ""), LadoStageExecution: {Harness: HarnessCodex, Model: "gpt-5.1-codex"}, LadoStageIntegration: claude("opus", "medium"), LadoStageResolve: claude("haiku", "")}, nil},
		"a long model":        {LadoStageAgents{LadoStagePlan: claude(strings.Repeat("m", LadoStageValueMaxBytes), "")}, nil},
		"effort alone":        {LadoStageAgents{LadoStagePlan: claude("", "high")}, ErrLadoStageEffortWithoutModel},
		"no model":            {LadoStageAgents{LadoStagePlan: claude("", "")}, ErrLadoStageInvalid},
		"an unknown stage":    {LadoStageAgents{"review": claude("opus", "")}, ErrLadoStageInvalid},
		"an empty stage":      {LadoStageAgents{"": claude("opus", "")}, ErrLadoStageInvalid},
		"another harness":     {LadoStageAgents{LadoStagePlan: {Harness: HarnessAider, Model: "x"}}, ErrLadoStageHarnessUnsupported},
		"no harness":          {LadoStageAgents{LadoStagePlan: {Model: "opus"}}, ErrLadoStageHarnessUnsupported},
		"a model with space":  {LadoStageAgents{LadoStagePlan: claude("opus 5", "")}, ErrLadoStageInvalid},
		"a padded model":      {LadoStageAgents{LadoStagePlan: claude(" opus", "")}, ErrLadoStageInvalid},
		"a model with a line": {LadoStageAgents{LadoStagePlan: claude("opus\nx", "")}, ErrLadoStageInvalid},
		"an effort with tab":  {LadoStageAgents{LadoStagePlan: claude("opus", "hi\tgh")}, ErrLadoStageInvalid},
		"a control character": {LadoStageAgents{LadoStagePlan: claude("opus\x00", "")}, ErrLadoStageInvalid},
		"an overlong model":   {LadoStageAgents{LadoStagePlan: claude(strings.Repeat("m", LadoStageValueMaxBytes+1), "")}, ErrLadoStageInvalid},
		"an overlong effort":  {LadoStageAgents{LadoStagePlan: claude("opus", strings.Repeat("e", LadoStageValueMaxBytes+1))}, ErrLadoStageInvalid},
	} {
		err := ValidateLadoStageAgents(tc.agents)
		if tc.want == nil && err != nil {
			t.Errorf("%s: %v, want nil", name, err)
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}

func TestLadoStageSetting(t *testing.T) {
	project := LadoStageAgents{LadoStagePlan: {Harness: HarnessCodex, Model: "gpt-5"}}
	global := LadoStageAgents{LadoStagePlan: {Harness: HarnessClaudeCode, Model: "fable", Effort: "high"}}
	agent, source, ok := LadoStageSetting(LadoStagePlan, HarnessCodex, project, global)
	if !ok || source != LadoStageFromProject || agent.Model != "gpt-5" {
		t.Errorf("codex plan = %+v %q %v, want the project's", agent, source, ok)
	}
	agent, source, ok = LadoStageSetting(LadoStagePlan, HarnessClaudeCode, project, global)
	if !ok || source != LadoStageFromGlobal || agent.Model != "fable" || agent.Effort != "high" {
		t.Errorf("claude plan = %+v %q %v, want the global one past the codex project setting", agent, source, ok)
	}
	if agent, source, ok = LadoStageSetting(LadoStageCritic, HarnessClaudeCode, project, global); ok {
		t.Errorf("critic = %+v %q, want none", agent, source)
	}
	if _, _, ok = LadoStageSetting(LadoStagePlan, HarnessAider, project, global); ok {
		t.Error("an aider plan took a setting")
	}
}

// Both scopes empty keep every stage at the value LADO used before the
// setting existed.
func TestResolveLadoStageAgentsEmpty(t *testing.T) {
	in := LadoStageInput{PlanHarness: HarnessClaudeCode, PlanModel: "opus", PlanEffort: "high",
		ExecutionHarness: HarnessCodex, ExecutionModel: "gpt-5"}
	plan := LadoStageResolved{Harness: HarnessClaudeCode, Model: "opus", Effort: "high", Source: LadoStageFromOrchestrator}
	execution := LadoStageResolved{Harness: HarnessCodex, Model: "gpt-5", Source: LadoStageFromWorker}
	want := LadoStageResolution{
		Plan:        plan,
		Critic:      LadoStageResolved{Harness: HarnessClaudeCode, Model: "opus", Effort: "high", Source: LadoStageFromPlanStage},
		Execution:   execution,
		Integration: LadoStageResolved{Harness: HarnessCodex, Model: "gpt-5", Source: LadoStageFromExecutionStage},
		Resolve:     LadoStageResolved{Harness: HarnessCodex, Model: "gpt-5", Source: LadoStageFromExecutionStage},
	}
	if got := ResolveLadoStageAgents(in); got != want {
		t.Fatalf("resolution = %+v\nwant %+v", got, want)
	}
	in.Project, in.Global = LadoStageAgents{}, LadoStageAgents{}
	if got := ResolveLadoStageAgents(in); got != want {
		t.Fatalf("resolution of empty maps = %+v\nwant %+v", got, want)
	}
	for _, stage := range LadoStages {
		if got := want.Stage(stage); got.Source == "" {
			t.Errorf("Stage(%s) = %+v", stage, got)
		}
	}
	if got := want.Stage("review"); got != (LadoStageResolved{}) {
		t.Errorf("Stage(review) = %+v, want zero", got)
	}
}

func TestResolveLadoStageAgentsOrder(t *testing.T) {
	claude := func(model, effort string) LadoStageAgent {
		return LadoStageAgent{Harness: HarnessClaudeCode, Model: model, Effort: effort}
	}
	codex := func(model, effort string) LadoStageAgent {
		return LadoStageAgent{Harness: HarnessCodex, Model: model, Effort: effort}
	}
	base := LadoStageInput{PlanHarness: HarnessClaudeCode, PlanModel: "opus", PlanEffort: "xhigh",
		ExecutionHarness: HarnessClaudeCode, ExecutionModel: "sonnet"}
	type stageWant struct {
		stage  LadoStage
		model  string
		effort string
		source LadoStageSource
	}
	for name, tc := range map[string]struct {
		project, global LadoStageAgents
		taskModel       bool
		want            []stageWant
	}{
		"project before global": {
			project: LadoStageAgents{LadoStagePlan: claude("fable", "max")},
			global:  LadoStageAgents{LadoStagePlan: claude("opus", "high"), LadoStageCritic: claude("haiku", "low")},
			want: []stageWant{
				{LadoStagePlan, "fable", "max", LadoStageFromProject},
				{LadoStageCritic, "haiku", "low", LadoStageFromGlobal},
			},
		},
		"critic inherits the plan's final pair": {
			global: LadoStageAgents{LadoStagePlan: claude("fable", "high")},
			want: []stageWant{
				{LadoStagePlan, "fable", "high", LadoStageFromGlobal},
				{LadoStageCritic, "fable", "high", LadoStageFromPlanStage},
			},
		},
		// A plan setting with a model only brings no effort: the
		// orchestrator's xhigh stays out, and the critic inherits the pair.
		"a model only setting keeps its effort empty": {
			project: LadoStageAgents{LadoStagePlan: claude("fable", "")},
			want: []stageWant{
				{LadoStagePlan, "fable", "", LadoStageFromProject},
				{LadoStageCritic, "fable", "", LadoStageFromPlanStage},
			},
		},
		"a critic setting with a model only keeps the plan's effort out": {
			project: LadoStageAgents{LadoStagePlan: claude("opus", "high"), LadoStageCritic: claude("haiku", "")},
			want: []stageWant{
				{LadoStagePlan, "opus", "high", LadoStageFromProject},
				{LadoStageCritic, "haiku", "", LadoStageFromProject},
			},
		},
		"settings of another harness are skipped": {
			project: LadoStageAgents{LadoStagePlan: codex("gpt-5", "high"), LadoStageExecution: codex("gpt-5", "")},
			global:  LadoStageAgents{LadoStageIntegration: codex("gpt-5", "")},
			want: []stageWant{
				{LadoStagePlan, "opus", "xhigh", LadoStageFromOrchestrator},
				{LadoStageCritic, "opus", "xhigh", LadoStageFromPlanStage},
				{LadoStageExecution, "sonnet", "", LadoStageFromWorker},
				{LadoStageIntegration, "sonnet", "", LadoStageFromExecutionStage},
			},
		},
		"integration and resolve inherit the execution's final pair": {
			global: LadoStageAgents{LadoStageExecution: claude("opus", "medium"), LadoStageResolve: claude("haiku", "")},
			want: []stageWant{
				{LadoStageExecution, "opus", "medium", LadoStageFromGlobal},
				{LadoStageIntegration, "opus", "medium", LadoStageFromExecutionStage},
				{LadoStageResolve, "haiku", "", LadoStageFromGlobal},
				{LadoStagePlan, "opus", "xhigh", LadoStageFromOrchestrator},
			},
		},
		"the task's own model comes before the settings": {
			project:   LadoStageAgents{LadoStageExecution: claude("opus", "high"), LadoStageIntegration: claude("haiku", "")},
			taskModel: true,
			want: []stageWant{
				{LadoStageExecution, "sonnet", "", LadoStageFromTask},
				{LadoStageIntegration, "haiku", "", LadoStageFromProject},
				{LadoStageResolve, "sonnet", "", LadoStageFromExecutionStage},
			},
		},
	} {
		in := base
		in.Project, in.Global, in.TaskModel = tc.project, tc.global, tc.taskModel
		got := ResolveLadoStageAgents(in)
		for _, w := range tc.want {
			stage := got.Stage(w.stage)
			if stage.Model != w.model || stage.Effort != w.effort || stage.Source != w.source || stage.Harness != HarnessClaudeCode {
				t.Errorf("%s: %s = %+v, want claude-code %s %q from %s", name, w.stage, stage, w.model, w.effort, w.source)
			}
		}
	}
}
