# LADO step 6 — Approved queue and dispatcher: plan (contracts)

> 승인: 2026-10-23 소유자 — 체인 러너 13단계 × $25. 소유자 결정 2026-10-23: 범위 "6단계 전부", 베이스 이동은 "기록만 하고 진행", 동시 실행 상한은 "화면에서 설정"(기본 전체 2·프로젝트 1·메모리 tight 대기), 실행 시간 상한 "기본 8시간". 이 문서가 단계 엔트리의 공통 계약이다. 단계 엔트리가 이 문서보다 구체적이면 엔트리를 따른다.
> 근거 스펙: `docs/LJY-PRD/TDD.md` §3, §4.2-§4.3, §5.2-§5.3, §6, §9, §10, §13, §14 (gates 1, 13, 15, 17, 18), §15 step 6; `docs/LJY-PRD/HARNESS.md` §8, §12, §14; `docs/LJY-PRD/PRD.md` EXEC-001..004, HAR-005.

## 0. Rules for every step

- Read `AGENTS.md` and `CLAUDE.md`; UI steps also read `DESIGN.md`. Code comments in English, matching nearby density. New code goes in LADO files (`lado_*`, `ladoqueue`, `ladogate`, `ladotask`) inside AO's layers; shared upstream files get only small hook edits (TDD §1.2). Never use the word "harness" for LADO concepts (say rule, policy, setup, agent).
- Query files `backend/internal/storage/sqlite/queries/lado_*.sql` stay ASCII only (sqlc v1.31.1 cuts SQL after non-ASCII); read the generated `gen/*.sql.go` after `npm run sqlc`. Never hand-edit `gen/`, `openapi.yaml`, `frontend/src/api/schema.ts`, `routeTree.gen.ts`, `tray-catalog.generated.ts`.
- Store tests use `sqlitetest.MustOpen(t)`; no fresh `sqlite.Open` per test. LADO tables never get FOREIGN KEYs to upstream tables (`sessions`, `projects`); joins to them in queries are fine.
- Renderer strings go in all 8 locale files (`frontend/src/renderer/i18n/{en,ko,ja,zh-CN,es,fr,de,pt-BR}.json`, flat keys, appended after the existing LADO keys) with real translations; Korean follows the fork's glossary (규칙, 권한 상한, 대기열, 실행, 확인 필요, 차단됨, 실패, 승인). The coverage test fails on hardcoded English in JSX. No particle right after a placeholder in Korean (rephrase instead of `을(를)`).
- `<button>` ignores Tailwind font classes (unlayered button rule): style an inner `<span>`.
- The Write tool turns `\u` escapes into real characters; write such strings with python if they must stay escaped.
- No `git push`, `gh`, PRs, publishing or writes outside the worktree (the guard refuses them). Never touch the real `~/.ao`.
- Subagents and long commands only in the foreground (`run_in_background: false`): when a `claude -p` session ends its last turn, it waits a bounded time for background work and then kills it, and the step ends without its commit.
- The design and its approvals are done: skip brainstorming and planning gates of any skill and do not ask the user questions — implement this step, commit, rerun the gates, fill After/회고.
- Every step leaves `go build ./... && go vet ./...` green, and from S10 on also `npm --prefix frontend run typecheck`.

## 1. Scope

In: a queue entry written in the plan approval transaction; the dispatcher (claim and lease, the six checks of TDD §5.3, Chat spawn, link, restart recovery, retry with backoff, admission caps and memory hold, maximum run time); the dispatch grant in the worker gate; explicit permission modes; the per-turn approval cap; refusing agent and interface changes of an execution; the worker prompt's LADO rules section, rebuilt on restore; cancel kills a running execution; task states Queued, Dispatching, Running, Failed, Completed and Closed unmerged after dispatch; dispatch settings and per-project caps on screen; the LADO mode switch in the app (no `AO_LADO_DEV`); `GET /lado/queue`, `GET /lado/executions/{sessionId}`, `GET/PUT /lado/dispatch-settings`; the active snapshot on the execution's session view and a LADO line on its board card; docs.

Out (owning step): tool allow and deny lists (no rule needs them, and Chat and the Codex TUI cannot apply them); dependency scheduling and path-overlap checks (TASK-005, EXEC-004 overlap); Validating, Integration pending, Integrating and the integration approval (8); local setup and setup drift (7); cancelling runs on an emergency protected-rule change; releases (9).

Decisions (record in docs at S13):

a. Approving a plan writes its queue entry in the same transaction, one entry per approval. A newer approval of the task supersedes the task's open entries.
b. A valid approval made before step 6 has no entry; the task shows Review required with reason `approved_before_queue`, and approving again queues it.
c. Executions run in Chat mode (`RequestedMode = chat`): D8 puts Chat first on Windows, only Chat has per-turn approval settings to cap, and the prompt is not passed on a command line. If Chat cannot start for the agent, the attempt fails and is retried.
d. Each attempt has its own client request id `lado:<entry-id>/<attempt>`, because AO keeps a failed request id unusable. The hash is `sha256(plan_sha256 + "\n" + snapshot_sha256)` in hex. The gate grants a worker spawn only for an id whose entry is leased right now for that attempt, in the same project, with that hash.
e. Base moves are recorded, not enforced: the execution stores the plan's `base_sha`, the base SHA at dispatch and how many commits the base moved. The plan's `base_ref` must name the project's worktree base branch (AO cannot start a worker from another base); otherwise the entry is Blocked with `base_ref_mismatch`.
f. Admission: at most `maxConcurrent` running executions overall (default 2, 1-8) and per project (default 1, 1-8), and none while memory pressure is tight when `holdOnMemoryTight` is on (default on). Tight uses the product UI thresholds (`packages/product-ui/src/resource-presentation.ts`): psi > 20, memorystatus >= 4, available_pct > 90. A held entry waits; it does not use an attempt.
g. Maximum run time: `maxRunMinutes` (default 480; 0 = no limit; the UI offers off, 1, 2, 4, 8, 12, 24 hours). A running execution past its deadline is killed through AO (a dirty worktree stays), marked timed out, and the task shows Failed.
h. Retries: three attempts per entry, then Failed. Waits before attempt 2 and 3: 30 s, 2 min. An unreadable issue or an admission hold waits without using an attempt; the issue is read again after 1 min.
i. Per-turn approval: `PATCH .../conversation/settings` caps the approval at the snapshot's ceiling (TDD §6.4); `default` counts as above every ceiling (Codex runs `default` as bypass, Claude lets `~/.claude/settings.json` decide). A config option that would choose a mode above the ceiling is refused with `LADO_APPROVAL_ABOVE_CEILING`, because the provider switches before AO records it. An agent switch or an interface transition of an execution is refused with `LADO_EXECUTION_FIXED`.
j. The LADO rules text reaches the worker as an additional system-prompt section, found through the session's client request id, so restore, resume and every rebuild take it from the stored snapshot, never from live rules. The section is omitted when the snapshot has no prompt text.
k. After dispatch: Running while the session lives or its PR is open; Completed when the PR merged (step 8 adds the integration approval); Closed unmerged when the PR closed unmerged, or the issue closed while no execution runs; Failed when attempts ran out, the run timed out, or the session ended without a PR.
l. LADO mode needs no `AO_LADO_DEV` any more (the variable is dropped). Turning it on still refuses while tracker intake is on; turning it off needs presence (desktop confirmation; dev presence in a browser). Mode off pauses dispatch for that project (entries wait with `lado_mode_off`); running executions go on.
m. Dispatch settings and per-project caps change without presence and write audit events with actor `api`.
n. The worker prompt is the approved plan file (front matter and body) with a short lead and the instruction to link the issue from the PR. `IssueContext` is the pinned issue snapshot of the approved revision, rendered like LADO's `lado/<task>/issue.md` (every text fenced).

## 2. Storage — LADO migration `0006_lado_queue.sql`

```sql
-- +goose Up
CREATE TABLE lado_queue_entries (
    id                     TEXT PRIMARY KEY CHECK (id <> ''),
    task_id                TEXT NOT NULL REFERENCES lado_tasks (id),
    project_id             TEXT NOT NULL CHECK (project_id <> ''),
    plan_approval_id       TEXT NOT NULL UNIQUE REFERENCES lado_plan_approvals (id),
    policy_snapshot_id     TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    enqueued_at            TIMESTAMP NOT NULL,
    attempt_count          INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at        TIMESTAMP,
    lease_token            TEXT,
    claimed_at             TIMESTAMP,
    lease_expires_at       TIMESTAMP,
    waiting_reason         TEXT NOT NULL DEFAULT '',
    review_required_reason TEXT NOT NULL DEFAULT '',
    blocked_reason         TEXT NOT NULL DEFAULT '',
    error_message          TEXT NOT NULL DEFAULT '',
    dispatched_session_id  TEXT,
    failed_at              TIMESTAMP,
    superseded_at          TIMESTAMP,
    cancelled_at           TIMESTAMP
);
CREATE INDEX lado_queue_entries_by_task ON lado_queue_entries (task_id, enqueued_at);
CREATE INDEX lado_queue_entries_open ON lado_queue_entries (enqueued_at)
    WHERE dispatched_session_id IS NULL AND failed_at IS NULL AND superseded_at IS NULL
      AND cancelled_at IS NULL AND review_required_reason = '' AND blocked_reason = '';

CREATE TABLE lado_executions (
    session_id         TEXT PRIMARY KEY CHECK (session_id <> ''),
    task_id            TEXT NOT NULL REFERENCES lado_tasks (id),
    queue_entry_id     TEXT NOT NULL UNIQUE REFERENCES lado_queue_entries (id),
    plan_revision_id   TEXT NOT NULL REFERENCES lado_plan_revisions (id),
    plan_approval_id   TEXT NOT NULL REFERENCES lado_plan_approvals (id),
    policy_snapshot_id TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    attempt            INTEGER NOT NULL CHECK (attempt > 0),
    plan_base_sha      TEXT NOT NULL DEFAULT '',
    dispatch_base_sha  TEXT NOT NULL DEFAULT '',
    base_commits_ahead INTEGER,
    started_at         TIMESTAMP NOT NULL,
    deadline_at        TIMESTAMP,
    timed_out_at       TIMESTAMP
);
CREATE INDEX lado_executions_by_task ON lado_executions (task_id, started_at);

CREATE TABLE lado_dispatch_settings (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    max_concurrent       INTEGER NOT NULL CHECK (max_concurrent BETWEEN 1 AND 8),
    max_run_minutes      INTEGER NOT NULL CHECK (max_run_minutes >= 0),
    hold_on_memory_tight INTEGER NOT NULL CHECK (hold_on_memory_tight IN (0, 1)),
    updated_at           TIMESTAMP NOT NULL
);

ALTER TABLE lado_project_settings
    ADD COLUMN max_concurrent INTEGER NOT NULL DEFAULT 1 CHECK (max_concurrent BETWEEN 1 AND 8);

-- +goose Down
-- (drop in reverse order; SQLite drops the added column with ALTER TABLE DROP COLUMN)
```

- `session_id` and `dispatched_session_id` name `sessions.id` without a FOREIGN KEY (TDD §4.2). `project_id` is copied from the task so dispatch can filter without a join.
- No settings row means the defaults (2, 480, on). A project without a `lado_project_settings` row has cap 1.
- CDC (`ladoCDCTriggers` in `lado_migrate.go`, never in the migration): a queue entry insert, and an update that changes any of `lease_token`, `waiting_reason`, `review_required_reason`, `blocked_reason`, `dispatched_session_id`, `failed_at`, `superseded_at`, `cancelled_at`, `attempt_count`, publishes `{"kind":"lado_task","taskId":...,"projectId":...}` (TDD §4.3). An execution insert or an update of `timed_out_at` publishes `{"kind":"lado_execution","sessionId":...,"taskId":...,"projectId":...}`. Settings tables publish nothing.
- Queries in `queries/lado_queue.sql` (ASCII only), named for what they do: insert and supersede entries; list entries for tasks; claimable entries (open, lease empty or expired, `next_attempt_at` empty or due, project in LADO mode); claim (compare-and-set on the lease, attempt_count + 1); release (clear the lease, set `next_attempt_at` and `waiting_reason`); hold (set `waiting_reason`, keep attempts); mark review required, blocked, failed; cancel a task's entries; insert an execution and set `dispatched_session_id` (one transaction); count live executions overall and per project (join `sessions` on `is_terminated = 0`, plus entries leased now); executions past their deadline whose session lives; mark timed out; read and upsert dispatch settings; set a project's cap.

## 3. Domain (`domain/lado_queue.go`, `domain/lado_task.go`)

- `LadoQueueEntryID string`; `LadoQueueEntry` with every column above (times as `time.Time`, zero when NULL; `DispatchedSessionID SessionID`).
- `LadoQueueState` derived by `(e LadoQueueEntry) State(now time.Time)`: `superseded`, `cancelled`, `dispatched`, `failed`, `review_required`, `blocked`, `dispatching` (lease token set and lease not expired), `waiting` (open with a `waiting_reason` or a future `next_attempt_at`), `queued`.
- Reason constants (stable strings, sent to the renderer):
  - review: `rules_changed`, `issue_edited`, `plan_superseded`, `approved_before_queue`.
  - blocked: `blocking_snapshot`, `base_ref_mismatch`, `agent_unsupported`.
  - waiting: `slot_global`, `slot_project`, `memory_tight`, `issue_unreadable`, `retry`, `lado_mode_off`.
- `LadoExecution` with every column above, plus read-side facts: `SessionTerminated bool`, `PR LadoExecutionPR{URL string; Number int; Merged, Closed bool}` (newest PR of the session, empty when none).
- `LadoDispatchSettings{MaxConcurrent, MaxRunMinutes int; HoldOnMemoryTight bool; UpdatedAt time.Time}` with `DefaultLadoDispatchSettings()` and `Validate()` (`LADO_DISPATCH_SETTINGS_INVALID`: cap 1-8, minutes in {0, 60, 120, 240, 480, 720, 1440}).
- `LadoTask` gains `Queue LadoQueueEntry` (the entry of the newest approval, zero when none) and `Execution LadoExecution` (the newest execution of the task, zero when none).
- New task states: `queued`, `dispatching`, `running`, `failed`, `completed`. `approved` stays in the enum for compatibility but `State()` no longer returns it. `State()` order:
  1. cancelled;
  2. with an execution: PR merged → completed; PR closed unmerged → closed_unmerged; timed out, or session ended without a PR → failed; otherwise running;
  3. issue done or cancelled → closed_unmerged;
  4. valid approval and rules changed → review_required (`rules_changed`); entry review reason → review_required;
  5. valid approval and blocking snapshot → blocked (`blocking_snapshot`); entry blocked reason → blocked;
  6. entry failed → failed;
  7. valid approval and no entry → review_required (`approved_before_queue`);
  8. entry dispatching → dispatching; entry queued or waiting → queued;
  9. latest revision → planned; else draft.
- `(t LadoTask) StateReason() string` returns the reason behind review_required, blocked, failed (`attempts_exhausted`, `timed_out`, `ended_without_pr`) and queued (`waiting_reason`).

## 4. Store (`store/lado_queue_store.go`, `store/lado_plan_store.go`, `store/lado_task_store.go`)

- `ApproveLadoPlan(ctx, approval, proof, event, snapshot *LadoPolicySnapshot, entry *LadoQueueEntry)`: inside the existing transaction, after the snapshot, supersede the task's open entries and insert `entry` (requires a snapshot; a nil snapshot writes no entry). The approval insert, entry insert and audit event commit together.
- Task reads (`LadoTask`, `ListLadoTasks`, `ListLiveLadoTasks`) fill `Queue` and `Execution` (with `SessionTerminated` from `sessions.is_terminated` and the newest PR facts of the session).
- Dispatcher primitives, each under `writeMu.LockContext` and one transaction where it writes more than one row:
  - `ClaimLadoQueueEntries(ctx, now, leaseUntil time.Time, limit int, newToken func() string) ([]LadoQueueEntry, error)`
  - `ReleaseLadoQueueEntry(ctx, id, token, nextAttempt time.Time, waiting string) (bool, error)`
  - `HoldLadoQueueEntry(ctx, id, token, waiting string, nextAttempt time.Time) (bool, error)` (attempt_count back by one: a hold is not an attempt)
  - `MarkLadoQueueEntryReviewRequired(ctx, id, token, reason)`, `MarkLadoQueueEntryBlocked(ctx, id, token, reason)`, `FailLadoQueueEntry(ctx, id, token, message, at)`
  - `LinkLadoExecution(ctx, entryID, token string, exec LadoExecution) error` (insert + `dispatched_session_id` + clear lease, one transaction; `ErrLadoLeaseLost` when the token no longer holds)
  - `LeasedLadoQueueEntries(ctx) ([]LadoQueueEntry, error)` (for recovery)
  - `CountLiveLadoExecutions(ctx, now) (total int, perProject map[ProjectID]int, error)`
  - `LadoExecutionsPastDeadline(ctx, now) ([]LadoExecution, error)`, `MarkLadoExecutionTimedOut(ctx, sessionID, at)`
  - `LadoExecution(ctx, sessionID) (LadoExecution, bool, error)`, `LadoExecutionByClientRequest(ctx, clientRequestID string) (LadoExecution, bool, error)` (parses `lado:<entry>/<attempt>`)
  - `LadoDispatchGrant(ctx, entryID, attempt int, now) (LadoDispatchGrant{ProjectID, Hash string; Active bool}, error)`
  - `LadoDispatchSettings(ctx)`, `SetLadoDispatchSettings(ctx, settings, event)`; `SetLadoProjectMaxConcurrent(ctx, projectID, n, event)`; `ListLadoQueue(ctx, projectID *ProjectID)`.
  - `CancelLadoTask` also cancels the task's open entries.

## 5. Worker gate and hooks (`service/ladogate`, `ports/lado_gate.go`)

- `WorkGate` grows:
  ```go
  ClampTurnSettings(ctx context.Context, target domain.SessionID, s ChatTurnSettings) (ChatTurnSettings, error)
  AuthorizeConfigMode(ctx context.Context, target domain.SessionID, mode domain.PermissionMode) error
  AuthorizeExecutionChange(ctx context.Context, target domain.SessionID) error
  ```
  The two test gates that implement `WorkGate` (`session_manager/lado_gate_test.go`, `service/chat/lado_gate_test.go`) gain no-op methods.
- `AuthorizeSpawn`: a worker in a LADO project is allowed only with a `ClientRequestID` that parses as `lado:<entry>/<attempt>` and a grant that is active, names the same project and has the same hash; any other worker still gets `LADO_TASK_REQUIRED`. A grant read error refuses.
- `ClampTurnSettings`: for a session that is a LADO execution (found by its client request id), an approval above the snapshot's ceiling, or `default`, becomes the ceiling. Other sessions pass unchanged.
- `AuthorizeConfigMode`: refuses a mode above the ceiling, or `default`, for an execution (`LADO_APPROVAL_ABOVE_CEILING`, details `{sessionId, mode, ceiling}`).
- `AuthorizeExecutionChange`: refuses for an execution (`LADO_EXECUTION_FIXED`, details `{sessionId}`).
- Hook lines in upstream files: chat `SetTurnSettings` (before the controller writes) calls `ClampTurnSettings`; `SetConfigOption` calls `AuthorizeConfigMode` before the provider call when the option resolves to an approval mode; the agent-switch entry points (TUI and Chat) and the interface-transition entry point call `AuthorizeExecutionChange`.
- Prompt: `LadoPromptSection` becomes `func(ctx, kind domain.SessionKind, projectID domain.ProjectID, sessionID domain.SessionID) string`. Orchestrators keep their section. For a worker, `ladotask.PromptSource.WorkerSection` reads the session's client request id, finds the entry and its snapshot, and returns the snapshot's `prompt` (empty for any other session).

## 6. Task service (`service/ladotask`) and project service (`service/ladoproject`)

- `ApprovePlan` builds the entry (`lado-queue-` + NewID, project from the task) and passes it to the store.
- `IssueCheck(ctx, task) (IssueCheck{SHA256 string; State domain.NormalizedIssueState}, error)`: reads the issue with trusted comments as `View` does and hashes `NewLadoIssueSnapshot(...)`, so it compares with the revision's `issue_snapshot_sha256`.
- `RunInput(ctx, task) (RunInput{PlanText, IssueContext, Revision LadoPlanRevision, Approval, Snapshot LadoPolicySnapshot}, error)`: the approved revision's pinned plan bytes, and the pinned issue snapshot of its request rendered like `renderIssueFile`.
- `PromptSource.WorkerSection(ctx, sessionID)` as in §5.
- `Cancel` cancels the task's entries and, when an execution runs, asks the dispatcher's killer to stop the session (`Killer` dependency; the issue is never touched).
- Re-approval is allowed for review_required and failed, not for blocked (unchanged rule from step 5, plus failed).
- `ladoproject`: `ModeSwitch` is always on (drop `AO_LADO_DEV` and its wiring and texts); `UpdateSettings` takes an optional `maxConcurrent` next to an optional `ladoMode` (at least one); audit `project.max_concurrent_changed` (actor `api`).

## 7. Dispatcher (`service/ladoqueue`)

- `Deps{Store, Tasks, Policy, Sessions, Memory, Projects, Git, Clock, NewID, Log, Interval (15 s), Lease (2 min)}`. `Sessions` spawns through `sessionsvc.Service.Spawn` and stops through `RequestKill`; `Memory` is the daemon's `usage` memory reader (`Pressure`).
- `Start(ctx) <-chan struct{}` runs `Tick` at once and then every `Interval`; `Tick(ctx, now)` is the unit tests drive.
- `Tick` order: recover leases → stop runs past their deadline → claim up to the free slots → dispatch each claimed entry. Every write after a spawn uses `context.WithoutCancel`.
- `dispatch(entry)` checks, in order (TDD §5.3):
  1. the approval is still valid and is the task's newest → else review required `plan_superseded`;
  2. the issue: unreadable → hold `issue_unreadable` (1 min); closed → cancel the entry (`IssueCheck` records the observed state, so the task shows Closed unmerged); edited (`IssueCheck` hash differs from the revision's) → review required `issue_edited`;
  3. `Policy.CurrentSHA256(task)` equals the snapshot's → else review required `rules_changed`;
  4. base: `base_ref` equals the base AO starts the worker from (the project's worktree base branch; for a workspace project, the base of the issue's repository — S07 checks how AO picks it) → else blocked `base_ref_mismatch`; record the base SHA now and the commits ahead (`Git.RevParse`, `Git.CountCommits(dir, from, to)`), unknown counts stay NULL;
  5. admission (re-checked after the claim, before spawning): global and project caps, memory → hold;
  6. the snapshot has no blocking problem and the agent is supported → else blocked `blocking_snapshot`/`agent_unsupported`.
- Spawn config: `Kind worker`, `ProjectID`, `Harness` and model from the snapshot's agent, `AgentConfig.Permissions` = the snapshot's permission mode (never empty, never `default`), `RequestedMode chat`, `IssueID` = the task's issue ref, `IssueContext` and `Prompt` from `RunInput`, `ClientRequestID`/`ClientRequestHash` per §1 d, `DisplayName` = the issue title.
- After `Spawn` returns: `LinkLadoExecution` with `started_at` = now and `deadline_at` = now + `maxRunMinutes` (NULL when 0). A spawn error: release with the backoff of §1 h, or fail after the third attempt with the error text (never a token).
- Recovery (also gate 13): for each leased entry, `GetSessionByClientRequestID(lado:<entry>/<attempt>)`; a committed session → link it; none, or uncommitted with the lease expired → release (`retry`); a lease still active → leave it.
- Deadlines: each tick, executions past `deadline_at` whose session lives → `RequestKill`, then `MarkLadoExecutionTimedOut`; audit `execution.timed_out` (actor `lado`).
- Mode off: entries of projects with LADO mode off are not claimed; the queue view shows them waiting with `lado_mode_off`.
- `Killer` for the task service: `Stop(ctx, sessionID)` → `RequestKill`.

## 8. HTTP (`/api/v1/lado`, LAN-blocked by the existing prefix)

- Task DTO adds `queue` (`state`, `reason`, `attempts`, `nextAttemptAt`, `error`) and `execution` (`sessionId`, `startedAt`, `deadlineAt`, `timedOut`, `pr`), and the new states in the `state` enum.
- `GET /lado/queue?projectId=` (projectId optional): entries with task id, issue ref and title, state, reason, attempts, next attempt.
- `GET /lado/executions/{sessionId}`: task (id, title, issue ref and URL), plan revision (id, number, sha256), approval time, snapshot (sha256, agent, permission mode, ceiling, rule names, prompt), base (plan SHA, dispatch SHA, commits ahead), started, deadline, timed out, PR. 404 `LADO_EXECUTION_NOT_FOUND` for a session that is not an execution.
- `GET /lado/dispatch-settings`, `PUT /lado/dispatch-settings` (`{maxConcurrent, maxRunMinutes, holdOnMemoryTight}`, all required).
- `PUT /lado/projects/{projectId}/settings`: `ladoMode` and `maxConcurrent` both optional, at least one; the settings response adds `maxConcurrent`. The description drops `AO_LADO_DEV`.
- Specgen operations and schema names in `lado_operations.go`; `npm run api` regenerates `openapi.yaml` and `schema.ts`.
- Daemon: build the dispatcher in `wireLadoWork`, start it after the task service, wait for it on shutdown next to `ladoTasksDone`; the gate gets the store as its grant reader.

## 9. Electron main (`frontend/src/main/lado-presence.ts`)

- New channel `lado:presence:setLadoMode(projectId, on)`: only `on = false` asks; main reads the project's settings, shows a native confirmation in the app's language ("Turn off LADO mode for <project>? Queued tasks wait until it is on again; running workers keep running."), then signs `PUT /lado/projects/{id}/settings {"ladoMode": false}`. Turning on is a plain renderer call. Preload, the three bridges and the tray catalog (`node scripts/sync-tray-catalog.mjs`) follow the existing channels.

## 10. Renderer

- Settings → **Runs** (new section after Rules, `components/settings/RunsSection.tsx`, registered in `settingsCatalog.tsx`): concurrent runs (1-8), maximum run time (off, 1, 2, 4, 8, 12, 24 hours), hold while memory is tight (switch). Saves on change; shows the daemon's error in place.
- Project settings → **LADO** (new section, local projects only, `components/lado/LadoProjectSection.tsx`, registered where `LadoProjectRulesSection` is): LADO mode switch (off goes through the desktop confirmation; a cancelled confirmation leaves it on) and concurrent runs in this project (1-8).
- Lanes (`LadoLanes.tsx`): the Plan lane holds draft, planned, review_required, blocked, failed; the Queued lane holds queued and dispatching, each card with its reason line (waiting for a free slot, for memory, for the issue, retry at a time; attempt n of 3). Dispatched tasks leave the lanes.
- Task view: after dispatch, a status line with the session link; Failed shows the reason and enables Approve plan again.
- Board session cards: a LADO line for executions (plan revision, rules hash prefix), from the tasks the board already loads (`execution.sessionId`).
- Session view: a LADO panel in the inspector rail (`SessionInspectorRail.tsx`) with the active snapshot from `GET /lado/executions/{sessionId}`: agent and mode, ceiling, rule names, the worker rule text (collapsed), plan revision, base moved by n commits, deadline.
- `lib/lado.ts` clients and query keys, `hooks/useLado.ts`, `lib/lado-errors.ts` codes, `lib/lado-events.ts` handles `lado_execution` (invalidate the execution and the project's tasks).
- DESIGN.md decisions log gets one row for Runs, the project LADO section, the new lane states, the card line and the panel (S11).

## 11. Steps

| Step | Scope |
|---|---|
| S01 | migration 0006, `queries/lado_queue.sql`, sqlc gen, migration test |
| S02 | domain: queue entry, execution, settings, task states and reasons |
| S03 | store: entry in the approval, supersede, task reads with queue and execution, CDC triggers |
| S04 | store: claim, release, hold, marks, link, counts, deadlines, grant, settings, project cap, queue list |
| S05 | gate: grant, turn-setting cap, config mode, execution change; upstream hooks; worker prompt section |
| S06 | ladotask: approval entry, IssueCheck, RunInput, WorkerSection, cancel with killer, re-approval; ladoproject: mode switch without dev, project cap |
| S07 | ladoqueue: tick, claim, checks 1-6, spawn config, link, retry and failure (gates 15, 17, 18) |
| S08 | ladoqueue: admission, recovery (gate 13), deadlines, base SHAs, killer |
| S09 | HTTP routes and DTOs, specgen, `npm run api`, daemon wiring |
| S10 | renderer data layer, errors, events; Electron main mode-off presence; bridges; tray catalog |
| S11 | Settings → Runs, Project settings → LADO, DESIGN.md row |
| S12 | lanes, task view, board card line, inspector panel |
| S13 | docs: TDD, HARNESS, PRD, LADO.md, LADO.ko.md |
