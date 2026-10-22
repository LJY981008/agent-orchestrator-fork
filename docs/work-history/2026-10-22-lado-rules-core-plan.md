# LADO step 5 — Rules core: approved plan (contracts)

> 승인: 2026-10-20 소유자 — 접근 B("전부 지금"), 권한 상한 기본값 `auto`, 체인 러너 13단계 × $25(2026-10-22). 이 문서가 단계 엔트리의 공통 계약이다. 단계 엔트리가 이 문서보다 구체적이면 엔트리를 따른다.
> 근거 스펙: `docs/LJY-PRD/HARNESS.md` §3-§8·§12·§14·§15, `docs/LJY-PRD/TDD.md` §1·§3·§4·§5.2·§9·§13·§14·§15, `docs/LJY-PRD/PRD.md` HAR-001..005.

## 0. Rules for every step

- Read `AGENTS.md` and `CLAUDE.md`; UI steps also read `DESIGN.md`. Code comments in English, matching nearby density. New code goes in LADO files (`lado_*`, `ladopolicy`) inside AO's layers; shared upstream files get only small hook edits (TDD §1). Never use the word "harness" for LADO concepts (say rule, policy, setup).
- Query files `backend/internal/storage/sqlite/queries/lado_*.sql` stay ASCII only (sqlc v1.31.1 cuts SQL after non-ASCII); read the generated `gen/*.sql.go` after `npm run sqlc`. Never hand-edit `gen/`, `openapi.yaml`, `frontend/src/api/schema.ts`, `routeTree.gen.ts`, `tray-catalog.generated.ts`.
- Store tests use `sqlitetest.MustOpen(t)`; no fresh `sqlite.Open` per test. LADO tables never get FOREIGN KEYs to upstream tables.
- Renderer strings go in all 8 locale files (`frontend/src/renderer/i18n/{en,ko,ja,zh-CN,es,fr,de,pt-BR}.json`, flat keys, appended after the existing LADO keys) with real translations; Korean follows the fork's glossary (규칙, 보호 규칙, 권한 상한, 상속, 켜기/끄기, 보관, 확인 필요, 차단됨, 승인). The coverage test fails on hardcoded English in JSX.
- `<button>` ignores Tailwind font classes (unlayered button rule): style an inner `<span>`.
- The Write tool turns `\u` escapes into real characters; write such strings with python if they must stay escaped.
- No `git push`, `gh`, PRs, publishing or writes outside the worktree (the guard refuses them). Never touch the real `~/.ao`.
- Subagents and long commands only in the foreground (`run_in_background: false`): when a `claude -p` session ends its last turn, it waits a bounded time for background work and then kills it, and the step ends without its commit.
- The design and its approvals are done (2026-10-20/22): skip brainstorming and planning gates of any skill and do not ask the user questions — implement this step, commit, rerun the gates, fill After/회고.
- Every step leaves `go build ./... && go vet ./...` green, and from S08 on also `npm --prefix frontend run typecheck`.

## 1. Scope

In: rule definitions with immutable versions; Global ON/OFF, Project/Task INHERIT/FORCE_ON/FORCE_OFF; Disable (Global OFF), Archive/Unarchive and Delete kept separate; built-in rules; resolution; effective preview (project, task); capability check; declared mutual exclusion (`conflictsWith`); rule snapshot written in the plan approval transaction; approval signs plan sha256 + snapshot sha256; Review required / Blocked derived at read time; change impact preview (`dryRun`); audit events; CDC kind `lado_rules`; UI in Settings, Project settings, task view and board.

Out (owning step): worker prompt injection, applying the ceiling, explicit permission mode, queue (6); local setup, `setup.change.requires_presence`, conflict acceptances (7); validators and the gates `integration.approval.required`, `git.protected_branch.no_direct_write`, `test.command.must_pass`, `paths.protected` (8); HAR-006 AI drafts, profiles, templates. Not modelled until something consumes them: rule triggers (every step-5 rule applies to every LADO execution of its scope), bindings pinned to an explicit version (bindings follow the active = highest version), per-project ceiling values.

Decisions (record in docs at S13): a. approval body `{sha256, policySha256}`; b. Review required/Blocked derived, not stored; c. only Claude Code and Codex verified, every other agent blocks (fail closed); d. ordinary rule changes need no presence (audit actor `api`), the ceiling mode needs presence, protected rules refuse bindings/archive/delete; e. delete only user rules no snapshot used; f. AO project instructions are read-only Project entries hashed into the snapshot; g. snapshot carries the AO build (`config.AppVersion`), so an AO update makes approved tasks Review required; h. ceiling default `auto` — Codex at `auto` runs workspace-write + on-request + no network, so commits, pushes and network calls ask for approval (TDD §6.6).

## 2. Storage — LADO migration `0005_lado_rules.sql`

```sql
-- +goose Up
CREATE TABLE lado_rules (
    id          TEXT PRIMARY KEY CHECK (id <> ''),
    builtin     INTEGER NOT NULL CHECK (builtin IN (0, 1)),
    protected   INTEGER NOT NULL CHECK (protected IN (0, 1)),
    created_at  TIMESTAMP NOT NULL,
    archived_at TIMESTAMP
);
CREATE TABLE lado_rule_versions (
    id               TEXT PRIMARY KEY CHECK (id <> ''),
    rule_id          TEXT NOT NULL REFERENCES lado_rules (id),
    number           INTEGER NOT NULL CHECK (number > 0),
    name             TEXT NOT NULL CHECK (name <> ''),
    category         TEXT NOT NULL DEFAULT '',
    instruction      TEXT NOT NULL DEFAULT '',
    enforcement_json TEXT NOT NULL CHECK (json_valid(enforcement_json)),
    config_json      TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config_json)),
    conflicts_json   TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(conflicts_json)),
    sha256           TEXT NOT NULL CHECK (length(sha256) = 64),
    actor            TEXT NOT NULL CHECK (actor <> ''),
    created_at       TIMESTAMP NOT NULL,
    UNIQUE (rule_id, number)
);
CREATE TABLE lado_rule_bindings (
    rule_id    TEXT NOT NULL REFERENCES lado_rules (id),
    scope      TEXT NOT NULL CHECK (scope IN ('global', 'project', 'task')),
    scope_id   TEXT NOT NULL,
    state      TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (rule_id, scope, scope_id),
    CHECK (
        (scope = 'global' AND scope_id = '' AND state IN ('on', 'off')) OR
        (scope <> 'global' AND scope_id <> '' AND state IN ('force_on', 'force_off'))
    )
);
CREATE INDEX lado_rule_bindings_by_scope ON lado_rule_bindings (scope, scope_id);
CREATE TABLE lado_policy_snapshots (
    id            TEXT PRIMARY KEY CHECK (id <> ''),
    approval_id   TEXT NOT NULL UNIQUE REFERENCES lado_plan_approvals (id),
    sha256        TEXT NOT NULL CHECK (length(sha256) = 64),
    snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)),
    blocking      INTEGER NOT NULL CHECK (blocking IN (0, 1)),
    created_at    TIMESTAMP NOT NULL
);
CREATE TABLE lado_policy_snapshot_rules (
    snapshot_id     TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    rule_id         TEXT NOT NULL REFERENCES lado_rules (id),
    rule_version_id TEXT NOT NULL REFERENCES lado_rule_versions (id),
    PRIMARY KEY (snapshot_id, rule_version_id)
);
CREATE INDEX lado_policy_snapshot_rules_by_rule ON lado_policy_snapshot_rules (rule_id);
-- +goose Down: drop the five tables in reverse order.
```
Header comment (ASCII is not required in migrations, but keep it short): rule definitions are global; bindings turn a rule on/off globally or force it for one project or task; no project/task binding means INHERIT; `scope_id` names `projects.id` or `lado_tasks.id` without a FOREIGN KEY; versions never change; a snapshot is the canonical JSON of what applied to one approval and `lado_policy_snapshot_rules` lists the versions it used, so a used rule is never deleted.

CDC (`storage/sqlite/lado_migrate.go` `ladoCDCTriggers`, never in migrations): rule, rule version and binding changes publish `session_updated` with `session_id` NULL and payload `{"kind":"lado_rules","projectId":…}`: one row per project in LADO mode (`lado_project_settings.lado_mode = 1`, joined to `projects`) for `lado_rules` insert/update/delete, `lado_rule_versions` insert and global bindings; a project binding publishes to that project; a task binding to the task's project. Trigger names `lado_cdc_rules_*`, `lado_cdc_rule_versions_insert`, `lado_cdc_rule_bindings_{insert,update,delete}` (delete uses OLD).

## 3. Domain (`domain/lado_rule.go`, `domain/lado_policy.go`)

```go
type LadoRuleID string; type LadoRuleVersionID string; type LadoPolicySnapshotID string
type LadoRuleScope string     // "global" | "project" | "task"
type LadoBindingState string  // "on" | "off" (global) · "force_on" | "force_off" (project, task) · "inherit" (request only, never stored)
type LadoEnforcement string   // "prompt" | "validator" | "tool_policy" | "workflow_gate"
type LadoStrength string      // "prevents" | "detects" | "advisory"
const LadoRulePlanApproval LadoRuleID = "plan.approval.required"
const LadoRulePermissionCeiling LadoRuleID = "agent.permission.ceiling"
const LadoRuleKoreanCommits LadoRuleID = "git.commit.message.korean"

type LadoRuleContent struct {
	Name        string            `json:"name"`
	Category    string            `json:"category"`
	Instruction string            `json:"instruction"`
	Enforcement []LadoEnforcement `json:"enforcement"`
	Config      json.RawMessage   `json:"config"`         // "{}" unless the rule has settings; ceiling: {"mode":"auto"}
	Conflicts   []LadoRuleID      `json:"conflictsWith"`
}
func (c LadoRuleContent) Normalize() LadoRuleContent // trim text; sort+dedupe Conflicts; compact Config ("{}" when empty); non-nil slices
func (c LadoRuleContent) CanonicalJSON() []byte    // Normalize, encoder SetEscapeHTML(false), trailing newline trimmed
func (c LadoRuleContent) SHA256() string
func (c LadoRuleContent) Strength() LadoStrength    // workflow_gate or tool_policy -> prevents; validator -> detects; else advisory
func (c LadoRuleContent) Has(e LadoEnforcement) bool

type LadoRuleVersion struct { ID LadoRuleVersionID; RuleID LadoRuleID; Number int; Content LadoRuleContent; SHA256, Actor string; CreatedAt time.Time }
type LadoRuleBinding struct { RuleID LadoRuleID; Scope LadoRuleScope; ScopeID string; State LadoBindingState; UpdatedAt time.Time }
type LadoRule struct {
	ID LadoRuleID; Builtin, Protected bool
	Version   LadoRuleVersion   // active = highest number
	Global    LadoBindingState  // on | off
	Overrides []LadoRuleBinding // project and task bindings
	CreatedAt, ArchivedAt time.Time
}
type LadoRuleSeed struct { Rule LadoRule; Event LadoAuditEvent }
type LadoCeilingConfig struct { Mode PermissionMode `json:"mode"` }
var LadoCeilingModes = []PermissionMode{PermissionModeAcceptEdits, PermissionModeAuto, PermissionModeBypassPermissions} // lowest first; "default" is never a ceiling
func LadoPermissionRank(m PermissionMode) int // 1..3 for LadoCeilingModes, 0 otherwise
var ErrLadoRuleChanged = errors.New("the rule has a newer version")
var ErrLadoRuleReferenced = errors.New("a rule snapshot uses the rule")

type LadoProblemSeverity string // "blocking" | "warning" | "info"
type LadoPolicyProblem struct {
	Code     string              `json:"code"`
	Severity LadoProblemSeverity `json:"severity"`
	Rules    []LadoRuleID        `json:"rules,omitempty"`
	Details  map[string]string   `json:"details,omitempty"`
}
type LadoPolicySnapshotRule struct { RuleID LadoRuleID; VersionID LadoRuleVersionID }
type LadoPolicySnapshot struct { ID LadoPolicySnapshotID; ApprovalID LadoPlanApprovalID; SHA256 string; Content []byte; Blocking bool; Rules []LadoPolicySnapshotRule; CreatedAt time.Time }
type LadoPolicySnapshotRef struct { ID LadoPolicySnapshotID; SHA256 string; Blocking bool }
```
`domain/lado_presence.go`: `LadoPresenceRuleChange = "rule.protected_change"`.
`domain/lado_task.go`: `LadoTaskPlan` gains `Snapshot LadoPolicySnapshotRef` (snapshot of the newest approval; empty for approvals made before rules existed) and `PolicySHA256 string` (what the rules give the approved revision now; filled by the task service; empty = not computed); `func (p LadoTaskPlan) RulesChanged() bool { return p.PolicySHA256 != "" && p.Snapshot.SHA256 != p.PolicySHA256 }`; new states `LadoTaskReviewRequired = "review_required"`, `LadoTaskBlocked = "blocked"`; in `State()` the approved branch becomes: RulesChanged → review_required; Snapshot.Blocking → blocked; else approved. Cancelled and closed still win.

## 4. Store (`store/lado_rule_store.go`, `store/lado_plan_store.go`)

All writes under `s.writeMu` + `s.inTx`; the audit event goes in the same transaction (`appendLadoAuditEventTx`); `unwrapLadoSentinel` also passes `ErrLadoRuleChanged`, `ErrLadoRuleReferenced`.
```go
EnsureLadoBuiltinRules(ctx, seeds []domain.LadoRuleSeed) ([]domain.LadoRuleID, error)        // only missing ids: rule, version, global binding, event
ListLadoRules(ctx, includeArchived bool) ([]domain.LadoRule, error)                         // ordered by created_at, id
LadoRule(ctx, id) (domain.LadoRule, bool, error)
ListLadoRuleVersions(ctx, id) ([]domain.LadoRuleVersion, error)                              // oldest first
CreateLadoRule(ctx, rule domain.LadoRule, event) (domain.LadoRule, error)                    // rule + version 1 + global binding + rule.Overrides
AddLadoRuleVersion(ctx, v domain.LadoRuleVersion, base int, proof *domain.LadoPresenceProof, event) (domain.LadoRule, error) // latest != base -> ErrLadoRuleChanged; reused nonce -> ErrLadoPresenceReplayed
SetLadoRuleArchived(ctx, id, archivedAt time.Time, event) (domain.LadoRule, error)            // zero time unarchives
DeleteLadoRule(ctx, id, event) error                                                          // used by a snapshot -> ErrLadoRuleReferenced
SetLadoRuleBinding(ctx, b domain.LadoRuleBinding, event) error                                // inherit deletes the row
ListLadoModeProjectIDs(ctx) ([]domain.ProjectID, error)
ListLiveLadoTasks(ctx) ([]domain.LadoTask, error)                                             // every project, not cancelled, issue linked, with plan facts
LadoPolicySnapshot(ctx, approvalID domain.LadoPlanApprovalID) (domain.LadoPolicySnapshot, bool, error)
ApproveLadoPlan(ctx, approval, proof, event, snapshot *domain.LadoPolicySnapshot) (domain.LadoPlanApproval, domain.LadoAuditEvent, error) // non-nil: snapshot (ApprovalID = approval.ID) and its rule rows in the same tx
```
Task reads (`ladoTaskPlan`, `ladoProjectPlans`, `ListLiveLadoTasks`) fill `Plan.Snapshot` for the newest approval.

## 5. Policy service (`service/ladopolicy`)

Built-ins (`builtins.go`, seeded at daemon start by `EnsureBuiltins`, version 1, actor `lado`):
| id | name | category | enforcement | protected | config | global |
|---|---|---|---|---|---|---|
| `plan.approval.required` | Plan approval required | workflow | workflow_gate | yes | `{}` | on |
| `agent.permission.ceiling` | Permission ceiling | tools | tool_policy | yes | `{"mode":"auto"}` | on |
| `git.commit.message.korean` | Commit messages in Korean | git | prompt | no | `{}` | off |
Korean instruction: `Write Git commit subjects and bodies primarily in Korean. Conventional Commit prefixes such as feat, fix, docs, refactor, test and chore may stay in English.`

Capabilities (`capabilities.go`): classes `prompt`, `permission_mode`; status `supported|degraded|unsupported|unverified`. claude-code and codex: both supported (evidence: Claude `--append-system-prompt-file` / ACP `_meta.systemPrompt`; Codex `model_instructions_file` / app-server `developerInstructions`; AO maps permission modes for both). cursor: prompt unsupported. fx: prompt degraded. Anything else and missing classes: unverified.

AO instructions (`aorules.go`): `ReadAORules(project domain.ProjectRecord) []AORule` with kinds `worker_instructions` (`Config.AgentRules`), `worker_instructions_file` (`Config.AgentRulesFile`, read under `project.Path` with the same rules as `session_manager/prompt.go` `projectRelativeFile`/`buildProjectRules`; trimmed text hashed; > 4 MiB or unreadable -> `ReadError`), `orchestrator_instructions` (`Config.OrchestratorRules`). Fields `Kind, Path, SHA256, Chars, ReadError`. Snapshots carry only the worker kinds.

Resolution (`resolve.go`, pure): `Resolve(in Input) Resolution`.
```go
type AgentChoice struct { Harness domain.AgentHarness; Model string; Source string } // source "task" | "project" | ""
type Input struct { Rules []domain.LadoRule; ProjectID domain.ProjectID; TaskID domain.LadoTaskID; Agent AgentChoice; PlanCeiling domain.PermissionMode; AORules []AORule; AOBuild string }
type Source struct { Scope domain.LadoRuleScope; ScopeID string; State domain.LadoBindingState }
type Entry struct { Rule domain.LadoRule; On bool; Source Source; Project, Task domain.LadoBindingState } // inherit when no binding
type Resolution struct { Entries []Entry; Ceiling, Mode domain.PermissionMode; Prompt string; Capabilities []Capability; Problems []domain.LadoPolicyProblem; Content SnapshotContent; JSON []byte; SHA256 string }
func (r Resolution) Blocking() bool; func (r Resolution) SnapshotRules() []domain.LadoPolicySnapshotRule
```
1. Non-archived rules, sorted by created_at then id. Protected → ON, source global/on. Otherwise the nearest non-INHERIT wins: task → project → global. Only bindings of the resolved project/task count.
2. Ceiling = mode of `agent.permission.ceiling`; missing or not in `LadoCeilingModes` → `accept-edits` + blocking `LADO_CEILING_MISSING`.
3. Task scope: plan ceiling in `LadoCeilingModes` → `min(plan, ceiling)`, warning `LADO_PLAN_CEILING_CLAMPED {plan, ceiling}` when lowered; empty or `default` → ceiling. Project scope: Mode = ceiling.
4. Prompt = ON rules with prompt enforcement and a non-empty instruction, in entry order:
```
## LADO rules

The developer approved these rules for this run. Follow them together with the project rules above.

### <name>

<instruction>
```
(sections joined by one blank line; no trailing newline; empty string when no rule applies).
5. Task scope only: agent = task.Agent, else project `Config.Worker.Harness`; none → blocking `LADO_AGENT_NOT_CHOSEN`. Required: `permission_mode` always, `prompt` when the prompt is not empty. unsupported → blocking `LADO_AGENT_CAPABILITY_UNSUPPORTED {agent, capability}`; unverified → blocking `LADO_AGENT_CAPABILITY_UNVERIFIED`; degraded → warning `LADO_AGENT_CAPABILITY_DEGRADED`. Codex with mode below `bypass-permissions` → info `LADO_CODEX_ASKS_APPROVAL {mode}`.
6. Conflicts: ON pair A, B where either lists the other → blocking `LADO_RULES_CONFLICT` rules `[A, B]` sorted, once per pair.
7. Unreadable `agentRulesFile` → blocking `LADO_AO_RULES_FILE_UNREADABLE {path, reason}`.
8. Problems sorted by severity (blocking, warning, info), code, rules.
9. Snapshot (`snapshot.go`, format name `lado-policy/1`; field order is the format, a change needs a new name):
```go
type SnapshotContent struct {
	Compiler string `json:"compiler"`; AOBuild string `json:"aoBuild"`; ProjectID string `json:"projectId"`; TaskID string `json:"taskId"`
	Agent SnapshotAgent `json:"agent"`                 // harness, model, permissionMode
	PermissionCeiling string `json:"permissionCeiling"`
	Rules []SnapshotRule `json:"rules"`               // ON rules: ruleId, versionId, version, sha256, name, protected, enforcement, strength, source{scope,scopeId,state}
	AORules []SnapshotAORule `json:"aoRules"`         // worker kinds: kind, path, sha256
	Prompt string `json:"prompt"`; WorkflowGates []string `json:"workflowGates"`
	Capabilities []SnapshotCapability `json:"capabilities"` // class, status
	Problems []domain.LadoPolicyProblem `json:"problems"`
}
```
Canonical bytes: encoder with `SetEscapeHTML(false)`, newline trimmed, every slice non-nil (`[]`). `SHA256` = hex sha256 of the bytes.

Service (`service.go`, `impact.go`):
```go
type Deps struct { Store Store; Projects Projects; Blobs Blobs; AOBuild string; Clock func() time.Time; NewID func() string; Log *slog.Logger }
func (s *Service) EnsureBuiltins(ctx) error
func (s *Service) List(ctx, includeArchived bool) ([]RuleView, error)                // RuleView{Rule; Labels map[string]string} label per "scope:scopeId" (project name, task issue title)
func (s *Service) Rule(ctx, id) (RuleView, []domain.LadoRuleVersion, error)
func (s *Service) Create(ctx, in CreateInput) (ChangeResult, error)                    // CreateInput{Name, Category, Instruction; Conflicts; Scope; ScopeID; On bool; DryRun bool}
func (s *Service) Update(ctx, id, in UpdateInput) (ChangeResult, error)                // UpdateInput{BaseVersion; Name, Category, Instruction *string; Conflicts *[]id; CeilingMode *domain.PermissionMode; Archived *bool; Reason string; DryRun bool; Presence func(action, subject string) (domain.LadoPresenceProof, error)}
func (s *Service) Delete(ctx, id) error
func (s *Service) SetBinding(ctx, in BindingInput) (ChangeResult, error)               // BindingInput{RuleID; Scope; ScopeID; State; DryRun}
func (s *Service) Effective(ctx, in EffectiveInput) (EffectiveView, error)             // EffectiveInput{ProjectID; TaskID}; EffectiveView{Resolution; Task *domain.LadoTask; Approved *ApprovedView}
func (s *Service) Snapshot(ctx, task domain.LadoTask, rev domain.LadoPlanRevision) (domain.LadoPolicySnapshot, error) // for approval; new id lado-snapshot-<uuid>
func (s *Service) CurrentSHA256(ctx, task domain.LadoTask) (string, error)             // the approved (= latest) revision
type ChangeResult struct { Rule RuleView; Impact Impact; Applied bool }
type Impact struct { Projects []ImpactProject /* ID, Name */; Tasks []ImpactTask /* ID, ProjectID, Title, IssueRef */ }
type ApprovedView struct { Snapshot domain.LadoPolicySnapshot; Content SnapshotContent; RevisionNumber int; ApprovedAt time.Time; Matches bool; Added, Removed, Changed []domain.LadoRuleID }
```
- Create at Global: global `on`/`off` from `On`; at Project or Task: global `off` plus that scope `force_on`. Task scope refuses a cancelled task (`LADO_TASK_CANCELLED`).
- Update adds a version only when the content sha changes; `BaseVersion` must equal the active number. Protected rules: name, category, instruction, conflicts and archive → `LADO_RULE_PROTECTED`; only `agent.permission.ceiling` takes `CeilingMode`, which needs `Presence(LadoPresenceRuleChange, ruleID)` (nil → 403 `LADO_PRESENCE_REQUIRED`), actor `human` or `human:dev-presence`, audit `rule.protected_changed` with from, to, version, proof, dev, reason. Dry runs never need presence and write nothing.
- Bindings: protected → `LADO_RULE_PROTECTED`; archived → `LADO_RULE_ARCHIVED`; global takes on/off, project/task take inherit/force_on/force_off (`LADO_BINDING_STATE_INVALID`, `LADO_BINDING_SCOPE_INVALID`); unknown project or task → 404 `LADO_SCOPE_NOT_FOUND`.
- Delete: built-in → `LADO_RULE_BUILTIN`; used by a snapshot → `LADO_RULE_REFERENCED` (archive instead).
- Validation (400): `LADO_RULE_NAME_REQUIRED`, `LADO_RULE_NAME_TOO_LONG` (120 runes), `LADO_RULE_CATEGORY_TOO_LONG` (40), `LADO_RULE_INSTRUCTION_REQUIRED`, `LADO_RULE_INSTRUCTION_TOO_LONG` (8000), `LADO_RULE_CONFLICT_UNKNOWN`, `LADO_RULE_CONFLICT_SELF`, `LADO_RULE_CONFLICT_PROTECTED`, `LADO_RULE_CONFLICTS_TOO_MANY` (20), `LADO_RULE_UPDATE_EMPTY`, `LADO_RULE_CEILING_INVALID`. Others: 404 `LADO_RULE_NOT_FOUND`; 409 `LADO_RULE_CHANGED`, `LADO_RULE_ARCHIVED`; 500 `LADO_RULES_LOAD_FAILED`, `LADO_RULES_SAVE_FAILED`. Errors carry values in `details`.
- Audit actions: `rule.created`, `rule.updated`, `rule.archived`, `rule.unarchived`, `rule.deleted`, `rule.binding_set` (scope, scopeId, from, to), `rule.protected_changed`. Actor `api` for unverified callers, `lado` for seeding.
- Agent for a task: `task.Agent`, else the project's `Config.Worker.Harness`. Plan ceiling: parse the pinned plan blob (`domain.ParseLadoPlan`) of the revision, cached by revision id.
- Impact: projects in LADO mode whose project-scope content changes; live tasks whose valid approval's snapshot sha equals the current sha and would differ after the change. `Applied` false on dry runs.
- Effective for a task with a valid approval also returns `Approved` (stored snapshot, `Matches` = current sha equals it, rule ids added/removed/changed between the two).

## 6. Task service (`service/ladotask`)

```go
type Policy interface {
	Snapshot(ctx context.Context, task domain.LadoTask, rev domain.LadoPlanRevision) (domain.LadoPolicySnapshot, error)
	CurrentSHA256(ctx context.Context, task domain.LadoTask) (string, error)
}
```
`Deps.Policy`. `ApprovePlan(ctx, revisionID, sha256, policySHA256 string, proof)`: after 4b's checks, `policySHA256 == ""` → 400 `LADO_POLICY_SHA_REQUIRED`; Policy nil → 500 `LADO_RULES_UNAVAILABLE`; snapshot sha ≠ `policySHA256` → 409 `LADO_RULES_CHANGED`; store with the snapshot (ApprovalID = approval id); audit payload adds `policySnapshot`, `blocking`. A review_required or blocked task may approve its latest revision again. List, Get, View and the tasks returned by approve/cancel/update fill `Plan.PolicySHA256` for tasks whose approval is valid (errors logged at debug, field left empty).

## 7. HTTP (`/api/v1/lado`, LAN-blocked by the existing prefix; DTOs in `lado_rules_dto.go`)

| Route | Operation id |
|---|---|
| `GET /lado/rules?includeArchived=` | `listLadoRules` → `LadoRuleListResponse{rules}` |
| `POST /lado/rules` | `createLadoRule` → `LadoRuleChangeResponse` |
| `GET /lado/rules/{ruleId}` | `getLadoRule` → `LadoRuleDetailResponse{rule, versions}` |
| `PATCH /lado/rules/{ruleId}` | `updateLadoRule` → `LadoRuleChangeResponse` (raw body ≤ 64 KiB read first; presence header param) |
| `DELETE /lado/rules/{ruleId}` | `deleteLadoRule` → 204 |
| `PUT /lado/bindings` | `setLadoBinding` → `LadoRuleChangeResponse` |
| `GET /lado/effective?projectId=|taskId=` | `getLadoEffective` → `LadoEffectiveResponse` |
| `POST /lado/plans/{revisionId}/approve` | body `ApproveLadoPlanRequest{sha256, policySha256 (omitempty; the service requires it)}` |

JSON (camelCase): `LadoRuleResponse{id, name, category, instruction, builtin, protected, archived, version, versionId, sha256, enforcement[], strength, ceiling?{mode}, conflictsWith[], global, overrides[{scope, scopeId, state, label}], createdAt, updatedAt, archivedAt?}`; `LadoRuleVersionResponse{id, number, name, category, instruction, enforcement[], ceiling?{mode}, conflictsWith[], sha256, actor, createdAt}`; `CreateLadoRuleRequest{name, category, instruction, conflictsWith[], scope, scopeId, on, dryRun}`; `UpdateLadoRuleRequest{baseVersion, name?, category?, instruction?, conflictsWith?, ceiling?{mode}, archived?, reason?, dryRun}`; `SetLadoBindingRequest{ruleId, scope, scopeId, state, dryRun}`; `LadoRuleChangeResponse{applied, rule?, impact{projects[{id, name}], tasks[{id, projectId, title, issueRef}]}}`; `LadoEffectiveResponse{projectId, taskId, agent{harness, model, source}, permissionCeiling, permissionMode, rules[{ruleId, name, category, builtin, protected, version, on, strength, enforcement[], global, project, task, source{scope, scopeId, state}}], aoRules[{kind, path, sha256, chars, readError}], prompt, capabilities[{class, status}], problems[{code, severity, rules[], details{}}], blocking, snapshotSha256, approved?{snapshotSha256, approvedAt, revision, matches, blocking, added[], removed[], changed[]}}`. Task responses: `state` adds `review_required`, `blocked`.
Nil service → `apispec.NotImplemented` (parity test). Wiring: `LadoController.Rules LadoRuleService`; `httpd/api.go` `APIDeps.LadoRules`; `daemon/lado_wiring.go` `wireLadoWork` builds the policy service (`AOBuild: cfg.AppVersion`, blobs `ladoblob.New(cfg.StateDir)`), calls `EnsureBuiltins` (error logged), sets `ladotask.Deps.Policy`, returns it; `daemon/daemon.go` takes the third value and passes `LadoRules`.

## 8. Electron main (`frontend/src/main/lado-presence.ts`)

- Approve: after reading the plan, `GET /api/v1/lado/effective?taskId=<plan.task.id>`; read `snapshotSha256` (hex 64), the count of `rules[].on`, `permissionCeiling`, the count of blocking problems; the dialog detail adds `lado.presence.approveRules` (and `lado.presence.approveRulesBlocking` when > 0); body `{"sha256":…,"policySha256":…}`.
- New channel `LADO_PRESENCE_CHANGE_RULE_CHANNEL = "lado:presence:changeRule"`, request `{ruleId, ceiling:{mode}}`, trusted sender only, mode in the three ceiling modes; `GET /api/v1/lado/rules/<ruleId>` must answer a protected `agent.permission.ceiling`; confirm with `lado.presence.ruleChangeTitle` / `lado.presence.ceilingChangeDetail` (`{{from}}` → `{{to}}` via `lado.presence.mode.{acceptEdits,auto,bypassPermissions}`) and button `lado.presence.change`; sign `PATCH /api/v1/lado/rules/<ruleId>` body `{"baseVersion":<n>,"ceiling":{"mode":"<mode>"}}`; result `{status:"changed", rule} | {status:"cancelled"} | {status:"error", code?, message, details?}`. New keys join `frontend/scripts/sync-tray-catalog.mjs` KEYS; regenerate with `node scripts/sync-tray-catalog.mjs`.
- `shared/lado-presence.ts`, `preload.ts` (`lado.presence.changeRule`), and the `lado` stubs in `renderer/lib/bridge.ts`, `renderer/test/setup.ts`, `e2e/support/fake-bridge.ts`.

## 9. Renderer

- `lib/lado.ts`: types from `schema.ts`; `fetchLadoRules`, `fetchLadoRule`, `createLadoRule`, `updateLadoRule`, `deleteLadoRule`, `setLadoBinding`, `fetchLadoEffective`, `changeLadoCeiling` (bridge `presence.changeRule`; in a browser a direct PATCH that only dev presence accepts); `approveLadoPlan(revisionId, sha256, policySha256)`. Keys `["lado","rules",archived]`, `["lado","rule",id]`, `["lado","effective",projectId,taskId]`. Hooks in `hooks/useLado.ts`; after a change invalidate `["lado","rules"]`, `["lado","rule"]`, `["lado","effective"]`, `["lado","tasks"]`, `["lado","task"]`.
- `lib/lado-rules.ts`: `useLadoRuleChange()` runs the change as a dry run, opens the impact dialog when `impact.tasks` is not empty, applies on confirm, applies directly otherwise; built-in name/description keys by id (`lado.rules.builtin.{planApproval,permissionCeiling,koreanCommits}.{name,description}`), strength labels, problem texts (`lado.policy.problem.*`).
- `lib/lado-errors.ts`: every new error code; `lib/event-transport.ts`: `kind === "lado_rules"` invalidates rules, effective and that project's tasks and task views.
- Settings → Rules (`components/settings/RulesSection.tsx`, catalog id `rules`, `stores/ui-store.ts`): search; card "Protected rules" (Plan approval required: muted "Always on"; Permission ceiling: shared Select → main's native confirmation); one card per category with rows: name, one detail line (category · strength · overrides summary), Switch (global) and a row menu (Edit, History, Archive, separator, Delete); collapsed "Archived" (Unarchive, Delete); quiet "New rule" button in the section header. Dialogs in `components/lado/`: `LadoRuleEditorDialog.tsx` (Name, Category, Instruction, Conflicts with checkbox menu, "On for every LADO project" switch when created at Global), `LadoRuleHistoryDialog.tsx` (versions list + content), `LadoRuleImpactDialog.tsx` (approved tasks that will need a new approval); rows in `LadoRuleRows.tsx`.
- Project settings → Rules (`components/lado/LadoProjectRulesSection.tsx`, project section `rules` in `SettingsDialog.tsx` for local LADO projects only): rows with shared Select Inherit / On / Off, effective state and source; protected rows "Always on"; card "From AO project config" (read-only rows, read errors shown); "New rule for this project".
- Task view (`LadoTaskDialog.tsx` + `components/lado/LadoTaskRules.tsx`): right pane tabs Plan | Rules (`components/ui/tabs.tsx`). Rules tab: agent + mode line, problems (needs-you dot for blocking), approved-vs-now (matches, or added/removed/changed), task overrides, AO instructions, collapsible worker rule text. Approve plan is enabled again for `review_required` and `blocked`, and sends the effective `snapshotSha256` read right before approving.
- Board (`LadoLanes.tsx`): `review_required` → "Rules need review", `blocked` → "Blocked", both attention tone in the Plan lane.
- DESIGN.md decisions log gets one row for these surfaces (S11).

## 10. Steps

| Step | Scope |
|---|---|
| S01 | migration 0005, `queries/lado_rules.sql`, sqlc gen |
| S02 | domain types, task states |
| S03 | store: rules, versions, bindings, built-in seeding |
| S04 | store: snapshot in the approval, plan facts, live tasks, delete guard, CDC |
| S05 | ladopolicy: built-ins, capabilities, AO instructions |
| S06 | ladopolicy: resolve, prompt, snapshot |
| S07 | ladopolicy: service, presence, impact, effective |
| S08 | ladotask: policy, approval snapshot, derived states; approve DTO field |
| S09 | HTTP rules routes, specgen, `npm run api`, api.go, daemon wiring |
| S10 | renderer data layer, errors, events; Electron main presence; bridges; tray catalog |
| S11 | Settings → Rules UI, dialogs, DESIGN.md row |
| S12 | Project settings → Rules, task view Rules tab and re-approval, lanes |
| S13 | docs: TDD, HARNESS, PRD, LADO.md, LADO.ko.md |
