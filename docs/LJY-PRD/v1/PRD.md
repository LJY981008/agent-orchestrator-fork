# LADO — Product Requirements Document

- Version: 1.2 (fork-based revision draft)
- Date: 2026-10-18
- Status: Requirements baseline; OrchestratorInc fork is a conditional technical choice pending Windows PoC

## 1. Product definition
LADO (Local AI Development Orchestrator) is a local-first desktop control center for managing multiple Git projects with AI agents. Users capture work as notes, optionally publish GitHub issues, review evidence-backed proposals and plans, approve execution, monitor isolated background workers, validate results, and approve integration without keeping many IDE windows open.

### Target audience and distribution
Initially a single developer on Windows. Design for future open-source distribution without introducing multi-tenant accounts or cloud services in MVP.

### Goals
- Reduce idle IDE overhead and centralize asynchronous AI coding work.
- Support both officially authenticated subscription-oriented CLI tools and metered API integrations.
- Preserve work and decision history across app/worker crashes.
- Keep users in control of plans, sensitive actions and merges.
- Make significant AI claims traceable to source evidence; allow configurable independent review.
- Provide configurable, individually switchable Global / Project / Task harness rules, including AI-assisted rule authoring.

### Non-goals (MVP)
Cloud execution, multi-user collaboration, remote deployment, automatic production deployment, full autonomous merge, unlimited provider compatibility, guaranteed reproducibility of opaque CLI sessions.

## 2. Core user journey
1. Register a local Git repository and configure its base branch.
2. Sync project context and review generated knowledge/expert profile.
3. Write a task memo; normalize it and optionally create/link a GitHub issue.
4. Review an evidence-backed proposal, risk and priority (Critical/High/Medium/Low).
5. Request and iterate a versioned plan; approve a specific revision.
6. Preview the effective harness, permissions, budget, target commit and worktree.
7. Execute in a dedicated Git worktree via a background CLI or API worker.
8. Run deterministic checks and optional independent AI review.
9. Inspect reports and diffs; approve integration or request revisions.
10. Record outcome, evidence, commits, audit events and clean up safe workspaces.

## 3. Functional requirements
Priority: P0 = MVP; P1 = core expansion; P2 = later.

### Projects & knowledge
- **PRJ-001 P0** Register/edit/remove project paths, repository and base branch.
- **PRJ-002 P0** Explicit full sync with versioned knowledge snapshot, provenance and commit SHA.
- **PRJ-003 P1** Incremental sync, staleness detection, conventions and build command discovery.
- **PRJ-004 P1** Per-project specialist profiles generated from approved project knowledge.
- **PRJ-005 P2** Cross-project dependencies and optional CI/operational signals.

### Tasks, proposals & plans
- **TASK-001 P0** Local task memo, tags, status, priority and optional GitHub issue linkage.
- **TASK-002 P0** Structured proposal with rationale, evidence, assumptions, risks and alternatives.
- **TASK-003 P0** Versioned plan with steps, acceptance criteria, affected areas and test commands.
- **TASK-004 P0** Human approval of an immutable plan revision before code execution.
- **TASK-005 P1** Dependency graph, conflict warnings, plan revalidation against changed code.
- **TASK-006 P1** Sync issue status without treating GitHub as the authoritative execution state.

### Provider and agent execution
- **AI-001 P0** One existing upstream structured CLI driver (Codex app-server or Claude ACP) verified on real Windows, with streaming logs, cancellation and process-tree cleanup.
- **AI-002 P1** Metered API runtime with tool loop, usage tracking and secure API-key storage.
- **AI-003 P1** Multiple providers, runtime/model/credential profiles, per-role assignment.
- **AI-004 P1** Capability detection, explicit fallback policy and unsupported-operation blocking.
- **AI-005 P1** Token/cost estimates when available; never invent subscription remaining quota.

### Harness rules
- **HAR-001 P0** Global, Project and Task rule scopes.
- **HAR-002 P0** Individually toggle ordinary rules ON/OFF; OFF does not delete.
- **HAR-003 P0** Three-state Project/Task override: INHERIT / FORCE_ON / FORCE_OFF.
- **HAR-004 P0** Protected security gates cannot be disabled through lower scopes.
- **HAR-005 P0** Effective harness preview and immutable snapshot fixed at queue admission; relevant policy changes before start trigger review, never silent replacement.
- **HAR-006 P1** AI-assisted natural-language rule draft/edit/toggle with preview and approval.
- **HAR-007 P1** Rule triggers, prompt/validator/tool-policy/workflow-gate enforcement.
- **HAR-008 P1** Conflict detection, versioning, application logs and diagnostics.
- **HAR-009 P2** Import/export of portable rule templates and profiles.

### Execution and integration
- **EXEC-001 P0** Persistent daemon (UI close does not stop workers), LADO-specific durable approved-execution queue and isolated worktree per execution; reuse upstream components where verified.
- **EXEC-002 P0** Stream events; support cancellation, timeout, recovery and reports.
- **EXEC-003 P1** Parallel workers with CPU/RAM/concurrency and budget admission control.
- **EXEC-004 P1** Revalidate baseline commit, detect overlap, schedule dependencies.
- **MERGE-001 P0** Review diff and reports; prohibit unattended base-branch merge/push via enforceable execution boundaries, not only UI confirmation.
- **MERGE-002 P1** Dedicated integration worktree/session, conflict handling, regression tests and PR creation.

### Evidence and verification
- **EVD-001 P0** Attach claims to source references, commit SHAs, tests, logs or explicitly labeled inference.
- **EVD-002 P1** Verify source existence and detect stale evidence; distinguish provenance from claim truth.
- **VER-001 P0** Deterministic build/test/lint gates with actual exit codes and logs.
- **VER-002 P1** Independent same-model, cross-model or cross-provider review, configurable per stage.
- **VER-003 P1** Round limits, failure classification, disagreement handling and explicit inconclusive state.

## 4. UX surfaces
Dashboard; Projects & Knowledge; Task Board; Proposal/Plan History; Execution Monitor; Review & Evidence Center; Harness Manager; Provider & Resource Settings. Harness Manager must show rule name, scope, effective state, source of override, enforcement type, version and last change. Provide per-rule toggles and a task-level effective-policy preview.

## 5. Safety, privacy and trust
- Default deny for destructive commands, protected paths and unattended merge/push.
- No credentials in YAML, prompts, logs, issue bodies or exports.
- Worktree is not a security sandbox; enforce OS-level restrictions where possible.
- Treat repository text, tool output and retrieved documents as untrusted input.
- A rule expressed only in a prompt is advisory, not mechanically enforced.
- AI-produced rule changes are drafts until approved; safety gates require explicit owner action.
- Snapshot policies per execution; changes normally apply to future runs, except emergency revocation.

## 6. Quality requirements and acceptance
- No bypass of required plan/merge approval in automated tests.
- State and event logs survive forced process termination and restart.
- Every completed execution has identifiable plan, harness snapshot, base SHA, diff, checks and report.
- A Global ON rule can be Project OFF and Task ON; protected gates reject FORCE_OFF.
- CLI adapter and API adapter conform to a shared task/event contract by P1.
- Compare resource use with a baseline of multiple VS Code windows; report measurements, not assumed savings.

## 7. Delivery milestones and fork decision
- **M0 (10 working-day Windows fork PoC):** Pin `OrchestratorInc/agent-orchestrator` commit, build Electron/Go, validate one CLI, persistent daemon, central approval guard, minimal harness resolver/snapshot, worktree, deterministic test, crash recovery and no unapproved integration. This is a go/no-go gate, not an MVP.
- **M1:** Single-project task/proposal/plan/approval UI, durable execution state, harness manager, evidence records and human-reviewed report.
- **M2:** Parallel resource-aware scheduling, multiple CLI providers, capability probes, optional API runtime feasibility study.
- **M3:** Natural-language harness proposal and approval, knowledge snapshots, independent verification, provenance/staleness checks.
- **M4:** Integration worktree, conflicts, dependencies, resource budgets, hardened OS/tool enforcement.
- **M5:** Windows installer/update, third-party notices, security review, public-source distribution readiness.

### M0 acceptance gates
1. One supported CLI works in an actual Windows installation, not merely in source/CI.
2. Closing UI leaves the explicitly persistent daemon and in-flight worker running.
3. No worker starts without an approved, unchanged plan revision and bound harness snapshot.
4. Global ON → Project OFF → Task ON resolves ON; protected gates cannot be overridden.
5. An execution snapshot stays unchanged during rule edits; pre-start changes trigger re-review.
6. Actual test exit code blocks success despite an agent claiming completion.
7. Merge/push to protected base is not possible without a separately approved integration action.
8. Cancel/kill cleans the selected driver process tree; daemon restart reconciles without duplicate starts.
9. Unsupported mandatory tool enforcement is reported and blocks dispatch.

## 8. Open decisions and evidence boundary
- **Conditional base:** `OrchestratorInc/agent-orchestrator` (Apache-2.0 root LICENSE), initial stack Go daemon + Electron/React + SQLite; final adoption requires M0.
- **Alternatives:** OpenHands + software-agent-sdk for stronger direct API agent tool-loop reuse; stefan1294 for reference only.
- **Not yet proven:** native Windows build/run, installed CLI login, live cancellation/recovery, full CI status, sandbox guarantees, API runtime and end-to-end bypass resistance.
- **ADRs pending:** initial CLI driver, local authentication and IPC, persistence semantics, Windows tool/OS isolation, GitHub sync, fork maintenance/upstream policy, distribution license and notices.
- **Source basis:** LADO_Open_Source_Technical_Review.md (Codex, 2026-10-18; inspected commits AO 6f299014, OpenHands 852132d3, stefan1294 f78e1b06). The review inspected source but did not run the Windows PoC.

## 9. Traceability
Technical implementation: `TDD.md`. Rule semantics and enforcement: `HARNESS.md`. Each requirement ID must map to design section and test case before release.
