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

As built in step 7, the AO system layer enters each snapshot as one hash (`aoSystem`, §8) over what AO installs, read from the code that installs it: the Claude Code hook events and their command prefix, the Codex hook events, the `ao` MCP server's name and arguments, and the `using-ao` skill's files. The Project layer holds no adopted files: untracked project files stay where they are, and repo-tracked files are listed read-only (§9.1, §9.5).

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
- Compiled: the prompt text of the ON rules with prompt enforcement, the workflow gates, the ceiling and the run's mode; from step 8 the validators of the ON rules with validator enforcement (`validators`, §8), which the verification loop reads from the execution's snapshot (TDD §8). The setup home came with step 7.
- Problems are blocking, warning or info. Any blocking problem marks the snapshot (§8) as blocking.

As built in step 7 (`service/ladopolicy/setup.go`, `service/ladosetup/effective.go`), setup items join the resolution:

- They resolve like ordinary rules, Task over Project over Global, with one difference: an item without a Global row is ON. They have no triggers, and no protected floor.
- A task gets the items of its agent's tool: a Claude Code task the Claude Code items, a Codex task the Codex ones. The effective preview lists that tool's live items, the ones that are off included, with the binding behind each state.
- The conflict checks of §10 run over the ON items in the scope. Their results join the problems, so a blocking one blocks; an accepted warning leaves the problems and is recorded (§8, §10).
- Resolution reads only the stored versions of managed items. Settings left at the original locations, repo-tracked files and drift do not enter it (§9.5, §9.6).

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

As built in step 8 (`service/ladoverify/commits.go`, TDD §8.3), for the built-in rule only:

- **Where it lives.** The rule's built-in version 2 is version 1's name and instruction with enforcement `[prompt, validator]` and the config `{"language":"ko","allowPrefixes":["feat","fix","docs","refactor","test","chore","style","perf","build","ci","revert"]}`. The daemon appends it to a stored rule only while that rule's active version is byte-identical to version 1 (§14), so a rule the developer edited keeps its versions, and its validator runs only while its enforcement holds `validator`. The snapshot compiles it as `commit_message_locale` (§8); the loop reads it from there.
- **What it judges.** The commits in merge-base..head without merges, and the PR title, which becomes the squash commit's subject. Once one allowed prefix and the code-like words are stripped, a subject with a Hangul syllable left is `ok`. Three or more Latin words, two of them of three letters or more, with no Hangul, is `not_korean`, which fails `commit_messages_failed`; anything else, such as `chore: bump deps`, `Update README.md` or `fix: CI`, is `unclear`, as is a body of five or more Latin words without Hangul, and goes to the developer as the confirm `commit_messages_unclear`. The schema's `allow_issue_ids` is not a setting: issue refs are always code-like words.
- **Failure action.** `block_and_request_fix`: the integration waits on the failure, and the worker gets one fix request per head naming the failing subjects (TDD §8.4). The other fields of the example (`owner`, `triggers`, `default_enabled`) are still not stored.

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

`owner`, `triggers`, `default_enabled`, `validator` and `failure_action` are not stored yet: the Global binding takes the place of `default_enabled`, triggers wait for the first rule that needs one (§4), and validators come with step 8. As built in step 8, a validator is still no field of its own: `validator` enforcement plus the version's `config` make one, only built-in rules have it, and the code decides its kind (§14).

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

As built in step 7, setup items add no capability class: a kind that cannot reach both the terminal and the Chat path of its tool is never adoptable (§9.1), and a task gets only the items of its own agent's tool (§4).

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

As built in step 7:

- The format is `lado-policy/2`. After `aoRules` come `setup`, `aoSystem` and `acceptedConflicts`. `setup` lists the ON items of the task's agent tool, sorted by tool, kind and name: item id, tool, kind, name, version id and number, the version's sha256, and the binding it came from; an ON `enabledPlugins` item also carries `installedSha256`, the hash of the user-scope install records (path, version, commit) of the plugins it turns on, or of the fact that they could not be read. `aoSystem` is `{"sha256": ...}` (§2). `acceptedConflicts` lists the accepted warnings as code, item id, detail and acceptance id. `service/ladopolicy` `TestSnapshotFormatIsLadoPolicy2` pins the bytes.
- The compiler name is part of the hash, so every approval made before step 7 shows Review required once, as an AO update does. New approvals pin the setup item versions they used (`lado_policy_snapshot_setup`); deleting or restoring an item never removes its versions.
- A setup change, a kept drift, an acceptance or its revocation after approval gives the approved revision another hash: the task shows Review required, and the dispatcher sends its entry there with `rules_changed`, which the app words "Rules or local setup changed since approval". The task's Rules tab lists the setup items added, removed and changed since the approval, and says when only the installed plugins changed.
- A running execution keeps the setup it was approved with: its Claude Code config home and its Codex options are built from the pinned versions, found through the session's client request id (§9.4).
- Not in the snapshot: repo-tracked files (§9.5), the settings left at the original locations, drift (§9.6), and the contents of the plugin folders, of which only the install records count.

As built in step 8:

- The format is `lado-policy/3`. Right after `workflowGates` comes `validators` (never null): one entry per ON rule with validator enforcement, in the order of `rules`, as `{ruleId, kind, config}`. The kinds are `checks_must_pass` (`test.command.must_pass`, config `{}`), `protected_paths` (`paths.protected`, `{globs}`) and `commit_message_locale` (the Korean rule, `{language, allowPrefixes}`; a version whose config is `{}` gets the defaults). `service/ladopolicy` `TestSnapshotFormatIsLadoPolicy3` pins the bytes.
- The compiler name and the four new protected gates (§14) change the hash, so every approval made before step 8 shows Review required once, as step 7's did; so does an approved task with the Korean rule on, once the daemon upgrades that rule to its version 2. On a database from before step 8 the gates are created at the upgrade, after the rules that existed, so their order in `rules`, `validators` and the prompt differs from a new database's; hashes are only compared within one database.
- A running execution keeps its snapshot's validators: the verification loop reads them from the execution's snapshot, not from the current rules. An execution whose snapshot is `lado-policy/2` gets every verification item except `commit_messages` and the protected half of `paths` (TDD §8).
- Still not built: cancelling affected runs on an emergency change to a protected gate.

## 9. Local setup adoption (D11)

Decided by the owner on 2026-10-19 and recorded in [adr/0005](adr/0005-local-harness-adoption.md). Settings spread over many places are hard to manage, so LADO becomes the single place: an adopted item is backed up, copied into LADO's managed home and removed from its original location. From then on only sessions launched by this app use it.

Consequence: Claude Code and Codex used directly, outside this app (terminal, IDE extensions), no longer load adopted items. Restore (§9.7) undoes an adoption.

Built in step 7 (TDD §15, item 7) with three departures from this section, decided or approved by the owner on 2026-10-26: `~/.claude/plugins` stays where it is and only the settings keys that turn plugins on are adopted (§9.1); untracked project files are not adopted (§9.1); repo-tracked files are not hashed into the snapshot (§9.5). Adoption runs on Linux only, the one OS where delivery was measured; Windows and macOS refuse it until step 9 measures them, and restore runs everywhere. Each subsection below ends with what was built.

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

As built in step 7 (`service/ladosetup`, `scan_claude.go`, `scan_codex.go`, `scan_projects.go`):

- **Plugins stay where they are** (owner's decision). `~/.claude/plugins/` holds 510 MB on the owner's machine and Claude Code writes it as it runs, so it is listed as never adopted (`plugins_stay`) and every config home links it. Only the settings keys `enabledPlugins` and `extraKnownMarketplaces` are adopted, and the snapshot hashes what the enabled plugins have installed (§8). This replaces "as one unit" in the Claude Code table.
- **Codex keeps its folders and most of `config.toml`.** Codex keeps its login under `CODEX_HOME`, so LADO reaches Codex sessions through launch options (§9.4), which carry instructions and MCP servers to both the terminal and Chat, and nothing else. `skills/` and `plugins/` stay (`no_tui_path`), `prompts/` too (`deprecated`), and `rules/` and the keys Codex writes itself are tool state. This replaces the Codex table's "Config", "Prompts, skills, plugins" rows.
- **Untracked project files are not adopted.** `CLAUDE.local.md` and `.claude/settings.local.json` in a registered project's main checkout are listed read-only with the reason "AO worktrees do not load this file" (`worktrees_do_not_load`). AO's worktrees never contain them, so adopting them would change direct use without serving any AO session, and writing them into worktrees risks committing personal files. This replaces the Claude Code table's "Untracked project files" row and §9.3's `projects/<project-id>/`.

Each inventory entry has its tool, kind, name, location (`claude_dir`, `claude_json`, `codex_home`, `project`, or `managed` for a live item), path, size and sha256 (for a folder item, the sha256 of its file manifest), and a status with a stable reason: `managed`, `adoptable`, `not_adoptable`, `never`, `not_managed` or `repo_tracked`.

| Tool | Adoptable, one item each | Listed only (reason) |
|---|---|---|
| Claude Code | Each top-level `*.md` (`instructions`); each key of `settings.json` (`setting`), where an `env` item leaves its login variables in place; each entry of `skills/`, `output-styles/` and `hooks/`, and each file under `agents/` and `commands/` at any depth; each user MCP server in the `mcpServers` of `~/.claude.json` | `.credentials.json` (`credentials`); login keys, and an `env` with only login variables (`login_key`); `plugins/` (`plugins_stay`); `projects/`, `sessions/`, history, caches and the rest of `~/.claude.json` (`app_state`); a linked `settings.json`, `~/.claude.json` or item folder (`symlink`, since adoption rewrites the file); a file LADO cannot read or hash (`unreadable`); anything else (`unknown_location`) |
| Codex | `AGENTS.md` and `AGENTS.override.md` (`instructions`); each `[mcp_servers.<name>]` table whose name uses only `[A-Za-z0-9_-]` and which holds no literal secret (`mcp_server`) | `auth.json`, `secrets/` (`credentials`); `skills/`, `plugins/` (`no_tui_path`); `prompts/` (`deprecated`); `rules/` and the keys `projects`, `notice`, `hooks` and `skills` (`tool_state`); `model` and `model_reasoning_effort` (`ao_sets_per_session`); a server written as an inline table (`inline_table`), with a name a `-c` option would have to quote (`name_unsupported`), or with `env` values, `http_headers` or `bearer_token` (`literal_credentials`), which would land on a command line; sessions, logs and databases (`app_state`); anything else (`unknown_location`) |
| Projects | None | Untracked `CLAUDE.local.md` and `.claude/settings.local.json` (`worktrees_do_not_load`); tracked `CLAUDE.md` and `AGENTS.md` at any depth, `.claude/settings.json`, `.claude/{skills,agents,commands}/`, `.mcp.json` and `.codex/` (`repo_tracked`, from `git ls-files` in the main checkout, 10 s per project) |

- An adoption moves an entry by default. Settings keys outside `hooks`, `permissions`, `env`, `model`, `modelSettings`, `statusLine`, `outputStyle`, `enabledPlugins`, `extraKnownMarketplaces` and `includeCoAuthoredBy` default to keep: LADO manages a copy and the original stays.
- Discovery reads the files only. The app-server's `config/read` and Codex's other lists are not used; the probe used `config/read` once to measure the layers (§9.4).
- Organization-managed settings are not read, because the daemon gives the service no OS policy path; the reason `managed_settings` waits for it. Scratch projects have no repository and are not read; of a workspace project only the root repository is.
- What runs the tools is read too: live AO sessions of each tool, and the computer's processes (§9.2).
- The scan is cached and runs again when the daemon starts, on demand (`POST /lado/setup/scan`) and after every LADO write. A file is opened without blocking and read only when it is a regular file, so a pipe never stalls the scan, and an item file that became a link after the listing is not hashed through it.

After the step 7 review (2026-10-27): login variables are matched by one rule, case-insensitively (the `ANTHROPIC_`, `CLAUDE_CODE_USE_`, `AWS_`, `VERTEX_REGION_` and `AZURE_` prefixes, `CLAUDE_CODE_OAUTH_TOKEN`, `CLOUD_ML_REGION`, `GOOGLE_APPLICATION_CREDENTIALS`, `GOOGLE_CLOUD_PROJECT` and the proxy variables). When the `env` turns a cloud provider login on, the whole `env` stays in place as `not_adoptable` with the reason `provider_login_env`, since the session's login depends on it. A tool folder or a settings file that is itself a symbolic link makes its entries `not_adoptable` with the reason `root_is_link` (adopting one is 409 `LADO_SETUP_ROOT_IS_LINK`); restore still writes through it. `literal_credentials` also covers a Codex server whose arguments carry a header, a token or a key, or whose URL carries a user or a query.

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

As built in step 7 (`adopt.go`, `backup.go`, `managed.go`, `rewrite.go`; one adoption at a time):

1. **Preview** (`POST /lado/setup/adoptions` with `dryRun`). The service scans again and refuses an entry that is not adoptable (400 `LADO_SETUP_NOT_ADOPTABLE`, with `already_managed` when LADO manages that name already). Per entry the answer gives the action (move or keep), the managed path and the delivery (`home` for Claude Code, `overlay` for Codex). It lists every hook and status line command it would rewrite: a command that names a moved path as `~/.claude/…`, `$HOME/.claude/…`, `${HOME}/.claude/…` or the absolute folder gets `"$CLAUDE_CONFIG_DIR"/…`, quoted to fit the command. It adds the conflicts of the moved entries (§10), the warning that Claude Code and Codex used outside the app stop loading them (`LADO_SETUP_DIRECT_USE_STOPS`), the preconditions, and `planSha256`: the sha256 of canonical JSON `{"format":"lado-setup-adoption/1","os":...,"entries":[{tool, kind, name, originPath, sha256, action}]}`.
2. **Approve and check again.** The desktop app signs `planSha256` (`setup.adopt`). The service scans and computes again; another hash is 409 `LADO_SETUP_CHANGED`, with nothing written. Then 409 `LADO_SETUP_HOOK_PATH_MOVED` for a command that names a moved path the rewrite cannot reach, 409 `LADO_SETUP_OS_UNVERIFIED` on an OS whose delivery was not measured, and 409 `LADO_SETUP_TOOL_RUNNING` with the live AO sessions of the tools in the adoption (on any login, accounts included) and the running Claude Code and Codex programs outside AO. The other blocking conflicts do not stop an adoption: left in place, the item conflicts the same way, and the preview and the confirmation show it.
3. **Back up** into `<state>/lado/backups/<adoption-id>/` (TDD §4.5): each original with its mode and mtime, a key file whole, and `manifest.json` with the keys taken out of each key file. Flush, read everything back and hash it; a mismatch stops before anything is removed.
4. **Copy** into blobs and the managed folder (§9.3) and check the hashes. A key item goes into its managed key file alone; the file's other keys are not rewritten.
5. **Remove** the originals. Files and the entries of the item folders are deleted; the folders themselves (`skills/`, `agents/`, `commands/`, `output-styles/`, `hooks/`) stay, emptied, so AO's account linker never recreates anything in the user's folder. A folder is renamed beside itself first and then deleted. Each original is hashed once more right before, and only bytes the backup holds are removed. `settings.json` and `~/.claude.json` are rewritten without the adopted keys or servers, the order and the rest kept, in two-space JSON through a temp file and a rename with the file's mode; an adopted `env` leaves its login variables, and the key goes once none is left. `config.toml` loses each adopted `[mcp_servers.<name>]` block line by line, comments and other tables untouched, and the result must parse.
6. **Record** the adoption, its items and their first versions, the `setup.adopted` audit event and the proof in one transaction, by `human` (`human:dev-presence` under dev presence). Then save the drift baseline (`setup-baseline.json`, §9.6).
7. **Verify**: build a temporary home for the computer's own login and find every adopted Claude Code item in it by hash, and parse every Codex `-c` value. The answer carries the checks.

Failures. Before the removal, nothing is removed or recorded, and the backup folder stays for diagnosis. When the record fails after the removal (a store error, or a proof replayed meanwhile), the originals come back from the backup and the managed folder loses what was never recorded. When the removal itself fails, it stops there: with something removed, the adoption is recorded `partial` with the error (500 `LADO_SETUP_ADOPTION_FAILED`, `state: partial`) and restore puts back what it moved; with nothing removed yet, nothing is recorded. The removal and the record run to the end even when the request is cancelled. On Windows a locked file is a removal failure.

After the step 7 review (2026-10-27):

- **The proof first.** Before the lock and the scan, the service checks the entries, `planSha256`, the proof's signature and expiry, and whether the proof was used (403 `LADO_PRESENCE_REPLAYED`), so a slow scan no longer outlives the proof.
- **`pending` before the removal.** After the backup and the copy, the adoption row is written as `pending` together with the proof, and only then are the originals removed. The record turns it `completed` or `partial` with its items and versions; when nothing was removed the row is deleted. A row a daemon stop left `pending` becomes `partial` at the next start, with the message "Adoption was interrupted: …", and restore puts back the originals its backup moved even though it recorded no items. When the record fails and some originals cannot be put back, the error names them (`details.putBackFailed`, `state: partial`) and the adoption is kept for restore.
- **More that stops an adoption.** A setting left in place, or an MCP server of either tool, whose value names a file the adoption moves (`env`, `permissions`, a server's `command`, `args`, `env` or `url`) is the blocking conflict `LADO_SETUP_PATH_USED_IN_PLACE` (details `key`, `path`, and `item` or `origin`) and stops the adoption like an unrewritable hook path. `otelHeadersHelper` is rewritten with hooks and the status line when it moves with the file. Codex reads `AGENTS.md` and `AGENTS.override.md` as one, so both are adopted together with the same action (400 `LADO_SETUP_PAIR_REQUIRED`, details `name` and `pair`). A new item is refused on an unverified OS too (409 `LADO_SETUP_OS_UNVERIFIED`).
- **The desktop app** refuses without asking where the daemon would, `LADO_SETUP_PATH_USED_IN_PLACE` included (TDD §9).

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

As built in step 7 (`managed.go`, `home.go`):

```text
<state>/lado/harness/
  claude/
    CLAUDE.md, *.md                     # instructions
    settings.json                       # the managed settings keys only
    mcp.json                            # {"mcpServers": {...}}, the managed servers
    skills/ agents/ commands/ output-styles/ hooks/
  codex/
    AGENTS.md, AGENTS.override.md
    config.toml                         # the managed [mcp_servers.<name>] tables
  trees/<sha256>/                       # folder item versions, written out for the config homes
```

- No `plugins/` (§9.1), no Codex `prompts/`, `skills/` or `plugins/`, and no `projects/<project-id>/` (§9.1).
- The managed folder is where the developer sees and edits the managed items: a text item's edit in the app rewrites its file there after the version is stored, and a folder item is edited there by hand, which drift then shows (§9.5, §9.6). Sessions never read it: config homes and Codex options are built from the stored versions (blobs, and the trees written out from them), so an edit here reaches no session until it is kept as a version.
- A key item lives in its key file next to the other managed keys; LADO adds and removes that key only, so a hand edit of another key stays until drift deals with it.
- Folders 0700 and files 0600 (0700 when executable), since a managed MCP server can hold a secret.

### 9.4 Delivery to sessions

The managed home takes the place of the original location for every session this app launches.

- **Account sessions.** AO's account homes (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`; `adapters/agentaccount/claude.go:58-67`, `adapters/agentaccount/codex.go:41-66`) link shared entries from `~/.claude` and `~/.codex` (`adapters/agentaccount/claude.go:26-30,268-274`, `adapters/agentaccount/codex.go:15-35,198-230`). After adoption they link from the managed home instead. AO's linker also creates missing entries in the user's real folder (`adapters/agentaccount/share.go:35-38,62-76`); that must stop, or the next launch would undo the adoption.
- **Sessions on the computer's own login.** AO sets no config folder for them today (`service/agentaccounts/service.go:898-900`). LADO gives them a home too: a per-execution folder that links credentials and app state from the real folder and setup items from the snapshot. To verify per tool and OS before shipping: Claude's credential lookup under `CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR` (AO sets both for accounts), Codex `auth.json`, and file and directory links on Windows.
- **Fallback: launch options.** Claude `--settings`, `--mcp-config`, `--plugin-dir` and `--append-system-prompt-file` (AO uses only the last today; AO's cloud reviewer already passes `--setting-sources`, `cloud/internal/workerexec/command.go:121-126`). Codex `-c` overrides and the app-server thread `config` (`adapters/chatdriver/codexappserver/driver.go:432-472`).
- An item kind is adoptable only if one of these mechanisms reaches **both** the TUI and the Chat launch path of its tool. Otherwise it stays where it is and the inventory says why.
- Each execution's home is built from the snapshot's blobs, so it cannot change during the run (§8).
- AO's system layer is added on top exactly as today.

As built in step 7, the owner's design A (TDD §9 has the launch paths and the hooks):

- **Nothing changes before the first adoption.** A session launches as before until a managed item of its tool is live.
- **Claude Code: a config home for every session.** Each Claude Code session AO launches gets `<state>/lado/homes/<session-id>/claude/`, built on the session's own folder (the account home, or the computer's Claude folder for its own login): links to the folder's state entries and `plugins`, the folder's item entries and top-level `*.md` merged with the managed items, `settings.json` as the folder's file plus the managed keys, and a copy of `.claude.json` whose `mcpServers` LADO keeps as the folder's servers plus the managed ones. `CLAUDE_CONFIG_DIR` points at the home. Account homes stay as AO builds them, and AO's linker is not changed: adoption empties the item folders and keeps them (§9.2), so the linker has nothing to create.
- **The login stays where it lives.** Measured while designing step 7 (claude 2.1.283): a link cannot share Claude's credentials file. Claude writes `.credentials.json` through a staged temp file and a rename, refuses to write through a symlink, and refuses to write in place a file with more than one hard link. But Claude takes its login folder from its own variable, `CLAUDE_SECURESTORAGE_CONFIG_DIR` (an empty value means `~/.claude`), and on macOS the Keychain item has no suffix when that value is empty. So the home keeps the login where it lives: `CLAUDE_SECURESTORAGE_CONFIG_DIR` is the account home for an account session (AO already sets it so), and for the computer's own login the daemon's `CLAUDE_CONFIG_DIR` when that was set, else `""` on macOS and the real Claude folder on Linux and Windows. The variable is undocumented, so step 7 measured it on Linux with the real binaries: the login holds with it and is gone without it, the home's items load in print mode and through the ACP adapter (claude-agent-acp 0.70.0), transcripts land in the base's `projects/` through the link, and no credentials file appears in the home (TDD §15, item 7). A home needs no `.claude.json` for the login; the copy is for the base's servers, onboarding state and workspace trust.
- **Codex: launch options on the real `CODEX_HOME`.** Codex keeps its login under `CODEX_HOME`: `auth.json` by default, a keyring entry named by a hash of the canonical `CODEX_HOME`, and on Windows by default an encrypted `secrets/codex_auth.age` whose passphrase sits in Credential Manager under the same hash. No variable moves the login apart from `CODEX_HOME`, so a home of its own per session would lose the computer's login. Codex sessions therefore keep the real `CODEX_HOME`, and LADO adds the managed items on top:

  | Item | Terminal | Chat (app-server) |
  |---|---|---|
  | MCP server | `-c mcp_servers.<name>=<inline table>` on the launch and restore commands; measured to land in the `sessionFlags` layer (codex 0.163.0) | The session's MCP list after AO's `ao` server, whose thread `config` gets the whole table |
  | Instructions | A `## User instructions (managed by LADO)` section of AO's system prompt, before LADO's rules: the first of `AGENTS.override.md` and `AGENTS.md` that has text, as Codex itself picks | The same section |

  Adopting one of the two instruction files while the other stays in `CODEX_HOME` makes Codex read the one that stayed and LADO add the adopted one, so both apply, unlike before; the adoption dialog warns about it. Codex reads its own `AGENTS.md` as a user message, so the section weighs a little differently.
- **Which versions.** An execution gets the versions its snapshot pinned, through its client request id; every other session the current versions of the items on for its project. Both are built from stored versions only (§8, §9.6).
- **Not delivered.** AO's reviewer launcher builds its own environment and gets no adopted items; neither do the background tasks that write titles and suggestions. As built in step 8, the reviewer gets the adopted instructions of its own tool in its system prompt, at the versions the worker got when the tools match, and nothing that runs: no settings, hooks, skills, agents, commands or MCP servers (TDD §9). Since the step 7 review a home links every other entry of its base as well (below), so `keybindings.json`, `rules/` and the like reach every session.
- **Fallback: launch options for Claude Code** were not needed.

After the step 7 review (2026-10-27; TDD §9 has the files):

- **The rest of the base is linked.** A home links every entry of its base except `.credentials.json`, `settings.json` (written), `backups/`, `.claude.json` and `.claude.json.*`. An item folder is merged only while the session gets a managed item of that kind; otherwise it is one link to the base's folder.
- **What the session writes is kept.** Cleanup and reconcile no longer delete it: an unknown file or folder in the home goes to the same place in the base, or beside what is there as `<name>.lado-restored`; an MCP server the session added to the home's `.claude.json` goes into the base's file when the name is free; a managed copy the session changed goes back to the managed folder when that copy still holds the latest version (drift then shows it as `edited`), else beside it as `<path>.lado-session-<session-id>`. A home that cannot be cleaned stays for the next cleanup.
- **A new base gets a new copy.** When a session's base changes, the home copies `.claude.json` again from the new base and carries the servers the session added into the old base first.
- **Pins.** An execution whose snapshot pinned no setup items gets only the items created after that snapshot, not every current item, so items its task had turned off stay off.

### 9.5 Create, edit, delete, toggle (HAR-012)

- Every change to a managed item shows a diff and needs approval before it is written. The previous content stays as a blob, and an audit event records the change.
- Changes that can run code or widen access (hooks, MCP servers, permissions, environment) need human-presence approval. Other changes are approved in the UI.
- A toggle is a binding: OFF at a scope keeps the item in the managed home but leaves it out of homes built for that scope.
- Repo-tracked files stay read-only in LADO. "Edit" creates a task with a prefilled plan, or the developer edits the repo directly. The next scan sees the new hash, and queued executions of that project become Review required.

As built in step 7 (`changes.go`; owner's decision 3):

- **What changes where.** Text items (instructions, a settings key, an agent, a command, an output style, a hook script, an MCP server, and a skill whose only file is `SKILL.md`) are edited in the app. Folder items (a skill with more files, a hook folder) are turned on or off and deleted in the app, and edited in the managed folder (§9.3), where drift shows the change for keep or revert; editing one in the app is 409 `LADO_SETUP_FOLDER_ITEM`. New items: instructions, a settings key, a one-file skill (its `SKILL.md` needs `name` and `description` front matter), an agent, a command, an output style and an MCP server; for Codex only instructions (`AGENTS.md`, `AGENTS.override.md`) and MCP servers. A hook script cannot be created in the app; it comes in by adoption only. Login keys and login variables are refused everywhere (`LADO_SETUP_LOGIN_KEY`).
- **Every change runs as a dry run first.** The answer shows the old and new content, the conflicts the change adds and removes, its impact (the LADO projects whose setup changes and the approved tasks that would need a new approval), whether it needs the desktop app, and `changeSha256`: the sha256 of the request in canonical JSON without `dryRun`, which the proof signs. An edit names the latest version as `baseVersion` (409 `LADO_SETUP_ITEM_CHANGED` otherwise). Each version is a blob; the managed file is rewritten after the transaction. Every change writes its audit event with it: `setup.item_created`, `setup.item_updated`, `setup.item_deleted`, `setup.binding_set`, `setup.drift_kept`, `setup.drift_reverted`, `setup.conflict_accepted`, `setup.conflict_revoked`.
- **Presence** (`setup.change.requires_presence`, §14; owner's decision of 2026-10-27, which replaced the step 7 list). One check, `domain.LadoSetupNeedsPresence(kind, name, content)`, decides which items need the desktop app: every hook script and MCP server; every settings key except an allow list of safe keys (`theme`, `verbose`, `outputStyle`, `model`, `modelSettings`, `includeCoAuthoredBy`, `showClearContextOnPlanAccept`, `inputNeededNotifEnabled`, `agentPushNotifEnabled`, `preferredNotifChannel`, `spinnerTipsEnabled`, `cleanupPeriodDays`, `language`); and a command, skill (its `SKILL.md`) or agent whose front matter has `allowed-tools` or `hooks` (any case, `-` or `_`) or whose text runs a command (`` !` ``). Content that cannot be read or front matter that does not parse needs it too. For such an item every change is confirmed: creating it, editing it (when the old or the new content needs it), deleting it, every binding state at every scope (on, off, `inherit`, `force_on`, `force_off`; a binding that changes nothing is not a change), and keeping its drift. Without a proof such a change is 403 `LADO_PRESENCE_REQUIRED` and writes nothing; the proof is checked before the change's slower checks. Changes to other items, drift revert, and accepting or revoking a warning need none and are recorded by `api`.
- **A toggle is a binding**, with the rules' scopes: Global `on`, `off` or `inherit` (no row, which is on), Project and Task `inherit`, `force_on` or `force_off`. A cancelled task takes only `inherit`.
- **Repo-tracked files** are listed read-only per project, on the project's Rules page under "From the repository". Their Edit opens the New Task composer for that project with a first line `Update <path>` in the app's language, so the change goes through a task and its plan. They are **not hashed into the snapshot** (a departure from the last sentence above): they reach the worker through the worktree, which starts from the base branch, and step 6 records how far that branch moved instead of refusing it. The scan reads the main checkout, whose files can differ from the branch a worktree gets, so hashing them would ask for review on edits no worker sees and miss the ones it does.
- **Decided by the owner (2026-10-27).** The three questions step 7 left open (no confirmation for deleting or turning off, front matter created without one, a list of keys that need it) are settled by the check above: deleting and turning off are confirmed like any change, front matter counts, and the list is an allow list of keys that need none. The heuristic reads only those front matter keys and `` !` ``; a folder skill's other files do not count (an agent's `tools`, which narrows, does not either).

### 9.6 Drift (HAR-014)

LADO rescans at app start, on demand, and before admission.

- New items at original locations after adoption (for example, Claude Code used directly wrote a new `settings.json`, or a plugin was installed outside LADO) are shown as new local items. Adoption is offered, never automatic. Claude Code writes `~/.claude.json` during normal use, so this kind of drift is expected.
- A managed file whose hash no longer matches its record was edited outside LADO. The user chooses to keep it (as a new version) or revert it.

As built in step 7 (`drift.go`):

- **When.** Drift is computed with every scan: when the daemon starts, on demand (Rescan, `POST /lado/setup/scan`), and after each LADO write. There is no scan before admission or dispatch, because drift cannot change what a session gets: config homes and Codex options come from stored versions only (§9.4), and resolution reads those versions (§4). A change reaches sessions and approved tasks only when it becomes a version, by keep or by an edit, and that changes the snapshot hash, which the dispatcher already compares before it spawns (§8).
- **`edited`**: a managed file or folder no longer hashes to its item's latest version (a folder by its file manifest, execute bits included). Keep makes the managed content the next version (`setup.drift_kept`; the desktop app confirms for the kinds of §9.5); the request names the content's sha256, and another one is 409 `LADO_SETUP_CHANGED`. Revert writes the latest version over the managed copy (`setup.drift_reverted`). A copy LADO cannot read, or one whose shape changed (a folder where a file was), can only be reverted (409 `LADO_SETUP_DRIFT_UNREADABLE`), and a Codex server that gained a literal secret cannot be kept (`literal_credentials`).
- **`new_local`**: an original location holds an adoptable entry that `setup-baseline.json` does not have, or has with another hash, such as a `settings.json` key Claude Code wrote when used directly, or a new skill. It is offered for adoption and never moved by itself. When LADO already manages that name, the entry says so and cannot be adopted (`already_managed`) until the managed item is deleted.
- The baseline is rewritten at the end of each adoption with what remains at the original locations, and a restore adds what it put back, so a restored entry is not new.
- Settings → Local setup shows drift in a "Changes found" card (§12).

### 9.7 Restore (HAR-015)

- Restoring an adoption writes every manifest entry back to its original path, when that path is free or unchanged since adoption.
- If something new is there, both are kept: the restored copy gets a `.lado-restored` suffix, and the report lists it.
- Keys go back into `settings.json` and `~/.claude.json` only where they are absent.
- Managed copies stay until the user deletes them. An audit event records the restore.
- Restored files must be byte-identical to the backup (PRD gate 14).

As built in step 7 (`restore.go`):

- Restore works per adoption, after the desktop app's confirmation (`setup.restore`, the adoption id). It waits for what runs the tools, as adoption does (409 `LADO_SETUP_TOOL_RUNNING`), but not for the OS: it runs on Windows and macOS too, so the way back stays open where adoption is refused. A second restore is 409 `LADO_SETUP_ALREADY_RESTORED`.
- It reads the backup's `manifest.json` only when its sha256 is the one the adoption recorded, and it restores only the items the adoption recorded, so a partial adoption gets back what it removed and nothing else.
- A file or folder goes back with its bytes, mode and mtime when its path is free, through a temp name and a rename, and is checked against the manifest. When something else sits there, that stays and the original goes beside it as `<name>.lado-restored` (then `-2` up to `-99`); when the original is there already, nothing is written.
- Keys go back into `settings.json`, `~/.claude.json` and `config.toml` only where absent, next to the key they followed; a present key with another value is reported as kept. An `env` gets back only the variables the adoption took that are absent. A key file nobody changed since the adoption comes back whole, byte for byte; a key file that is gone gets back the adopted keys alone, never the login keys and app state it once held.
- The report lists each item as `restored`, `restored_beside`, `key_kept` or `failed`, with paths relative to their root (`lado-setup-restore/1`). When an item fails, nothing is recorded and the first failure is 500 `LADO_SETUP_RESTORE_FAILED`; what was put back stays, and restoring again finishes the rest without writing anything twice.
- A recorded restore marks the adoption and its items restored (`setup.restored`): nothing resolves or delivers them any more, their versions stay readable for the snapshots that pinned them, and the inventory lists the originals as adoptable again. The managed copies, the blobs and the backup stay.

After the step 7 review (2026-10-27): an item deleted in LADO stays out of a restore, with the status `skipped_deleted` (its key is left out of a key file too, so the file is not put back whole); an item LADO made later versions of is restored with its original bytes and the note `kept_in_lado`, since those versions stay readable in LADO; the audit payload counts `skippedDeleted`. The adoption's view (`GET /lado/setup/adoptions/{adoptionId}`) carries the restore's `preconditions` (the live AO sessions and the running programs of its tools) until it is restored or its backup cannot be read, and the adoptions card shows them beside Restore with Check again; the desktop app refuses to ask while they run. A `pending` adoption has no Restore until it ends.

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

As built in step 7 (`conflicts.go`), the checks are deterministic and read only the items' content:

| Code | When | Severity |
|---|---|---|
| `LADO_SETUP_MCP_NAME_TAKEN` | An MCP server named `ao`, in any case, for either tool | Blocking |
| `LADO_SETUP_SKILL_NAME_TAKEN` | A Claude Code skill or command named `using-ao` | Blocking |
| `LADO_SETUP_ENV_RESERVED` | An `env` setting that sets `AO_*`, `CLAUDE_CONFIG_DIR`, `CLAUDE_SECURESTORAGE_CONFIG_DIR` or `CODEX_HOME` | Blocking |
| `LADO_SETUP_DENIES_AO` | A `permissions.deny` rule that covers an `ao` command (`Bash(ao…)`) or AO's tool server (`mcp__ao…`) | Blocking |
| `LADO_SETUP_HOOK_PATH_MOVED` | A hook or status line command that still names a path adoption moved out of the Claude folder (§9.2) | Blocking |
| `LADO_SETUP_PATH_USED_IN_PLACE` | Any other settings value, or an MCP server's `command`, `args`, `env` or `url`, that names such a path (added after the step 7 review, §9.2) | Blocking |
| `LADO_SETUP_DENIES_GIT` | A `permissions.deny` rule that covers `git` or `gh` | Warning |
| `LADO_SETUP_ALLOWS_PROTECTED` | A `permissions.allow` rule that covers `git push`, `git update-ref`, `git merge`, `git reset` or `gh pr merge` (added in step 8, with the gates) | Warning: LADO enforces the gate elsewhere |
| `LADO_SETUP_HOOK_MAY_BLOCK_TOOLS` | A `PreToolUse` hook whose matcher covers `Bash` or AO's tool server: empty, `*`, a name list with them, or a pattern that matches them | Warning |
| `LADO_SETUP_HOOK_ADDS_PROMPT_TEXT` | A `UserPromptSubmit` or `SessionStart` command hook | Warning |
| `LADO_SETUP_DEFAULT_MODE_ABOVE_CEILING` | `permissions.defaultMode` above the permission ceiling | Warning |
| `LADO_SETUP_HOOK_SHARES_AO_EVENT` | A hook on an event AO installs its own Claude Code hook on | Info: Claude runs both |
| `LADO_SETUP_PROJECT_OVERRIDES` | The project's repository `.claude/settings.json` sets a top-level key a managed setting sets; the project's value wins | Info |
| `LADO_SETUP_PLUGIN_HOOKS_UNREADABLE` | An enabled plugin whose hooks LADO could not read | Info |

- **Blocking only for what is certain.** A blocking result stands for content that breaks what AO adds to every session, read from the item itself: a name AO uses, a variable AO sets, a deny rule on AO's own commands, a hook path that adoption left dangling. A hook is a script, though, and what it denies is only known when it runs, so the first table's "Hooks that block tools AO needs" became the warning `LADO_SETUP_HOOK_MAY_BLOCK_TOOLS`: its matcher reaches AO's tools, and the developer, who knows the script, can accept it. A deny that covers some `git` or `gh` commands is certain, but it is often deliberate (`Bash(git push --force:*)` counts too, since rules are read by prefix) and leaves AO itself working, so it is a warning as well. A warning blocks nothing; it stays in the problems until accepted.
- **Acceptance.** Only a warning can be accepted (otherwise 400 `LADO_SETUP_CONFLICT_NOT_ACCEPTABLE`), and only one the item's latest version produces. An acceptance holds for that code, item, detail and content sha256, in its scope and the narrower ones (Global covers every project and task, a project its tasks), until it is revoked or the item changes. The snapshot records it in `acceptedConflicts` (§8). Since the step 7 review the conflicts card revokes one ("Revoke acceptance"), without a confirmation.
- **Plugins.** The hooks of the plugins `enabledPlugins` turns on, read from the user-scope installs in `installed_plugins.json` (`hooks/hooks.json`), go through the hook checks too; project and local installs are not read.
- **Where they run.** On the conflicts list of a scope (Settings → Local setup shows Global, with the permission ceiling from the rules); in the effective preview and the snapshot, over the task's agent tool (§4); in the adoption preview, for the moved entries plus every unrewritten hook path and every value left in place that names a moved path, which alone stop an adoption (§9.2); and in every change's dry run, as the conflicts it adds and removes.
- **Not built.** The AI comparison of instructions against LADO prompt rules comes later. "Kind unsupported by the chosen agent" is not a conflict: a task only gets its own tool's items (§4). `permissions.ask` is not checked, and `allow` only for the protected git actions below.

As built in step 8 (`conflicts.go`), allow rules against protected gates: `LADO_SETUP_ALLOWS_PROTECTED` reads each `permissions.allow` rule the way the deny checks read theirs (`Bash(...)` prefix rules through `bashRuleCovers`, so `Bash(git:*)` covers `git push`), against `git push`, `git update-ref`, `git merge`, `git reset` and `gh pr merge`. It runs whatever the rules say, since the gates it names are protected and always on. One warning per covering rule, with the rule's text as its detail (the acceptance's key) and `rule` and `commands` (the covered commands joined with ", ") in its details. It is a warning the developer can accept, because the gates hold elsewhere: the merge gate and the `lado/integration` status (TDD §7.1, §7.2) and the base branch check (TDD §7.3) do not depend on the agent's permissions.

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

As built in step 7 (DESIGN.md decisions log, 2026-10-26). Local setup is its own Settings section rather than a part of Rules, and the conflicts are a card in it:

- **Settings → Local setup** (`frontend/src/renderer/components/settings/LocalSetupSection.tsx`), between Rules and Runs. A header line with one sentence, the time of the last scan, Rescan, Adopt… (when something can be adopted) and New item. On an OS whose delivery was not measured, one line in the caution color says why adoption is refused, and Adopt is disabled; on a verified OS there is no such line. Then, in order:
  - **Conflicts**: blocking, then warnings, then info, each with its target; Edit, Turn off (a Global switch to off) and Accept (warnings only, at Global).
  - **Changes found**, when there is drift: an edited managed copy opens a review with its diff against the latest version, and offers keeping it as version n+1 (the desktop app confirms for the kinds of §9.5) or reverting to version n; a new item at an original location offers adopting just that item.
  - **One card per tool**, its entries grouped by kind, each with its state, where it is, and why it cannot be adopted. A managed item has a Global switch and a menu with Edit, History, Review change (when edited outside LADO) and Delete. What is never adopted, not managed, or tracked by a repository sits in a collapsed group under the card.
  - **Adoptions**, newest first, each with its item count and state (completed, partially adopted, restored) and Restore, which shows the restore report when done.
- **Adopt dialog** (`components/lado/setup/LadoSetupAdoptDialog.tsx`): every candidate checked with its default action, Move or Keep per row, and a preview that follows the selection: where each item goes, the commands it rewrites, the warning that Claude Code and Codex used outside the app stop loading what moves, the conflicts, the running sessions and programs, and a note for a Codex instruction file adopted without its pair. Confirm is disabled, with the reason, when nothing moves, a hook path cannot be rewritten, the OS is not verified or a tool runs; other blocking conflicts show in the caution color and do not disable it. Then the desktop app asks. A partial adoption ends on a result that names where it stopped and points to Restore.
- **Item editor** (`LadoSetupItemEditor.tsx`): text items only, in the Markdown editor or a code editor for JSON and scripts. Review change runs the dry run and shows the diff, the conflicts it adds and removes and whether the desktop app will ask; Save confirms the impact when approved tasks would need a new approval, then asks the desktop app when needed. When another change came first (409), it offers Reload and keeps the edit. A folder item shows its files read-only, with the managed path and a note to edit there and keep the change from Changes found. **History** lists the versions with their content and actor. **New item** picks the tool, the kind, the name and the content; a hook script cannot be created.
- **Project settings → Rules**: after the rules, the project's local setup (both tools' managed items with Inherit, On or Off, the state and where it comes from), then **From the repository**: the repository's setup files with their sizes, "loaded by the tool from the worktree", and an Edit that opens New Task for the project with `Update <path>` as its first line (§9.5).
- **Task view, Rules tab**: below the approved snapshot's comparison, the local setup added, removed and changed since approval ("installed plugins changed" when only those did) and the accepted warnings; then the task's own local setup card with Inherit, On or Off, read-only for a cancelled or closed task. The review reason reads "Rules or local setup changed since approval".
- **LADO run section** in the session view: "Local setup · n items", collapsed, with each item's name and kind.
- Not yet: revoking an acceptance on screen, conflicts for a project or task scope in Settings, and a look at the screens in the real desktop app, dark theme, narrow widths and Korean (the renderer tests and one light, English check in a browser lab cover them).

As built in step 8 (DESIGN.md decisions log, 2026-10-27, two rows):

- **Session view: Verification** (`frontend/src/renderer/components/lado/LadoVerificationSection.tsx`), its own inspector section under **LADO run** once the worker opened a PR: the run's state and head, one row per verification item with its outcome's dot and one muted detail line, the check runs with their output and Run again, the evidence per criterion, and Approve integration… or Merge. TDD §8 has the details; the task view's status line and the board's session line are TDD §3.3's.
- **Project settings → LADO: Checks** (`LadoProjectChecksCard.tsx`), under the mode card: one muted line on what the commands do (LADO runs them in each run's worktree at the PR's head, outside any agent sandbox, so the desktop app asks to confirm a change), the commands as rows of a name and a command in mono under quiet column labels, each with a remove icon and the daemon's problem with that row under it; a quiet Add command, disabled at 10 with a muted line saying so; Save commands while something changed, which goes through the desktop confirmation; and a Time limit row (5, 10, 15, 30, 60, 90 or 120 minutes) that saves at once without one. Rows whose name and command are both empty are not sent, and names and commands are trimmed first. A cancelled confirmation keeps the edits.
- **Project settings → LADO: Branch protection** (`LadoProjectProtectionCard.tsx`; "Protection" in the plan, renamed so it is not taken for the protected rules or paths): one row per repository, `<repo> · <branch>` in mono, with a dot and its state: Protected (ready dot); Not protected, with the missing parts in words; Could not check, with the error; or, for GitLab, Not protected with "GitLab: LADO only detects", each of the last three with the caution dot. The heading carries the oldest check's time ("Last checked …") and a quiet Check again. A muted line on what to set in GitHub's rulesets (`lado/integration` in mono) shows only while a repository is not protected or could not be checked.
- **Settings → Rules.** The Protected rules card lists the four new gates with their names and descriptions in the app's language (`lib/lado-rules.ts`; Korean 통합 승인 필수, 기준 브랜치 직접 쓰기 금지, 테스트 통과 필수, 보호 경로). The Protected paths row shows its glob count ("9 paths") and a quiet Edit in place of "Always on", which opens **Edit protected paths** (`LadoProtectedPathsDialog.tsx`): one path or pattern per line in mono, the count beside the label, a muted line on the pattern syntax, and the daemon's rule for each line checked before sending ("Line n: …"); Save paths goes through the desktop confirmation, which names the globs added and removed and says approved tasks will need a new approval. A cancelled confirmation keeps the dialog and its text.
- In a browser, without the desktop app, the check commands and protected paths answer `LADO_PRESENCE_UNAVAILABLE` with their own sentence. Not yet: a look in the real desktop app with its native confirmations, the dark theme and narrow widths (a light, English browser lab at 1400 px saw the project and rule screens; the run screens had only renderer tests), and the required checks of a protected branch, which the card does not show.

## 13. Storage

`lado_rules`, `lado_rule_versions`, `lado_rule_bindings`, `lado_policy_snapshots`, `lado_policy_snapshot_rules`, `lado_setup_adoptions`, `lado_setup_items`, `lado_setup_item_versions`, `lado_conflict_acceptances`. They follow TDD §4: separate migration track, `lado_` prefix, no foreign keys into upstream tables, audit in `lado_audit_events`. Step 5 created the first five (migration `0005_lado_rules.sql`); the setup and conflict tables come with step 7.

As built in step 7, migration `0007_lado_setup.sql` created six tables (TDD §4.2):

| Table | Holds |
|---|---|
| `lado_setup_adoptions` | Each adoption: actor, OS, the plan's and the backup manifest's sha256, `completed` or `partial` with its error, and the restore's time and report |
| `lado_setup_items` | Each item: tool, kind, name (one live item per tool, kind and name), where adoption found it, its adoption (none for an item made in the app), and when it was deleted or restored; rows are never removed |
| `lado_setup_item_versions` | Immutable versions: content kind (`file`, `json`, `tree`), sha256 (the blob's name), size, executable bit, actor |
| `lado_setup_bindings` | Global `on`/`off` (no row is ON) and Project or Task `force_on`/`force_off` (no row is INHERIT) |
| `lado_conflict_acceptances` | Accepted warnings: code, item, detail, the item content's sha256, scope, actor, and when revoked |
| `lado_policy_snapshot_setup` | The item versions each snapshot pinned |

Their changes publish `lado_setup` events (TDD §4.3), and every write appends its audit event in the same transaction.

## 14. Protected gates

| Gate | Enforced at | Strength | Registered |
|---|---|---|---|
| `plan.approval.required` | Worker gate (TDD §6.2) | Prevents | Step 5, as a built-in protected rule that is always on. The worker gate has refused unapproved workers since step 3; from step 6 it lets through only the dispatcher's spawn for a queue entry it holds |
| `integration.approval.required` | `lado/integration` required check and AO merge hook (TDD §7.1-7.2) | Prevents (T1) | Step 8, as a built-in protected rule (category `workflow`, `[workflow_gate, prompt]`, "Integration approval required": "Never merge your pull request or ask anyone to merge it; the developer approves the integration in LADO."). The integration approval posts `lado/integration`, and AO's merge route refuses a LADO PR whose head has no approval (TDD §7.1, §7.2) |
| `git.protected_branch.no_direct_write` | GitHub ruleset for the remote; reflog check locally (TDD §7.3) | Prevents remotely, detects locally | Step 8, as a built-in protected rule (`git`, `[workflow_gate, prompt]`, "No direct writes to the base branch": "Never push to, reset or update the base branch, locally or on the remote; work only on your own branch and open a pull request."). The ruleset's parts are the protection item, the local reflog the base branch item, both confirm codes the approval acknowledges (TDD §7.1, §7.3) |
| `test.command.must_pass` | Required checks or local runner (TDD §8.1-8.2) | Blocks integration | Step 8, as a built-in protected rule (`workflow`, `[validator, prompt]`, "Tests must pass": "Before you push, run the project's tests and checks; LADO runs the project's check commands and reads the required CI checks on your pull request's head."), validator `checks_must_pass`. The required checks and the project's check commands both run for every project; a project with neither is a confirm (`no_tests`) (TDD §8.1, §8.2) |
| `paths.protected` | Path validator on `base..head` (TDD §8.3) | Detects; blocks integration | Step 8, as a built-in protected rule (`git`, `[validator, prompt]`, "Protected paths": "Do not change protected paths unless the approved plan names them.") with the config `{"globs": [...]}`, by default `.github/**`, `.gitlab-ci.yml`, `.gitlab/**`, `**/CLAUDE.md`, `**/AGENTS.md`, `.claude/**`, `.codex/**`, `.mcp.json`, `.ao/**`; validator `protected_paths`. The worker's prompt adds `Protected paths: ` and the configured globs on their own line, so the instruction's hash stays and the globs follow the config. The globs change only with the desktop app's confirmation (`PATCH /lado/rules/paths.protected` with `{"baseVersion": n, "paths": {"globs": [...]}}`, 1-50 globs valid as `affected_paths` entries, presence `rule.protected_change` as for the ceiling, audit `rule.protected_changed` with `{from, to}`). Matching is case-sensitive (TDD §8.3) |
| `agent.permission.ceiling` | Explicit mode, per-turn cap, Codex mapping (TDD §6.4-6.6) | Prevents, within the tool | Step 5, as a built-in protected rule at `auto` whose mode changes only with presence. Step 6 applies it to runs: the snapshot's mode at spawn, the per-turn cap, and the refusal of approval-mode options above it (TDD §6.4, §6.5) |
| `setup.change.requires_presence` | Human-presence channel (§9.5) | Prevents | Step 7, as a built-in protected rule that is always on (version 1 by `lado`, "Local setup changes need the desktop app's confirmation", with `workflow_gate` enforcement only, so its instruction never reaches a worker's prompt), refusing bindings, edits, archiving and deletion like the other protected rules. The local setup service asks the desktop app for the changes §9.5 lists, and for every adoption and restore |
| `git.commit.message.korean` (optional, not protected by default) | Commit validator | Detects (heuristic) | Step 5, as a built-in rule that starts off and gives the worker prompt text only, so it is advisory. The validator comes with step 8. Step 8: built-in version 2 adds `validator` enforcement and the config `{"language":"ko","allowPrefixes":[...]}` (validator `commit_message_locale`, §5); while it is on, a clearly English commit fails the integration and an unclear one asks the developer |

Validators are registered in code. AI cannot invent a validator that runs arbitrary shell. Step 5's built-in rules are seeded at daemon start (`service/ladopolicy` `EnsureBuiltins`, version 1 by `lado`). Only missing ones are seeded, so a stored built-in keeps its versions, its ceiling mode and its Global binding across restarts and upgrades.

As built in step 8, there are eight built-in rules, seven of them protected and Global on. `EnsureBuiltins` also upgrades: a built-in whose active version is byte-identical to one of its earlier built-in versions gets the newer content as its next version, by `lado`, with the audit event `rule.builtin_upgraded` (`{from, to}`, the stored version numbers). A rule the developer edited keeps its versions, and a change that lands meanwhile wins (the upgrade is only logged). Only the Korean rule has an earlier built-in version so far. Of the protected rules, only the ceiling's mode and the protected paths' globs can change, each with the desktop app's confirmation; another protected rule given `paths` is 403 `LADO_RULE_PROTECTED`, and an ordinary one 400 `LADO_PROTECTED_PATHS_INVALID` with `{rule}`.

## 15. Acceptance tests

Each item names the tests that cover it, all in `service/ladopolicy` unless named otherwise, or the step that owns it.

1. Global ON / Project OFF / Task ON resolves to ON. Step 5: `TestResolveBindings` (PRD gate 3).
2. Project INHERIT follows a Global toggle. Step 5: `TestResolveBindings`.
3. A protected gate rejects Task FORCE_OFF and AI-originated bypass. Step 5: `TestResolveBindings` (a protected gate stays on despite FORCE_OFF and Global off), `TestProtectedRulesRefuseBindingsArchiveAndDelete` (the API refuses FORCE_OFF, Global off, archiving, edits and deletion), `TestCeilingChangeNeedsPresence` (an API caller cannot raise the ceiling without the desktop app's confirmation). AI drafts do not exist yet (HAR-006); an agent calling the API meets the same refusals.
4. Disabling keeps rule history and leaves existing snapshots unchanged. Step 5: `TestDisablingKeepsHistoryAndSnapshots`.
5. The Korean commit rule blocks a clearly non-compliant commit in `base..head` and allows configured prefixes. Step 8, with the validator; step 5 has the rule's prompt text only. Step 8, `service/ladoverify`: `TestKoreanCommitValidator` (clearly English subjects fail, Korean ones with `feat:` or `fix(scope):` pass, short or code-only ones are unclear, an English body under a Korean subject is unclear, and the PR title is judged too) and `TestNoVerifyCommitIsJudged` (a commit made with `--no-verify` in a temp repository fails all the same, PRD gate 9); `service/ladopolicy` `TestEnsureBuiltinsUpgradesAnUnchangedKoreanRule` (the rule gets its validator unless the developer edited it) and `TestValidatorsFollowTheOnRules`.
6. A natural-language change produces an approval preview, not an immediate change. Later, with HAR-006.
7. A missing enforcement capability is reported and blocks dispatch. Step 5: `TestResolveCapabilities` and `TestEffectiveBlocksAnUnsupportedAgent` (a blocking problem naming the agent and the capability). Step 6 refuses the dispatch: `service/ladoqueue` `TestUnsupportedAgentBlocksWithoutSpawning` (PRD gate 15).
8. Conflicting mandatory rules block dispatch with an actionable diagnostic. Step 5: `TestResolveConflicts` (a blocking `LADO_RULES_CONFLICT` naming both rules, once per pair). Step 6 refuses the dispatch of a blocking snapshot: `service/ladoqueue` `TestDispatchStopsAtTheFirstFailingCheck`.
9. Adoption writes the backup and manifest before removing anything; restore is byte-identical; credential files are untouched. Step 7, `service/ladosetup`: `TestAdoptionBacksUpBeforeRemoving` (the backup is verified and the managed copy checked before the first removal), `TestAdoptionNeverTouchesCredentials` (adopting everything adoptable leaves `.credentials.json`, `projects/`, `history.jsonl`, Codex `auth.json` and `sessions/` as they were, and `~/.claude.json` loses only its servers), `TestRestoreIsByteIdentical` (bytes, modes and mtimes come back) (PRD gate 14).
10. After adoption, AO sessions on both account and own-computer login see the adopted items, and the tools run directly do not. Step 7: `TestClaudeHomeCarriesManagedItems` (a session on the computer's own login and one on an account each get the managed items in a home of their own), `TestDirectUseNoLongerSeesAdoptedItems`; with the real binaries on Linux, `service/ladosetup/probe` `TestDeliveryProbeClaudeTUI`, `TestDeliveryProbeClaudeChat` and `TestDeliveryProbeCodex` (run with `AO_LADO_SETUP_PROBE=1`).
11. AO's account linker no longer creates entries in `~/.claude` after adoption. Step 7: `TestAccountLinkerCreatesNothingAfterAdoption` (AO's real `agentaccount.Claude.Prepare` on a fake home creates nothing, since adoption keeps the item folders).
12. Editing a managed item after admission marks the entry Review required; the running session is unaffected. Step 7, on step 6's queue: `service/ladotask` `TestSetupChangeAfterApprovalNeedsReview` (with the real rule and setup services), `service/ladosetup` `TestExecutionHomeUsesPinnedVersions` and `TestCodexDeliveryUsesPinnedVersions`.
13. An adopted MCP server named `ao` is a blocking conflict. Step 7: `TestConflictMCPServerNamedAO` (in any case, for either tool, and it cannot be accepted).
14. A repo-tracked CLAUDE.md is never moved; "Edit" creates a task. Step 7: `TestRepoTrackedFilesAreNeverAdoptable` (tracked files are listed read-only and never adoptable, untracked personal files as files worktrees never load); in the renderer, `LadoProjectRulesSection.test.tsx` "lists the repository's setup files and edits one through a new task".
15. A new `~/.claude/settings.json` after adoption shows up as a new item and is not moved automatically. Step 7: `TestDriftShowsNewLocalItems`.
