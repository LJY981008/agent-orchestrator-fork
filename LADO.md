# LADO

[한국어](LADO.ko.md)

LADO is a fork of [Orchestrator.inc's Agent Orchestrator](https://github.com/OrchestratorInc/agent-orchestrator) (AO) for one developer on their own machine, Windows first. AO lets you run many coding agents at once. LADO adds the guard rails that make that safe to leave running:

- **Work items are issues.** A task is an issue on the project's GitHub or GitLab repository. You create it from the New Task composer or import open issues; LADO tracks the issue and never edits it behind your back.
- **A worker starts only from a plan you approved.** A read-only plan worker writes the plan for the issue, a separate critic reviews the draft unless the plan is small, and the plan answers the review. You review it, ask for changes, and approve one exact revision in the desktop app. An agent cannot approve its own work. The project's orchestrator writes no plans; it relays LADO's one-line notices to you.
- **Rules decide what agents may do**: global, project and task rules with protected gates, fixed into a snapshot with each plan approval. The worker gets that snapshot's rules in its instructions, runs within its permission ceiling, and keeps the agent it was approved with.
- **Verification before integration**: LADO checks each head of the worker's pull request (the required CI checks, the project's own check commands, commit messages and changed paths, and AO's code review when the plan asks for it) and matches the worker's evidence against what it found. You approve the integration of that exact head in the desktop app. LADO then marks the head on GitHub (`lado/integration`), and AO's Merge refuses a head you did not approve.

LADO is built as additions to AO, not a rewrite: new code lives in new files inside AO's layers, and upstream files get only small hook lines, so upstream changes keep merging in.

## Status

The build order is in [docs/LJY-PRD/TDD.md §15](docs/LJY-PRD/TDD.md#15-build-order-agreed-2026-10-19-by-dependency).

| Step | What it brings | State |
| --- | --- | --- |
| 1a | Fork defaults: updates come from this fork, upstream telemetry is off, agents keep running after the app quits | Merged |
| 1c | Start at login and the Background settings | Merged |
| 2 | LADO's own migration track and an append-only, hash-chained audit log | Merged |
| 3 | The worker gate (no worker starts or receives work without LADO's grant) and LADO project settings | Merged |
| 4a | Repository connections (access token and SSH, link or clone, workspaces with several repositories) and issue-backed tasks | Merged |
| — | Korean as the desktop app's default language, with a reviewed Korean catalog | Merged |
| 4b | Plan requests and revisions, plans written in issue comments, approvals confirmed in the desktop app, the Plan and Queued lanes, the task view | Merged |
| 5 | Rules core: global, project and task rules with protected gates, an effective-rules preview, and a rule snapshot fixed with each plan approval | Merged |
| 6 | The approved-work queue and dispatcher; LADO mode becomes usable end to end | Merged |
| 7 | Adopting your local agent setup (CLAUDE.md and AGENTS.md, settings, hooks, skills, MCP servers) into one managed place that every session the app starts gets, with restore | Merged |
| 8 | Verification, evidence and integration approval: each PR head is checked (required checks, the project's check commands, commit messages, changed paths, the base branch, branch protection, review when the plan asks), the worker's evidence is matched against those records, and AO's merge needs your approval of that head | Merged |
| — | Plan workers ([#27](https://github.com/LJY981008/agent-orchestrator-fork/issues/27)): a read-only plan worker per plan request, an adversarial critic for drafts that are not small, the role instructions in Settings → Rules, and one-line notices to the orchestrator | Merged |
| — | Parallel parts ([#32](https://github.com/LJY981008/agent-orchestrator-fork/issues/32)): a plan splits its steps into parts that run in their own workers at once, merged into one integration branch and finished by an integration worker that reviews them and opens the one PR; task and worker caps apart, waits for low disk and for overlapping tasks | In review |
| 9 | Releases of this fork: a workflow that builds the desktop app (Windows first) and publishes it where the app looks for updates, and the checks that need a real Windows install | Planned |

## Trying it

Build and run AO from source as the [development guide](docs/development.md) describes. Then:

1. Connect the repository from Add Project → Connect a repository: an access token for the host and the repository's SSH URL. LADO checks both, then links the project or clones it.
2. Turn LADO mode on in the project's settings, on the **LADO** page. LADO refuses while the project's tracker intake is on: a LADO project takes issues by import. The same page sets how many of the project's tasks run at once (one by default). Turning LADO mode off asks you to confirm in the desktop app; queued tasks then wait until it is on again, and running workers keep running.
3. Create a task from New Task, or import open issues. The board's Plan lane shows them.
4. Open a task and choose Request a plan. LADO starts a read-only plan worker, a Chat session with the project's orchestrator agent, that writes the plan; the card and the task view show where it is (waiting for its turn, writing, the draft under review) with a link to its session. Unless the plan is small (three affected paths or fewer and no database schema, external API, authentication or data deletion change), a separate critic session reviews the draft, and the plan answers each serious finding in its "계획 검수" section. If a comment on the issue already holds the plan, the plan worker follows it; Use as plan on a comment says so explicitly. Only comments by people who can work on the repository reach an agent. The orchestrator gets one line, such as "LADO 알림: #12 계획 리비전 1 — 칸반에서 확인", once it is idle.
5. Review the plan, with its review (the findings and how the plan answered them) and what it took (time and tokens), ask for changes, which starts a new plan worker with your note, and approve it. The desktop app asks you to confirm and signs the approval itself; the task moves to the Queued lane.
6. When a run slot is free, LADO checks the approval again (the plan, the issue, the rules and the base branch) and starts a worker in Chat with the approved plan, the issue as it was pinned and the approved rules. The task leaves the lanes; the worker's session card shows a LADO line, and the session's inspector shows the rules the run was approved with. If the issue changed since the plan was made, or the rules changed since the approval, the task asks for a new approval instead. A worker that does not start after three attempts, that reaches the maximum run time, or whose session ends without a PR leaves the task Failed in the Plan lane, and the plan can be approved again.

   A plan with **parts** runs differently: the plan view lists them by phase (parts in one phase run at once, a part that depends on others after them), then the integration. LADO makes an integration branch at the approved base and starts every part that can start in its own fresh worker, on its own branch from that branch's tip. When a part's worker reports done, LADO merges its branch into the integration branch, without checking anything out and without touching your base or any other branch; a conflict goes to a merge worker. Once every part is merged, an integration worker reviews the parts against each other, fixes contradictions (or asks you when that needs a decision), does the remaining steps, and opens the task's one pull request. The task view shows each part's state, why it waits, its session and the merge, and the card, which stays in the Queued lane until the integration worker starts, shows how many parts are merged. A part worker that ends without reporting done gets one more try in a new session; then the task fails and names the part.
7. Once the worker opens a pull request, the task is Validating while LADO checks the PR's head: the required CI checks, the project's check commands, commit messages (when the Korean commit rule is on), the changed paths against the plan, the base branch and the branch's protection, and AO's review when the plan asks for one. When something fails, the worker gets a message and pushes a fix, and LADO checks the new head. The session's **Verification** section shows each check, the command logs (with Run again) and the evidence per acceptance criterion.
8. When the head passes, or needs only your confirmation (a path outside the plan, an unclear commit message, an unprotected branch, a moved local base), the task is Integration pending. **Approve integration…**, in the Verification section or the task view, asks in the desktop app with everything to confirm listed. Then **Merge** merges that head through AO, and on a protected GitHub branch GitHub's own merge works too. A new push needs a new approval.

The project's **LADO** page also has **Overlapping work**: whether a task waits while another task of the project that may change the same files runs (on by default), and the files whose overlap does not count, such as locale files, generated files and documents. It also has **Checks**, the commands LADO runs in each run's worktree at the PR's head (a change asks you to confirm in the desktop app, since they run outside any agent sandbox) with their time limit, and **Branch protection**, whether each base branch's GitHub rules require a pull request and `lado/integration` and block force pushes and deletion, with Check again. **Settings → Rules** lists the protected gates, and its **Protected paths** row edits the paths a worker may change only when the plan names them. Its **Planning** card holds what plan workers are told, **Plan designer** and **Plan critic**: edit them like any rule, and restore an older version from their history.

**Settings → Local setup** lists your Claude Code and Codex setup: the user-level files, settings keys, skills, agents, commands, hooks and MCP servers, and each project's setup files. Adopt… moves the items you pick into `~/.ao/lado/harness` after a backup, and from then on every Claude Code and Codex session the app starts gets them, while Claude Code and Codex run outside the app no longer do. Close Claude Code and Codex first: the app refuses while either runs. Adoption works on Linux only for now; Windows and macOS refuse it until step 9 checks them. You can then edit, turn off or delete the managed items there, per project on the project's **Rules** page, and per task on its Rules tab; changes that can run commands ask you to confirm in the desktop app. **Restore** on an adoption puts everything back where it was.

**Settings → Runs** sets how many approved tasks run at once across projects (two by default; a task split into parts counts once), how many LADO workers run at once (six by default, part, merge and integration workers included), whether new runs and parts wait while the free disk space is under a threshold (on, 10 GB), the maximum run time (8 hours by default; off, or 1 to 24 hours; it stops applying once the worker opens a PR) and whether queued tasks wait while the computer is low on memory (on by default). Its **Plan workers** card sets how many plan requests run at once, apart from the runs (two by default), whether small plans skip the review (on), how many affected paths a small plan may have (three) and the path patterns that mark a database schema, external API, authentication or data deletion change.

`AO_LADO_DEV_PRESENCE=1` lets approvals, and turning LADO mode off, through without the desktop app's confirmation, for the renderer running in a browser during development. `AO_LADO_DEV_SETUP_IGNORE_PROCESSES=1` lets a lab daemon that runs inside Claude Code or Codex adopt and restore anyway. Never set either on a daemon you use for real work.

## Where things live

Everything LADO keeps is under `~/.ao` (or `AO_DATA_DIR`), as for AO: its tables in AO's database, pinned plans and issue snapshots, check command logs and the workers' evidence files in `~/.ao/lado/blobs`, each plan request's files (the pinned issue, the revision a change starts from, the draft) in `~/.ao/data/lado/plan-requests`, GitLab tokens in an owner-only file, and the github.com token in AO's own token store. Adopted local setup lives in `~/.ao/lado/harness`, the originals' backups in `~/.ao/lado/backups`, and each Claude Code session's config home in `~/.ao/lado/homes`. The one exception is the opt-in Start at login entry, which is the operating system's login item. Adopting and restoring change `~/.claude`, `~/.claude.json` and `~/.codex` only when you confirm it, and never touch your logins.

## Documentation

- [docs/LJY-PRD/PRD.md](docs/LJY-PRD/PRD.md): requirements, mapped to what AO already has, and the acceptance gates.
- [docs/LJY-PRD/TDD.md](docs/LJY-PRD/TDD.md): how each part is built on AO, the hook lines in upstream files, and the build order.
- [docs/LJY-PRD/HARNESS.md](docs/LJY-PRD/HARNESS.md): rules and local setup adoption.
- [docs/LJY-PRD/adr/](docs/LJY-PRD/adr/): decisions such as the threat model, the human-presence approval channel and issues as work items.

## Known limits

- Windows-only behavior (keeping Chat sessions alive after quit, cleaning up child processes) still needs checking on a real Windows install.
- GitHub Enterprise is not supported, because AO's GitHub API base is fixed to api.github.com.
- A Copilot orchestrator wraps every message in AO's "spawn a worker" directive, so LADO projects should use another agent as their orchestrator.
- Approvals need the desktop app; there is no approval from the CLI or the mobile app, by design.
- Plan workers run with the project's orchestrator agent and model, and only Claude Code and Codex can be plan workers. They cannot see the project's Claude memory, so put what every plan needs to know in the Plan designer rule or the repository's documents. A plan worker is read-only: it cannot run a build or a test to learn about the code.
- Only Claude Code and Codex run LADO tasks; a task with another agent shows Blocked. A LADO run stays in Chat with the agent it was approved with, and cannot switch either while it runs.
- Local setup adoption works on Linux only until step 9 checks Windows and macOS; restore works everywhere. AO's reviewer gets only the adopted instructions, and the background tasks that write titles get no adopted items.
- LADO builds and tests nothing between parts: the part, merge and integration workers do. The overlap check is generous on purpose: work that might touch the same files waits even when it would not have conflicted. The integration branch's merges need git 2.38 or later.
- A plan's base must be the branch workers start from. If that branch moved on after the plan was made, the run records by how many commits, but still starts.
- GitLab projects have no remote merge control yet: LADO only detects a merge without approval there, and each integration asks you to confirm that. The same goes for a GitHub branch without the rules LADO needs.
- A GitHub ruleset that takes `lado/integration` from one app only cannot be met by LADO's status, so LADO reads that branch as not protected and says to allow any source. LADO verifies only the newest pull request of a run.
- Check commands run on your computer outside any agent sandbox. LADO keeps the daemon's tokens and git credentials out of their environment, but a command can still read any file you can, such as a stored token. On Windows, stopping the processes they start waits for step 9's checks.
