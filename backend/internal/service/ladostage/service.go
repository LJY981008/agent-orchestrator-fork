// Package ladostage owns the model and effort each LADO stage's workers run
// with (issue #103): the global defaults and each project's override, the
// checks a value passes before it is kept, and the project view that shows
// what each stage will run.
//
// A kept value must not break a later spawn. LADO spawns every worker in chat
// mode, which checks the agent config in session_manager's resolveAgentConfig,
// so the save check follows that path: a Claude Code model must be in a
// readable, fresh catalog; a Codex model passes alone, but a Codex effort
// needs the same catalog check; an effort must be one the model offers.
package ladostage

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// Error codes the service reports, one for each domain error of a stage
// setting.
const (
	CodeInvalid            = "LADO_STAGE_INVALID"
	CodeEffortWithoutModel = "LADO_STAGE_EFFORT_WITHOUT_MODEL"
	CodeHarnessUnsupported = "LADO_STAGE_HARNESS_UNSUPPORTED"
	CodeHarnessMismatch    = "LADO_STAGE_HARNESS_MISMATCH"
	CodeModelUnsupported   = "LADO_STAGE_MODEL_UNSUPPORTED"
	CodeModelUnverified    = "LADO_STAGE_MODEL_UNVERIFIED"
	CodeEffortUnsupported  = "LADO_STAGE_EFFORT_UNSUPPORTED"
)

// Why a project's or the global value does not apply, or applies with a
// warning, in a project's view.
const (
	// ReasonHarnessMismatch means the value names another harness than the
	// harness the project's stage runs, so the stage skips it.
	ReasonHarnessMismatch = "harness_mismatch"
	// ReasonModelUnavailable means the global value applies but the
	// project's cached catalog does not offer its model (or its effort for
	// the model), so the spawn may fail. It only warns: the value still
	// applies.
	ReasonModelUnavailable = "model_unavailable"
)

// AuditAction is the audit action of a changed scope.
const AuditAction = "stage_agents.changed"

// Store reads and writes the stage settings.
type Store interface {
	LadoStageAgents(ctx context.Context, projectID domain.ProjectID) (domain.LadoStageAgents, error)
	SetLadoStageAgents(ctx context.Context, projectID domain.ProjectID, agents domain.LadoStageAgents, audit domain.LadoAuditEvent) error
}

// Projects reads AO's project registry.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Catalog reads an agent's model catalog: the agent service's Models, with
// projectID "" for the machine scope. refresh asks for a new discovery, as
// AO's spawn does.
type Catalog interface {
	Models(ctx context.Context, agentID, projectID string, refresh bool) (ports.AgentModelCatalog, error)
}

// Deps wires a Service.
type Deps struct {
	Store    Store
	Projects Projects
	Catalog  Catalog
	// Clock stamps changes; nil uses time.Now.
	Clock func() time.Time
}

// Service reads and changes the stage settings.
type Service struct {
	deps Deps
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	return &Service{deps: deps}
}

// StageView is one stage as a project sees it.
type StageView struct {
	Stage domain.LadoStage
	// Harness is the harness the project's stage runs: the orchestrator's,
	// else the worker's, for plan and critic; the worker's for execution,
	// integration and resolve. Empty when the project names none.
	Harness domain.AgentHarness
	// Project and Global are the two scopes' values, nil without one.
	Project, Global *domain.LadoStageAgent
	// ProjectApplies is true when the project's value is set and names the
	// stage's harness; ProjectReason is ReasonHarnessMismatch when it is set
	// and does not.
	ProjectApplies bool
	ProjectReason  string
	// GlobalApplies is true when the global value is what the stage uses: it
	// is set, names the stage's harness and no project value applies.
	// GlobalWarning is ReasonHarnessMismatch for a global value of another
	// harness, ReasonModelUnavailable for an applying one the project's
	// cached catalog does not offer, else empty.
	GlobalApplies bool
	GlobalWarning string
	// Effective is what the stage's workers run with and where it comes
	// from, as domain.ResolveLadoStageAgents gives it.
	Effective domain.LadoStageResolved
}

// ProjectView is every stage of a project, in domain.LadoStages order.
type ProjectView struct {
	ProjectID domain.ProjectID
	Stages    []StageView
}

// Global returns the global defaults; a stage without one is missing.
func (s *Service) Global(ctx context.Context) (domain.LadoStageAgents, error) {
	return s.read(ctx, domain.LadoStageGlobalScope)
}

// SetGlobal replaces the global defaults with agents, checked against the
// machine-scope catalogs, and returns them as kept. A stage agents leaves out
// follows the default afterwards. A refused value keeps nothing; sending the
// current values writes nothing.
func (s *Service) SetGlobal(ctx context.Context, agents domain.LadoStageAgents) (domain.LadoStageAgents, error) {
	agents = clean(agents)
	if err := checkShape(agents); err != nil {
		return nil, err
	}
	if err := s.checkCatalog(ctx, domain.LadoStageGlobalScope, agents); err != nil {
		return nil, err
	}
	if err := s.write(ctx, domain.LadoStageGlobalScope, agents); err != nil {
		return nil, err
	}
	return s.Global(ctx)
}

// Project returns the project's view of every stage.
func (s *Service) Project(ctx context.Context, projectID domain.ProjectID) (ProjectView, error) {
	project, err := s.project(ctx, projectID)
	if err != nil {
		return ProjectView{}, err
	}
	return s.view(ctx, project)
}

// SetProject replaces the project's override with agents, checked against
// the project-scope catalogs and the harness each of the project's stages
// runs, and returns the project's view. A refused value keeps nothing;
// sending the current values writes nothing.
func (s *Service) SetProject(ctx context.Context, projectID domain.ProjectID, agents domain.LadoStageAgents) (ProjectView, error) {
	if projectID == domain.LadoStageGlobalScope {
		return ProjectView{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	project, err := s.project(ctx, projectID)
	if err != nil {
		return ProjectView{}, err
	}
	agents = clean(agents)
	if err := checkShape(agents); err != nil {
		return ProjectView{}, err
	}
	harnesses := stageHarnesses(project.Config)
	for _, stage := range domain.LadoStages {
		agent, ok := agents[stage]
		if ok && agent.Harness != harnesses[stage] {
			return ProjectView{}, stageError(apierr.KindInvalid, CodeHarnessMismatch, stage,
				fmt.Sprintf("The %s stage of this project runs %s, not %s.", stage, cmp.Or(string(harnesses[stage]), "no agent"), agent.Harness),
				map[string]any{"harness": string(agent.Harness), "stageHarness": string(harnesses[stage])})
		}
	}
	if err := s.checkCatalog(ctx, projectID, agents); err != nil {
		return ProjectView{}, err
	}
	if err := s.write(ctx, projectID, agents); err != nil {
		return ProjectView{}, err
	}
	return s.view(ctx, project)
}

// clean turns nil into an empty map, so a scope cleared
// with nil and one cleared with {} compare and audit alike.
func clean(agents domain.LadoStageAgents) domain.LadoStageAgents {
	if agents == nil {
		return domain.LadoStageAgents{}
	}
	return agents
}

// checkShape runs domain.ValidateLadoStageAgents and reports its error under
// the matching code.
func checkShape(agents domain.LadoStageAgents) error {
	err := domain.ValidateLadoStageAgents(agents)
	if err == nil {
		return nil
	}
	code := CodeInvalid
	switch {
	case errors.Is(err, domain.ErrLadoStageEffortWithoutModel):
		code = CodeEffortWithoutModel
	case errors.Is(err, domain.ErrLadoStageHarnessUnsupported):
		code = CodeHarnessUnsupported
	}
	return apierr.Invalid(code, err.Error(), nil)
}

// checkCatalog checks each value against its harness's catalog in scope, as
// a chat spawn checks it (see the package comment). The catalog is read with
// refresh, as the spawn reads it, once per harness that needs it.
func (s *Service) checkCatalog(ctx context.Context, scope domain.ProjectID, agents domain.LadoStageAgents) error {
	catalogs := map[domain.AgentHarness]catalogRead{}
	for _, stage := range domain.LadoStages {
		agent, ok := agents[stage]
		if !ok || !needsCatalog(agent) {
			continue
		}
		read, ok := catalogs[agent.Harness]
		if !ok {
			read = s.readCatalog(ctx, agent.Harness, scope, true)
			catalogs[agent.Harness] = read
		}
		if read.err != nil {
			return stageError(apierr.KindUnavailable, CodeModelUnverified, stage,
				fmt.Sprintf("The %s model catalog could not be read, so the %s stage model %q cannot be checked.", agent.Harness, stage, agent.Model),
				map[string]any{"model": agent.Model})
		}
		if read.catalog.Stale {
			return stageError(apierr.KindUnavailable, CodeModelUnverified, stage,
				fmt.Sprintf("The %s model catalog is stale, so the %s stage model %q cannot be checked.", agent.Harness, stage, agent.Model),
				map[string]any{"model": agent.Model})
		}
		switch offer(read.catalog, agent) {
		case offerNoModel:
			return stageError(apierr.KindInvalid, CodeModelUnsupported, stage,
				fmt.Sprintf("%s does not offer the %s stage model %q.", agent.Harness, stage, agent.Model),
				map[string]any{"model": agent.Model})
		case offerNoEffort:
			return stageError(apierr.KindInvalid, CodeEffortUnsupported, stage,
				fmt.Sprintf("The model %q does not offer the %s stage effort %q.", agent.Model, stage, agent.Effort),
				map[string]any{"model": agent.Model, "effort": agent.Effort})
		}
	}
	return nil
}

// needsCatalog reports whether a chat spawn checks the value against the
// catalog: every Claude Code model, and a Codex model with an effort.
func needsCatalog(agent domain.LadoStageAgent) bool {
	return agent.Harness == domain.HarnessClaudeCode || agent.Effort != ""
}

type catalogRead struct {
	catalog ports.AgentModelCatalog
	err     error
}

func (s *Service) readCatalog(ctx context.Context, harness domain.AgentHarness, scope domain.ProjectID, refresh bool) catalogRead {
	if s.deps.Catalog == nil {
		return catalogRead{err: errors.New("no model catalog")}
	}
	catalog, err := s.deps.Catalog.Models(ctx, string(harness), string(scope), refresh)
	return catalogRead{catalog: catalog, err: err}
}

type offerResult int

const (
	offerOK offerResult = iota
	offerNoModel
	offerNoEffort
)

// offer reports whether catalog offers the value's model, and its effort for
// the model. A Claude family alias without efforts of its own takes the
// family's newest model's, as the spawn does.
func offer(catalog ports.AgentModelCatalog, agent domain.LadoStageAgent) offerResult {
	idx := slices.IndexFunc(catalog.Models, func(m ports.AgentModelInfo) bool { return m.ID == agent.Model })
	if idx < 0 {
		return offerNoModel
	}
	if agent.Effort == "" {
		return offerOK
	}
	selected := &catalog.Models[idx]
	if len(selected.Efforts) == 0 && agent.Harness == domain.HarnessClaudeCode {
		if concrete := sessionmanager.NewestClaudeFamilyModel(catalog.Models, agent.Model); concrete != nil {
			selected = concrete
		}
	}
	if !slices.Contains(selected.Efforts, agent.Effort) {
		return offerNoEffort
	}
	return offerOK
}

// stageError is a refused value of one stage.
func stageError(kind apierr.Kind, code string, stage domain.LadoStage, message string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details["stage"] = string(stage)
	return apierr.New(kind, code, message, details)
}

// write replaces the scope's values with agents and audits the change, unless
// they are the current values.
func (s *Service) write(ctx context.Context, scope domain.ProjectID, agents domain.LadoStageAgents) error {
	current, err := s.read(ctx, scope)
	if err != nil {
		return err
	}
	if maps.Equal(current, agents) {
		return nil
	}
	scopeName := "global"
	if scope != domain.LadoStageGlobalScope {
		scopeName = string(scope)
	}
	payload, err := json.Marshal(map[string]any{"scope": scopeName, "from": current, "to": agents})
	if err != nil {
		return apierr.Internal("LADO_STAGE_UPDATE_FAILED", "Failed to update LADO stage agents")
	}
	event := domain.LadoAuditEvent{
		At:          s.deps.Clock().UTC(),
		Actor:       "api",
		Action:      AuditAction,
		SubjectKind: "stage_agents",
		SubjectID:   scopeName,
		Payload:     payload,
	}
	if err := s.deps.Store.SetLadoStageAgents(ctx, scope, agents, event); err != nil {
		return apierr.Internal("LADO_STAGE_UPDATE_FAILED", "Failed to update LADO stage agents")
	}
	return nil
}

func (s *Service) read(ctx context.Context, scope domain.ProjectID) (domain.LadoStageAgents, error) {
	agents, err := s.deps.Store.LadoStageAgents(ctx, scope)
	if err != nil {
		return nil, apierr.Internal("LADO_STAGE_LOAD_FAILED", "Failed to load LADO stage agents")
	}
	return clean(agents), nil
}

func (s *Service) project(ctx context.Context, projectID domain.ProjectID) (domain.ProjectRecord, error) {
	project, ok, err := s.deps.Projects.GetProject(ctx, string(projectID))
	if err != nil {
		return domain.ProjectRecord{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !project.ArchivedAt.IsZero() {
		return domain.ProjectRecord{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return project, nil
}

// stageHarnesses is the harness each of the project's stages runs, as the
// plan worker (ladotask planAgent) and the execution agent (ladopolicy
// ChooseAgent without a task's own agent) pick it.
func stageHarnesses(cfg domain.ProjectConfig) map[domain.LadoStage]domain.AgentHarness {
	plan := cmp.Or(cfg.Orchestrator.Harness, cfg.Worker.Harness)
	execution := cfg.Worker.Harness
	return map[domain.LadoStage]domain.AgentHarness{
		domain.LadoStagePlan:        plan,
		domain.LadoStageCritic:      plan,
		domain.LadoStageExecution:   execution,
		domain.LadoStageIntegration: execution,
		domain.LadoStageResolve:     execution,
	}
}

// view builds the project's view from both scopes.
func (s *Service) view(ctx context.Context, project domain.ProjectRecord) (ProjectView, error) {
	projectID := domain.ProjectID(project.ID)
	projectAgents, err := s.read(ctx, projectID)
	if err != nil {
		return ProjectView{}, err
	}
	globalAgents, err := s.read(ctx, domain.LadoStageGlobalScope)
	if err != nil {
		return ProjectView{}, err
	}
	cfg := project.Config
	harnesses := stageHarnesses(cfg)
	planModel, planEffort := orchestratorValue(cfg, harnesses[domain.LadoStagePlan])
	resolved := domain.ResolveLadoStageAgents(domain.LadoStageInput{
		Project:          projectAgents,
		Global:           globalAgents,
		PlanHarness:      harnesses[domain.LadoStagePlan],
		PlanModel:        planModel,
		PlanEffort:       planEffort,
		ExecutionHarness: harnesses[domain.LadoStageExecution],
		// The policy passes the worker's model with no effort and AO adds
		// the worker's effort at spawn; the view shows that effort, which
		// is what the worker runs with.
		ExecutionModel:  cmp.Or(cfg.Worker.AgentConfig.Model, cfg.AgentConfig.Model),
		ExecutionEffort: cmp.Or(cfg.Worker.AgentConfig.Effort, cfg.AgentConfig.Effort),
	})
	catalogs := map[domain.AgentHarness]catalogRead{}
	view := ProjectView{ProjectID: projectID, Stages: make([]StageView, 0, len(domain.LadoStages))}
	for _, stage := range domain.LadoStages {
		harness := harnesses[stage]
		sv := StageView{Stage: stage, Harness: harness, Effective: resolved.Stage(stage)}
		if agent, ok := projectAgents[stage]; ok {
			sv.Project = &agent
			if agent.Harness == harness {
				sv.ProjectApplies = true
			} else {
				sv.ProjectReason = ReasonHarnessMismatch
			}
		}
		if agent, ok := globalAgents[stage]; ok {
			sv.Global = &agent
			switch {
			case agent.Harness != harness:
				sv.GlobalWarning = ReasonHarnessMismatch
			case !sv.ProjectApplies:
				sv.GlobalApplies = true
				if needsCatalog(agent) {
					read, ok := catalogs[harness]
					if !ok {
						read = s.readCatalog(ctx, harness, projectID, false)
						catalogs[harness] = read
					}
					// An unreadable catalog says nothing about the model, so
					// it warns of nothing; the spawn reports it.
					if read.err == nil && offer(read.catalog, agent) != offerOK {
						sv.GlobalWarning = ReasonModelUnavailable
					}
				}
			}
		}
		view.Stages = append(view.Stages, sv)
	}
	return view, nil
}

// orchestratorValue is the model and effort plan workers ran with before the
// stage settings: the project's, overridden by the orchestrator role's when
// the role names no harness or the plan harness (ladotask planAgent).
func orchestratorValue(cfg domain.ProjectConfig, harness domain.AgentHarness) (string, string) {
	merged := cfg.AgentConfig
	role := cfg.Orchestrator
	if role.Harness == "" || role.Harness == harness {
		merged.Model = cmp.Or(role.AgentConfig.Model, merged.Model)
		merged.Effort = cmp.Or(role.AgentConfig.Effort, merged.Effort)
	}
	return merged.Model, merged.Effort
}
