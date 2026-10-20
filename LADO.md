# LADO

[한국어](LADO.ko.md)

LADO is a fork of [Orchestrator.inc's Agent Orchestrator](https://github.com/OrchestratorInc/agent-orchestrator) (AO) for one developer on their own machine, Windows first. AO lets you run many coding agents at once. LADO adds the guard rails that make that safe to leave running:

- **Work items are issues.** A task is an issue on the project's GitHub or GitLab repository. You create it from the New Task composer or import open issues; LADO tracks the issue and never edits it behind your back.
- **A worker starts only from a plan you approved.** The project's orchestrator writes a plan for the issue. You review it, ask for changes, and approve one exact revision in the desktop app. An agent cannot approve its own work.
- **Rules decide what agents may do** (coming): global, project and task rules with protected gates, frozen into a snapshot when work is queued.
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
| 5 | Rules: scopes, toggles, protected gates, snapshots | Planned |
| 6 | The approved-work queue and dispatcher; LADO mode becomes usable end to end | Planned |
| 7 | Adopting your local agent setup (CLAUDE.md, hooks, skills, MCP) into one managed place | Planned |
| 8 | Verification, evidence and integration approval | Planned |

Until step 6, a project in LADO mode cannot start workers, so changing LADO mode is a developer setting (below).

## Trying it

Build and run AO from source as the [development guide](docs/development.md) describes. Then, with the daemon started with `AO_LADO_DEV=on`:

1. Turn LADO mode on for a project. There is no switch in the app yet:

   ```bash
   curl -X PUT "http://127.0.0.1:<port>/api/v1/lado/projects/<project-id>/settings" \
     -H 'content-type: application/json' -d '{"ladoMode":true}'
   ```

   The daemon's port is in `~/.ao/running.json`. Turning LADO mode off needs a human-presence proof; until the app has a switch for it, start the daemon with `AO_LADO_DEV_PRESENCE=1` as well to turn it off with the same call.
2. Connect the repository from Add Project → Connect a repository: an access token for the host and the repository's SSH URL. LADO checks both, then links the project or clones it.
3. Create a task from New Task, or import open issues. The board's Plan lane shows them.
4. Open a task and choose Request a plan. If a comment on the issue already holds the plan, LADO's orchestrator follows it; Use as plan on a comment says so explicitly. Only comments by people who can work on the repository reach an agent.
5. Review the plan, ask for changes, and approve it. The desktop app asks you to confirm and signs the approval itself; the task moves to the Queued lane.

`AO_LADO_DEV_PRESENCE=1` lets approvals through without the desktop app's confirmation, for the renderer running in a browser during development. Never set it on a daemon you use for real work.

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
