# LADO 5. Adopt the local agent setup into one LADO-managed location

Date: 2026-10-19
Status: Direction accepted by the owner. Per-tool delivery (below) is proposed and must be verified on each tool and OS before it ships.

## Context

The developer already has a lot of agent setup on the machine: user-level and project-level CLAUDE.md and AGENTS.md, settings with hooks and permissions, skills, subagents, commands, plugins and MCP servers. It is spread over `~/.claude`, `~/.claude.json`, `~/.codex` and every repository. LADO rules would conflict with it, and managing it in many places is hard.

Facts at `55b02b848`:

- **No AO UI lists this setup.** Only a Chat-only skills list (`httpd/controllers/conversations_skills.go:16-40`) and live MCP status exist.
- **The computer's own login uses the real folders directly.** AO sets no config folder for it (`service/agentaccounts/service.go:898-900`), so the user's `~/.claude` and `~/.codex` load in every such session.
- **Claude `default` reads the user's file.** It passes no permission flag, so `~/.claude/settings.json` decides the mode (`adapters/agent/claudecode/claudecode.go:160-162`).
- **Account homes mirror the real folders.** They link `skills`, `plugins`, `agents`, `commands`, `output-styles`, `hooks` and top-level `*.md` from `~/.claude` (`adapters/agentaccount/claude.go:26-30,268-274`), and copy settings key by key without login keys (:32-41, :280-323). For Codex they link nearly everything in `~/.codex` except credentials (`adapters/agentaccount/codex.go:15-35,198-230`).
- **AO writes into the user's folders.** The account linker creates missing shared entries in the user's real folder, or moves the account's copy there (`adapters/agentaccount/share.go:35-38,62-76`). AO also writes workspace trust into `~/.claude.json` (`adapters/agent/claudecode/claudecode.go:236-252`).
- **AO adds its own layer.** Worktree hooks (`adapters/agent/claudecode/hooks.go:11-16`), Codex hook flags (`adapters/agent/codex/hooks.go:72-116`), an `ao` MCP server (`session_manager/chat_spawn.go:345-373`) and a system prompt.
- **Codex can report its own configuration.** The app-server offers `config/read` with layers and origins, plus `hooks/list` and `plugin/list`. AO does not use them yet (`adapters/chatdriver/codexappserver/codexproto/protocol.gen.go:53-99,1225-1277`).

## Decision

The owner chose a single management point: an adopted item is backed up, copied into LADO's managed home, and removed from where it was. From then on only sessions launched by this app use it.

- **What is adopted.** User-level Claude Code and Codex setup and untracked project files (HARNESS §9.1).
- **What is never adopted.** Credentials, login keys, session history, caches, organization-managed settings and repo-tracked files.
- **Flow.** Scan → preview → preconditions (no running Claude or Codex holding the files) → human-presence approval ([0002](0002-human-presence-approval-channel.md)) → backup with a hashed manifest under `<state>/lado/backups/` → copy into `<state>/lado/harness/` → remove the originals (only adopted keys inside `settings.json` and `~/.claude.json`) → record and verify.
- **Delivery to sessions:**
  - Account homes link from the managed home instead of the user's folders.
  - The linker stops creating entries in the user's real folders.
  - Sessions on the computer's own login get a per-execution home built from the snapshot. It links credentials and app state from the real folder.
  - If a home cannot be built, launch options are the fallback.
  - An item kind is adoptable only if it reaches both the TUI and the Chat path of its tool.
- **Changes.** Every change to a managed item shows a diff and needs approval. Changes that can run code or widen access need human presence. Versions are kept as blobs. Repo-tracked files change only through git, for example a task with a prefilled plan.
- **Drift and restore.** Items that reappear at original locations are offered for adoption, never moved automatically. Restore writes the backup back byte for byte and never overwrites something newer.

## Consequences

- Claude Code and Codex used directly, including IDE extensions, no longer see adopted items. That is the intended effect. Restore is the way back.
- LADO edits the upstream account linker (`adapters/agentaccount/share.go`) and adds a launch-home hook. Both are small hooks under D1.
- Removing or restoring files under the user's tool folders happens only on an explicit, approved request. LADO's own copies, backups and manifests stay under `~/.ao`, as `AGENTS.md:120` requires.
- Each execution's setup home is built from immutable blobs, so running agents never see edits made after admission.
- To verify before shipping: Claude credential lookup with `CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR`, Codex `auth.json` linking, file and directory links on Windows, and whether Claude ACP honors the same homes as the TUI.
