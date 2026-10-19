# LADO — Requirements (AO extension spec)

- Version: 2.0
- Date: 2026-10-19
- Base: this repository, a fork of `OrchestratorInc/agent-orchestrator` (origin `LJY981008/agent-orchestrator-fork`), mapped at `55b02b848`
- Related: [TDD.md](TDD.md) (extension points and design), [HARNESS.md](HARNESS.md) (rules and local harness), [adr/](adr/)
- Previous version: [v1/PRD.md](v1/PRD.md) (1.2). v1 judged AO as a candidate base. v2 treats this repository as the product and lists only what LADO adds.

Paths: Go code under `backend/internal/` is cited without that prefix (for example `session_manager/manager.go:945`). Other paths start at the repo root.

## 1. What LADO is

LADO (Local AI Development Orchestrator) is this AO fork with three additions:

1. **Approved work before AO sessions.** Task memo → proposal → versioned plan → human approval → execution as an AO session → verification → human-approved integration.
2. **Rules (the "LADO harness").** Global / Project / Task rules resolved into an immutable snapshot per execution. The developer's existing local agent setup (CLAUDE.md, AGENTS.md, hooks, skills, plugins, MCP servers) is adopted into one LADO-managed location and managed there.
3. **Windows-first operation and fork-owned distribution defaults.**

Everything else is AO and stays AO: the daemon, sessions, worktrees, agent adapters, Chat/TUI controllers, PR/CI observation, review, artifacts, the board, accounts and usage. Section 4 says for each requirement what AO already has.

Target user: one developer on Windows. Open-source distribution may follow. Multi-tenant accounts and cloud execution are not planned.

### 1.1 Vocabulary

AO already uses several of LADO's words with other meanings. These docs use the right column.

| Term | AO meaning (upstream code) | LADO meaning (these docs) |
|---|---|---|
| harness | Agent adapter id: `sessions.harness`, `domain.AgentHarness` (`claude-code`, `codex`, ...) | Rules plus adopted local agent setup that govern an agent. Code and UI say **rules**; `DESIGN.md:287` bans "Harness" as a settings label |
| task | The New Task dialog, which spawns a session immediately | A pre-session work item (`lado_tasks`) |
| plan | The agent's own in-turn steps (`domain.ConversationPlan`, `domain/conversation.go:465-493`) | A plan artifact revision, approved by its sha256 |
| review | AO code review of a PR (`review/`) | Same thing; VER-002 uses it |
| approval | A Chat tool-permission request (`conversation_activities`, kind approval) | Human approval of a plan revision or an integration, through the human-presence channel |
| execution | — | One AO session linked to a task, a plan revision and a rule snapshot (`lado_executions`) |
| orchestrator | Project coordinator session that spawns workers with `ao spawn` (`session_manager/prompt.go:197-267`) | Same session. In LADO projects it only writes proposals and plans |

### 1.2 Goals

- Run AI coding work in the background without keeping many IDE windows open.
- Keep the human in control of plans, merges and rule changes.
- Make every outcome traceable: plan hash, rule snapshot, base SHA, head SHA, checks, review.
- Manage all agent rules and local agent setup from one place, with toggles, history and restore.
- Use both subscription CLIs and the metered API-key agents that AO already integrates.

### 1.3 Non-goals

Cloud execution, multi-user collaboration, automatic production deployment, unattended merge, and defending against a deliberately adversarial agent (T2, §2).

## 2. Threat model and what the controls guarantee

Recorded in [adr/0001](adr/0001-threat-model-t1-first.md).

- **T1, in scope: agent mistakes and prompt drift.** The agent leaves the approved plan, starts extra workers, edits outside scope, merges or pushes, writes non-compliant commits, skips tests, or reports success that the checks do not support.
- **T2, out of scope for now: a prompt-injected agent that tries to evade controls.** Agents run as the same OS user as the human. They can call the unauthenticated loopback API (`AGENTS.md:108`; requests without an `Origin` header pass CORS, `httpd/cors.go:13-27`) and have `ao` on PATH (`agentlaunch/env.go:74-95`). From a worktree they can move local `main` with `git update-ref refs/heads/main HEAD` and skip commit hooks with `--no-verify` (both reproduced). Defending against this needs OS isolation, such as a separate OS user or an AppContainer (the direction upstream records in `docs/adr/0002-secure-interactive-reviewer-gateway.md`).

| Control | Against T1 | Against T2 |
|---|---|---|
| Spawn/send gate in the daemon | Prevents | Bypassable (direct process or DB access) |
| GitHub ruleset with a required `lado/integration` check | Prevents remote merge | Bypassable (the agent holds the same GitHub token) |
| Local base-ref check (SHA and reflog) | Detects | Detects unless the reflog is rewritten |
| Commit validator on `base..head` | Detects and blocks integration | Same |
| Human-presence approval | Prevents approval by an agent | Forgeable (a same-user process can read the key from the app's or daemon's memory) |
| Prompt rules | Advisory | None |

Every UI and doc surface labels a control with its real strength: prevents, detects or advisory. Nothing is described as sandboxed.

## 3. User journey on AO surfaces

| # | Step | AO today | LADO change |
|---|---|---|---|
| 1 | Register repo and base branch | Add Project flow (`frontend/src/renderer/components/CreateProjectFlow.tsx`); `ProjectConfig.DefaultBranch` (`domain/projectconfig.go:24-26`) | Keep. Add a per-project LADO mode switch |
| 2 | Project knowledge | None | Add a pinned knowledge snapshot (P0, PRJ-002) |
| 3 | Task memo, optional GitHub issue | New Task spawns at once; tracker port is read-only (`ports/tracker.go:9,21-25`) | Add tasks and a Plan lane; creating issues needs a tracker write port (P1) |
| 4 | Proposal | Orchestrator session (`session_manager/prompt.go:197-267`) | Orchestrator writes a proposal artifact only |
| 5 | Plan revision and approval | Artifact versions with sha256 (`storage/sqlite/migrations/0196_session_artifact_versions.sql`) | Plan revision = pinned artifact version; approval bound to its sha256 |
| 6 | Preview rules, permissions and base | None | Add an effective-rules preview |
| 7 | Execute | `Manager.Spawn` (`session_manager/manager.go:945`) in worktree branch `ao/<session>/root` | Keep. Only the LADO dispatcher may start workers in LADO projects |
| 8 | Checks and review | PR checks (`pr_checks`), AO review (`review/review.go`) | Add required-check gate, local runner, commit validator, review policy |
| 9 | Integration | Merge route `POST /api/v1/prs/{id}/merge` (`httpd/controllers/prs.go:26-55`) | Add integration approval, `lado/integration` status, GitHub ruleset check |
| 10 | Record and clean up | Archive, worktree cleanup, worker reports (`storage/sqlite/migrations/0153_reports.sql`) | Add audit log and evidence links |

## 4. Requirements and AO status

Status: **HAVE** = AO has it and LADO uses it as is. **EXTEND** = AO has the base and LADO adds new files and small hooks. **ADD** = new LADO code. Priority: P0 = first usable LADO, P1 = next, P2 = later.

### 4.1 Projects and knowledge

| ID | P | Requirement | Status | AO today | LADO change |
|---|---|---|---|---|---|
| PRJ-001 | P0 | Register, edit and remove projects, repository and base branch | HAVE | `service/project/service.go` (Add :194, UpdateSettings :704, Remove :827 archives); routes `httpd/controllers/projects.go:26-38`; `DefaultBranch` ("auto" = repo default) | Add LADO mode and LADO project settings in `lado_project_settings`, not in `projects.config` (TDD §4) |
| PRJ-002 | P0 | Explicit sync into a versioned knowledge snapshot with provenance and commit SHA | ADD | None. Nearest: orchestrator prompt, `AgentRulesFile` | Orchestrator run writes `knowledge.md`; LADO pins it with sha256 and commit SHA, like a plan (TDD §5.1) |
| PRJ-003 | P1 | Incremental sync, staleness, conventions, build/test command discovery | ADD | Setup and cleanup scripts `PostCreate`/`PreRemove` (`domain/projectconfig.go:35-38`); run configurations in `.ao/launch.json` (`previewserver/manager.go:31-32`); no build/test command field | LADO project settings hold build/test/lint commands (feeds VER-001); staleness = snapshot SHA vs base head |
| PRJ-004 | P1 | Per-project specialist profiles from approved knowledge | EXTEND | Role overrides `Worker`/`Orchestrator` (`domain/projectconfig.go:49-53,160-163`), `Reviewers` (:58) | Profile = role + agent config + rule set, stored by LADO |
| PRJ-005 | P2 | Cross-project dependencies, CI/ops signals | ADD | — | Later |

### 4.2 Tasks, proposals and plans

| ID | P | Requirement | Status | AO today | LADO change |
|---|---|---|---|---|---|
| TASK-001 | P0 | Task memo, tags, status, priority, optional GitHub issue link | ADD | No task table or route (searched migrations, `domain/`, `frontend/src/renderer/routes/`). Issue ids exist: `sessions.issue_id` (`0001_init.sql:25`), canonical form `github:owner/repo#N` (`observe/trackerintake/observer.go:260-274`) | `lado_tasks` with `issue_ref` in AO's canonical form; status is derived (§5) |
| TASK-002 | P0 | Structured proposal: rationale, evidence, assumptions, risks, alternatives | EXTEND | Orchestrator session; per-session artifact directory named in the prompt (`session_manager/manager.go:5895-5918`) | LADO prompt section tells the orchestrator to write `proposal.md` in a fixed schema; LADO pins the version |
| TASK-003 | P0 | Versioned plan: steps, acceptance criteria, affected paths, test commands | EXTEND | `session_artifact_versions` (sha256, version per path); at most 50 versions per path, older ones pruned (`artifactversions/capture.go:27-37`); deleted with the session on kill with `deleteArtifacts` (`artifactversions/delete.go:33-60`) | `lado_plan_revisions` points at (session, path, version, sha256). LADO copies the bytes into its own content store so pruning cannot remove an approved plan |
| TASK-004 | P0 | Human approval of one exact plan revision before code runs | ADD | None; AO approvals are tool-permission requests | `lado_plan_approvals` bound to sha256, through the human-presence channel ([adr/0002](adr/0002-human-presence-approval-channel.md)) |
| TASK-005 | P1 | Dependency graph, conflict warnings, plan revalidation against changed code | ADD | — | Task dependency table; dispatcher checks base drift and overlapping paths |
| TASK-006 | P1 | Issue status sync without making GitHub the source of execution state | EXTEND | Tracker reads (`adapters/tracker/github/tracker.go`); `lifecycle/reactions.go:645` `ApplyTrackerFacts` exists but is not wired | Show issue state on the task; write back through a new tracker write port |

### 4.3 Agents and providers

| ID | P | Requirement | Status | AO today | LADO change |
|---|---|---|---|---|---|
| AI-001 | P0 | One structured CLI driver verified on real Windows, with streaming, cancel and process-tree cleanup | HAVE, verify | Codex app-server and Claude ACP drivers (`adapters/chatdriver/registry/registry.go:82-101`) in detached per-session hosts (`docs/architecture.md:10-14`) | Chat mode first; verify on Windows (TDD §10) |
| AI-002 | P1 | Metered API runtime with tool loop, usage tracking, key storage | HAVE | API-key adapters such as OpenHands, OpenCode, Aider, Goose, Qwen, Crush, Cline (`adapters/agent/*/auth.go`); AO's built-in Unreal Agent with OpenAI/OpenRouter/Fireworks/Ollama providers | Use the existing adapters; no new runtime |
| AI-003 | P1 | Multiple providers, model and credential profiles, per-role assignment | HAVE / EXTEND | 37 TUI adapters (`adapters/agent/registry/registry.go:55-96`), 16 Chat drivers; per-folder Claude/Codex accounts (`0210_agent_accounts.sql`, `service/agentaccounts/`); role overrides | Per-role assignment comes from LADO profiles |
| AI-004 | P1 | Capability detection, explicit fallback, blocking of unsupported operations | HAVE / EXTEND | `ports.ChatCapability` flags (`ports/chat.go:170-234`), production floor (:246-251), optional adapter interfaces (`ports/agent.go`) | Map rule enforcement classes to capabilities; an unsupported mandatory rule blocks dispatch (HARNESS §7) |
| AI-005 | P1 | Token and cost estimates when available; never invent quota | HAVE | Usage collector for claude-code, codex, kimi (`service/usage/collector.go:60-97`); account limits read from the CLIs (`service/agentaccounts/`); Chat cost is nil for subscriptions (`ports/chat.go:598-616`) | Show per execution |

### 4.4 Rules and local harness (details in HARNESS.md)

| ID | P | Requirement | Status | AO today | LADO change |
|---|---|---|---|---|---|
| HAR-001 | P0 | Global, Project and Task scopes | EXTEND | Project-only prompt rules `AgentRules`, `AgentRulesFile`, `OrchestratorRules` (`domain/projectconfig.go:40-47`), injected by the system prompt builder (`session_manager/prompt.go:82-98,147-169`). No global setting exists | `lado_rule_*` tables. AO project rules appear as Project-scope rules |
| HAR-002 | P0 | Toggle ordinary rules ON/OFF; OFF does not delete | ADD | — | HARNESS §4 |
| HAR-003 | P0 | INHERIT / FORCE_ON / FORCE_OFF at Project and Task scope | ADD | — | HARNESS §4 |
| HAR-004 | P0 | Protected gates cannot be disabled from lower scopes | ADD | — | HARNESS §4 |
| HAR-005 | P0 | Effective preview; snapshot fixed at queue admission; a relevant change before start triggers review, never silent replacement | ADD | — | HARNESS §8 |
| HAR-006 | P1 | AI-drafted rule changes with preview and approval | EXTEND | One-shot `chat.Service.RunBackgroundTask` (`service/chat/background.go:13-46`), no HTTP route | LADO route wraps it; the result is a draft |
| HAR-007 | P1 | Triggers and enforcement: prompt, validator, tool policy, workflow gate | EXTEND | Prompt: system prompt builder. Tool policy: permission modes, `LaunchConfig.AllowedTools/DisallowedTools` (`ports/agent.go:707-735`). Gates: none | HARNESS §6 |
| HAR-008 | P1 | Conflict detection, versioning, application log, diagnostics | ADD | — | HARNESS §10 |
| HAR-009 | P2 | Import/export of rule templates and profiles | ADD | — | Later |
| HAR-010 | P0 | Discover local agent setup for Claude Code and Codex: user- and project-level instruction files, settings (hooks, permissions), skills, subagents, commands, plugins, MCP servers | ADD | No listing UI (searched `httpd/controllers/`, `frontend/src/renderer/components/settings/`). Chat-only skills list (`httpd/controllers/conversations_skills.go:16-40`). Codex app-server offers unused `config/read` with layers and origins (`adapters/chatdriver/codexappserver/codexproto/protocol.gen.go:1225-1277`) | Inventory of path, tool, kind, scope, sha256 (HARNESS §9.1) |
| HAR-011 | P0 | Adopt into one LADO-managed location: back up the original, copy it into the LADO path, remove it from the original location; AO-launched sessions use the managed copy | ADD | AO account homes link `skills`, `plugins`, `agents`, `commands`, `hooks` and top-level `*.md` from `~/.claude` (`adapters/agentaccount/claude.go:26-30,268-274`) and recreate missing ones there (`adapters/agentaccount/share.go:35-38,62-76`) | Managed home under `~/.ao/lado/harness/`; account linking reads from it ([adr/0005](adr/0005-local-harness-adoption.md)) |
| HAR-012 | P0 | Toggle, create, edit and delete adopted items after a diff preview and approval; versioned | ADD | — | Repo-tracked files are not moved; they change through git (HARNESS §9.5) |
| HAR-013 | P0 | Detect conflicts among AO's own injected setup, LADO rules, adopted items and repo files | ADD | AO injects worktree hooks into `.claude/settings.local.json` (`adapters/agent/claudecode/hooks.go:11-16`), Codex hooks as `-c` flags (`adapters/agent/codex/hooks.go:72-116`), an `ao` MCP server (`session_manager/chat_spawn.go:345-373`) and a system prompt | HARNESS §10 |
| HAR-014 | P1 | Detect drift: items that reappear at original locations; offer re-adoption | ADD | — | HARNESS §9.6 |
| HAR-015 | P0 | Restore adopted items to their original locations from backup | ADD | — | Ships together with HAR-011 |

### 4.5 Execution and integration

| ID | P | Requirement | Status | AO today | LADO change |
|---|---|---|---|---|---|
| EXEC-001 | P0 | Persistent daemon, durable approved-execution queue, isolated worktree per execution | EXTEND | Daemon with detached Chat hosts; `AO_KEEP_DAEMON` defaults off (`frontend/src/main/daemon-owner.ts:12-15`), so the daemon stops 5 s after the app quits (`daemon/daemon.go:1083-1097`); one worktree per session (`adapters/workspace/gitworktree/workspace.go:277-355`); claim/lease pattern for automations (`storage/sqlite/queries/automations.sql:111-161`) | Keep-daemon on by default and start at login; `lado_queue_entries` with claim/lease; dispatcher (TDD §5.3) |
| EXEC-002 | P0 | Event streaming, cancel, timeout, recovery and reports | HAVE / EXTEND | CDC → SSE, kill and restore, worker reports (`0153_reports.sql`) | Per-execution timeout in the dispatcher |
| EXEC-003 | P1 | Parallel workers with concurrency, CPU/RAM and budget admission | ADD | No session limit or admission control (searched `session_manager/`, `service/`, `config/`). Memory pressure exists for display only (`service/usage/memory.go:73-152`) | Dispatcher admission: per-project and global caps, memory pressure |
| EXEC-004 | P1 | Revalidate base commit, detect overlap, schedule dependencies | ADD | `sessions.diff_base_sha` is the merge-base at spawn (`0040_add_session_diff_base.sql`, `session_manager/manager.go:2060-2096`) | Dispatcher and VALIDATING checks |
| MERGE-001 | P0 | No unattended merge or push to the base branch, enforced outside the UI | EXTEND | Merge route checks head SHA, CI, comments, mergeability (`service/pr/action_service.go:95-155`, `domain/pr.go:248-264`) but requires no approval; agents open PRs, AO never does (`session_manager/prompt.go:273,284`) | GitHub ruleset with required `lado/integration` check, merge-route hook, local base-ref detection ([adr/0003](adr/0003-enforcement-points.md)) |
| MERGE-002 | P1 | Separate integration session, conflict handling, regression tests, PR creation | EXTEND | Agent-created PRs; dirty worktrees are preserved, never force-deleted (`adapters/workspace/gitworktree/discard.go:149-199`) | An integration run is its own execution with its own approved plan |

### 4.6 Evidence and verification

| ID | P | Requirement | Status | AO today | LADO change |
|---|---|---|---|---|---|
| EVD-001 | P0 | Claims linked to source refs, SHAs, tests or logs, or labelled as inference | EXTEND | Facts that carry SHAs: `pr_checks.commit_hash` (`0001_init.sql:72-83`), `review_run.target_sha` (`0012_add_review_tables.sql:31`), artifact sha256 (`0196`), reports (`0153`) | `lado_evidence_links` point at those rows instead of copying them |
| EVD-002 | P1 | Source existence and staleness checks | ADD | — | Compare evidence SHAs with current head and base |
| VER-001 | P0 | Deterministic build/test/lint gates with real exit codes and logs | EXTEND | PR checks through GitHub `statusCheckRollup` (`adapters/scm/github/observer_provider.go:394-407,561-592`). Required checks and branch protection are not read for GitHub | Gate on the base branch's required checks for the head SHA (read the ruleset). Local runner only for repos without CI |
| VER-002 | P1 | Independent review, configurable per stage | HAVE / EXTEND | AO review picks the reviewer in order (`review/review.go:1884-1908`), records the target SHA, supports auto-review; needs a PR (:337-338); the reviewer works in the worker's workspace (:1386) | Policy decides when review is required and with which agent |
| VER-003 | P1 | Round limits, failure classes, disagreement, explicit inconclusive state | EXTEND | No round limit; auto-review stops after 3 failed runs per head (`review/review.go:131`); review nudges capped at 3 (`lifecycle/reactions.go:18`) | LADO counts `review_run` rows per execution; adds INCONCLUSIVE |

## 5. Work item states

States are derived and never stored, as `AGENTS.md:111` requires for sessions. TDD §3.2 has the derivation rules.

- **Before a session exists**, from LADO records: Draft, Proposed, Planned, Review required, Blocked, Approved, Queued, Dispatching, Cancelled.
- **After the session exists**, from AO facts plus LADO verification records: Running, Validating, Integration pending, Integrating, Completed, Closed unmerged, Failed.

## 6. UX surfaces

| LADO surface | AO surface it extends | Notes |
|---|---|---|
| Task board | Project board (`frontend/src/renderer/components/SessionsBoard.tsx`, `packages/product-ui/src/SessionsBoardView.tsx`) | Two lanes before AO's Building lane: **Plan** (the human's turn) and **Queued** (the machine's turn). Cards in them are tasks, not sessions: record that in the DESIGN.md decisions log before UI work |
| Task detail: proposal and plan history, approval | Session view patterns; artifact viewer | Approve opens a native confirmation ([adr/0002](adr/0002-human-presence-approval-channel.md)) |
| Execution monitor | Session view and inspector (`frontend/src/renderer/components/SessionInspector.tsx`) | Inspector shows task, plan revision, rule snapshot, base SHA |
| Review and evidence | PR, checks and review panes in the inspector | Evidence links and integration approval |
| Rules manager | Settings dialog (`frontend/src/renderer/components/SettingsDialog.tsx`, section registry `frontend/src/renderer/components/settings/settingsCatalog.tsx`) | Section name **Rules**. Includes adopted local items and the adoption flow |
| Provider and resource settings | Settings → Agents and Accounts (`DESIGN.md:287`) | Keep |
| Dashboard | Home and board | No new dashboard (`DESIGN.md:95`) |

## 7. Safety rules

- In a LADO project, no worker session starts without an approved, unchanged plan revision.
- Credentials never appear in rules, prompts, logs or exports. Adoption never moves credentials, login state or session history.
- A worktree is not a sandbox. The UI says that same-user agents (T2) are out of scope.
- Repository text, tool output and issue bodies are untrusted input. AO already wraps issue context in trust-boundary text (`session_manager/prompt.go:191-195`).
- A rule that exists only in a prompt is advisory, and the UI says so.
- AI-drafted rule changes stay drafts until approved. Protected gates and local-harness adoption or restore need human-presence approval.
- Rule changes apply to future executions; an admitted snapshot never changes. Emergency revocation cancels affected runs and writes an audit event.

## 8. Acceptance gates (T1)

These replace v1's M0 gates. "W" marks gates that must also pass on a real Windows install.

1. A worker spawn in a LADO project without a dispatcher grant is rejected for every path: the sessions route, `ao spawn` from an orchestrator, `/orchestrators/delegate`, automations, tracker intake, cues.
2. Editing a plan after approval produces a new sha256; the old approval cannot dispatch it.
3. Global ON → Project FORCE_OFF → Task FORCE_ON resolves ON; a protected gate rejects FORCE_OFF.
4. A rule edit after queue admission marks the entry Review required and leaves its snapshot unchanged.
5. CI and review nudges reach the linked session; `ao send` from the orchestrator to a worker is rejected.
6. A failing required check keeps the execution out of Integration pending even when the agent reports `done`.
7. An agent's `gh pr merge` is refused by GitHub without a `lado/integration` success for that head SHA; AO's merge route refuses without an integration approval.
8. Moving the local base branch from a worktree (`git update-ref`) is detected and blocks integration.
9. A non-compliant commit message made with `--no-verify` blocks integration (validator on `base..head`).
10. Approval routes reject calls without a human-presence proof, including curl and the `ao` CLI.
11. (W) Closing the window leaves the daemon and Chat hosts running; reopening the app replaces the daemon gracefully (`frontend/src/shared/daemon-takeover.ts:44-57`) while sessions and in-flight Chat turns continue (`docs/architecture.md:10-14`); login start works.
12. (W) Cancel or kill leaves no orphaned child processes for the Chat drivers.
13. A daemon restart during dispatch creates no duplicate session, and the queue lease recovers.
14. Adoption writes the backup and its manifest before removing any original; restore returns byte-identical files; credentials are untouched.
15. An unsupported mandatory enforcement blocks dispatch with a diagnostic.

## 9. Decisions

| # | Decision | Recorded in |
|---|---|---|
| D1 | Keep syncing upstream. New code goes in new files inside AO's layers; shared registry files get only small hooks; generated files are regenerated, never merged | TDD §1 |
| D2 | These docs are an AO extension spec | This rewrite |
| D3 | Defend against T1 now; T2 needs OS isolation and comes later | [adr/0001](adr/0001-threat-model-t1-first.md) |
| D4 | The loopback API stays unauthenticated; approvals go through a separate human-presence channel: a native confirmation in Electron main and an HMAC proof whose key is derived in memory from the app's per-launch token. Mechanism settled 2026-10-19 | [adr/0002](adr/0002-human-presence-approval-channel.md) |
| D5 | Enforcement points: GitHub rulesets for remote merge, detection for the local base ref, Spawn/send gate for workers; LADO projects keep tracker intake and automations off; Codex "default" stops mapping to bypass | [adr/0003](adr/0003-enforcement-points.md) |
| D6 | Same SQLite DB; LADO migrations on a separate track (own directory and goose version table, after upstream); `lado_` tables only; no columns or triggers on upstream tables; separate audit table. Amended 2026-10-19 from "9000-range numbers" | [adr/0004](adr/0004-data-same-db-separate-migration-track.md) |
| D7 | Execution = AO session + link table; derived state; plan approved by hash; queue uses claim/lease; evidence links to AO records; VER-001 uses GitHub required checks first; AI-002 uses existing API-key adapters; commit validator checks `base..head` | TDD §3, §5, §8 |
| D8 | Windows: Chat mode first, TUI later; keep-daemon on by default plus login start | TDD §10 |
| D9 | Releases from the fork; PostHog key removed and telemetry off by default | TDD §11 |
| D10 | Fixes that help upstream become upstream PR candidates | TDD §12 |
| D11 | Local harness is adopted into one LADO-managed location (back up, copy, remove original); CRUD after approval; repo-tracked files change through git | [adr/0005](adr/0005-local-harness-adoption.md), HARNESS §9 |
| D12 | Build in 8 steps by dependency, one branch and PR each; fork defaults split into three PRs; the rules core comes before the queue. Agreed 2026-10-19 | TDD §15 |

## 10. Traceability

TDD §14 maps every acceptance gate to its test. Each requirement ID must map to a TDD section and a test before release.
