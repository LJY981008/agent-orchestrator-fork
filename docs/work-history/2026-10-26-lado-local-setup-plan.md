# LADO step 7 — Local setup adoption: plan (contracts)

> 승인: 2026-10-26 소유자 — 설계 승인(A안: Claude 는 세션별 설정 홈, Codex 는 실행 옵션 덧씌우기). 소유자 결정 2026-10-26: 범위 "전부 한 번에", plugins 는 "제자리 + 켜기 설정만 채택", 편집은 "텍스트는 앱 안, 폴더형은 켜기·삭제", OS 는 "검증된 OS 만 허용"(Linux 는 이 기계에서 실측, Windows·macOS 는 실측 전까지 거절). 체인 러너로 실행(단계 수·예산은 TSV 머리말). 이 문서가 단계 엔트리의 공통 계약이다. 단계 엔트리가 이 문서보다 구체적이면 엔트리를 따른다.
> 근거 스펙: `docs/LJY-PRD/HARNESS.md` §2, §4, §7-§10, §12-§15; `docs/LJY-PRD/TDD.md` §1.2, §3.1, §4, §5.2-§5.3, §9, §13, §14 (gate 14), §15 step 7; `docs/LJY-PRD/PRD.md` HAR-005, HAR-010..015; `docs/LJY-PRD/adr/0005-local-harness-adoption.md`.

## 0. Rules for every step

- Read `AGENTS.md` and `CLAUDE.md`; UI steps also read `DESIGN.md`. Code comments in English, matching nearby density. New code goes in LADO files (`lado_*`, `ladosetup`, `ladopolicy`, `ladotask`) inside AO's layers; shared upstream files get only small hook edits (TDD §1.2), counted by the `diff-budget` gates. Never use the word "harness" for LADO concepts (say rule, policy, setup, agent).
- **Never touch the real tool folders.** No step adopts, restores, rewrites or deletes anything under the developer's real `~/.claude`, `~/.claude.json` or `~/.codex`, and no step runs a daemon against them. `service/ladosetup` never looks up a home or tool folder itself: every path (the Claude folder, `~/.claude.json`, the Codex home, the state dir) comes from the daemon through `Deps` (gate `no-home-lookups`). Its tests use `t.TempDir()` homes and run with `go-tests-isolated` (HOME and the tool variables point into a temp dir). The only exception is S01's probe, which reads the real Claude login through `CLAUDE_SECURESTORAGE_CONFIG_DIR` (read-only `auth status`, a few `claude -p --model haiku` turns) inside a lab home.
- Query files `backend/internal/storage/sqlite/queries/lado_*.sql` stay ASCII only (sqlc v1.31.1 cuts SQL after non-ASCII); read the generated `gen/*.sql.go` after `npm run sqlc`. Never hand-edit `gen/`, `openapi.yaml`, `frontend/src/api/schema.ts`, `routeTree.gen.ts`, `tray-catalog.generated.ts`.
- Store tests use `sqlitetest.MustOpen(t)`; no fresh `sqlite.Open` per test. LADO tables never get FOREIGN KEYs to upstream tables (`sessions`, `projects`); joins to them in queries are fine.
- Renderer strings go in all 8 locale files (`frontend/src/renderer/i18n/{en,ko,ja,zh-CN,es,fr,de,pt-BR}.json`, flat keys, appended after the existing LADO keys) with real translations; Korean follows the fork's glossary (규칙, 로컬 설정, 채택, 복원, 관리 폴더, 변경 감지, 충돌, 확인 필요, 차단됨, 승인). The coverage test fails on hardcoded English in JSX. No particle right after a placeholder in Korean (rephrase instead of `을(를)`).
- `<button>` ignores Tailwind font classes (unlayered button rule): style an inner `<span>`.
- The Write tool turns `\u` escapes into real characters; write such strings with python if they must stay escaped.
- No `git push`, `gh`, PRs, publishing or writes outside the worktree (the guard refuses them), except the scratch dirs the probe and tests create. Never touch the real `~/.ao`.
- Subagents and long commands only in the foreground (`run_in_background: false`): when a `claude -p` session ends its last turn, it waits a bounded time for background work and then kills it, and the step ends without its commit.
- The design and its approvals are done: skip brainstorming and planning gates of any skill and do not ask the user questions — implement this step, commit, rerun the gates, fill After/회고.
- Every step leaves `go build ./... && go vet ./...` green, and from S14 on also `npm --prefix frontend run typecheck`.
- Step 0 of every entry: read the earlier entries' `## 비교/회고` and their "다음 단계 메모" (the runner only hands over the previous commit). An entry edited mid-chain by the main session is committed at once with a pathspec.

## 1. Scope and decisions

In: inventory of the Claude Code and Codex user-level setup (HAR-010); adoption with backup, manifest, copy into the managed home and removal (HAR-011); restore (HAR-015); delivery to every session AO launches (Claude: per-session config homes; Codex: launch overlay); toggles as Global/Project/Task bindings, in-app editing of text items with a diff, creation and deletion, versions as blobs (HAR-012); deterministic conflict checks with acceptances (HAR-013); drift (HAR-014); setup items, the AO system layer and accepted conflicts in the rule snapshot (`lado-policy/2`, HAR-005); the protected rule `setup.change.requires_presence`; presence-confirmed adoption, restore and code-running changes; Settings → Local setup, the project's Rules page, the task's Rules tab and the run panel; docs.

Out (owning step or later): the optional AI comparison of instructions against LADO prompt rules (later); allow rules against protected gates that step 8 adds (step 8); AO's reviewer launcher, which builds its own environment and so gets no adopted items (step 8, recorded); real-install checks on Windows and macOS, which keep adoption refused there (step 9); project untracked files (decision k).

Owner decisions (2026-10-26):

1. Build the whole step at once.
2. `~/.claude/plugins` stays where it is (510 MB, written by Claude at runtime); only the `enabledPlugins` and `extraKnownMarketplaces` settings keys are adopted. The snapshot records a hash of what is installed. This departs from HARNESS §9.1 ("as one unit"); S17 records it.
3. Text items are edited in the app with a diff and an approval; folder items (a skill with more than one file, a hook script folder) are toggled or deleted in the app and edited in the managed folder, where drift shows the change for keep or revert.
4. Adoption runs only on an OS whose delivery was measured. Linux is measured in S01 on this machine; Windows and macOS stay `unverified` and refuse adoption until step 9 measures them. The code still handles Windows paths, symlink failures (Developer Mode) and file locks, with unit tests.

Facts measured while designing (2026-10-26; do not measure again, S01 confirms the live ones):

- Claude Code 2.1.283 (`/home/code/.local/node/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe`, a native binary with the JS inside). `CLAUDE_CONFIG_DIR` moves every user-level path: `CLAUDE.md`, `settings.json`, `skills/`, `agents/`, `commands/`, `output-styles/`, `plugins/`, `projects/` and the global config `.claude.json` (AO already writes trust into `<CLAUDE_CONFIG_DIR>/.claude.json`, `adapters/agent/claudecode/claudecode.go:236-252`). Linux and Windows keep the login in `<dir>/.credentials.json`; macOS keys the Keychain item on the folder.
- The credentials file cannot be shared by a link: Claude writes it through a staged temp file and a rename (`Jne` with `exactMode`), refuses a symlink (`O_NOFOLLOW`, state `refused-symlink`) and refuses an in-place write to a file with more than one hard link. But the credential folder has its own variable: `Fw()` returns `CLAUDE_SECURESTORAGE_CONFIG_DIR` when it is defined (an empty value means `~/.claude`), and the Keychain service name has no suffix when that variable is empty, so `CLAUDE_CONFIG_DIR=<home>` plus `CLAUDE_SECURESTORAGE_CONFIG_DIR=<the real Claude folder>` (Linux, Windows) or `""` (macOS) reads and writes the computer's own login where it lives. AO already sets the variable for accounts, with the account home as its value (`adapters/agentaccount/claude.go:58-67`). It is undocumented, so S01 measures it.
- Codex keeps its login under `CODEX_HOME`: `auth.json` by default, a keyring entry keyed by a hash of the canonical `CODEX_HOME` with `keyring`, and on Windows by default an encrypted `secrets/codex_auth.age` whose passphrase sits in Credential Manager under the same hash. No variable separates the login folder from `CODEX_HOME`, so a per-session `CODEX_HOME` would lose the computer's own login. Codex therefore keeps the real `CODEX_HOME`, and LADO adds adopted items through `-c` (TUI argv, the `codex app-server` process) and the thread `config`. Through those, only instructions (as AO's system prompt section) and `mcp_servers.*` reach both paths; `skills/` (except app-server `skills/extraRoots/set`), `plugins/`, `prompts/` (deprecated) and `rules/` (written by Codex) do not.
- AO's account linker (`adapters/agentaccount/share.go`) creates a missing shared entry in the real folder or moves the account's copy there, never re-points an existing link, and prunes links that dangle into the real folder. Account homes link `projects`, `sessions`, `session-env`, `file-history`, `shell-snapshots`, `todos`, `paste-cache`, `tasks`, `plans`, `transcripts`, `skills`, `plugins`, `agents`, `commands`, `output-styles`, `hooks`, `history.jsonl` and every top-level `*.md`, copy `settings.json` key by key without login keys, and copy `mcpServers` and `theme` into the account's own `.claude.json`. Removing a key from the real file removes the copy AO wrote into each account on its next launch, unless the account changed it.
- Every launch path runs `runtimeEnv` → `augmentAgentRuntimeEnv` (AO's setup-token injection is there and skips when `CLAUDE_CONFIG_DIR` is set) → `prepareWorkspace` (the Claude trust write; returns an error). `resumeChatController` (`session_manager/chat_spawn.go`, the Chat resume path) does not call `prepareWorkspace`.
- AO's usage collector accepts only transcripts that resolve under the real `projects/` and `sessions/`; agent switching's `safeNativeTranscriptPath` drops a transcript that resolves outside the session's config folder (`session_manager/agent_switching.go:1759-1790`).
- `procmem.Snapshot` lists processes on Unix (`ps`) and Windows (Toolhelp) with their command lines.
- On this machine: `~/.claude/settings.json` keys `env, permissions, model, hooks, statusLine, enabledPlugins, extraKnownMarketplaces, modelSettings, showClearContextOnPlanAccept, theme, verbose, inputNeededNotifEnabled, agentPushNotifEnabled`; `~/.claude.json` 124 KB with no user MCP servers; `~/.codex/config.toml` keys `model, model_reasoning_effort, mcp_servers (figma, notion: url only), projects`.

My decisions (record in docs at S17):

a. **Nothing changes until the first adoption.** Without an active managed item a session launches exactly as today.
b. **Claude delivery: a config home for every Claude session** once an active managed Claude item exists, at `<state>/lado/homes/<session-id>/claude/`, reconciled on every launch (spawn, restore, resume, Chat start and resume, agent switch, interface transition). Its base is the session's own folder: the account home for an account session, the real Claude folder for the computer's own login. The home links the base's state entries (the account linker's list above, `plugins` included), merges the base's `skills/`, `agents/`, `commands/`, `output-styles/`, `hooks/` and top-level `*.md` with the managed items (a managed item wins a name clash), writes `settings.json` as the base's file plus the adopted keys, and copies the base's `.claude.json` with `mcpServers` = the base's servers plus the managed ones. `CLAUDE_CONFIG_DIR` points at the home; `CLAUDE_SECURESTORAGE_CONFIG_DIR` keeps AO's account value, or for the computer's own login is the daemon's `CLAUDE_CONFIG_DIR` when that was set, else `""` on macOS and the real folder elsewhere. Changes Claude makes to the copied `.claude.json` stay in that session's home.
c. **Which versions.** A LADO execution's home uses the versions its snapshot pinned (found through the session's client request id, as the worker's rules text is), so a later edit never reaches a running worker. Every other session uses the current versions for its project (Global and Project bindings). Homes are built only from stored versions (blobs and materialized trees), never from the live managed folder.
d. **Codex delivery: overlay, real `CODEX_HOME`.** Adopted MCP servers reach the TUI as `-c mcp_servers.<name>=<inline table>` (codex adapter hook, launch and restore) and Chat through the MCP server list AO already passes to the driver (where AO's own `ao` server is). Adopted `AGENTS.md` (and `AGENTS.override.md`) text reaches both as an additional system-prompt section for Codex sessions. A server is adoptable only in `[mcp_servers.<name>]` table form, with a name of `[A-Za-z0-9_-]` characters and no literal secret fields (`env` values, `http_headers`, `bearer_token`): those would land on a command line.
e. **What is adoptable** (HARNESS §9.1 as amended): Claude top-level `*.md`; `settings.json` keys except login keys (the `env` item leaves the credential variables of `adapters/agentaccount/claude.go:32-41` in place); one item per entry of `skills/`, `agents/`, `commands/` (nested paths), `output-styles/`, `hooks/`; one item per user MCP server in `~/.claude.json`. Codex `AGENTS.md`, `AGENTS.override.md` and `[mcp_servers.<name>]` tables. Default action: move, except settings keys outside `hooks, permissions, env, model, modelSettings, statusLine, outputStyle, enabledPlugins, extraKnownMarketplaces, includeCoAuthoredBy`, which default to keep in place. Listed with a reason and never adopted: credentials and login keys, app state (`projects/`, `sessions/`, history, caches and the rest of `~/.claude.json`), `plugins/`, Codex `skills/`, `plugins/`, `prompts/`, `rules/`, `hooks` and its other keys, repo-tracked files, organization-managed settings, and anything else in those folders (`unknown_location`).
f. **Containers stay.** Adoption removes the entries inside `skills/`, `agents/`, `commands/`, `output-styles/` and `hooks/`, never the folders themselves, so AO's account linker never recreates anything in the real folder and `share.go` needs no change.
g. **Hook paths.** A `hooks` or `statusLine` command that names a moved path in the original folder (`~/.claude/…`, `$HOME/.claude/…`, `${HOME}/.claude/…` or the absolute folder) is rewritten to `"$CLAUDE_CONFIG_DIR"/…` in the adopted value, and the preview lists each rewrite. A reference adoption cannot rewrite is a blocking conflict.
h. **Preconditions** for adoption and restore: a verified OS (adoption only; restore runs on any OS); no live AO session of that tool; no outside Claude or Codex process (`claude`, `claude.exe`, `codex`, `codex.exe`, or a command line with `@anthropic-ai/claude-code`, `@openai/codex` or `claude-agent-acp`), listed in the refusal. `AO_LADO_DEV_SETUP_IGNORE_PROCESSES=1` skips the outside-process part for labs and is recorded in the audit payload.
i. **Presence** (`setup.change.requires_presence`, a built-in protected rule): adoption, restore, and changes that can run code or widen access need the desktop app's confirmation: creating or editing a `hook_script` or `mcp_server`, or the settings keys `hooks`, `permissions`, `env`, `statusLine`, `enabledPlugins`, `extraKnownMarketplaces`; turning such an item on at any scope; keeping such an item's drift. Turning off, deleting, inherit, text edits and drift revert need none (actor `api`).
j. **Conflicts are deterministic** (§6). Blocking ones cannot be accepted; warnings can, per item version and scope, and the snapshot records the acceptances; info needs nothing.
k. **Project untracked files are not adopted.** `<project>/.claude/settings.local.json` and `CLAUDE.local.md` in a registered project's main checkout are listed read-only with the reason "AO worktrees do not load this file": AO's worktrees never contain them, so adopting them would change direct use without serving AO sessions, and writing them into worktrees risks committing personal files. This departs from HARNESS §9.1 (Project row) and §9.3 (`projects/<id>/`); S17 records it.
l. **Repo-tracked files** (`CLAUDE.md` and `AGENTS.md` at any depth, `.claude/settings.json`, `.claude/{skills,agents,commands}/`, `.mcp.json`, `.codex/`) are listed read-only per project, loaded by the tool from the worktree, and not hashed into the snapshot: they reach the worker through the base branch, whose movement step 6 records and does not refuse. "Edit" opens the New Task composer for that project, prefilled. This departs from HARNESS §9.5's last sentence; S17 records it.
m. **Snapshot `lado-policy/2`**: `setup` (the ON items of the task's agent tool), `aoSystem` and `acceptedConflicts` after `aoRules`. Every approval made before it shows Review required once (the compiler name is part of the hash), as an AO update does. The dispatcher's reason stays `rules_changed`; the app words it "rules or local setup changed".
n. **Drift** is rescanned when the daemon starts, on demand and after each LADO write. It never changes a session or a task state (homes come from stored versions); keeping a drift creates a version, which does.
o. **Restore** works per adoption, writes moved files back byte for byte, keeps both copies when something new sits at the path (`<name>.lado-restored`), puts keys back only where absent, and stops delivering that adoption's items (their versions stay readable).
p. **Settings → Local setup** is its own settings section after Rules (Rules, Local setup, Runs).

## 2. Storage — LADO migration `0007_lado_setup.sql`

```sql
-- +goose Up
CREATE TABLE lado_setup_adoptions (
    id              TEXT PRIMARY KEY CHECK (id <> ''),
    created_at      TIMESTAMP NOT NULL,
    actor           TEXT NOT NULL CHECK (actor <> ''),
    os              TEXT NOT NULL CHECK (os <> ''),
    plan_sha256     TEXT NOT NULL CHECK (length(plan_sha256) = 64),
    manifest_sha256 TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL CHECK (state IN ('completed', 'partial')),
    error_message   TEXT NOT NULL DEFAULT '',
    restored_at     TIMESTAMP,
    restore_report  TEXT NOT NULL DEFAULT '' CHECK (restore_report = '' OR json_valid(restore_report))
);

CREATE TABLE lado_setup_items (
    id          TEXT PRIMARY KEY CHECK (id <> ''),
    tool        TEXT NOT NULL CHECK (tool IN ('claude-code', 'codex')),
    kind        TEXT NOT NULL CHECK (kind IN ('instructions', 'setting', 'skill', 'agent', 'command', 'output_style', 'hook_script', 'mcp_server')),
    name        TEXT NOT NULL CHECK (name <> ''),
    origin_path TEXT NOT NULL DEFAULT '',
    adoption_id TEXT REFERENCES lado_setup_adoptions (id),
    created_at  TIMESTAMP NOT NULL,
    deleted_at  TIMESTAMP,
    restored_at TIMESTAMP
);
CREATE UNIQUE INDEX lado_setup_items_live ON lado_setup_items (tool, kind, name)
    WHERE deleted_at IS NULL AND restored_at IS NULL;

CREATE TABLE lado_setup_item_versions (
    id           TEXT PRIMARY KEY CHECK (id <> ''),
    item_id      TEXT NOT NULL REFERENCES lado_setup_items (id),
    version      INTEGER NOT NULL CHECK (version > 0),
    content_kind TEXT NOT NULL CHECK (content_kind IN ('file', 'json', 'tree')),
    sha256       TEXT NOT NULL CHECK (length(sha256) = 64),
    size_bytes   INTEGER NOT NULL CHECK (size_bytes >= 0),
    executable   INTEGER NOT NULL DEFAULT 0 CHECK (executable IN (0, 1)),
    actor        TEXT NOT NULL CHECK (actor <> ''),
    created_at   TIMESTAMP NOT NULL,
    UNIQUE (item_id, version)
);

CREATE TABLE lado_setup_bindings (
    item_id    TEXT NOT NULL REFERENCES lado_setup_items (id),
    scope      TEXT NOT NULL CHECK (scope IN ('global', 'project', 'task')),
    scope_id   TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL CHECK (state IN ('on', 'off', 'force_on', 'force_off')),
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (item_id, scope, scope_id),
    CHECK ((scope = 'global' AND scope_id = '' AND state IN ('on', 'off'))
        OR (scope <> 'global' AND scope_id <> '' AND state IN ('force_on', 'force_off')))
);

CREATE TABLE lado_conflict_acceptances (
    id             TEXT PRIMARY KEY CHECK (id <> ''),
    code           TEXT NOT NULL CHECK (code <> ''),
    item_id        TEXT NOT NULL REFERENCES lado_setup_items (id),
    detail         TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL CHECK (length(content_sha256) = 64),
    scope          TEXT NOT NULL CHECK (scope IN ('global', 'project', 'task')),
    scope_id       TEXT NOT NULL DEFAULT '',
    actor          TEXT NOT NULL CHECK (actor <> ''),
    created_at     TIMESTAMP NOT NULL,
    revoked_at     TIMESTAMP
);

CREATE TABLE lado_policy_snapshot_setup (
    snapshot_id     TEXT NOT NULL REFERENCES lado_policy_snapshots (id),
    item_version_id TEXT NOT NULL REFERENCES lado_setup_item_versions (id),
    PRIMARY KEY (snapshot_id, item_version_id)
);

-- +goose Down
-- drop in reverse order
```

- `name` is the item's key within its tool and kind: the file name for instructions and output styles, the settings key, the entry under `skills/` or `hooks/`, the relative path under `agents/` or `commands/`, the MCP server name. `origin_path` is where adoption found it (absolute; `<file>#<key>` for keys).
- A Global binding row is optional: no row means ON for a setup item (adopted items were active), unlike rules. Project and Task rows force on or off; their absence is inherit.
- An item with `deleted_at` or `restored_at` is not delivered and not resolved; its versions stay, and a snapshot pin keeps them readable. Hard deletion never happens.
- Queries in `queries/lado_setup.sql` (ASCII only), named for what they do: insert an adoption, its items and their first versions in one transaction (with the audit event); mark an adoption partial; list adoptions; mark an adoption restored with its report and set `restored_at` on its items; insert a version with a base-version check; list live items with their latest version; get an item with its versions; soft-delete; upsert and delete bindings; list bindings for a project and for a task; insert and revoke acceptances and list the live ones; insert snapshot pins in the approval transaction; read the pinned versions of a snapshot and of a client request id (`lado:<entry>/<attempt>` → entry → snapshot).
- CDC (`ladoCDCTriggers` in `lado_migrate.go`, never in the migration): an insert or update on `lado_setup_items`, an insert on `lado_setup_item_versions`, any change on `lado_setup_bindings` and `lado_conflict_acceptances` publish `{"kind":"lado_setup","projectId":...}` once per project in LADO mode (a Project binding: its project; a Task binding: the task's project), like the rule triggers. Adoption rows publish nothing on their own.

## 3. Domain (`domain/lado_setup.go`)

- `LadoSetupTool` (`claude-code`, `codex`), `LadoSetupKind` (the eight kinds), `LadoSetupContentKind` (`file`, `json`, `tree`).
- `LadoSetupItem`, `LadoSetupItemVersion`, `LadoSetupBinding`, `LadoSetupAdoption`, `LadoConflictAcceptance` with every column; `LadoSetupItem.Live()`.
- `LadoSetupTree` (canonical manifest of a folder item): entries `{path, mode ("file"|"exec"), size, sha256}` sorted by path, `CanonicalJSON()` and `SHA256()`; paths are slash-separated, relative, without `..`.
- Inventory: `LadoSetupEntry{Tool, Kind, Name, Location, Path, Size, SHA256, Tracked bool, Status, Reason, DefaultAction, ItemID}` with statuses `managed`, `adoptable`, `not_adoptable`, `never`, `not_managed`, `repo_tracked`, and stable reason codes (`credentials`, `login_key`, `app_state`, `plugins_stay`, `tool_state`, `no_tui_path`, `deprecated`, `inline_table`, `literal_credentials`, `name_unsupported`, `unknown_location`, `worktrees_do_not_load`, `ao_sets_per_session`, `repo_tracked`, `managed_settings`).
- Drift: `LadoSetupDrift{ItemID, Kind ("edited"|"new_local"), Path, SHA256}`.
- Conflicts: codes (§6) and the problem shape of `domain/lado_policy.go` (`code`, `severity`, `rules`, `details`), with setup item ids in `details["item"]`.
- `LadoSetupDeliveryVerified(goos string, tool LadoSetupTool) bool`: `linux` → both true (S01); every other OS false.
- Presence actions in `domain/lado_presence.go`: `setup.adopt` (subject: the plan sha256), `setup.restore` (adoption id), `setup.change` (the change's sha256, §12).

## 4. Store (`store/lado_setup_store.go`, plus the approval transaction in `store/lado_plan_store.go`)

- Every write takes `writeMu.LockContext`, runs in one transaction with its audit event (`appendLadoAuditEventTx`) and, for presence actions, its proof (`insertLadoPresenceProof`, replay → `ErrLadoPresenceReplayed`).
- `RecordLadoSetupAdoption(ctx, adoption, items, versions, event, proof)`; `MarkLadoSetupAdoptionPartial(ctx, id, message)`; `RestoreLadoSetupAdoption(ctx, id, report, at, event, proof)`; `AddLadoSetupItemVersion(ctx, itemID, baseVersion, version, event, proof)` (409 `LADO_SETUP_ITEM_CHANGED` when the base is not the latest); `CreateLadoSetupItem(ctx, item, version, event, proof)`; `DeleteLadoSetupItem(ctx, id, at, event)`; `SetLadoSetupBinding(ctx, binding, event, proof)` (inherit deletes the row); `AcceptLadoConflict` / `RevokeLadoConflictAcceptance`; readers for live items with latest versions, an item with its versions, bindings by project and by task, live acceptances, adoptions.
- `ApproveLadoPlan` gains the snapshot's setup pins: inside the existing transaction, after the snapshot rows, check that every pinned version exists and insert `lado_policy_snapshot_setup`; a missing version answers the same "rules changed" error as a missing rule version (TDD §5.2).
- `LadoSetupVersionsForClientRequest(ctx, clientRequestID)` for execution homes (parses `lado:<entry>/<attempt>`, returns the pinned versions; not an execution → empty and false).

## 5. Inventory (`service/ladosetup/inventory.go`)

- `Deps{Store, Blobs (ladoblob), StateDir, ClaudeDir, ClaudeJSON, CodexHome, ClaudeDirFromEnv bool, GOOS, Projects (registered projects: id, name, path), Processes (procmem-based lister), Sessions (live AO sessions by harness), Clock, NewID, Log}`.
- Scan: the Claude folder (top-level `*.md`, `settings.json` keys, the five item folders, state and unknown entries), `~/.claude.json` (`mcpServers` entries only), the Codex home (`AGENTS.md`, `AGENTS.override.md`, `config.toml` tables and keys, its folders), each registered local project's main checkout (untracked files of decision k, repo-tracked files of decision l through `git ls-files`, 10 s per project, errors per project). Statuses and reasons per decision e; `managed` for live items with their item id; size and sha256 for every adoptable or managed entry (folder items: the tree manifest's sha256).
- The scan result (`LadoSetupInventory{Entries, Drift, Adoptions, OS, Verified map[tool]bool, ScannedAt}`) is cached in memory and rebuilt on demand, at daemon start and after every LADO write.
- Processes (decision h) come from `procmem.Snapshot`; live AO sessions from AO's session store (harness `claude-code` or `codex`, not terminated).

## 6. Conflicts and the AO system layer (`service/ladosetup/conflicts.go`, `aosystem.go`)

Checks run over the effective setup of a scope (Global, a project, a task), plus the enabled plugins' hooks (`hooks/hooks.json` of each enabled plugin's install path in `installed_plugins.json`; unreadable → info):

| Code | When | Severity |
|---|---|---|
| `LADO_SETUP_MCP_NAME_TAKEN` | an MCP server named `ao` (any case) | blocking |
| `LADO_SETUP_SKILL_NAME_TAKEN` | a skill or command named `using-ao` | blocking |
| `LADO_SETUP_ENV_RESERVED` | adopted `env` sets `AO_*`, `CLAUDE_CONFIG_DIR`, `CLAUDE_SECURESTORAGE_CONFIG_DIR` or `CODEX_HOME` | blocking |
| `LADO_SETUP_DENIES_AO` | a `permissions.deny` entry matching `Bash(ao…)` or `mcp__ao…` | blocking |
| `LADO_SETUP_HOOK_PATH_MOVED` | a hook or `statusLine` command names a moved path that was not rewritten (decision g) | blocking |
| `LADO_SETUP_DENIES_GIT` | a `permissions.deny` entry matching `Bash(git…)` or `Bash(gh…)` | warning |
| `LADO_SETUP_HOOK_MAY_BLOCK_TOOLS` | a `PreToolUse` hook whose matcher matches `Bash`, `*` or `mcp__ao` | warning |
| `LADO_SETUP_HOOK_ADDS_PROMPT_TEXT` | a `UserPromptSubmit` or `SessionStart` command hook | warning |
| `LADO_SETUP_DEFAULT_MODE_ABOVE_CEILING` | `permissions.defaultMode` above the permission ceiling | warning |
| `LADO_SETUP_HOOK_SHARES_AO_EVENT` | a hook on one of the events AO installs (`adapters/agent/claudecode/hooks.go`) | info |
| `LADO_SETUP_PROJECT_OVERRIDES` | the project's repo `.claude/settings.json` sets a key an adopted setting sets (Claude: project wins over user) | info |

- An accepted warning (matching code, item, detail, the item version's sha256 and the scope or a wider one) is listed with its acceptance and leaves the attention list; the snapshot records it. Accepting a blocking or info result is refused (`LADO_SETUP_CONFLICT_NOT_ACCEPTABLE`).
- `aoSystem`: the sha256 of canonical JSON of what AO adds to every session: the Claude hook events and command prefix, the Codex hook events, the `ao` MCP server's name and arguments shape, and the `using-ao` skill's bytes (from the code that installs them; a test pins that the inputs follow AO's sources). Computed once per daemon.

## 7. Adoption (`service/ladosetup/adopt.go`)

1. **Preview** (`dryRun`): the request names entries (tool, kind, name) and an action (move or keep); the service rescans, rejects entries that are not `adoptable`, and returns per entry the action, the managed path, how sessions get it (home or overlay), the hook rewrites (decision g), the conflicts it would add, the warning that direct use outside the app stops loading moved items, the precondition results (§1 h), and `planSha256` = sha256 of canonical JSON `{"format":"lado-setup-adoption/1","os":...,"entries":[{tool,kind,name,originPath,sha256,action}]}` sorted.
2. **Apply** (presence `setup.adopt` with subject `planSha256`): rescan and recompute; another hash → 409 `LADO_SETUP_CHANGED` and nothing written. Preconditions again (409 `LADO_SETUP_OS_UNVERIFIED`, `LADO_SETUP_TOOL_RUNNING` with the list).
3. **Back up** every original into `<state>/lado/backups/<adoption-id>/files/<root>/<relative path>` (roots `claude_dir`, `claude_json`, `codex_home`) keeping mode and mtime; a key-edited file is backed up whole. Write `manifest.json` (`format lado-setup-backup/1`, entries: root, relative and absolute path, type file|dir|keys, sha256, size, mode, mtime, tool, kind, name, action, removed keys with the sha256 of each value). fsync, then re-read and re-hash everything; any mismatch stops before anything is removed. Directories 0700, files 0600 (backups can hold MCP secrets).
4. **Copy** into blobs (file bytes, canonical JSON values, every file of a tree plus its manifest) and the managed home (`<state>/lado/harness/claude/…`, `<state>/lado/harness/codex/…`: `settings.json` with the adopted keys, `mcp.json` with the adopted servers, `config.toml` with the adopted `[mcp_servers.<name>]` tables, the files and folders), then verify the hashes.
5. **Remove** the originals: files and folder entries are deleted (containers stay, decision f); `settings.json` and `~/.claude.json` are rewritten without the adopted keys or servers (order and other content kept, 2-space JSON, temp file and rename, the original mode), `config.toml` loses the adopted `[mcp_servers.<name>]` blocks line by line (header to the next table header; comments and other tables untouched).
6. **Record** the adoption, items, first versions (actor `human` or `human:dev-presence`) and the audit event `setup.adopted` in one transaction; save the drift baseline (what remains at the original locations).
7. **Verify** structurally: build a probe home for the computer's own login in a temp dir and check every adopted Claude item is in it with its hash; parse every Codex `-c` value with the TOML parser. The result is in the response.

A failure before step 5 removes nothing and records nothing (the backup folder stays for diagnosis). A failure during step 5 stops, records the adoption `partial` with the error (the moved items are live), and leaves restore available. Windows: a locked file is a failure of step 5 with the path.

## 8. Restore and drift (`service/ladosetup/restore.go`, `drift.go`)

- **Restore** (presence `setup.restore`, subject the adoption id; the preconditions of adoption except the OS check, so the way back stays open everywhere): for each manifest entry, a free path gets the backed-up bytes, mode and mtime; an occupied one keeps what is there and gets the backup as `<name>.lado-restored`; keys go back into `settings.json`, `~/.claude.json` and `config.toml` only where absent (a present key is reported). Every restored file must hash to the manifest. The adoption gets `restored_at` and its JSON report; its items get `restored_at`; audit `setup.restored`. Managed copies, blobs and the backup stay.
- **Drift**: `edited` when a managed home file or folder no longer hashes to its item's latest version (shown with a diff for text items); `new_local` when an original location holds an adoptable entry that is not in the drift baseline (for example a new `~/.claude/settings.json` key written by Claude used directly, or a new skill), offered for adoption and never moved by itself. Keep (`setup.drift_kept`, presence for presence kinds) makes the managed content a new version; revert (`setup.drift_reverted`) rewrites the managed copy from the latest version.

## 9. Delivery — Claude (`service/ladosetup/home.go`, hook in `session_manager`)

- `session_manager` gets an optional `LadoSetupHome` (set like `SetWorkGate`): `PrepareClaudeHome(ctx, sessionID, projectID, env map[string]string) error`. It is called at the top of `prepareWorkspace` for Claude agents (before the trust write) and in `resumeChatController` before the controller env is prepared; an error fails the launch with 500 `LADO_SETUP_HOME_FAILED`. A nil hook keeps upstream behavior. The step lists every launch path and whether it passes through one of the two calls.
- The home follows decision b and c. State entries are links; a missing state entry is created in the base first (as AO's linker does for accounts); merged folders hold one link per base entry and per managed item (materialized trees under `<state>/lado/harness/trees/<sha256>/`, files copied from blobs). Reconcile, never delete-and-recreate: entries Claude created inside the home stay. Homes of terminated or missing sessions are removed when the daemon starts and after scans (links are removed, never followed).
- `safeNativeTranscriptPath` also accepts a transcript that resolves under the resolved target of the config folder's `projects` link, so agent switching keeps its handoff transcript in a home.
- Any AO reader of a session's `CLAUDE_CONFIG_DIR` that would now look for credentials in the home (for example `backend/pkg/agentcreds`) honors `CLAUDE_SECURESTORAGE_CONFIG_DIR`; the step lists the readers it checked.

## 10. Delivery — Codex (`service/ladosetup/codex.go`, hooks in the codex adapter and the Chat MCP lists)

- `adapters/agent/codex` gets an optional overrides hook: `func(ctx, sessionID string) ([]string, error)` returning `-c` pairs, appended to `ProviderArgs` in `GetLaunchCommand` and the restore command; an error fails the launch. The value is a canonical TOML inline table.
- The Chat MCP server lists `session_manager` builds for Codex sessions (`chat_spawn.go`, `agent_switching_chat.go`, next to the `ao` server) get the adopted servers as `ports.ChatMCPServerConfig`; Claude sessions get none there (their servers come from the home).
- The LADO prompt section (`ladotask.PromptSource`) adds, for a Codex session, `## User instructions (managed by LADO)` with the adopted `AGENTS.override.md` or `AGENTS.md` text (pinned for an execution, current otherwise).

## 11. Policy integration (`service/ladopolicy`)

- `Deps.Setup` (a `SetupSource` that `ladosetup` implements): `EffectiveSetup(ctx, projectID, taskID, harness)` → the ON items of that harness's tool with their latest versions and the binding behind each, the conflicts (as problems), the live acceptances that apply, and `aoSystem`. A nil source resolves no setup, so step 5's tests keep their meaning.
- Compiler `lado-policy/2`: after `aoRules`, `setup` [{itemId, tool, kind, name, versionId, version, sha256, source{scope, scopeId, state}}] sorted by tool, kind and name; `aoSystem` {"sha256"}; `acceptedConflicts` [{code, itemId, detail, acceptanceId}] sorted. Setup problems join `problems`. `TestSnapshotFormatIsLadoPolicy2` pins the bytes.
- `Snapshot` returns the setup version ids (`SnapshotSetup()`) for the approval transaction's pins.
- `GET /lado/effective` shows the setup items, `aoSystem`, the conflicts with their acceptances, and, for an approved task, the setup items added, removed and changed since approval (next to the rules diff).
- Impact (dry run of a setup change): the LADO projects whose effective setup changes and the approved tasks whose hash changes, through the same machinery as rule impact.
- Built-in protected rule `setup.change.requires_presence` (`EnsureBuiltins`, version 1 by `lado`, workflow-gate enforcement, no prompt text, always on, refuses bindings, edits, archive and delete like the other protected rules).

## 12. Changes (`service/ladosetup/changes.go`)

- Create (instructions, setting, skill with one `SKILL.md`, agent, command, output style, MCP server), edit (text items: a file or a JSON value; folder items refuse with `LADO_SETUP_FOLDER_ITEM`), delete, binding set, drift keep or revert, conflict accept or revoke. Every one takes `dryRun`, which writes nothing and returns the diff (old and new text), the conflicts it adds or removes, the impact (§11) and `changeSha256` = sha256 of canonical JSON of the request without `dryRun`. A real request that needs presence (decision i) carries the presence proof for subject `changeSha256`; without it: 403 `LADO_PRESENCE_REQUIRED` (existing code).
- Edits take `baseVersion` (409 `LADO_SETUP_ITEM_CHANGED`). Each version is a blob; the managed home file is rewritten after the transaction commits.
- Audit actions: `setup.adopted`, `setup.restored`, `setup.item_created`, `setup.item_updated`, `setup.item_deleted`, `setup.binding_set`, `setup.drift_kept`, `setup.drift_reverted`, `setup.conflict_accepted`, `setup.conflict_revoked`.

## 13. HTTP (`/api/v1/lado/setup`, LAN-blocked by the existing prefix)

| Route | Purpose |
|---|---|
| `GET /lado/setup/inventory` | The inventory (§5) with drift, adoptions, the OS and its verification |
| `POST /lado/setup/scan` | Rescan; same answer |
| `GET /lado/setup/conflicts?projectId=&taskId=` | Conflicts and acceptances for a scope (Global without parameters) |
| `POST /lado/setup/adoptions` (P unless `dryRun`) | Preview or adopt (§7) |
| `GET /lado/setup/adoptions/{id}` | One adoption with its manifest summary |
| `POST /lado/setup/adoptions/{id}/restore` (P) | Restore (§8) |
| `GET /lado/setup/items/{id}` | An item, its versions and its current content (text) or tree manifest |
| `POST /lado/setup/items`, `PATCH /lado/setup/items/{id}`, `DELETE /lado/setup/items/{id}` | Create, edit, delete (§12; P per decision i unless `dryRun`) |
| `PUT /lado/setup/bindings` | Global on/off, Project or Task inherit/force_on/force_off (P when turning a presence kind on) |
| `POST /lado/setup/items/{id}/drift` | Keep or revert (P for a presence kind's keep) |
| `POST /lado/setup/conflicts/acceptances`, `DELETE /lado/setup/conflicts/acceptances/{id}` | Accept or revoke a warning |

Specgen operations and schema names in `lado_operations.go` (`LadoPresenceHeaderParam` on P routes); `npm run api`. Codes follow §6, §7 and §12 plus `LADO_SETUP_UNAVAILABLE` (500, no service) and `LADO_SETUP_NOT_FOUND`. The daemon builds `ladosetup` in `wireLadoWork` with the folders it already resolves (`daemon.go` Claude folder and `~/.claude.json`, `codexHome`), the state dir, the blob store, the store, `procmem`, and installs the home and overlay hooks before anything spawns.

## 14. Electron main (`frontend/src/main/lado-presence.ts`)

- New channels, each following the existing steps (trusted sender, minimal renderer input, the daemon re-read, a native confirmation from main's catalog, a fresh proof, the exact body):
  - `lado:presence:adoptSetup(entries)`: main sends the dry run, shows "Adopt <n> items from Claude Code and Codex? Claude Code and Codex used outside this app will no longer load them. A backup is kept, and Restore puts them back." with the counts per tool and any blocking conflicts, then signs `POST /lado/setup/adoptions` with `planSha256`.
  - `lado:presence:restoreSetup(adoptionId)`: reads the adoption, asks "Restore <n> items to their original locations?", signs the restore.
  - `lado:presence:changeSetup(request)`: validates the request's shape (route, method, body fields), sends it as a dry run, shows the item, its kind and why it needs confirmation ("This change can run commands or widen access."), signs the same request with `changeSha256`.
- Preload, the three bridges and the tray catalog (`node scripts/sync-tray-catalog.mjs`) follow the existing channels.

## 15. Renderer

- `lib/lado-setup.ts` (clients, query keys under `["lado", "setup", …]`, mutations that dry-run first and go through presence when the answer says so), hooks, `lado-errors.ts` codes and details, `lado-events.ts` handles `lado_setup` (reload the inventory, conflicts, effective previews and the project's tasks).
- **Settings → Local setup** (`components/settings/LocalSetupSection.tsx`, after Rules): the OS line (verified or not, and why adoption is refused), Rescan, a conflicts card (by severity; turn off, edit, accept), one card per tool with its entries grouped by kind (state, location, a Global switch for managed items, a menu with Edit, History, Delete and drift actions), Adopt… (a dialog with the entries, their actions, rewrites, warnings and preconditions, then the desktop confirmation), and an adoptions card with Restore.
- **Item editor** (text items): `MarkdownEditor` for Markdown, a plain code editor for JSON, the diff with `@pierre/diffs`, the impact dialog of rules (`LadoRuleImpactDialog` pattern), History with each version's content and actor. Folder items show their files read-only.
- **Project settings → Rules**: the managed setup items with Inherit/On/Off and where the state comes from, and a read-only "From the repository" card (decision l) whose Edit opens the New Task composer prefilled.
- **Task view Rules tab** and the **run panel**: the setup items of the snapshot and the comparison with now; the review reason reads "rules or local setup changed".
- DESIGN.md decisions log gets one row for the section, the dialog, the editor and the project and task additions.

## 16. Steps

| Step | Scope |
|---|---|
| S01 | probe: real-binary delivery measurement on Linux (`service/ladosetup/probe`, skipped without `AO_LADO_SETUP_PROBE=1`) |
| S02 | migration 0007, `queries/lado_setup.sql`, sqlc gen, migration test |
| S03 | domain: items, versions, trees, bindings, adoptions, acceptances, inventory entries and reasons, OS table, presence actions |
| S04 | store: adoption, versions, items, bindings, acceptances, pins in the approval, readers, CDC |
| S05 | inventory: scan, statuses and reasons, processes, live sessions, cache |
| S06 | conflicts, acceptances matching, AO system layer hash |
| S07 | adoption: preview, plan hash, preconditions, backup, copy, removal, rewrites, record, verify |
| S08 | restore and drift |
| S09 | Claude delivery: homes, `session_manager` hooks, transcript fix, credential readers, cleanup |
| S10 | Codex delivery: TUI overrides, Chat MCP lists, instructions section |
| S11 | policy: `SetupSource`, `lado-policy/2`, pins, effective preview, impact, protected rule |
| S12 | changes: create, edit, delete, bindings, drift keep and revert, acceptances, presence rules |
| S13 | HTTP routes and DTOs, specgen, `npm run api`, daemon wiring, scan at start |
| S14 | renderer data layer, errors, events; Electron main presence channels; bridges; tray catalog |
| S15 | Settings → Local setup: section, conflicts, items, adoption dialog, adoptions and restore, DESIGN.md row |
| S16 | editor, history, drift review, creation; project Rules page; task Rules tab and run panel |
| S17 | docs: TDD, HARNESS, PRD, ADR-0005, LADO.md, LADO.ko.md, lab recipe |

## 17. Acceptance tests (HARNESS §15 items 9-15, PRD gate 14)

| Item | Test (package) |
|---|---|
| 9 Backup before removal; restore byte-identical; credentials untouched | `TestAdoptionBacksUpBeforeRemoving`, `TestAdoptionNeverTouchesCredentials` (S07), `TestRestoreIsByteIdentical` (S08) — `service/ladosetup` |
| 10 AO sessions on accounts and the own login see adopted items; direct use does not | `TestClaudeHomeCarriesManagedItems` (own login and account base), `TestDirectUseNoLongerSeesAdoptedItems` (S09); S01's probe with the real binaries |
| 11 The account linker creates nothing in `~/.claude` after adoption | `TestAccountLinkerCreatesNothingAfterAdoption` (S09, real `agentaccount.Claude.Prepare` on a fake home) |
| 12 An edit after admission → Review required; the running session is unaffected | `TestSetupChangeAfterApprovalNeedsReview` (S11, `service/ladotask` with real policy and setup), `TestExecutionHomeUsesPinnedVersions` (S09) |
| 13 An adopted MCP server named `ao` blocks | `TestConflictMCPServerNamedAO` (S06) |
| 14 A repo-tracked CLAUDE.md is never moved; Edit creates a task | `TestRepoTrackedFilesAreNeverAdoptable` (S05); renderer test of the Edit action (S16) |
| 15 A new `~/.claude/settings.json` key after adoption is a new item, not moved | `TestDriftShowsNewLocalItems` (S08) |
