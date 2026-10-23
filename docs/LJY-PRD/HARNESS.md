# LADO — Rules and local setup (AO extension spec)

- Version: 2.0
- Date: 2026-10-19
- Related: [PRD.md](PRD.md), [TDD.md](TDD.md), [adr/0005](adr/0005-local-harness-adoption.md)
- Previous version: [v1/HARNESS.md](v1/HARNESS.md) (1.1). Kept: concepts, resolution, schema, enforcement classes, snapshot semantics, natural-language flow. New: layers over AO (§2), AO hook points (§6), local setup adoption (§9), conflict checks (§10).

Paths: Go code under `backend/internal/` is cited without that prefix. Other paths start at the repo root.

## 1. Purpose and names

The LADO harness is everything that decides what an AI worker knows, what it may do, what it must produce, and how its work is checked. It has two sources:

- **Rules**: versioned LADO rules with Global, Project and Task bindings.
- **Local setup**: the developer's existing agent configuration (CLAUDE.md, AGENTS.md, settings, hooks, skills, subagents, commands, plugins, MCP servers), adopted into one LADO-managed location so LADO is the only place to manage it (§9).

In AO, "harness" means the agent adapter (`sessions.harness`, `domain.AgentHarness`). LADO code therefore says `rule` or `policy` for rules and `setup` for local setup. The UI is a Settings section called **Rules** with a **Local setup** part. `DESIGN.md:287` bans "Harness" as a settings label.

## 2. Layers

| Layer | Content | Editable in LADO |
|---|---|---|
| AO system | What AO itself adds to every session: worktree hooks in `.claude/settings.local.json` (11 events calling `ao hooks claude-code <event>`, `adapters/agent/claudecode/hooks.go:11-16,42-54`); Codex hooks as `-c hooks.*` flags (`adapters/agent/codex/hooks.go:72-116`); AO's system prompt sections (`session_manager/prompt.go:74-145`); the `using-ao` skill (`skillassets/`); the `ao` MCP server in Chat (`session_manager/chat_spawn.go:345-373`); `AO_*` environment (`session_manager/manager.go:5970-6032`); workspace trust entries (`adapters/agent/claudecode/claudecode.go:236-252`) | No. Always on, shown read-only |
| Protected gates | §14 | Owner only, with human-presence approval |
| Task | Task-scope bindings | Yes |
| Project | LADO project rules; AO's `AgentRules`, `AgentRulesFile`, `OrchestratorRules` (`domain/projectconfig.go:40-47`), shown as Project rules; adopted untracked project files; repo-tracked files (CLAUDE.md, AGENTS.md, `.claude/settings.json`, `.mcp.json`) | LADO rules and adopted items: yes. Repo-tracked files: through git (§9.5) |
| Global | LADO global rules; adopted user-level setup | Yes |

For ordinary rules the nearest explicit binding wins: Task over Project over Global. Protected gates are a floor that lower scopes cannot remove. The AO system layer is outside resolution: turning it off would break AO, so conflicts with it are reported (§10), not toggled.

LADO decides what reaches the agent. The agent tool then combines the pieces with its own precedence. Claude Code, for example, merges hook and permission arrays from every settings source, and orders scalar settings managed > command line > local > project > user. The conflict checks model that merge so the preview shows what the agent will actually see. For Codex, app-server `config/read` returns the loaded layers and their origins (`adapters/chatdriver/codexappserver/codexproto/protocol.gen.go:1225-1277`), which lets LADO verify the result.

## 3. Core concepts

- **Rule definition**: stable id, version, instructions, triggers, enforcement, owner.
- **Rule binding**: attaches a definition to Global, Project or Task scope.
- **Override**: `INHERIT`, `FORCE_ON`, `FORCE_OFF` at Project and Task scope.
- **Protected gate**: an owner-controlled constraint that ordinary bindings and AI cannot override.
- **Setup item**: one adopted piece of local setup (tool, kind, scope, managed path, sha256). It takes part in resolution like an ordinary rule.
- **Adoption**: one approved batch that moved setup items, with its backup manifest.
- **Effective harness**: the resolved set for one task or execution.
- **Snapshot**: the immutable, hashed effective harness stored with an execution.
- **Agent profile**: role, output schema, context budget and agent preference.

## 4. Resolution

1. Load the active version of every rule and setup item, with its Global binding. Rules default to OFF unless configured; adopted setup items default to ON, since they were active before adoption.
2. Apply the Project override, then the Task override. The nearest non-INHERIT value wins.
3. Apply protected gates as a floor.
4. Evaluate triggers. Inactive or non-matching rules are not applied.
5. Run the conflict checks (§10). An unresolved blocking conflict blocks dispatch.
6. Compile prompt text, validators, tool policy, workflow gates and the setup home separately.
7. Store the effective list, the source binding of each entry, versions, content hashes and the canonical snapshot hash.

Examples for ordinary rules: Global OFF + Project FORCE_ON → ON. Global ON + Project FORCE_OFF + Task INHERIT → OFF. Task FORCE_ON then restores ON. A protected rule rejects any attempt to disable it. Disabling never deletes a definition.

Repo-tracked files are loaded by the agent tool straight from the worktree, so LADO cannot switch them off per scope. They are listed as "loaded by the tool" and change through git.

As built in step 5 (`service/ladopolicy`, TDD §9), for rules only; setup items join in step 7:

- Rules are taken in creation order, archived ones left out. A protected rule is ON whatever its bindings say. Otherwise the nearest binding that is not INHERIT wins, Task over Project over Global, and only the bindings of the project and task being resolved count. A rule whose Global binding is not ON is OFF.
- Triggers: step 5's rules have none, so each one applies to every LADO execution in its scope. Trigger evaluation (step 4 above) comes with the first trigger a rule needs, not as an "every run" trigger stored on each rule.
- The permission ceiling is the mode of `agent.permission.ceiling` (§14). A missing or invalid one falls back to `accept-edits` and blocks. A task's run gets the plan's `permission_ceiling` when it is below the ceiling, and the ceiling otherwise; a plan above the ceiling is lowered to it with a warning.
- The agent is the task's choice, else the project's worker; no agent blocks. Then the capability check (§7) and the conflict check run. The conflict check covers declared exclusions only: two ON rules where either names the other in `conflictsWith` block, once per pair. The other checks of §10 come with step 7.
- Compiled: the prompt text of the ON rules with prompt enforcement, the workflow gates, the ceiling and the run's mode. Validators come with step 8, the setup home with step 7.
- Problems are blocking, warning or info. Any blocking problem marks the snapshot (§8) as blocking.

## 5. Rule schema (example)

```yaml
id: git.commit.message.korean
version: 1
name: Commit messages in Korean
owner: user
category: git
protected: false
default_enabled: true
triggers: [before_integration]
enforcement: [prompt, validator]
instruction: "Write Git commit subjects and bodies primarily in Korean. Conventional Commit prefixes may stay in English."
validator:
  kind: commit_message_locale
  config:
    language: ko
    allow_prefixes: [feat, fix, docs, refactor, test, chore]
    allow_issue_ids: true
failure_action: block_and_request_fix
```

The validator runs over `base..head` in VALIDATING (TDD §8.3), not in a commit hook, because `--no-verify` skips hooks. A locale check is heuristic: technical terms and short messages are ambiguous, so uncertain cases go to the owner instead of being reported as certain.

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

As stored in step 5 (`lado_rules`, `lado_rule_versions`, `lado_rule_bindings`; TDD §3.1):

- The rule: a stable id (fixed for the built-ins, such as `plan.approval.required`; `lado-rule-<uuid>` for the developer's), whether it is built in and whether it is protected, and when it was created and archived.
- Its versions, numbered from 1 and never changed: `name` (at most 120 characters), `category` (at most 40, may be empty), `instruction` (required, at most 8,000), `enforcement`, `config` (a JSON object; the ceiling's is `{"mode": ...}`), `conflictsWith` (at most 20 other rules, never itself or a protected rule), the actor and the time, and the sha256 of the canonical content. A rule the developer creates has `prompt` enforcement and an empty config.
- Its bindings: Global `on` or `off`; Project and Task `force_on` or `force_off`. INHERIT is the absence of a row.

The canonical content is JSON in this field order, with text trimmed, `conflictsWith` sorted without duplicates and `config` compacted. The optional built-in, for example:

```json
{"name":"Commit messages in Korean","category":"git","instruction":"Write Git commit subjects and bodies primarily in Korean. Conventional Commit prefixes such as feat, fix, docs, refactor, test and chore may stay in English.","enforcement":["prompt"],"config":{},"conflictsWith":[]}
```

`owner`, `triggers`, `default_enabled`, `validator` and `failure_action` are not stored yet: the Global binding takes the place of `default_enabled`, triggers wait for the first rule that needs one (§4), and validators come with step 8.

## 6. Enforcement classes and AO hook points

| Class | Where LADO applies it | Strength (T1) | Limits |
|---|---|---|---|
| `prompt` | LADO section in `buildSystemPrompt` (`session_manager/manager.go:5692`). Claude TUI receives it through `--append-system-prompt-file` (`backend/pkg/agentruntime/command.go:289-311`), Claude ACP through `_meta.systemPrompt` (`adapters/chatdriver/claudeacp/driver.go:320-332`), Codex through `model_instructions_file` / `developer_instructions` (`backend/pkg/agentruntime/command.go:346-357`) or app-server `developerInstructions` (`adapters/chatdriver/codexappserver/driver.go:328-329,411-412`) | Advisory | Cursor gets no system prompt (`adapters/agent/cursor/cursor.go:96-98`) |
| `validator` | `service/ladoverify` in VALIDATING: commits, changed paths, test commands (TDD §8) | Detects; blocks integration | Heuristic validators report uncertainty |
| `tool_policy` | Explicit permission mode, Codex sandbox and per-turn cap (TDD §6.4-6.6); Claude tool allow and deny lists (`ports/agent.go:707-735`, TUI flags `backend/pkg/agentruntime/command.go:279-287`) | Prevents, within the agent tool's own enforcement | `bypass-permissions` ignores both lists (`ports/agent.go:721-728`). A same-user shell is not a sandbox (T2) |
| `workflow_gate` | Worker gate (TDD §6) and integration approval (TDD §7) | Prevents | — |

A rule that only has `prompt` enforcement is labelled advisory everywhere.

## 7. Capability evaluation

Before admission LADO checks that the chosen agent can honor each mandatory rule:

- `prompt`: system prompt injection exists for the adapter (not Cursor; degraded for fx, `adapters/agent/fx/fx.go:3-5`).
- `tool_policy`: the adapter maps the permission ceiling. In Chat it must meet AO's production floor, which includes approvals (`ports/chat.go:246-251`).
- `workflow_gate` and `validator`: always available, because the daemon runs them.
- Setup items: the tool can receive the item kind on both its TUI and Chat paths (§9.4). Claude Code and Codex come first.

A mandatory rule that the agent cannot honor blocks dispatch with a diagnostic. `UNSUPPORTED` is an explicit result, never a silent fallback.

As built in step 5 (TDD §9), for a task: `permission_mode` is always required and `prompt` whenever the rules give the worker text. Claude Code and Codex support both. Every other agent is unsupported or unverified for at least one class, a blocking problem (`LADO_AGENT_CAPABILITY_UNSUPPORTED`, `LADO_AGENT_CAPABILITY_UNVERIFIED`), so a task with that agent shows Blocked once approved; a degraded class (fx's prompt) is a warning. Codex below `bypass-permissions` adds an info note that it asks before commits, pushes and network calls (TDD §6.6).

As built in step 6, the dispatcher looks again right before it spawns (TDD §5.3, check 6): a snapshot with a blocking problem, an agent other than Claude Code and Codex, or a snapshot without a permission mode blocks the queue entry, and no worker starts.

## 8. Snapshot and change semantics

- The snapshot is fixed with the plan approval (TDD §5.2). The approval's queue entry points at it, and the dispatcher compares it with a fresh resolution before it spawns (TDD §5.3). It holds rule version ids, the binding behind each entry, setup item ids and content hashes, the AO build and its system-layer hash, the compiler version, required capabilities, the permission ceiling, and the canonical sha256.
- The setup home for an execution is built from the snapshot's immutable blobs (§9.4). Editing an item later never changes a running agent.
- A relevant change before dispatch (rule edit, toggle, setup item edit, drift) marks the queue entry Review required. LADO never re-resolves silently or swaps the snapshot.
- Ordinary changes apply to the next run. An emergency change to a protected gate cancels affected runs and writes an audit event with who and why.
- A change to a plan, base or rule can require revalidation, according to impact.
- Rule versions and setup blobs referenced by past executions stay readable.

Write contract:

1. A rule has a stable `rule_id`. Each edit creates an immutable version. A binding names the rule and an explicit version or a version policy.
2. The Global binding is ON or OFF. Project and Task bindings are INHERIT, FORCE_ON or FORCE_OFF.
3. AI-proposed changes create a `RuleChangeProposal` (schema, target, old/new diff, conflicts, required capabilities, affected queued tasks). Nothing changes before approval.
4. On approval, the version, binding change and audit event are written in one transaction, guarded by optimistic concurrency on the active version.

As built in step 5:

- The snapshot is fixed with the plan approval, in its transaction (TDD §5.2); step 6's queue entry will point at it. Its format is `lado-policy/1`, canonical JSON whose field order is the format: the compiler name, the AO build (the desktop app version), the project and task, the agent (harness, model, permission mode), the permission ceiling, every ON rule (rule id, version id and number, the version's sha256, name, protected, enforcement, strength, and the binding it came from), the kind, path and sha256 of AO's worker instructions, the compiled prompt, the workflow gates, the capability results and the problems. Setup items and the AO system layer's hash come with step 7. `lado_policy_snapshot_rules` lists the versions it used.
- A change that gives an approved revision another hash marks the task Review required: a rule edit or toggle, an archive, the ceiling, the task's agent, AO's project instructions or the AO build. LADO never swaps the snapshot: the developer approves again, which writes a new snapshot, and the old one stays. The task's Rules tab compares the approved snapshot with now (rules added, removed or changed).
- The active version is the one with the highest number. Bindings follow it: no binding names a version or a version policy.
- Each change writes its version, binding or archive state together with its audit event in one transaction. A new version needs the active one as its base (`baseVersion`; 409 `LADO_RULE_CHANGED` otherwise). Ordinary changes need no presence and are recorded with the actor `api`. The ceiling's mode needs a presence proof, recorded as `rule.protected_changed` by `human` (`human:dev-presence` under dev presence). A request that edits a rule and archives it writes two transactions.
- Every change can run as a dry run first (`dryRun`), which writes nothing and lists its impact: the LADO projects whose rules change, and the approved tasks that would need a new approval. AI drafts (`RuleChangeProposal`, HAR-006) come later.
- A rule a snapshot used is never deleted; archive it instead. Built-in rules are never deleted, and protected rules are never archived or bound.

As built in step 6:

- The queue entry written with the approval names its snapshot. At dispatch the rules are resolved again for the approved revision. Another hash sends the entry to Review required (`rules_changed`) and starts nothing, so the developer approves again, which writes a new snapshot; a blocking snapshot is Blocked at dispatch too (§7).
- A running execution keeps its snapshot. The worker's system prompt takes the snapshot's rule text, found through the session's client request id, so restore and resume rebuild it from the snapshot (TDD §9). Its permission mode is the snapshot's, per-turn approvals and approval-mode options stay at or below the ceiling, and its agent and interface cannot change (TDD §6.4, §6.5). Rule changes reach only later runs.
- Not built: cancelling affected runs on an emergency change to a protected gate, and revalidation by impact.

## 9. Local setup adoption (D11)

Decided by the owner on 2026-10-19 and recorded in [adr/0005](adr/0005-local-harness-adoption.md). Settings spread over many places are hard to manage, so LADO becomes the single place: an adopted item is backed up, copied into LADO's managed home and removed from its original location. From then on only sessions launched by this app use it.

Consequence: Claude Code and Codex used directly, outside this app (terminal, IDE extensions), no longer load adopted items. Restore (§9.7) undoes an adoption.

### 9.1 Inventory (HAR-010)

AO resolves the user-level folders as `$CLAUDE_CONFIG_DIR` or `~/.claude` plus `~/.claude.json` (`daemon/daemon.go:1332-1337`), and `$CODEX_HOME` or `~/.codex` (`daemon/daemon.go:551`).

**Claude Code**

| Item | Location | Adopt |
|---|---|---|
| User instructions | `~/.claude/CLAUDE.md` and other top-level `*.md` (imports) | Yes, Global |
| User settings | `~/.claude/settings.json`: hooks, permissions, env, model, status line, output style, enabled plugins, marketplaces | Yes, Global. Login keys (`apiKeyHelper`, `awsAuthRefresh`, `awsCredentialExport`, `forceLoginMethod`, `forceLoginOrgUUID`) and credential env vars stay, using the same split as AO's account copier (`adapters/agentaccount/claude.go:32-41,280-323`) |
| Skills, subagents, commands, output styles, hook scripts | `~/.claude/{skills,agents,commands,output-styles,hooks}/` | Yes, Global |
| Plugins and marketplaces | `~/.claude/plugins/` | Yes, as one unit |
| User MCP servers | The `mcpServers` key of `~/.claude.json` | Yes, that key only. The rest of the file is app state |
| Untracked project files | `.claude/settings.local.json`, `CLAUDE.local.md` in a registered project's main checkout | Yes, Project |
| Repo-tracked project files | `CLAUDE.md` in any directory, `.claude/settings.json`, `.claude/{skills,agents,commands}/`, `.mcp.json` | No. Read-only in LADO (§9.5) |
| Credentials, history, caches | `.credentials.json`, `projects/`, `sessions/`, `todos/`, `history.jsonl`, `file-history/`, `shell-snapshots/` and similar | Never |
| Managed (organization) settings | OS policy location | Never; shown read-only |

AO's own entries in a worktree's `.claude/settings.local.json` belong to the AO system layer. They are recognized by the `ao hooks claude-code ` prefix (`adapters/agent/claudecode/hooks.go:14,58-65`).

**Codex**

| Item | Location | Adopt |
|---|---|---|
| User instructions | `~/.codex/AGENTS.md` | Yes, Global |
| Config | `~/.codex/config.toml`: model, profiles, providers, MCP servers, hooks, approval and sandbox defaults | Yes, Global |
| Prompts, skills, plugins | `~/.codex/{prompts,skills,plugins}/` | Yes, Global |
| Repo-tracked files | `AGENTS.md` in any directory, `.codex/` | No. Read-only in LADO |
| Credentials, history, caches | `auth.json`, `sessions/`, `history.jsonl`, `log/`, caches | Never |

Discovery uses a filesystem scan of these paths, and, for Codex, the app-server's `config/read`, `hooks/list`, `plugin/list` and `skills/list` (`adapters/chatdriver/codexappserver/codexproto/protocol.gen.go:53-99`; `adapters/chatdriver/codexappserver/skills.go:41-93`) to confirm what Codex actually loads. Each inventory entry records tool, kind, scope, path, size, sha256, tracked or not, and whether it can be adopted, with the reason when it cannot. Other agents (Gemini, OpenCode, Cursor and the rest) are listed as "not managed" for now.

### 9.2 Adoption flow (HAR-011)

1. **Scan** and show the inventory.
2. **Preview** each item's action (move, keep in place, skip), its managed path and how sessions will receive it. State plainly that direct use of Claude Code and Codex outside this app will stop loading the moved items.
3. **Preconditions**: no Claude or Codex process holds the files (Windows locks them, and a running session would lose its setup mid-run), and no AO session of that tool is running on the computer's own login.
4. **Approve** through the human-presence channel ([adr/0002](adr/0002-human-presence-approval-channel.md)).
5. **Back up** every original into `<state>/lado/backups/<adoption-id>/`, keeping the relative path, mode and mtime, and write `manifest.json` (original path, sha256, size, mode, tool, kind, action). Flush, then re-hash to verify.
6. **Copy** into the managed home (§9.3) and verify the hashes.
7. **Remove** the originals. Whole files and directories are deleted. In `settings.json` and `~/.claude.json` only the adopted keys are removed and the file is rewritten atomically; login keys and app state stay.
8. **Record** `lado_setup_adoptions`, `lado_setup_items` and an audit event, and save the hashes of what remains at the original locations as the drift baseline.
9. **Verify** per tool that sessions now load the managed items. For Codex, `config/read` origins point at the managed home.

A failure before step 7 removes nothing. A failure during step 7 stops, reports, and leaves restore available.

### 9.3 Managed home

```text
<state>/lado/harness/
  claude/
    CLAUDE.md, *.md
    settings.json              # adopted user settings, without login keys
    skills/ agents/ commands/ output-styles/ hooks/ plugins/
    mcp.json                   # adopted mcpServers
  codex/
    AGENTS.md
    config.toml
    prompts/ skills/ plugins/
  projects/<project-id>/
    claude/settings.local.json, CLAUDE.local.md
```

Every saved version of every file is also kept as a blob in `<state>/lado/blobs/`, which gives history, snapshots and restore.

### 9.4 Delivery to sessions

The managed home takes the place of the original location for every session this app launches.

- **Account sessions.** AO's account homes (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`; `adapters/agentaccount/claude.go:58-67`, `adapters/agentaccount/codex.go:41-66`) link shared entries from `~/.claude` and `~/.codex` (`adapters/agentaccount/claude.go:26-30,268-274`, `adapters/agentaccount/codex.go:15-35,198-230`). After adoption they link from the managed home instead. AO's linker also creates missing entries in the user's real folder (`adapters/agentaccount/share.go:35-38,62-76`); that must stop, or the next launch would undo the adoption.
- **Sessions on the computer's own login.** AO sets no config folder for them today (`service/agentaccounts/service.go:898-900`). LADO gives them a home too: a per-execution folder that links credentials and app state from the real folder and setup items from the snapshot. To verify per tool and OS before shipping: Claude's credential lookup under `CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR` (AO sets both for accounts), Codex `auth.json`, and file and directory links on Windows.
- **Fallback: launch options.** Claude `--settings`, `--mcp-config`, `--plugin-dir` and `--append-system-prompt-file` (AO uses only the last today; AO's cloud reviewer already passes `--setting-sources`, `cloud/internal/workerexec/command.go:121-126`). Codex `-c` overrides and the app-server thread `config` (`adapters/chatdriver/codexappserver/driver.go:432-472`).
- An item kind is adoptable only if one of these mechanisms reaches **both** the TUI and the Chat launch path of its tool. Otherwise it stays where it is and the inventory says why.
- Each execution's home is built from the snapshot's blobs, so it cannot change during the run (§8).
- AO's system layer is added on top exactly as today.

### 9.5 Create, edit, delete, toggle (HAR-012)

- Every change to a managed item shows a diff and needs approval before it is written. The previous content stays as a blob, and an audit event records the change.
- Changes that can run code or widen access (hooks, MCP servers, permissions, environment) need human-presence approval. Other changes are approved in the UI.
- A toggle is a binding: OFF at a scope keeps the item in the managed home but leaves it out of homes built for that scope.
- Repo-tracked files stay read-only in LADO. "Edit" creates a task with a prefilled plan, or the developer edits the repo directly. The next scan sees the new hash, and queued executions of that project become Review required.

### 9.6 Drift (HAR-014)

LADO rescans at app start, on demand, and before admission.

- New items at original locations after adoption (for example, Claude Code used directly wrote a new `settings.json`, or a plugin was installed outside LADO) are shown as new local items. Adoption is offered, never automatic. Claude Code writes `~/.claude.json` during normal use, so this kind of drift is expected.
- A managed file whose hash no longer matches its record was edited outside LADO. The user chooses to keep it (as a new version) or revert it.

### 9.7 Restore (HAR-015)

- Restoring an adoption writes every manifest entry back to its original path, when that path is free or unchanged since adoption.
- If something new is there, both are kept: the restored copy gets a `.lado-restored` suffix, and the report lists it.
- Keys go back into `settings.json` and `~/.claude.json` only where they are absent.
- Managed copies stay until the user deletes them. An audit event records the restore.
- Restored files must be byte-identical to the backup (PRD gate 14).

### 9.8 Never adopted

Credentials and login state (`.credentials.json`, `auth.json`, keychain entries, login keys), session history and caches, organization-managed settings, repo-tracked files, and anything outside the known locations.

## 10. Conflict checks (HAR-013)

Checks run at scan time, on every edit and at admission. Results appear in the effective preview as blocking (dispatch blocked), warning or info.

| Check | Example | Severity |
|---|---|---|
| Hooks on the same events as AO's hooks | An adopted `UserPromptSubmit` or `Stop` hook | Info: Claude runs both |
| Hooks that block tools AO needs | A `PreToolUse` hook that denies `ao`, `git` or `gh` | Blocking |
| Hooks that add text to every prompt | A `UserPromptSubmit` or `SessionStart` hook that injects a style mode | Warning: it can override prompt rules, such as commit message language |
| Permission defaults above the ceiling | Adopted `permissions.defaultMode` set to `bypassPermissions` | Warning. LADO passes an explicit mode (TDD §6.5) |
| Allow rules against protected gates | `allow: Bash(git push:*)` while `git.protected_branch.no_direct_write` is on | Warning: the gate is enforced elsewhere |
| Deny rules against AO | A deny rule matching `ao` commands | Blocking |
| MCP server name taken | An adopted server named `ao`, AO's own Chat server (`session_manager/chat_spawn.go:345-373`) | Blocking |
| Skill or command name taken | An adopted skill named `using-ao` | Blocking |
| Reserved environment variables | Adopted `env` sets `AO_*`, `CLAUDE_CONFIG_DIR` or `CODEX_HOME` | Blocking |
| Instructions against LADO prompt rules | User CLAUDE.md asks for English commits; a LADO rule asks for Korean | Warning, from an optional AI comparison that the owner confirms |
| Project file against Global setup | Repo `.claude/settings.json` overrides an adopted Global setting | Info, with the tool's precedence explained |
| Kind unsupported by the chosen agent | A Claude plugin for a Codex execution | Warning |

To resolve a conflict, toggle the item off at a scope, edit it, or accept it. Accepted conflicts are recorded in the snapshot.

## 11. Natural-language management

User: "Add a global rule requiring Korean Git commit messages."

1. An agent drafts the rule schema, trigger, enforcement and scope through `chat.Service.RunBackgroundTask` (`service/chat/background.go:13-46`). AO runs background tasks in a temporary folder (`session_manager/chat_spawn.go:73-167`), which matters because ACP ignores the read-only flag (`service/chat/background.go:27-34`).
2. LADO validates the schema, conflicts, permissions, known validators and the scope's impact.
3. The UI shows the exact before/after diff, the affected tasks, and whether enforcement is mechanical or advisory.
4. The user approves. One transaction writes the version, binding and audit event.
5. The UI confirms what is saved and how the next run will behave.

For "turn that off for project A", LADO resolves the rule unambiguously or asks which one. AI never changes protected gates and never skips approval.

## 12. Rules manager UI

- Settings → **Rules**, registered in `frontend/src/renderer/components/settings/settingsCatalog.tsx`, with the parts Rules, Local setup and Conflicts. Project settings get a Rules section for the project scope. Task detail shows the effective preview and the task overrides.
- Group by scope, category and trigger, with search.
- Show definition version, binding override, effective state, where it is inherited from, protected status and enforcement strength.
- Toggle ON/OFF, with Inherit at Project and Task scope. Preview the affected projects and tasks before applying.
- Keep Disable, Archive and Delete separate, with confirmation and history.
- Show the active snapshot on every execution page.
- Keep draft, saved, active and "effective for this run" visibly distinct.
- Follow DESIGN.md: the settings modal composition (`DESIGN.md:282`), sentence case, the shared-list rule, and strings in the i18n files.

As built in step 5 (DESIGN.md decisions log, 2026-10-22):

- **Settings → Rules** (`frontend/src/renderer/components/settings/RulesSection.tsx`): a search over names, categories and instructions, with **New rule** at its end. A **Protected rules** card: Plan approval required reads "Always on", and Permission ceiling has a mode menu whose change the desktop app confirms. Then one card per category, in creation order with uncategorized rules last as "Other". Each row has the name, one line with the strength and how many projects and tasks override the rule, a Global switch, and a menu with Edit, History, Archive and Delete. Archived rules sit in a collapsed **Archived** card with Unarchive and Delete. The editor takes the name, category, instruction and the rules it conflicts with, and at creation whether the rule is on for every LADO project; History lists the versions with their content and actor.
- **Project settings → Rules**, for local LADO projects only (`frontend/src/renderer/components/lado/LadoProjectRulesSection.tsx`): every rule with Inherit, On or Off, its effective state and where that comes from; protected rules read "Always on", and the ceiling shows its mode. A read-only **From AO project config** card lists AO's instructions with their sizes and read errors. **New rule for this project** creates a rule that is off at Global and forced on here.
- **Task view**: the right pane has Plan and Rules tabs (`frontend/src/renderer/components/lado/LadoTaskRules.tsx`). Rules shows the agent and its mode, the problems by severity, the approved snapshot against now (a match, or the rules added, removed and changed), the task's overrides on the same rows as the project page, the AO instructions, and, collapsed, the rule text the worker will get. Approve is offered again for Review required and Blocked tasks.
- **Board**: a Review required task reads "Rules need review" and a Blocked one "Blocked", both in the Plan lane with the attention tone.
- Every change runs as a dry run first. When approved tasks would need a new approval, a dialog lists them and applies the change only on confirmation; Delete always asks, and for a rule a snapshot used says to archive it instead. Built-in rules offer no Delete.
- Not yet: the Local setup and Conflicts parts (step 7), grouping by trigger (§4), the active snapshot on execution pages (step 6), and AI drafts (HAR-006).

As built in step 6 (DESIGN.md decisions log, 2026-10-23):

- **The active snapshot on every execution page.** The session view's inspector, on its summary tab, has a **LADO run** section for a session that is a LADO execution (`frontend/src/renderer/components/lado/LadoExecutionPanel.tsx`, in the `context` slot of `SessionInspector.tsx`, not in the inspector rail's frame). It reads `GET /lado/executions/{sessionId}` and shows the agent and model, the mode, the permission ceiling, the plan revision and when it was approved, the rules with the snapshot's hash, the base and how many commits it moved since the plan (hidden when 0 or unknown), the deadline or that the run stopped at it, and, collapsed, the rule text the worker got. A session that is not an execution shows nothing.
- **Board.** A dispatched worker's session card has the line "LADO · plan r<n> · rules <7 hex>" (`frontend/src/renderer/components/lado/LadoSessionLine.tsx`), for the local projects in LADO mode. The lanes show the queue states and their reasons (TDD §3.3).
- **Settings → Runs** (`frontend/src/renderer/components/settings/RunsSection.tsx`, after Rules): concurrent runs (1-8), maximum run time (off, 1, 2, 4, 8, 12 or 24 hours) and "Wait while memory is tight". A change saves all three at once; a failed save shows the daemon's message under that row and returns to the stored values.
- **Project settings → LADO** (`frontend/src/renderer/components/lado/LadoProjectSection.tsx`), for every local project and before its Rules page: the LADO mode switch and the project's concurrent runs (1-8), which can change while the mode is off. Turning the mode off asks for the desktop app's confirmation, and the switch stays on while it asks and after a cancel (TDD §5.2).

## 13. Storage

`lado_rules`, `lado_rule_versions`, `lado_rule_bindings`, `lado_policy_snapshots`, `lado_policy_snapshot_rules`, `lado_setup_adoptions`, `lado_setup_items`, `lado_setup_item_versions`, `lado_conflict_acceptances`. They follow TDD §4: separate migration track, `lado_` prefix, no foreign keys into upstream tables, audit in `lado_audit_events`. Step 5 created the first five (migration `0005_lado_rules.sql`); the setup and conflict tables come with step 7.

## 14. Protected gates

| Gate | Enforced at | Strength | Registered |
|---|---|---|---|
| `plan.approval.required` | Worker gate (TDD §6.2) | Prevents | Step 5, as a built-in protected rule that is always on. The worker gate has refused unapproved workers since step 3; from step 6 it lets through only the dispatcher's spawn for a queue entry it holds |
| `integration.approval.required` | `lado/integration` required check and AO merge hook (TDD §7.1-7.2) | Prevents (T1) | Step 8 |
| `git.protected_branch.no_direct_write` | GitHub ruleset for the remote; reflog check locally (TDD §7.3) | Prevents remotely, detects locally | Step 8 |
| `test.command.must_pass` | Required checks or local runner (TDD §8.1-8.2) | Blocks integration | Step 8 |
| `paths.protected` | Path validator on `base..head` (TDD §8.3) | Detects; blocks integration | Step 8 |
| `agent.permission.ceiling` | Explicit mode, per-turn cap, Codex mapping (TDD §6.4-6.6) | Prevents, within the tool | Step 5, as a built-in protected rule at `auto` whose mode changes only with presence. Step 6 applies it to runs: the snapshot's mode at spawn, the per-turn cap, and the refusal of approval-mode options above it (TDD §6.4, §6.5) |
| `setup.change.requires_presence` | Human-presence channel (§9.5) | Prevents | Step 7 |
| `git.commit.message.korean` (optional, not protected by default) | Commit validator | Detects (heuristic) | Step 5, as a built-in rule that starts off and gives the worker prompt text only, so it is advisory. The validator comes with step 8 |

Validators are registered in code. AI cannot invent a validator that runs arbitrary shell. Step 5's built-in rules are seeded at daemon start (`service/ladopolicy` `EnsureBuiltins`, version 1 by `lado`). Only missing ones are seeded, so a stored built-in keeps its versions, its ceiling mode and its Global binding across restarts and upgrades.

## 15. Acceptance tests

Each item names the tests that cover it, all in `service/ladopolicy` unless named otherwise, or the step that owns it.

1. Global ON / Project OFF / Task ON resolves to ON. Step 5: `TestResolveBindings` (PRD gate 3).
2. Project INHERIT follows a Global toggle. Step 5: `TestResolveBindings`.
3. A protected gate rejects Task FORCE_OFF and AI-originated bypass. Step 5: `TestResolveBindings` (a protected gate stays on despite FORCE_OFF and Global off), `TestProtectedRulesRefuseBindingsArchiveAndDelete` (the API refuses FORCE_OFF, Global off, archiving, edits and deletion), `TestCeilingChangeNeedsPresence` (an API caller cannot raise the ceiling without the desktop app's confirmation). AI drafts do not exist yet (HAR-006); an agent calling the API meets the same refusals.
4. Disabling keeps rule history and leaves existing snapshots unchanged. Step 5: `TestDisablingKeepsHistoryAndSnapshots`.
5. The Korean commit rule blocks a clearly non-compliant commit in `base..head` and allows configured prefixes. Step 8, with the validator; step 5 has the rule's prompt text only.
6. A natural-language change produces an approval preview, not an immediate change. Later, with HAR-006.
7. A missing enforcement capability is reported and blocks dispatch. Step 5: `TestResolveCapabilities` and `TestEffectiveBlocksAnUnsupportedAgent` (a blocking problem naming the agent and the capability). Step 6 refuses the dispatch: `service/ladoqueue` `TestUnsupportedAgentBlocksWithoutSpawning` (PRD gate 15).
8. Conflicting mandatory rules block dispatch with an actionable diagnostic. Step 5: `TestResolveConflicts` (a blocking `LADO_RULES_CONFLICT` naming both rules, once per pair). Step 6 refuses the dispatch of a blocking snapshot: `service/ladoqueue` `TestDispatchStopsAtTheFirstFailingCheck`.
9. Adoption writes the backup and manifest before removing anything; restore is byte-identical; credential files are untouched. Step 7.
10. After adoption, AO sessions on both account and own-computer login see the adopted items, and the tools run directly do not. Step 7.
11. AO's account linker no longer creates entries in `~/.claude` after adoption. Step 7.
12. Editing a managed item after admission marks the entry Review required; the running session is unaffected. Step 7, on step 6's queue.
13. An adopted MCP server named `ao` is a blocking conflict. Step 7.
14. A repo-tracked CLAUDE.md is never moved; "Edit" creates a task. Step 7.
15. A new `~/.claude/settings.json` after adoption shows up as a new item and is not moved automatically. Step 7.
