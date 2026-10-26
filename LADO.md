# LADO

[한국어](LADO.ko.md)

LADO is a fork of [Orchestrator.inc's Agent Orchestrator](https://github.com/OrchestratorInc/agent-orchestrator) (AO) for one developer on their own machine, Windows first. AO lets you run many coding agents at once. LADO adds the guard rails that make that safe to leave running:

- **Work items are issues.** A task is an issue on the project's GitHub or GitLab repository. You create it from the New Task composer or import open issues; LADO tracks the issue and never edits it behind your back.
- **A worker starts only from a plan you approved.** The project's orchestrator writes a plan for the issue. You review it, ask for changes, and approve one exact revision in the desktop app. An agent cannot approve its own work.
- **Rules decide what agents may do**: global, project and task rules with protected gates, fixed into a snapshot with each plan approval. The worker gets that snapshot's rules in its instructions, runs within its permission ceiling, and keeps the agent it was approved with.
- **Verification before integration** (coming): required checks, LADO's own validators and an integration approval before anything merges.

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
| 6 | The approved-work queue and dispatcher; LADO mode becomes usable end to end | In progress |
| 7 | Adopting your local agent setup (CLAUDE.md, hooks, skills, MCP) into one managed place | Planned |
| 8 | Verification, evidence and integration approval | Planned |
| 9 | Releases of this fork: a workflow that builds the desktop app (Windows first) and publishes it where the app looks for updates, and the checks that need a real Windows install | Planned |

## Trying it

Build and run AO from source as the [development guide](docs/development.md) describes. Then:

1. Connect the repository from Add Project → Connect a repository: an access token for the host and the repository's SSH URL. LADO checks both, then links the project or clones it.
2. Turn LADO mode on in the project's settings, on the **LADO** page. LADO refuses while the project's tracker intake is on: a LADO project takes issues by import. The same page sets how many of the project's tasks run at once (one by default). Turning LADO mode off asks you to confirm in the desktop app; queued tasks then wait until it is on again, and running workers keep running.
3. Create a task from New Task, or import open issues. The board's Plan lane shows them.
4. Open a task and choose Request a plan. If a comment on the issue already holds the plan, LADO's orchestrator follows it; Use as plan on a comment says so explicitly. Only comments by people who can work on the repository reach an agent.
5. Review the plan, ask for changes, and approve it. The desktop app asks you to confirm and signs the approval itself; the task moves to the Queued lane.
6. When a run slot is free, LADO checks the approval again (the plan, the issue, the rules and the base branch) and starts a worker in Chat with the approved plan, the issue as it was pinned and the approved rules. The task leaves the lanes; the worker's session card shows a LADO line, and the session's inspector shows the rules the run was approved with. If the issue changed since the plan was made, or the rules changed since the approval, the task asks for a new approval instead. A worker that does not start after three attempts, that reaches the maximum run time, or whose session ends without a PR leaves the task Failed in the Plan lane, and the plan can be approved again.

**Settings → Runs** sets how many approved tasks run at once across projects (two by default), the maximum run time (8 hours by default; off, or 1 to 24 hours; it stops applying once the worker opens a PR) and whether queued tasks wait while the computer is low on memory (on by default).

`AO_LADO_DEV_PRESENCE=1` lets approvals, and turning LADO mode off, through without the desktop app's confirmation, for the renderer running in a browser during development. Never set it on a daemon you use for real work.

## Where things live

Everything LADO keeps is under `~/.ao` (or `AO_DATA_DIR`), as for AO: its tables in AO's database, pinned plans and issue snapshots in `~/.ao/lado/blobs`, GitLab tokens in an owner-only file, and the github.com token in AO's own token store. The one exception is the opt-in Start at login entry, which is the operating system's login item.

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
- Only Claude Code and Codex run LADO tasks; a task with another agent shows Blocked. A LADO run stays in Chat with the agent it was approved with, and cannot switch either while it runs.
- A plan's base must be the branch workers start from. If that branch moved on after the plan was made, the run records by how many commits, but still starts.
