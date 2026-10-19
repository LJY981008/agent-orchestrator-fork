# LADO 6. Work items are issues on the connected repository

Date: 2026-10-19
Status: Accepted. Decided by the owner on 2026-10-19. Replaces the local proposal step of the first version of these docs. Amended by the owner on 2026-10-20 for workspace projects.

## Context

The first version of these docs gave LADO its own record before a session: a task memo, then a proposal artifact (`lado_proposals`) with rationale, evidence, assumptions, risks and alternatives, then a plan. An issue was an optional link. The owner wants work tracked where the code lives: as issues on the project's GitHub or GitLab repository.

Facts at `55b02b848`:

- **Trackers are read-only.** The tracker port has `Get`, `List` and `Preflight` (`ports/tracker.go:21-25`), over GitHub REST and GitLab REST v4. Nothing in AO writes an issue; upstream defers that to its issue #40 (`adapters/tracker/github/tracker.go:26-29`). Apart from `gh repo create` in the project importer (`service/importer/importer.go:584`), AO's GitHub and GitLab writes are pull and merge request actions such as merging (`adapters/scm/github/merge_action.go:40`, `adapters/scm/gitlab/merge_action.go:45-51`).
- **Issue identity exists.** Canonical ids are `github:owner/repo#N` and `gitlab:group/project#N`, plus `@host` for self-managed GitLab (`observe/trackerintake/observer.go:255-274`), stored in `sessions.issue_id` (`0001_init.sql:25`). There is no issue table, and nothing observes an issue after a session starts: `ApplyTrackerFacts` has no caller (`lifecycle/reactions.go:645`).
- **Worker prompts fetch issues live.** A worker spawned with an issue id gets the issue fetched at spawn, unless the caller already supplied `IssueContext` (`service/session/issue_context.go:16-35`).
- **Tokens.** GitHub tokens come from `AO_GITHUB_TOKEN`, `GITHUB_TOKEN` or `gh auth token`; GitLab tokens from `AO_GITLAB_TOKEN`, `GITLAB_TOKEN` or `glab` (`daemon/scm_wiring.go:47-63`, `daemon/tracker_wiring.go:21-87`). Self-managed GitLab hosts come from env, read once at startup (`config/config.go:95-106,366-384`). AO stores a GitHub token in `<data>/github-pat.json`, unencrypted with mode 0600, but nothing reads it (`service/githubpat/githubpat.go:62-64,108-117,384`).
- **Clone exists.** `POST /api/v1/projects/clone` clones SSH or HTTPS URLs with the machine's git credentials and prompts off, then registers the project (`httpd/controllers/projects.go:29-31`, `service/project/clone.go:21-29,165-166`). AO handles no SSH keys.

## Decision

- **A task is an issue.** In a LADO project every task links exactly one issue on the project's repository. LADO creates the issue from the developer's request ("I'll build X"), or imports existing open issues the developer picks. The proposal step and `lado_proposals` are removed, and the orchestrator writes plans only.
- **Connection.** A project connects with an access token for its host and the repository's SSH URL, optionally with an SSH key file. LADO checks both, then links the registered project with the same `origin`, or clones through AO's clone flow. The github.com token goes into AO's existing store; GitLab tokens go into a LADO file with the same permissions. Stored tokens also join AO's token chains, after the `AO_*` overrides.
- **The plan is approved, not the issue.** At plan request LADO pins a snapshot of the issue: canonical id, title and body. The plan revision records the snapshot's hash, and approval binds to the plan's sha256. If the issue changed since its snapshot, dispatch sends the work back to review. The worker gets the pinned plan and snapshot, never a live fetch.
- **Execution state stays local.** Task states are derived from LADO and AO records plus the last observed issue state. The tracker never holds execution state; comments written back to the issue (TASK-006) are copies.

- **Workspace projects** (amended 2026-10-20). A project can be an AO workspace whose repositories, such as `back/` and `front/`, each have their own git and tracker. Issues can live in any of them, since no single one is sure to hold them all. The developer chooses the repository for each new task, and import lists every repository's issues. A clone can be a project of its own, start a new workspace, or join a registered one.

TDD §5.1 and §5.3 have the details.

## Alternatives

- **Local proposals with an optional issue link** (first version). Works offline and needs no write access, but splits work tracking between LADO and the repository. The owner chose issues instead.
- **Tracker intake** (AO today). It spawns a worker for each open issue, with no plan and no approval. It stays off in LADO projects ([0003](0003-enforcement-points.md)); import is the replacement the human controls.
- **Approve the issue body.** Anyone with access can edit it, so an approval would silently cover later edits.

## Consequences

- Creating, importing and planning a task need the tracker. Dispatch reads the issue too and waits while it cannot. Running sessions are unaffected.
- LADO adds AO's first issue writes: a write port with GitHub and GitLab adapters. If upstream builds its own (#40), LADO follows its shape (TDD §12).
- Tokens are unencrypted at rest, like AO's GitHub token, so any same-user process can read them (T2, [0001](0001-threat-model-t1-first.md)). They never enter an agent's environment, prompt or worktree.
- GitLab projects get issues and tasks now, but no remote merge control until a GitLab design for protected branches and status checks exists. Until then they are "unprotected: detection only" (TDD §7.1).
- GitHub Enterprise is out of scope while AO fixes its GitHub API base to api.github.com (`adapters/scm/github/client.go:21-22`).
