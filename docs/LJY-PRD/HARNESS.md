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

## 8. Snapshot and change semantics

- The snapshot is fixed at queue admission (TDD §5.2). It holds rule version ids, the binding behind each entry, setup item ids and content hashes, the AO build and its system-layer hash, the compiler version, required capabilities, the permission ceiling, and the canonical sha256.
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

## 13. Storage

`lado_rules`, `lado_rule_versions`, `lado_rule_bindings`, `lado_policy_snapshots`, `lado_setup_adoptions`, `lado_setup_items`, `lado_setup_item_versions`, `lado_conflict_acceptances`. They follow TDD §4: separate migration track, `lado_` prefix, no foreign keys into upstream tables, audit in `lado_audit_events`.

## 14. Protected gates

| Gate | Enforced at | Strength |
|---|---|---|
| `plan.approval.required` | Worker gate (TDD §6.2) | Prevents |
| `integration.approval.required` | `lado/integration` required check and AO merge hook (TDD §7.1-7.2) | Prevents (T1) |
| `git.protected_branch.no_direct_write` | GitHub ruleset for the remote; reflog check locally (TDD §7.3) | Prevents remotely, detects locally |
| `test.command.must_pass` | Required checks or local runner (TDD §8.1-8.2) | Blocks integration |
| `paths.protected` | Path validator on `base..head` (TDD §8.3) | Detects; blocks integration |
| `agent.permission.ceiling` | Explicit mode, per-turn cap, Codex mapping (TDD §6.4-6.6) | Prevents, within the tool |
| `setup.change.requires_presence` | Human-presence channel (§9.5) | Prevents |
| `git.commit.message.korean` (optional, not protected by default) | Commit validator | Detects (heuristic) |

Validators are registered in code. AI cannot invent a validator that runs arbitrary shell.

## 15. Acceptance tests

1. Global ON / Project OFF / Task ON resolves to ON.
2. Project INHERIT follows a Global toggle.
3. A protected gate rejects Task FORCE_OFF and AI-originated bypass.
4. Disabling keeps rule history and leaves existing snapshots unchanged.
5. The Korean commit rule blocks a clearly non-compliant commit in `base..head` and allows configured prefixes.
6. A natural-language change produces an approval preview, not an immediate change.
7. A missing enforcement capability is reported and blocks dispatch.
8. Conflicting mandatory rules block dispatch with an actionable diagnostic.
9. Adoption writes the backup and manifest before removing anything; restore is byte-identical; credential files are untouched.
10. After adoption, AO sessions on both account and own-computer login see the adopted items, and the tools run directly do not.
11. AO's account linker no longer creates entries in `~/.claude` after adoption.
12. Editing a managed item after admission marks the entry Review required; the running session is unaffected.
13. An adopted MCP server named `ao` is a blocking conflict.
14. A repo-tracked CLAUDE.md is never moved; "Edit" creates a task.
15. A new `~/.claude/settings.json` after adoption shows up as a new item and is not moved automatically.
