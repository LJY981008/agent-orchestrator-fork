# LADO — Technical design (AO extension spec)

- Version: 2.0
- Date: 2026-10-19
- Base: this repository at `55b02b848` (fork of `OrchestratorInc/agent-orchestrator`)
- Related: [PRD.md](PRD.md), [HARNESS.md](HARNESS.md), [adr/](adr/)
- Previous version: [v1/TDD.md](v1/TDD.md) (1.1). Removed from v1: the conditional-fork wording, the module list, the logical DDL, the TypeScript runtime contract and the IPC command list (they redefined parts of AO), and the PoC day plan and adoption decision.

Paths: Go code under `backend/internal/` is cited without that prefix. Other paths start at the repo root. Line numbers are at `55b02b848`.

## 1. Extension rules (D1)

LADO keeps merging upstream. Every LADO change is either a new file inside one of AO's layers, or a few hook lines in a shared file.

### 1.1 Where new code goes

| Layer | LADO files | AO pattern to copy |
|---|---|---|
| Domain | `domain/lado_*.go` | `domain/automation.go` |
| Ports | `ports/lado_gate.go` (worker gate interface), `ports/lado_issues.go` (issue write port) | `ports/agent_accounts.go`, `ports/tracker.go` |
| Services | `service/ladogate/` (worker gate), `service/ladoproject/` (project settings), `service/ladoconnect/` (repository connection: host tokens, SSH check, link or clone), `service/ladotask/` (issue-backed tasks, plans, approvals), `service/ladoqueue/` (queue, dispatcher), `service/ladopolicy/` (rules, resolution, snapshots), `service/ladosetup/` (local setup inventory, adoption, restore), `service/ladoverify/` (checks, validators, evidence); the gate checks for chat sends in `service/chat/lado_gate.go`; clones into a workspace project in `service/project/lado_workspace.go`, beside the clone and workspace registration helpers it reuses | `service/automation/` |
| Adapters | `adapters/tracker/github/lado_issues.go`, `adapters/tracker/gitlab/lado_issues.go` (issue writes and token checks, on a `LadoIssues` type reached through the tracker's `LadoIssues()`, so an upstream issue-write API cannot collide); a commit status file in `adapters/scm/github/` (§7.1) | AO's existing writes: `adapters/scm/github/merge_action.go`, `adapters/scm/gitlab/merge_action.go` |
| Session manager | `session_manager/lado_gate.go` holds the gate setter and its checks; the field is one hook line in `manager.go` | — |
| Storage | `storage/sqlite/migrations_lado/*.sql`, `storage/sqlite/queries/lado_*.sql`, `storage/sqlite/store/lado_*_store.go` | `storage/sqlite/store/awake_store.go`; narrow store interfaces as in `daemon/awake_wiring.go:13` |
| HTTP | `httpd/controllers/lado_*.go` with DTOs in `lado_*_dto.go` (not in `httpd/controllers/dto.go`) | `httpd/controllers/cues.go:23-65`, `httpd/controllers/artifact_versions_dto.go` |
| API spec | `httpd/apispec/specgen/lado_operations.go` returning `[]operation`; its `init` adds LADO's schema names to `schemaNames` | `cueOperations` in `httpd/apispec/specgen/build.go:1619` |
| Daemon | `daemon/lado_wiring.go`; `daemon/lado_tokens.go` (stored tokens in AO's chains); `daemon/lado_repo_clients.go` (LADO's per-host tracker clients) | `daemon/awake_wiring.go`, `daemon/automation_wiring.go` |
| Renderer | `frontend/src/renderer/components/lado/`, `frontend/src/renderer/lib/lado.ts`, `frontend/src/renderer/lib/lado-errors.ts` (LADO errors in the app's language), `frontend/src/renderer/hooks/useLado.ts`, `frontend/src/renderer/components/settings/RulesSection.tsx`, new route files in `frontend/src/renderer/routes/` | `frontend/src/renderer/routes/_shell.automations.tsx`, `frontend/src/renderer/components/settings/*Section.tsx` |
| Electron main | `frontend/src/main/lado-presence.ts`, `frontend/src/main/lado-autostart.ts` (login item), `frontend/src/main/lado-desktop.ts` (Background settings and IPC), `frontend/src/shared/lado-desktop.ts` (types), `frontend/src/main/lado-ui-locale.ts` (the desktop's Korean default, §11) | `frontend/src/main/daemon-owner.ts`, `frontend/src/main/update-settings.ts` |

Names start with `lado` (packages, files, tables, routes, i18n keys). LADO code never uses the word "harness" for its own concepts, because `sessions.harness` and `domain.AgentHarness` already mean "agent adapter". It says `rule`, `policy` or `setup`.

### 1.2 Hook lines in shared files

This is the complete list of upstream files LADO edits. Each edit stays a few lines that call into LADO code.

| File | Hook |
|---|---|
| `httpd/api.go` | `APIDeps` field (:26-90), `API` field (:119-153), constructor entry (:164-216), `Register` call (:255-283) |
| `httpd/apispec/specgen/build.go` | Append `ladoOperations()` in `operations()` (:706-732) |
| `httpd/lan_listener.go` | Add `/api/v1/lado` to `lanControlBlockedPrefixes` (:72-85) |
| `daemon/daemon.go` | The worker gate, installed right after the session manager is built (:611), before tracker intake (:698) and automations (:871) start; `APIDeps` literal (:982-1036), including the desktop token and its origin for presence proofs (:199-213, [adr/0002](adr/0002-human-presence-approval-channel.md)); `installLadoCredentials` right before the multi-tracker (:549), so every token chain built after it holds the stored tokens; `wireLadoWork` after the project service (:680), with the issue tracking loop it starts and the shutdown wait for it; start and stop of the dispatcher |
| `daemon/lifecycle_wiring.go` | `SetWorkGate` in the `sessionLifecycle` interface (:201-235) |
| `daemon/scm_wiring.go`, `daemon/tracker_wiring.go` | Add the stored github.com and gitlab.com tokens to the token chains right after the `AO_GITHUB_TOKEN` and `AO_GITLAB_TOKEN` env sources (`scm_wiring.go:47-63`, `tracker_wiring.go:21-87`); the GitLab tracker's default chain is rebuilt there with the stored token in it. The stored self-managed GitLab hosts reach the allowlist through `cfg.GitLab` (§5.1) |
| `storage/sqlite/db.go` | Drop LADO change triggers before upstream's first repair (:300); run the LADO track after `reconcileSchema` (:364) (§4.1) |
| `backend/sqlc.yaml` | Add `internal/storage/sqlite/migrations_lado` to `schema` |
| `session_manager/manager.go` | Gate field; gate call at the top of `Spawn` (:945) and `SendWithOptions` (:4788), before an attachment is staged in the target's workspace; LADO section in `buildSystemPrompt` (:5692), which spawn, restore (:3458), Chat start and agent switching all call; launch-home hook next to account env (:6036-6058) |
| `service/chat/service.go`, `service/chat/steer.go` | Gate field; gate call at the top of `Send` (:1213), `Steer` (`steer.go:115`) and `SteerOrSend` (`steer.go:157`, skipped for recovery, which delivers nothing) |
| `httpd/controllers/conversations.go` | Clamp turn settings on `PATCH .../conversation/settings` (:135); in LADO execution sessions, require the `app://renderer` origin and record the actor on approval resolve (:912-939) |
| `service/pr/action_service.go` | Integration approval check before merge (:95-155) |
| `service/project/clone.go`, `service/project/dto.go` | `CloneInput.GitConfig` (`json:"-"`, so HTTP callers cannot set it) becomes `--config` pairs for `git clone` (:165-166), which sets a connection's `core.sshCommand` before the first fetch |
| `service/project/store.go` | `AddLadoWorkspaceRepo` in the `Store` interface, so a clone into a registered workspace adds one repository without rewriting the project row or its other repositories (§5.1) |
| `adapters/agentaccount/share.go` | Read shared entries from the LADO managed home after adoption (:35-38, :62-76) |
| `frontend/src/renderer/components/settings/settingsCatalog.tsx`, `frontend/src/renderer/stores/ui-store.ts` | Rules section entry (:63+) and section id (:27-43) |
| `frontend/src/renderer/lib/event-transport.ts` | Route `payload.kind` values starting with `lado_` (next to :330-336) |
| `frontend/src/renderer/components/SessionsBoard.tsx`, `packages/product-ui/src/SessionsBoardView.tsx` | In a LADO project, render the Plan lane as the grid's `leadingColumn`, also while the project has no sessions. The route file only renders `SessionsBoard` |
| `frontend/src/renderer/components/TaskComposer.tsx` | In LADO projects (this computer's only), submit creates an issue-backed task instead of a session, no worktree is prepared, attachments are refused and effort is hidden; the composer's context row (`frontend/src/renderer/components/lado/LadoComposerContext.tsx`) names the issue's repository, asks for it in a workspace with several, and offers Import issues (§5.1) |
| `frontend/src/renderer/components/NewTaskDialog.tsx`, `GlobalNewTaskDialog.tsx`, `CommandPalette.tsx` | `onIssueTasksCreated` closes the dialog and opens the project's board |
| `frontend/src/renderer/components/CreateProjectFlow.tsx` | A "Connect a repository" source row next to Clone, for this computer only, opening the LADO connect dialog (§5.1) |
| `frontend/src/main.ts` | Presence IPC with the per-launch desktop token (:1397); the Background settings object, its IPC registration, and its keep-daemon default at the three `keepDaemonAlive` calls |
| `frontend/src/preload.ts` | Expose `lado.desktop` and the presence confirmation call to the renderer |
| `frontend/src/renderer/lib/bridge.ts`, `frontend/src/renderer/test/setup.ts`, `frontend/e2e/support/fake-bridge.ts` | `lado` stubs, because each one satisfies the full `AoBridge` type |
| `frontend/src/renderer/components/settings/GeneralSettingsSection.tsx` | Render the LADO Background group after Sessions |
| `frontend/src/renderer/i18n/*.json` | LADO keys appended at the end of each of the 8 locale files; `ko.json`'s existing values reviewed and completed for the Korean default (§11) |
| `frontend/src/main/ui-settings.ts`, `frontend/src/main.ts` | The desktop's UI settings defaults and coercion come from `lado-ui-locale.ts` (§11) |
| `CLAUDE.md`, `AGENTS.md` | The Start at login exception to the `~/.ao`-only rule (§10) |
| `frontend/forge.config.ts`, `frontend/src/shared/posthog-config.ts` and the files in §11 | Fork distribution defaults |

The separate migration track (§4.1) means no upstream migration test needs an edit.

### 1.3 Generated files

`backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts`, `frontend/src/renderer/routeTree.gen.ts` and `backend/internal/storage/sqlite/gen/*` are never merged by hand. On a conflict, take either side and regenerate: `npm run sqlc`, `npm run api`, and the renderer build for the route tree.

### 1.4 Upstream sync

```bash
git remote add upstream git@github.com:OrchestratorInc/agent-orchestrator.git   # once
git fetch upstream
git switch -c sync/upstream-<date> main
git merge upstream/main
# generated files: take one side, then regenerate
npm run sqlc && npm run api
npm run lint && npm run frontend:typecheck
```

`Untrivial-ai/agent-orchestrator`, the name in several defaults, redirects to `OrchestratorInc/agent-orchestrator`. The hotspots in the last 30 days were `httpd/controllers/dto.go` (70 commits), `httpd/apispec/specgen/build.go` (41) and `daemon/daemon.go` (40). LADO stays out of `httpd/controllers/dto.go` and keeps its other edits to the hook lines above.

### 1.5 Contract tests that constrain LADO

- `TestRouteSpecParity` (`httpd/apispec/parity_test.go:41-95`) fails when a mounted route has no spec operation, or the reverse. It builds the router with nil dependencies, so LADO controllers must mount their routes even when their service is nil, and answer with `apispec.NotImplemented` (`httpd/controllers/cues.go:62-65`).
- `TestBuild_MatchesEmbedded` (`httpd/apispec/specgen/build_test.go:71-81`) and the `api-drift` and `sqlc-drift` CI jobs (`.github/workflows/go.yml:274-319`) fail on stale generated files.
- The renderer i18n coverage test (`frontend/src/renderer/i18n/renderer-coverage.test.ts:114-115`) fails on hardcoded English in JSX. LADO strings go in all 8 locale files.

## 2. Architecture

```mermaid
flowchart TD
  subgraph Desktop["Electron"]
    R[Renderer: board + Plan/Queued lanes, task detail, Rules]
    MP[Main: presence confirm + signed proof]
  end
  subgraph Daemon["AO daemon (127.0.0.1, unauthenticated)"]
    API[REST controllers + /api/v1/lado/*]
    LC[ladoconnect: host tokens, SSH, link or clone]
    LT[ladotask: issue-backed tasks, plans, approvals]
    LQ[ladoqueue: queue + dispatcher]
    LP[ladopolicy: rules, snapshots]
    LS[ladosetup: local setup adoption]
    LV[ladoverify: checks, validators, evidence]
    G{{Worker gate}}
    M[AO session manager: Spawn / send]
    C[AO chat service: Send]
    O[AO orchestrator session]
    PJ[AO project service: clone, add]
    TRK[AO tracker reads + LADO issue writes]
    PR[AO PR service: merge]
    SCM[AO SCM observer: pr, pr_checks]
    RV[AO review engine]
    DB[(SQLite: AO tables + lado_* tables)]
  end
  GH[(GitHub / GitLab: issues, ruleset, lado/integration status)]
  R --> API
  MP -->|approval with proof| API
  API --> LC & LT & LQ & LP & LS & LV
  LC -->|clone or link| PJ
  LT -->|create, import, refresh issues| TRK
  TRK --> GH
  LT -->|coordination message with pinned issue| O
  O -->|writes plan.md artifacts| DB
  LT -->|pins artifact versions| DB
  LQ -->|dispatch grant| G
  G --> M
  G --> C
  M --> DB
  SCM --> DB
  LV --> DB
  LV --> RV
  PR -->|integration approval check| LV
  LV -->|post status| GH
  SCM -->|reads| GH
```

AO stays the owner of sessions, worktrees, PR facts and review runs. The tracker stays the owner of issue text and state; LADO keeps pinned copies and the facts it last observed. LADO owns the records before a session exists, the links from a session to its task and plan, rules, verification results and the audit log.

## 3. Domain model and derived state (D7)

### 3.1 Records

| Record | Table | Points at |
|---|---|---|
| Task | `lado_tasks` | `projects.id`; the canonical id of the repository that holds the issue (`issue_repo`, such as `github:owner/repo`), required because a workspace project has several; canonical issue id (`observe/trackerintake/observer.go:260-274`), required and unique among tasks that are not cancelled, and empty only while LADO creates the issue; agent and model preferences; the issue's last observed title, state, URL, snapshot hash and observation time |
| Issue snapshot | None: a blob in the LADO store (§4.5) | Canonical JSON of the issue's canonical id, title and body at the time a plan was requested |
| Plan revision | `lado_plan_revisions` | AO artifact version (session id, path, version, sha256) and a pinned blob, plus `base_ref`, `base_sha` and the sha256 of the issue snapshot it was planned from |
| Plan approval | `lado_plan_approvals` | Plan revision and its sha256; presence proof id |
| Queue entry | `lado_queue_entries` | Plan approval and rule snapshot; claim facts |
| Execution | `lado_executions` | `sessions.id` (primary key), task, plan revision, approval, queue entry, snapshot, base SHAs at dispatch |
| Rule snapshot | `lado_policy_snapshots` | Canonical JSON and its sha256 (HARNESS §8) |
| Integration approval | `lado_integration_approvals` | Execution, PR URL, head SHA; presence proof id |
| Check run | `lado_check_runs` | Execution, head SHA, command or validator, exit code, log blob sha256 |
| Evidence link | `lado_evidence_links` | AO rows: `pr_checks` key, `review_run.id`, artifact version, `lado_check_runs.id`, commit SHA |
| Audit event | `lado_audit_events` | Any of the above |
| Local setup | `lado_setup_items`, `lado_setup_adoptions` | HARNESS §9 |

`lado_executions` follows the precedent of `sessions.issue_id` (`0001_init.sql:25`): AO's session row stays as it is, and the link lives next to it.

Tags and priority (PRD TASK-001) are not stored yet; an issue's labels stay on the tracker. Until plans exist (step 4b), a task that is neither cancelled nor tied to a closed issue is Draft.

Repository connections have no table. A project's repositories are its `origin`, as in AO (`service/project/service.go:800-810`), or, for a workspace project, the `origin` of each repository AO registered under it (`workspace_repos`, `service/project/workspace_registration.go`). Tokens are stored per host (§4.5, §5.1).

### 3.2 Derived state

Nothing below is stored as a state column. Each state is computed at read time, as AO does for sessions (`AGENTS.md:111`).

| State | Derived from |
|---|---|
| Draft | Task exists, no plan revision |
| Planned | The latest plan revision has no valid approval |
| Review required | Queue entry has `review_required_reason` (snapshot no longer matches, base moved, issue edited since planning, plan superseded) |
| Blocked | Queue entry has `blocked_reason` (mandatory enforcement unsupported, unresolved conflict, LADO prerequisites missing) |
| Approved | Valid approval of the latest revision, no queue entry yet |
| Queued | Queue entry without an active lease or session |
| Dispatching | Queue entry with an active lease |
| Cancelled | Task has `cancelled_at` |
| Running | Execution exists and AO places the session in `building` (`backend/pkg/contract/kanban.go:12-29`) |
| Validating | PR exists and the head SHA has pending required checks, LADO checks or required review |
| Integration pending | Required checks, LADO validators and review policy pass for the head SHA; no integration approval for it |
| Integrating | Integration approval exists for the current head SHA; PR not merged |
| Completed | PR merged, and its merged head SHA has an integration approval |
| Closed unmerged | Not Completed, and the task has a closing disposition, the PR closed without merge, or the issue is closed while no execution runs |
| Failed | Dispatch attempts exhausted, or the session terminated without a PR and the task is not closed |

An approval is valid only while its revision is the task's latest and the pinned blob's sha256 still equals the approved hash. An issue edit does not invalidate it, because the approval covers the plan and not the issue; the dispatcher compares issue snapshots before it spawns (§5.3).

### 3.3 Board placement

AO's lanes are derived server-side (`DeriveKanbanPresentation`, `backend/pkg/contract/kanban.go:236-276`) and the renderer orders them in `packages/product-ui/src/session-presentation.ts:336-341`. LADO does not add a session column, because its cards are tasks. The project route renders two LADO lanes in front of AO's grid:

- **Plan**: Draft, Planned, Review required, Blocked. The human acts next.
- **Queued**: Approved, Queued, Dispatching. LADO acts next.

After dispatch the task leaves the LADO lanes and its session card appears in AO's lanes, with the plan revision and snapshot shown on the card. A task card is a new card type that shows the issue reference and title: add it to the DESIGN.md decisions log before building it.

## 4. Storage (D6, amended)

Recorded in [adr/0004](adr/0004-data-same-db-separate-migration-track.md).

### 4.1 Separate migration track

- Directory `storage/sqlite/migrations_lado/`, embedded like `migrations/` (`storage/sqlite/db.go:32-33`), files `NNNN_lado_<name>.sql` numbered from 0001 within the track.
- A goose v3.27.1 provider (`backend/go.mod:18`) in `storage/sqlite/lado_migrate.go`: `goose.NewProvider` with `WithTableName("lado_goose_db_version")` and `WithDisableGlobalRegistry(true)`. Upstream keeps the package-level goose state and `goose_db_version`.
- `migrate` in `storage/sqlite/db.go` (:292-365) gets two hook calls:
  1. Before upstream's first history repair (:300), drop every `lado_cdc_*` trigger. Upstream's repairs before `goose.Up` also rebuild tables by copy and rename, for example `conversation_turns` (:696) and `review` (:1138), so the drop cannot wait until `goose.Up` (:357-361).
  2. After `reconcileSchema` (:364), run the LADO track and then recreate the LADO change triggers (§4.3). `reconcileSchema` (:2026) still changes upstream tables: it adds missing columns, replays the effects of skipped migrations, and rewrites the `sessions` CHECK through `writable_schema` (:2093).

  Every database applies all of upstream (repairs, migrations, reconcile) first and LADO second, whether fresh or upgraded. Building step 2 showed this: the first design put both hooks around `goose.Up` alone.
- sqlc reads both directories in that order (`backend/sqlc.yaml` `schema` list).

Why not 9000-range numbers in the shared table: `migrate_snapshot_test.go:60-68` walks every integer version up to the highest, so 9001 would mean about 8,800 extra steps and 9,001 database images held in memory; `migrate_burned_versions_test.go:266-274` requires each number in a ledger that upstream appends to in every migration; and `WithAllowMissing` applies upstream's later migrations after LADO's only on upgraded databases.

### 4.2 Tables

- Table names start with `lado_`. No LADO migration adds a column, trigger or CHECK value to an upstream table.
- LADO rows refer to upstream rows by id (`sessions.id`, `projects.id`, PR URL) **without** a `FOREIGN KEY`. Foreign keys are on (`storage/sqlite/db.go:40`) and upstream rebuilds tables by copy and rename (`0103_review_run_cdc.sql:5-60`) and deletes session rows; a constraint from LADO would block or break those migrations. Foreign keys are used only between LADO tables. Reads handle a missing upstream row ("session missing").
- LADO project settings live in `lado_project_settings` (`project_id`, LADO mode, build/test/lint commands, integration policy), not in `projects.config`, because AO replaces that JSON as a whole (`service/project/service.go:754`). Step 3 created the table with `project_id`, `lado_mode` and `updated_at`; a project without a row has LADO mode off. The command and integration-policy columns come with the steps that use them.
- Query files (`storage/sqlite/queries/lado_*.sql`) stay ASCII-only. sqlc v1.31.1 (`storage/sqlite/gen/*.sql.go` headers) miscounts byte offsets after a non-ASCII character, and the generated SQL compiles but is cut in the wrong places. Diff `gen/` after every `npm run sqlc`. Found in step 2: a `§` in a header comment cut `LIMIT 1;` short and truncated the column name `prev_sha256` in the generated code.

### 4.3 Change events

AO's rule applies: change events come from DB triggers into `change_log` (`AGENTS.md:116`). LADO triggers sit on LADO tables only and use existing event types, because `change_log.event_type` has a fixed CHECK list (`0103_review_run_cdc.sql:30-48`) that each upstream rebuild re-declares.

- Rows tied to an execution: `session_updated` with that session id and payload `{"kind":"lado_execution", ...}`.
- Rows without a session (tasks, plans, queue, rules): `session_updated` with `session_id` NULL, one row per affected project, payload `{"kind":"lado_task", ...}`. This copies `0151_global_agent_model_catalog_cdc.sql:6-28`. Step 4a's triggers on `lado_tasks` fire when a task gets its issue and when a shown fact changes; a refresh that changes nothing publishes nothing.
- `change_log.project_id` must reference an existing project (`0103_review_run_cdc.sql:32`).
- The renderer routes `lado_*` kinds in `frontend/src/renderer/lib/event-transport.ts`, next to the `model_catalog` branch (:330-336).

LADO change triggers follow one rule, because an upstream rebuild of `change_log` by copy and rename fails while a trigger it does not know writes to that table:

- A LADO trigger whose body names an upstream table, in practice every `change_log` writer, is named `lado_cdc_*` and exists only as a statement in `ladoCDCTriggers` (`storage/sqlite/lado_migrate.go`). LADO migrations never create one.
- On every open, LADO drops every trigger matching `lado_cdc_*` before upstream runs and creates exactly the statements in `ladoCDCTriggers` after the LADO track (§4.1). Editing or removing a statement takes effect on the next open, with no migration.
- Triggers that touch only LADO tables, such as the audit log's append-only guards (§4.4), stay in migrations.
- `TestMigrateDropsLadoChangeTriggersBeforeUpstreamRebuilds` keeps a control: upstream `0103` fails with an unknown trigger on `change_log`. If upstream ever stops failing, the control says the drop step can go.

### 4.4 Audit log

`lado_audit_events` (`id`, `at`, `actor`, `action`, `subject_kind`, `subject_id`, `payload_json`, `prev_sha256`) is append-only: BEFORE UPDATE and BEFORE DELETE triggers on this LADO table raise an error. It is never pruned. `change_log` cannot serve as the audit log because it is pruned after 7 days or 100,000 rows (`cdc/retention.go:12,22`).

`prev_sha256` chains the entries:

- The first entry's `prev_sha256` is 64 zeros. Every later entry holds the sha256 of the entry before it.
- An entry is hashed in a canonical form: each field as a netstring (`<byte length>:<bytes>,`), in the order `id`, `at` (UTC, RFC 3339 with nanoseconds), `actor`, `action`, `subject_kind`, `subject_id`, `payload_json` (as stored; an empty payload is stored as `{}`), `prev_sha256`. The length prefixes make the encoding unambiguous, so no choice of field values gives two entries the same bytes. Changing the form breaks every stored chain.
- Appending reads the latest entry and inserts the next one in one transaction under the store's write lock.
- Verification reads the log in `id` order and reports the first entry whose `prev_sha256` does not match. The chain has no key: it detects an edit to any entry that has a successor, but not an edit to the newest entry, or a writer that rewrites every later entry too. Anchoring it outside the machine is T2 work ([adr/0001](adr/0001-threat-model-t1-first.md)).

### 4.5 Files

All under AO's state directory (`~/.ao`, or `AO_DATA_DIR`; `config/config.go:138-141,611-617`):

| Path | Content |
|---|---|
| `<state>/lado/blobs/<sha[0:2]>/<sha>` | Pinned plans, issue snapshots, knowledge snapshots, check logs (same layout as `artifactversions/blobs.go:24-33`) |
| `<data>/github-pat.json` | AO's own GitHub token file (`service/githubpat/githubpat.go:62-64`; `<data>` is `~/.ao/data` unless `AO_DATA_DIR` is set). LADO stores the github.com token through it |
| `<state>/lado/credentials/gitlab-tokens.json` | Tokens for gitlab.com and self-managed GitLab hosts, with the same permissions as AO's GitHub file: 0600 in a 0700 directory (`service/githubpat/githubpat.go:108-117`). The directory is separate because `<state>/lado/` itself is 0750 (`frontend/src/main/lado-desktop.ts`) |
| `<state>/lado/harness/` | Managed local setup (HARNESS §9.3) |
| `<state>/lado/backups/<adoption-id>/` | Originals and manifest from adoption |
| `<state>/lado/desktop.json` | Desktop Background settings: keep agents running after quit (§10) |

The human-presence key has no file: it is derived in memory from the desktop app's per-launch token ([adr/0002](adr/0002-human-presence-approval-channel.md)).

## 5. Work flow

### 5.1 Repository connection, tasks, plans, knowledge

Recorded in [adr/0006](adr/0006-issues-as-work-items.md). Work items are issues on the project's connected repositories; there is no separate proposal step. A single-repo project has one. A workspace project, such as a folder holding `back/` and `front/` with a git repository each, has one per workspace repository with a GitHub or GitLab `origin`, and any of them can hold issues.

**Connect a repository (PRJ-006).** The connect dialog takes the repository's SSH URL, an access token for its host, and optionally an SSH key file.

- **Host.** Provider and host come from the URL, parsed the way AO parses `origin` (`domain/repository_identity.go:24,36`). github.com, gitlab.com and self-managed GitLab are supported. GitHub Enterprise is not, because AO's GitHub API base is fixed to api.github.com (`adapters/scm/github/client.go:21-22`).
- **Token.** One per host, shared by every project on that host.
  - LADO checks it with the provider's `GET /user`, as the trackers' `Preflight` does, then reads the repository. A classic GitHub token lists its scopes in `X-OAuth-Scopes`; a fine-grained one does not, so LADO probes its reads and reports its writes as unverified. A GitLab personal access token reports its scopes through `GET /personal_access_tokens/self`; when it does not, writes are unverified too.
  - The github.com token goes into AO's own token store (`service/githubpat`). GitLab tokens go into `<state>/lado/credentials/gitlab-tokens.json` (§4.5).
  - Stored tokens join AO's token chains right after the `AO_GITHUB_TOKEN` and `AO_GITLAB_TOKEN` env sources and before the `gh`/`glab` CLIs (§1.2), so AO's PR observer and tracker use them too. Before LADO those used only env and the CLIs, and nothing read AO's stored GitHub token (`service/githubpat/githubpat.go:384` has no caller). The chains read the store on every request, so a stored github.com or gitlab.com token takes effect at once. LADO's own requests use per-host clients built from the same adapters, which read the store the same way.
  - The token must be able to read the repository, its pull requests and branch rules, create issues and post commit statuses. On GitLab that takes the `api` scope, and commit statuses take the Developer role. The dialog lists what is missing or unverified. Connecting needs a readable repository, issue access that is not known to be missing, and working SSH; the other permissions serve later steps and do not block.
  - A self-managed GitLab host joins AO's GitLab host allowlist, with its stored token as the host token unless `AO_GITLAB_HOST_TOKENS` names one. AO reads that config once at startup (`config/config.go:95-106`), so AO's own observers see a newly connected host or a replaced token when the daemon next starts. LADO's own requests reach the host at once.
- **SSH.** The URL must be an SSH URL: `git@host:path` or `ssh://`. LADO runs `git ls-remote` on it with every prompt off: `GIT_TERMINAL_PROMPT=0` and `GCM_INTERACTIVE=Never`, as AO's clone sets (`service/project/clone.go:165-166`), plus `GIT_SSH_COMMAND=ssh -o BatchMode=yes` (with the key file, when given), which AO's clone does not set. An unknown host key fails with instructions to connect once from a terminal; LADO never accepts host keys itself. With a key file, LADO sets `core.sshCommand` (`ssh -i '<key>' -o IdentitiesOnly=yes`, the path single-quoted with forward slashes, without `BatchMode`) in the repository's local config: with `git config --local` for a linked project, with `git clone --config` for a clone. Every git command in that repository and its worktrees then uses the key, agents' pushes included. Without a key file LADO leaves an existing `core.sshCommand` alone. LADO stores the path, never the key.
- **Checkout.** A registered project whose `origin` names the same repository is linked: same provider, host (any port dropped) and path, compared without case; the first by id wins. A workspace project links the same way through any of its repositories, and the key file then goes into that repository's checkout. AO has no such lookup; it only stores the origin (`service/project/service.go:800-810`).
- **Clone.** Otherwise the developer chooses where the clone goes, once the check shows no project to link:
  - its own project, through AO's clone flow (`service/project/clone.go`, the service behind `POST /api/v1/projects/clone`), which also registers the project;
  - the first repository of a new workspace: LADO creates the named folder, clones into it, and registers the folder through AO's `Add` with `AsWorkspace`, which ignores the repository in the folder's root `.gitignore` and commits that (`service/project/workspace_registration.go`);
  - one more repository of a registered workspace: LADO clones into the workspace folder, ignores and commits it the same way, and adds it to `workspace_repos` without rewriting the project row or its other repositories (`service/project/lado_workspace.go`). AO registers a workspace's repositories only when the workspace is added.

  A failure removes what LADO created: the new workspace folder, or the clone and its `.gitignore` change. The connect dialog offers the renderer's default destination, `~/ao/projects` or the last one used (`frontend/src/renderer/components/CreateProjectFlow.tsx:128-133`). The clone is the developer's checkout, not app state; AO's state for it stays under `~/.ao`.
- **Tokens stay in the daemon.** They never reach an agent's environment, prompt, worktree or a log. Agents keep pushing with the machine's git credentials, as in AO.

**Create a task (TASK-001, TASK-002).** In a LADO project the New Task composer (`frontend/src/renderer/components/TaskComposer.tsx`) creates an issue on one of the project's repositories instead of a session. In a workspace with more than one, the developer chooses the repository for each task, and the composer offers the last choice for the project again; the request names it, and the task records it. The first non-blank line becomes the title (at most 256 characters) and the rest the body (at most 65,000), GitHub's limits. The agent and model choice are saved as task preferences. Creating and importing need the project in LADO mode. The composer refuses attachments there, since an issue cannot carry local files, and prepares no worktree.

- **One issue per request.** LADO writes the task row without an issue ref, creates the issue through the write port with a `<!-- lado-task:<task-id> -->` marker in its body, then stores the issue ref. If the daemon stops in between, startup looks for the marker among the 100 newest issues, open or closed, of the repository the task records, and links the match, or deletes the row if there is none. Rows without an issue ref never appear on the board.
- A tracker answer in the 4xx range, rate limits included, means no issue was created: the row is deleted and the composer keeps the text.
- A 5xx, a dropped connection or an unreadable success leaves the outcome unknown. LADO searches for the marker at once: a match is linked, no match deletes the row, and a failed search keeps the row for the tracking loop, which resolves rows without an issue at startup (those older than the start) and on every pass (those older than two minutes).

**Import issues.** The composer's Import issues lists the open issues of each of the project's repositories, at most 200 per repository, grouped by repository in a workspace, through `Tracker.List` (`adapters/tracker/github/tracker.go:250` follows pagination and caches by ETag) and leaves out issues that already have a task that is not cancelled. A repository that cannot be read is named with the tracker's reason, and the other repositories' issues still show. Importing creates tasks, at most 100 per request from any of the project's repositories, and writes nothing to the tracker. It replaces tracker intake, which stays off in LADO projects ([adr/0003](adr/0003-enforcement-points.md)).

**Track issues.** LADO refreshes the issue of every task that is not cancelled every five minutes, one at a time with 15 s per read, pausing the pass after three failures in a row, and whenever a task is read on its own (`GET /lado/tasks/{id}`, the task view), through `Tracker.Get`. It stores the observed title, state, URL and snapshot hash on the task. A read that finds nothing changed updates only the observation time, which publishes no change event.

- AO has no such observer. Intake lists open issues only to spawn workers (`observe/trackerintake/observer.go:183-217`), and `ApplyTrackerFacts` has no caller (`lifecycle/reactions.go:645`).
- GitHub reports a `not_planned` close as cancelled and other closes as done; GitLab reports every close as done (`adapters/tracker/github/tracker.go:216-240`, `adapters/tracker/gitlab/tracker.go:263-268`). Both count as closed in §3.2.

**Request a plan.** LADO reads the issue and pins a snapshot in its blob store: canonical JSON of the canonical id, title and body. It then sends the project's orchestrator a daemon-originated coordination message that carries the snapshot inside AO's trust-boundary text (`session_manager/prompt.go:191-195`) and asks for `lado/<task-id>/plan.md` in the orchestrator's artifact directory. The desktop already starts an orchestrator when a project is added (`frontend/src/renderer/routes/_shell.tsx:608-689`).

- **Orchestrator prompt.** A LADO section in `buildSystemPrompt` states the plan schema, and states that the orchestrator must not run `ao spawn` or `ao send` to workers. The gate enforces the second part (§6).
- **Pinning.** AO records artifact versions after a file is stable across two polls (`artifactversions/capture.go:104-178`, poller every 30 s). For each new version of a LADO path, LADO creates the next revision with the issue snapshot hash of the request, copies the blob into its own store, checks the sha256 and validates the schema. An invalid plan cannot be approved. A user edit in the artifact viewer (`source=user`, `0200_session_artifact_version_source.sql`) is a new revision too.
- **Plan schema.** Markdown with YAML front matter: `task`, `issue` (canonical id), `base_ref`, `steps`, `acceptance_criteria`, `affected_paths` (globs), `test_commands`, `risk`, `permission_ceiling`, `review` (required or not, reviewer).

**The issue is not the plan.** Anyone with access can edit an issue at any time, so:

- Approval covers the pinned plan blob only. The issue enters as the snapshot the plan was made from, whose hash the revision records.
- Before spawning, the dispatcher compares the current issue with that snapshot (§5.3). An edit sends the entry to Review required. The human then requests a new plan, or confirms the current plan against the edited issue; that creates a new revision with the same plan text and the new snapshot, which needs its own approval.
- The worker gets the pinned plan and snapshot (§5.3). A plan that LADO copies into an issue comment (TASK-006) is never read back.
- Edits after dispatch do not reach the running worker. The task shows that the issue changed.

**Knowledge snapshot (PRJ-002).** Same mechanism as a plan, with `lado/knowledge.md`, pinned with the commit SHA of the base.

### 5.2 Approval

Approving a plan revision goes through the human-presence channel ([adr/0002](adr/0002-human-presence-approval-channel.md)). One transaction writes the approval (revision, sha256, proof id), resolves rules into a snapshot (HARNESS §8), creates the queue entry and appends the audit event. If resolution hits a fatal conflict, the entry is created with `blocked_reason` and the user sees why.

### 5.3 Queue and dispatcher

The queue copies the automation claim/lease pattern: `GetNextClaimableAutomationRun` and `ClaimAutomationRun` (`storage/sqlite/queries/automations.sql:111-137`) run in one transaction under the store write lock (`storage/sqlite/store/automation_store.go:180-210`) with a 2-minute lease (`service/automation/scheduler.go:19`). LADO adds a retry cap; AO counts `attempt_count` but never checks it.

`lado_queue_entries` columns: `id`, `task_id`, `plan_approval_id`, `policy_snapshot_id`, `enqueued_at`, `attempt_count`, `claimed_at`, `lease_expires_at`, `lease_token`, `dispatched_session_id`, `review_required_reason`, `blocked_reason`, `failed_at`, `error_message`, `cancelled_at`.

Before spawning, the dispatcher checks in this order:

1. The approval is still valid (§3.2).
2. The issue is open, and its current snapshot hash equals the one the plan revision recorded. Edited: Review required. Closed: no dispatch, and the task shows Closed unmerged. If the issue cannot be read, the entry stays Queued and is retried.
3. Re-resolving the rules gives the same snapshot hash. Otherwise: Review required.
4. The base ref has not moved past the plan's `base_sha` in a way policy forbids. Otherwise: Review required.
5. Admission: per-project and global concurrency caps, and memory pressure (`service/usage/memory.go:73-152`). AO has no admission control today.
6. The chosen agent supports every mandatory enforcement in the snapshot (HARNESS §7). Otherwise: Blocked.

Then it calls `Manager.Spawn` with `ClientRequestID = "lado:<queue-entry-id>"` and `ClientRequestHash` = sha256 of the plan hash and the rule snapshot hash. AO already replays an existing session for a known client request id (`session_manager/manager.go:949-957`), so a retry after a crash never creates a second session.

- The prompt is the plan body, and asks the worker to link the issue from its PR.
- `IssueID` is the canonical issue id and `IssueContext` the pinned issue snapshot, rendered the way AO renders a fetched issue (`service/session/issue_context.go:206-227`). With `IssueContext` set, AO skips its live issue fetch (`service/session/issue_context.go:16-35`) and appends the snapshot to the prompt inside its trust-boundary text (`session_manager/prompt.go:51-56`).
- The agent config, including an explicit permission mode, comes from the rule snapshot.

After `Spawn` returns, one transaction inserts `lado_executions` and sets `dispatched_session_id`. On restart, the dispatcher looks up claimed entries by client request id (`GetSessionByClientRequestID`) and either links the found session or releases the claim.

Cancelling a task with a running execution kills the session through AO. Dirty worktrees are preserved, as AO already does (`session_manager/workspace_cleanup.go:179-188`). Cancelling never closes or edits the issue.

## 6. Worker gate (D5)

Recorded in [adr/0003](adr/0003-enforcement-points.md).

### 6.1 Interface

`ports/lado_gate.go`:

```go
// WorkGate authorizes work in LADO projects. A nil gate keeps upstream behavior.
type WorkGate interface {
	AuthorizeSpawn(ctx context.Context, cfg SpawnConfig) error
	AuthorizeSend(ctx context.Context, target domain.SessionID, origin SendOrigin) error
}
```

`SendOrigin` is human, session, coordination, daemon or automation (§6.3). Step 6 adds `ClampTurnSettings(ctx, target, s ChatTurnSettings) (ChatTurnSettings, error)`, together with the executions and rule snapshots it reads (§6.4).

`service/ladogate` implements the gate over `lado_project_settings` and AO's sessions; a read error refuses the work. `Manager` and the chat service each hold the optional gate, and the daemon installs one gate on both right after the session manager is built, before anything can spawn or send. Projects with LADO mode off are never affected.

### 6.2 Spawn

In a LADO project a new worker session needs a dispatch grant: a `ClientRequestID` of the form `lado:<entry>` that matches a lease the dispatcher holds right now. The rest of the table explains which existing paths that rejects.

| Spawn path (how recognized) | LADO project |
|---|---|
| LADO dispatcher (grant) | Allow, from step 6. Until then no grant exists, and every worker spawn in a LADO project is refused |
| Orchestrator start (`Kind` orchestrator) | Allow |
| `ao spawn` from an orchestrator, `/orchestrators/delegate` (`ParentSessionID` set, `ports/session.go:27-30`; `service/session/delegation.go:66-123`) | Reject |
| Automation run (`AutomationRunID` set; `service/automation/scheduler.go`) | Reject, also when it would start an orchestrator |
| Tracker intake (`observe/trackerintake/observer.go:206-211`) | Reject. LADO mode cannot be turned on while the project's intake is on; AO's own settings can still turn intake on later, and the gate then refuses its spawns |
| Cue invocation (`service/cue/invoke.go`) | Reject |
| Sessions route, `ao spawn` by a person | Reject with "create a task" |
| Restore of an existing session | Not a spawn; allowed |
| Review launch (own launcher, `review/review.go:674`) | Not a spawn; review policy applies (§8.5) |

A refusal is a 409 API error: `LADO_TASK_REQUIRED`, or `LADO_AUTOMATION_REFUSED` for an automation.

### 6.3 Send

The gate classifies a message with fields AO already has: `MessageDeliveryOptions.AuthoredByUser` and `SenderSessionID` (`ports/chat.go:482-487`), `ChatUserMessage.Origin` = human, automation, daemon or provider (`domain/conversation.go:115-122`), and coordination delivery ids (`domain.CoordinationDeliveryID`).

| Origin | Target is a LADO execution |
|---|---|
| Human | Allow |
| Daemon nudge about the session's own PR: CI failure, review comments (`lifecycle/reactions.go:180-240`, reaching `Manager.send` through `daemon/lifecycle_wiring.go:445-463`) | Allow |
| Report relay to the orchestrator (coordination) | Allow |
| Another session (`SenderSessionID` set, for example `ao send` from the orchestrator) | Reject |
| Automation | Reject |

How the hooks read the origin, in this order:

- **`Manager.SendWithOptions`**, which `Send` and the `/send` route use. A `SenderSessionID` makes it another session; `ao send` sets it whenever it runs inside a session (`cli/send.go:94`). Otherwise `AuthoredByUser` makes it human, and a coordination delivery id (`domain.CoordinationDeliveryID`) makes it AO's coordination, such as a report relay. A message with none of these facts counts as the daemon's: lifecycle nudges arrive that way (`daemon/lifecycle_wiring.go:246-248`), and so do cue prompts (`service/cue/invoke.go:83`). Reading the sender session first means a message from a session cannot pass as a person's or as a relay by its text.
- **Chat service.** The conversation routes mark every request human and pass the CLI's sender separately (`httpd/controllers/conversation_steer.go:56-57,124-125`), so a sender session again wins. Then human and daemon origins map directly, and anything else counts as automation.

The gate refuses session and automation origins when the target is a session of a LADO project that is not its orchestrator. Workers there start only from the dispatcher; workers from before the mode was turned on are treated the same. A refusal is a 409 `LADO_SEND_REFUSED`.

The chat hooks are `Service.Send`, `Steer` and `SteerOrSend` (not for recovery, which delivers nothing). `SendForOwner` serves review conversations, which stay AO's; the side-question route only takes people's messages (`httpd/controllers/side_chats.go:176-177`); `RelaySessionChatTurn` is reached from `Manager.send` (`session_manager/chat_spawn.go:418`). Only AO's report relay (`SendSemantic`) and the interface-transition outbox, which replays messages that already passed the gate, call `send` without `SendWithOptions`.

### 6.4 Turn settings

`PATCH .../conversation/settings` (`httpd/controllers/conversations.go:135`) can change the approval mode per turn (`ports.ChatTurnSettings.Approval`, `ports/chat.go:497-505`). For a LADO execution the gate caps it at the snapshot's permission ceiling. Built in step 6, which brings executions and rule snapshots.

### 6.5 Explicit permission mode

LADO executions always get an explicit permission mode from the snapshot. An empty mode becomes `auto` (`session_manager/manager.go:2330-2332`), and Claude's `default` passes no flag, which lets `~/.claude/settings.json` decide (`adapters/agent/claudecode/claudecode.go:160-162`). Built in step 6, where the dispatcher sets it.

### 6.6 Codex permission modes

Today Codex `default` behaves like `bypass`:

- TUI: `--dangerously-bypass-approvals-and-sandbox` (`backend/pkg/agentruntime/command.go:198-208`).
- App-server: approval `never` with sandbox `danger-full-access` (`adapters/chatdriver/codexappserver/driver.go:602-654`).
- Projects store Codex `default` as bypass (`service/project/service.go:1087-1092`), and the editor shows it that way (`frontend/src/renderer/components/ProjectSettingsEditor.tsx:237-239`).
- AO's `accept-edits` and `auto` use `on-request` with `workspace-write` and no network (`adapters/chatdriver/codexappserver/driver.go:609-646`).

The first plan mapped `default` to `workspace-write` with network on and the common git directory writable. Measured on 2026-10-19 with `codex-cli 0.163.0` on Linux (`codex sandbox`, no model call), in both a plain repository and a linked worktree:

| Sandbox | `git add` / `git commit` | Network |
|---|---|---|
| `workspace-write` | Fails: `.git` is read-only | Blocked |
| `workspace-write`, `writable_roots` = the common git directory | Fails: `.git` stays read-only | Blocked |
| `workspace-write`, `network_access = true` | Fails | Works |
| `danger-full-access` (AO `default` and `bypass`) | Works | Works |

Codex keeps `.git` read-only under `workspace-write` whatever the writable roots, and the sandbox settings have no field to change that (`adapters/chatdriver/codexappserver/codexproto/protocol.gen.go:3979-3984`). In that sandbox a commit or push only runs after an approval escalates it.

**Decision (2026-10-19): the global mapping stays upstream's.** Sessions outside LADO projects keep AO's behavior, as §6.1 requires. LADO executions get an explicit mode from the snapshot (§6.5). The Codex ceiling for LADO projects, and whether it grants network access or relies on escalation for git, is decided with the rules core (§15 step 5). Windows needs its own measurement.

## 7. Integration (D5)

### 7.1 Remote: GitHub ruleset

A LADO project's base branch needs a ruleset that requires a pull request and these status checks: the CI checks and `lado/integration`. It must also block force pushes and deletion.

- LADO reads the ruleset (`GET /repos/{owner}/{repo}/rules/branches/{branch}`) when LADO mode is turned on and before each integration approval. AO reads no branch protection for GitHub today. Without the ruleset, the project shows "unprotected: detection only" and each integration needs an explicit acknowledgement.
- After an integration approval, LADO posts a `success` commit status `lado/integration` on the approved head SHA, through a new file in `adapters/scm/github/`. A new push changes the head SHA, which has no status, so GitHub blocks the merge again.
- AO's mergeability fact already turns blocked when GitHub reports `BLOCKED` (`adapters/scm/github/observer_provider.go:631-633,652-667`), so the board stays truthful.
- GitLab projects get issues and tasks (§5.1) but no remote merge control yet. They show "unprotected: detection only", so each integration needs the explicit acknowledgement. The GitLab design, protected branches with external status checks ([adr/0003](adr/0003-enforcement-points.md)), comes later.

### 7.2 AO merge route

`POST /api/v1/prs/{id}/merge` (`httpd/controllers/prs.go:26-55`) runs `service/pr/action_service.go:95-155`, which checks the head SHA, CI, comments and mergeability, but not approval (`domain/pr.go:248-264`). In a LADO project the hook also requires an integration approval for `expectedHeadSha`.

### 7.3 Local base ref (detection)

At dispatch LADO records the SHAs of `refs/heads/<base>` and `origin/<base>`. In VALIDATING and before an integration approval it compares them again and reads `git reflog refs/heads/<base>` for entries after dispatch. Unexpected movement blocks the execution and writes an audit event. If the reflog is off (`core.logAllRefUpdates`), the project shows "detection unavailable".

### 7.4 Integration runs (MERGE-002)

Conflict resolution and regression work after a merge attempt is a new task with its own plan approval, run as a normal execution.

## 8. Verification and evidence (D7)

### 8.1 Required checks (VER-001)

LADO reads the required status checks from the base branch ruleset and compares them with `pr_checks` rows for `commit_hash` = head SHA (`0001_init.sql:72-83`, primary key `(pr_url, name, commit_hash)`). AO already fetches checks through `statusCheckRollup` (`adapters/scm/github/observer_provider.go:394-407,561-592`). All required checks passed on the head SHA → pass.

### 8.2 Local runner (repos without CI)

LADO runs the project's configured commands in the execution worktree at the head SHA. The tree must be clean. The daemon starts the process, not the agent. LADO records the exit code, duration and log blob in `lado_check_runs`. On Windows the runner uses a Job Object so the whole process tree stops, because AO's ConPTY host kills only its direct child (`adapters/runtime/conpty/host_conpty_windows.go:103-105`).

### 8.3 Commit and path validators

In VALIDATING, LADO checks the commits in `base_sha..head_sha` against the snapshot's commit rules (for example Korean messages with English conventional prefixes, HARNESS §5), and the changed paths against the plan's `affected_paths` and the protected paths. Git hooks inside the worktree are only advisory, since `--no-verify` skips them. A violation keeps the execution out of Integration pending.

### 8.4 Agent reports are claims

The worker's `ao report --done` (`storage/sqlite/migrations/0153_reports.sql`, required by `session_manager/prompt.go:323-324`) is a claim, not evidence. Deterministic checks decide.

### 8.5 Review policy (VER-002, VER-003)

When the snapshot requires review, LADO starts an AO review for the head SHA (`httpd/controllers/reviews.go:108-117`). It counts `review_run` rows per execution against a round cap. `approved` passes; `changes_requested` needs revision; failed or cancelled runs are retried up to the cap, then INCONCLUSIVE. The reviewer works in the worker's workspace (`review/review.go:1386`); upstream's reviewer gateway (`reviewgateway/`, `docs/adr/0002-secure-interactive-reviewer-gateway.md`) is the path to isolating it.

### 8.6 Evidence links

`lado_evidence_links` (`id`, `execution_session_id`, `criterion`, `ref_kind`, `ref_key`, `sha`, `created_at`). `ref_kind` is one of `pr_check`, `review_run`, `artifact_version`, `lado_check_run`, `commit`. Each acceptance criterion in the plan links to evidence or is marked "inferred". Evidence whose SHA differs from the current head is stale (EVD-002).

## 9. Rules and local setup: integration points

HARNESS.md has the semantics. The code touch points are:

- **Resolution** at queue admission, stored in `lado_policy_snapshots` (§5.2).
- **Prompt rules**: the LADO section in `buildSystemPrompt` (`session_manager/manager.go:5692`; text assembled in `session_manager/prompt.go:74`), after AO's "## Project Rules" (`session_manager/prompt.go:82-98`). On restore it is rebuilt from the stored snapshot, not from live rules.
- **Adopted local setup**: the launch-home hook next to account env (`session_manager/manager.go:6036-6058`) and the share source in `adapters/agentaccount/share.go` ([adr/0005](adr/0005-local-harness-adoption.md)).
- **Tool policy**: permission mode, Codex sandbox, and `LaunchConfig.AllowedTools/DisallowedTools` (`ports/agent.go:707-735`; Claude TUI flags at `backend/pkg/agentruntime/command.go:279-287`, used today only by reviewers).
- **Capabilities**: `ports.ChatCapability` (`ports/chat.go:170-234`) and the optional adapter interfaces in `ports/agent.go`.

## 10. Windows (D8)

- **Chat mode first.** Codex app-server and Claude ACP run in detached per-session hosts (`docs/architecture.md:10-14`). TUI comes later because the ConPTY host kills only its direct child (`adapters/runtime/conpty/host_conpty_windows.go:103-105`).
- **Keep the daemon.** `AO_KEEP_DAEMON` is off unless set (`frontend/src/main/daemon-owner.ts:12-15`). On Windows, closing the last window quits the app (`frontend/src/main.ts:3521-3525`) and the daemon stops 5 s later (`daemon/daemon.go:1083-1097`). Packaged LADO builds treat keep-daemon as on unless `AO_KEEP_DAEMON` turns it off; dev builds keep upstream's default so a dev loop leaves no daemon behind. The owner becomes `persistent` (`frontend/src/main.ts:1429,1439`). Reopening the app does not reattach to that daemon: a new launch replaces a daemon from another launch gracefully and keeps its persistence mode (`frontend/src/shared/daemon-takeover.ts:44-57`), while Chat hosts keep in-flight turns (`docs/architecture.md:10-14`). The dispatcher therefore has to survive a daemon restart on every app launch (PRD gate 13).
- **Login start.** AO had none (searched `setLoginItemSettings`, `openAtLogin`, `autostart`). Settings > General > Background adds **Start at login** (off by default, packaged builds only) and **Keep agents running after quit** (on by default; `AO_KEEP_DAEMON` wins when set). Windows and macOS use `app.setLoginItemSettings`; Linux writes one `agent-orchestrator.desktop` entry in the XDG autostart directory, an exception to the `~/.ao`-only rule that the owner approved on 2026-10-19 and that `CLAUDE.md` and `AGENTS.md` record. The app opens its window normally at login; AO's tray is macOS-only (`frontend/src/main/tray-lifecycle.ts:5-7`). The keep-running choice is stored in `<state>/lado/desktop.json`.
- **CI.** Only part of the Go suite runs on Windows (`.github/workflows/go.yml:174-178`). Add the LADO packages to that job.
- **Local setup paths.** `%USERPROFILE%\.claude`, `%USERPROFILE%\.claude.json`, `%USERPROFILE%\.codex`. Adoption refuses to run while Claude or Codex processes hold those files.

## 11. Distribution defaults (D9)

| Default | Location | LADO value |
|---|---|---|
| Release and update repository | `frontend/forge.config.ts:24` (`DEFAULT_RELEASE_REPO`; `AO_RELEASE_REPO` overrides at build time) | `LJY981008/agent-orchestrator-fork` |
| Desktop download for `ao start` | `cli/start.go:31` | Fork |
| Version floor download URL | `desktop-version-floor.json:4` | Fork |
| Version floor manifest the app fetches, and its fallback download page | `frontend/src/main/desktop-version-floor.ts:4-5,15-16` (read from upstream's `main`, so upstream could force-quit fork builds) | Fork |
| macOS differential update metadata | `frontend/src/main/mac-differential-v2-protocol.ts:5` (accepted only from this repository) | Fork |
| Feature-build list when no `app-update.yml` is bundled | `frontend/src/main/feature-builds.ts:10` | Fork |
| Linux package homepage | `frontend/forge.config.ts:342,354` | Fork |
| Report-a-problem issue URL | `frontend/src/renderer/lib/report-problem.ts:26` | Fork |
| Report-a-problem support email and Discord | `frontend/src/renderer/lib/report-problem.ts:25,27-28` (upstream's maintainers) | Email goes to the fork owner with no CC; the Discord destination is removed |
| Repository link on Home | `frontend/src/renderer/components/HomePage.tsx:47` | Fork |
| PostHog key | `frontend/src/shared/posthog-config.ts:1` | Removed; telemetry off by default |
| Remote telemetry export | `frontend/src/main.ts:1279` (`posthog` in packaged builds) | Off in every build; explicit env still opts in |
| Sentry DSN for daemon crash reports | `frontend/src/shared/sentry-config.ts:12-13`, stamped on the daemon at `frontend/src/main.ts:1286` | Removed |
| Keep the daemon after quit | `frontend/src/main/daemon-owner.ts:12-15` | On in packaged builds (§10); dev builds keep upstream's default |
| UI language | `frontend/src/shared/ui-locale.ts:6,27-31` (`DEFAULT_LOCALE` and `DEFAULT_UI_SETTINGS`, English) | Korean in the desktop app: a missing or unknown stored language becomes `ko`, and a stored choice is kept. English stays i18n's fallback and source catalog and the language of the browser build and the tests. The Korean catalog was reviewed for tone and completed on 2026-10-20 |
| Pricing manifest | `pricing/fetch.go:16` (upstream raw URL) | Keep; data only |
| Release guard workflow | `.github/workflows/release-latest-guard.yml:17,67` | Already skipped outside upstream |

The mobile app keeps its own PostHog and Sentry constants under `packages/mobile/`; it is outside LADO's Windows desktop scope and unchanged.

Before public distribution: check file-level notices under the root Apache-2.0 license, and keep the private `ao-cloud` submodule out of builds (carried over from v1 §20).

## 12. Upstream PR candidates (D10)

1. Codex `accept-edits` and `auto` cannot commit: Codex keeps `.git` read-only under `workspace-write`, and AO grants that sandbox no network (§6.6). Report it with the measurement.
2. Approval resolve: record who resolved, and optionally refuse resolves without a browser `Origin` (`httpd/controllers/conversations.go:912-939`).
3. Optional spawn/send policy hook in `Manager` with a nil default (§6.1).
4. `ao pr merge` sends an empty body (`cli/pr.go:49`) while the route requires `prUrl` and `expectedHeadSha` (`httpd/controllers/prs.go:45-47`). Reproduce before reporting.
5. The migration snapshot test walks integer versions (`storage/sqlite/migrate_snapshot_test.go:60-68`); walking embedded versions would avoid work on the gaps that already exist.
6. Account sharing creates missing entries in the user's real `~/.claude` (`adapters/agentaccount/share.go:62-76`); a configurable source would help both projects.
7. AO stores a GitHub token (`service/githubpat`, `PUT /api/v1/github/pat`) that its PR observer and tracker never read (`service/githubpat/githubpat.go:384` has no caller). Adding it to the token chains (§5.1) helps both projects.
8. Issue writes. Upstream defers them to its issue #40 (`adapters/tracker/github/tracker.go:26-29`, `domain/projectconfig.go:59-61`). If upstream designs a write port, LADO's port (§1.1) should take its shape, and LADO's GitHub and GitLab adapters can be offered there.
9. `trackerIDForIssue` reads a canonical issue id as a native GitHub id: `github:acme/demo#12` becomes owner `github:acme`, and `gitlab:group/project#7@host` does not parse (`service/session/issue_context.go:37-60`, `:105-133`). Intake spawns pass canonical ids (`observe/trackerintake/observer.go:202-208`), so their issue context is silently missing. LADO's dispatcher sets `IssueContext`, which skips that path (`service/session/issue_context.go:16-18`).

## 13. HTTP surface

All under `/api/v1/lado/`, blocked on the LAN listener. Routes marked P need a human-presence proof.

| Route | Purpose |
|---|---|
| `POST /lado/connections/check` | Check a token, repository access and SSH; writes nothing |
| `POST /lado/connections` | Store the host token, set the SSH key, then link the project or clone where `placement` says: `standalone`, `new_workspace` or `workspace` |
| `GET /lado/credentials`; `PUT, DELETE /lado/credentials/{host}` | Host tokens. No response ever includes a token |
| `GET /lado/tasks?projectId=` (`includeCancelled` optional), `POST /lado/tasks`; `GET, PATCH /lado/tasks/{id}`; `POST /lado/tasks/{id}/cancel` | Tasks. `POST` creates the issue, then the task, and answers 201. `GET` by id reads the issue again |
| `GET /lado/projects/{id}/repositories` | The repositories a project's issues can live in |
| `GET /lado/projects/{id}/issues`; `POST /lado/tasks/imports` | Open issues without a task, with their repository, and the repositories that could not be read; import them as tasks |
| `POST /lado/tasks/{id}/plan-requests` | Ask the orchestrator for a plan |
| `GET /lado/tasks/{id}/plans`, `GET /lado/plans/{revision}` | Plan history |
| `POST /lado/plans/{revision}/approve` (P) | Plan approval |
| `GET /lado/queue` | Queue view |
| `GET /lado/executions/{sessionId}` | Links, checks, evidence |
| `POST /lado/executions/{sessionId}/integration-approve` (P) | Integration approval |
| `GET, POST /lado/rules`; `PATCH /lado/rules/{id}`; `PUT /lado/bindings`; `GET /lado/effective` | Rules |
| `GET /lado/setup/inventory`, `POST /lado/setup/scan`, `GET /lado/setup/conflicts` | Local setup |
| `POST /lado/setup/adoptions` (P), `POST /lado/setup/adoptions/{id}/restore` (P) | Adopt and restore |
| `GET, PUT /lado/projects/{projectId}/settings` (PUT is P when it turns LADO mode off, from step 4b; until step 6 any change of the mode needs `AO_LADO_DEV=on`) | Project settings; step 3 has LADO mode only |

Every LADO error carries a stable code, with the values its message names in `details`; a connection check reports each problem as a code plus the underlying error. The app shows them in the developer's language (`frontend/src/renderer/lib/lado-errors.ts`) and falls back to the daemon's English message for a code it does not know.

The CLI gets read-only commands at most. There is no approval command, by design.

## 14. Tests for the acceptance gates

| PRD §8 gate | Test |
|---|---|
| 1 Spawn paths | `service/ladogate` tests per spawn path (§6.2) and `session_manager` hook tests, plus a `service/ladoqueue` integration test once grants exist |
| 2 Plan hash | `service/ladotask` tests: edit after approval → new revision, old approval invalid |
| 3 Resolution | `service/ladopolicy` table tests (HARNESS §15) |
| 4 Snapshot stability | `service/ladoqueue`: rule edit after admission → Review required, snapshot row unchanged |
| 5 Send origins | `service/ladogate` tests per origin, and hook tests over `Manager.SendWithOptions` and chat `Send`, `Steer` and `SteerOrSend` (§6.3) |
| 6 Agent claims | `service/ladoverify`: report `done` with a failing required check stays Validating |
| 7 Merge | AO merge route test with and without integration approval; manual check against a sandbox GitHub repo with the ruleset |
| 8 Local base | `service/ladoverify` with a temp repo: `git update-ref` from a worktree → blocked |
| 9 Commit validator | Temp repo, commit with `--no-verify` → blocked |
| 10 Presence | Controller tests: no proof, wrong signature, expired, replayed nonce (also after a daemon restart), daemon token not from the app → rejected |
| 11-12 Windows | Manual run on a real Windows install, recorded under `docs/pr-evidence/` |
| 13 Restart | Fault test: kill the daemon between `Spawn` and the link insert → one session, linked after restart |
| 14 Adoption | `service/ladosetup` with a fake home: backup and manifest before removal; restore is byte-identical; credential files untouched |
| 15 Unsupported enforcement | Dispatcher test with an agent lacking a required capability → Blocked |
| 16 One issue per task | `service/ladotask` with a fake issue writer: stop between issue creation and the ref update → one issue, linked after restart (`TestRestartResolvesTasksWithoutAnIssue`); a lost answer → the marker search links the created issue; import → no tracker write (`TestImportWritesNothingToTheTracker`) |
| 17 Issue edits | `service/ladoqueue`: issue body edited after approval → Review required; the spawn config carries the pinned snapshot as `IssueContext` |
| 18 Token isolation | Dispatch with a stored token: its value is absent from the launch environment, the prompt, the worktree and LADO logs. Step 4a covers the connection service's errors and logs, the HTTP responses and the connect dialog (`TestConnectionErrorsAndLogsNeverCarryTheToken`); the launch environment and prompt come with dispatch in step 6 |

Storage tests seed from `migratedDatabaseSnapshot(t, version)` (`storage/sqlite/migrate_snapshot_test.go:34`) and run `Open` or `migrate` on the copy. One fresh `Open` replays every upstream migration, about 12 to 17 s under `-race`. Measured in step 2: the whole `storage/sqlite` package takes about 16 minutes under `-race` on the owner's machine (977 s on `main`); CI splits it into 4 shards with a 20-minute timeout each (`.github/workflows/go.yml:119-141`).

## 15. Build order (agreed 2026-10-19, by dependency)

Each item is its own branch and PR in the fork (`AGENTS.md`: one issue per PR). The `upstream` remote was added on 2026-10-19.

1. Fork defaults, in two PRs:
   - 1a `chore/lado-fork-defaults`: §11, and keep-daemon on by default (§10).
   - 1b was the global Codex `default` mapping. Dropped on 2026-10-19 after measuring it (§6.6); LADO executions set Codex modes through the snapshot instead.
   - 1c `feat/lado-login-start`: login start with a settings toggle (§10).
2. `feat/lado-storage-track`: the LADO migration track and the audit log (§4.1, §4.3, §4.4). It adds no other table; each later step adds the tables it uses.
3. `feat/lado-worker-gate`: the worker gate's spawn and send checks (§6.1-§6.3), `lado_project_settings` with LADO mode (§4.2) and its settings routes (§13). The mode switch stays behind `AO_LADO_DEV` until step 6: without the dispatcher a LADO project cannot start workers. Turn-setting clamps (§6.4) and explicit modes (§6.5) need executions and the rule snapshot, so they moved to step 6.
4. Issue-backed tasks, plans and approvals, in two PRs:
   - 4a `feat/lado-issue-tasks`: repository connection (host tokens, SSH check, link or clone), the issue write port for GitHub and GitLab, task creation and import, and issue tracking (§5.1, [adr/0006](adr/0006-issues-as-work-items.md)), plus a minimal Plan lane: Draft and Closed unmerged task cards with Cancel. The lane moved here from 4b on 2026-10-19 so that created and imported tasks are visible. Workspace projects joined 4a on 2026-10-20: every workspace repository can hold issues, the developer chooses the repository per task, and a clone can start a workspace or join one.
   - 4b: plan requests and revisions, approvals, the presence channel, the plan states in the Plan lane, the Queued lane and the task view (§3, §5.1, §5.2). The approval records the approval and its audit event; step 6 adds the rule snapshot and the queue entry to that transaction.
5. Rules core: scopes, toggles, protected gates, resolution and snapshots (HARNESS §3-§8, HAR-001 to HAR-005). It comes before the queue because admission (§5.2) and dispatch (§5.3) need the snapshot's hash, permission ceiling and agent config.
6. Approved queue and dispatcher (§5.3), with dispatch grants in the gate, turn-setting clamps (§6.4) and explicit permission modes (§6.5).
7. Local setup adoption (HARNESS §9, HAR-010 to HAR-015).
8. Verification, evidence and integration approval (§7, §8). Steps 7 and 8 do not depend on each other.

The first proposal had seven steps. It left keep-daemon and login start (§10) out of every step, and put the rules system (its step 6) after the approval and queue steps that need its snapshot.
