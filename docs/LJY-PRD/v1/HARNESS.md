# LADO — AI Harness Specification

- Version: 1.1 (fork integration revision draft)
- Date: 2026-10-18
- Related: `PRD.md`, `TDD.md`

## 1. Purpose
The harness is a local, versioned policy and context system governing what an AI worker knows, may do, must produce and how its work is checked. It is independent of any particular CLI, API provider or model.

## 2. Core concepts
- **Rule Definition:** stable ID, version, instructions, triggers, enforcement and owner.
- **Rule Binding:** attaches a definition to Global, Project or Task scope.
- **Override:** `INHERIT`, `FORCE_ON`, `FORCE_OFF` for Project/Task bindings.
- **Protected Gate:** owner-controlled security constraint, not overridable by ordinary scope settings or AI.
- **Effective Harness:** deterministic resolved rule set for one task/execution.
- **Harness Snapshot:** immutable serialized effective rules and policy version/hash stored with execution.
- **Agent Profile:** role, output schema, context budget and runtime/model preference.

## 3. Resolution semantics
1. Load active version of each rule definition and its Global binding (default OFF unless explicitly configured).
2. Apply Project override, then Task override; nearest non-INHERIT value wins.
3. Apply protected safety gates as a non-overridable floor.
4. Evaluate trigger and applicability; inactive or nonmatching rules are not injected.
5. Detect mutually exclusive rules; block execution if unresolved conflict affects a mandatory gate.
6. Compile prompt instructions, validators, tool policies and workflow gates separately.
7. Persist the effective list, originating scopes, rule versions, context hashes and a canonical snapshot hash.

For ordinary rules, Global OFF + Project FORCE_ON => ON; Global ON + Project FORCE_OFF + Task INHERIT => OFF; Task FORCE_ON restores ON. Protected rules reject disabling attempts. Disabling does not delete a definition.

## 4. Rule schema (illustrative)
```yaml
id: git.commit.message.korean
version: 1
name: Commit messages in Korean
owner: user
category: git
protected: false
default_enabled: true
triggers: [before_commit]
enforcement: [prompt, validator]
instruction: "Write Git commit subject and body primarily in Korean. Conventional Commit prefixes may remain in English."
validator:
  kind: commit_message_locale
  config:
    language: ko
    allow_prefixes: [feat, fix, docs, refactor, test, chore]
    allow_issue_ids: true
failure_action: block_and_request_fix
```
A locale validator is heuristic: technical terms and short messages can be ambiguous. Report uncertain cases and allow explicit owner review instead of falsely claiming linguistic certainty.

### Binding example
```yaml
bindings:
  global:
    git.commit.message.korean: FORCE_ON
  projects:
    project-a:
      git.commit.message.korean: FORCE_OFF
  tasks:
    task-104:
      git.commit.message.korean: FORCE_ON
```

## 5. Harness Manager UI
- Group by scope, category and trigger; search/filter.
- Show definition version, binding override, effective state, inherited-from source, protected status and enforcement.
- Toggle ON/OFF; provide a third 'Inherit' option at Project and Task levels.
- Preview affected projects/tasks before applying changes.
- Separate Disable, Archive and Delete with confirmations and history preservation.
- Show active policy snapshot on every execution detail page.
- Clearly distinguish **draft**, **saved**, **active** and **effective for current run**.

## 6. Natural-language management
User: "Add a global harness rule requiring Korean Git commit messages."
1. Agent parses request and proposes rule schema, trigger, enforcement and scope.
2. System validates schema, conflicts, permissions, known validators and scope impact.
3. UI shows exact before/after diff, affected tasks and whether enforcement is mechanical or advisory.
4. User approves; transactional write creates a new version/binding and audit event.
5. UI confirms saved state and next-run effective behavior.

For commands like "turn that off for project A", resolve the intended rule unambiguously or ask which rule. Never silently alter protected gates. AI can suggest but cannot bypass approval for security-sensitive changes.

## 7. Enforcement pipeline
- **Prompt instruction:** advisory text in agent context; not a security boundary.
- **Validator:** machine check, e.g. commit-message check, test exit code or output schema.
- **Tool policy:** allowlist/denylist for shell, file, network and Git operations.
- **Workflow gate:** blocks transitions such as execute without plan approval or merge without approval.

Apply hard constraints in host/OS where possible. If a CLI does not expose required controls, mark capability unsupported and block or run in an adequate external sandbox. Never represent a prompt-only rule as enforced.

## 8. Context and specialist profiles
Context Builder assembles approved project knowledge snapshot, current task, accepted plan, relevant source files, repo rules, tool capabilities and budget. Track provenance, tokens, commit SHA and staleness. Do not blindly inject entire repositories. Roles: Analyst, Architect, Developer, Reviewer, Integrator. Profiles are templates, not always-running agents.

## 9. Handoff contract
Each handoff contains `task_id`, `plan_version`, `harness_snapshot_id`, `base_commit`, `result_commit`, `changed_paths`, `artifact_refs`, `evidence_ids`, `tests`, `open_questions` and `permissions`. Reviewer receives original requirements, acceptance criteria, diff and evidence; avoid developer self-congratulation or unnecessary conversation history.

## 10. Execution and change semantics
- Freeze effective harness **at durable queue admission**; snapshot must be reproducible and auditable. If a relevant rule changes before dispatch, mark the queued execution REVIEW_REQUIRED and require re-approval rather than silently replacing its snapshot.
- Ordinary toggles affect future runs, not a currently executing agent.
- Emergency safety revocation may cancel active jobs; audit who changed it and why.
- Changing a plan, source baseline or rule can require revalidation according to impact policy.
- Rule versions referenced by historical executions must remain readable.

## 11. Verification policy
Independent review is optional per stage: `same_model`, `cross_model`, `cross_provider`, `multi_reviewer`. Reviewers are selected from connected supported runtimes. Enforce deterministic tests even when AI review is OFF. Set maximum rounds, time and cost; handle `PASS`, `NEEDS_REVISION`, `FAIL`, `INCONCLUSIVE`. Disagreement should be evidence-led, not a naive majority vote.

## 12. Diagnostics and audit
Check runtime presence, authentication status without exposing secrets, model/tool capabilities, Git/worktree support, project commands, validator availability, contradictory rules, unsupported enforcement and missing required reviewers. Persist rule create/edit/toggle events with actor, timestamp, old/new version, affected scope and reason.

## 13. Minimum acceptance tests
1. Global ON / Project OFF / Task ON resolves to ON.
2. Project INHERIT reflects Global toggle.
3. Protected gate rejects Task FORCE_OFF and AI-originated bypass.
4. Disabling preserves rule history and does not change existing execution snapshots.
5. Korean commit rule blocks a clearly noncompliant commit when validator enabled; permits configured prefixes.
6. Natural-language rule change produces an approval preview, not an immediate silent mutation.
7. Missing CLI enforcement capability is surfaced, not silently ignored.
8. Conflicting mandatory rules block execution with actionable diagnostics.

## 14. Fork-specific application contract (v1.1)
The upstream OrchestratorInc term **agent harness** refers to an execution adapter (Claude/Codex/Gemini); the **LADO harness** refers to versioned policy, context and enforceable gates. Never conflate them. The Go daemon hosts LADO `RuleRegistry`, `RuleResolver`, `PolicyCompiler`, `CapabilityEvaluator`, `ApprovalGuard` and `AuditRecorder` as new domain modules, translating their output into upstream native CLI/Chat driver capabilities.

| Enforcement class | LADO host responsibility | Provider limitation |
|---|---|---|
| `prompt` | Compile ordered instructions and provenance into agent context | Advisory only; cannot guarantee compliance |
| `validator` | Execute registered host-side validators with exit/status and evidence | Custom natural-language validator code excluded from MVP |
| `tool_policy` | Enforce command/path/network constraints at actual tool/OS boundary | Fail closed if driver has unrestricted shell and no sufficient isolation |
| `workflow_gate` | Authorize launch, retries, resumption and integration centrally | UI-only check is never sufficient |

### Versioned write contract
1. Rule has stable `rule_id`; each edit creates immutable `rule_version_id`; a binding references rule ID and an explicit active version or version-selection policy.
2. Global binding is `ON/OFF`; Project/Task overrides are `INHERIT/FORCE_ON/FORCE_OFF`. Resolve nearest explicit ordinary override, then apply protected gates.
3. AI rule edits create `RuleChangeProposal` with schema, target ID/scope, old/new diff, conflicts, capability requirements and impacted queued tasks; nothing mutates before user approval.
4. On approval, insert version/binding changes and audit record atomically; update active version via optimistic concurrency guard.
5. Queue admission stores canonical effective snapshot, selected version IDs, source bindings, rule/validator compiler versions, required capabilities and content hash.
6. A relevant change before dispatch flags the queued execution for re-review; during execution ordinary changes apply next run only. Emergency protected change cancels affected runs and logs the event.
7. Historical snapshots and referenced versions remain readable even after a rule is disabled or archived.

### MVP hard-rule catalog
Use host-owned registered gates: `plan.approval.required`, `integration.approval.required`, `git.protected_branch.no_direct_write`, `test.command.must_pass`, `paths.protected`, and optional `git.commit.message.korean` (heuristic, allow explicit human resolution of ambiguous cases). AI cannot invent and immediately run arbitrary shell validators.

### Fork PoC policy tests
- Global ON / Project FORCE_OFF / Task FORCE_ON => ON; protected FORCE_OFF rejected.
- Toggle OFF preserves definition/history and cannot mutate an admitted snapshot.
- Relevant pre-dispatch policy change => REVIEW_REQUIRED, not automatic snapshot swap.
- Attempt to launch via HTTP/automation/retry without approval => rejected.
- Runtime without required tool-policy enforcement => UNSUPPORTED and blocked.
- Attempt direct merge/push without integration approval => blocked by actual execution boundary, not only by prompt.
- All rule change proposals require human approval and produce immutable audit entries.

**Evidence boundary:** these are LADO requirements and proposed integration contracts, not features proven to exist in upstream. Source basis: LADO_Open_Source_Technical_Review.md (Codex, 2026-10-18; inspected commits AO 6f299014, OpenHands 852132d3, stefan1294 f78e1b06).
